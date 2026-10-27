package codemode

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/grafana/sobek"
)

const uiPluginMaxRejections = 32

var uiPluginScriptPosition = regexp.MustCompile(`Line ([0-9]+):([0-9]+)`)

// uiPluginEngine owns one intercepting module instance. Sobek is an
// interpreter, so the runtime has no JIT and cannot load executable code from
// the host filesystem.
type uiPluginEngine struct {
	runtime   *sobek.Runtime
	namespace *sobek.Object
	pending   map[uint64]*sobek.Promise
	order     []uint64
	failures  uiPluginFailures
}

type uiPluginFailures struct {
	rejected   []*sobek.Promise
	overflowed bool
	scriptErr  *uiPluginScriptError
}

func newUIPluginEngine() (*uiPluginEngine, error) {
	runtime := sobek.New()
	if err := installUIPluginRestrictions(runtime); err != nil {
		return nil, err
	}
	engine := &uiPluginEngine{
		runtime: runtime,
		pending: map[uint64]*sobek.Promise{},
	}
	runtime.SetPromiseRejectionTracker(engine.failures.track)
	return engine, nil
}

func installUIPluginRestrictions(runtime *sobek.Runtime) error {
	// Deny code generation from strings, including indirect access through the
	// function prototypes, before any user source is compiled.
	if _, err := runtime.RunString(`
		(() => {
			const deny = function () {
				throw new EvalError("code generation from strings is disabled");
			};
			const functionPrototype = Function.prototype;
			globalThis.eval = deny;
			globalThis.Function = deny;
			functionPrototype.constructor = deny;
			try {
				Object.getPrototypeOf(async function () {}).constructor = deny;
			} catch (error) {}
			try {
				Object.getPrototypeOf(function* () {}).constructor = deny;
			} catch (error) {}
		})();
	`); err != nil {
		return fmt.Errorf("restrict plugin code generation: %w", err)
	}
	// Binary buffers are omitted until the sandbox allocator supports quotas.
	for _, name := range []string{
		"console",
		"ArrayBuffer",
		"SharedArrayBuffer",
		"Atomics",
		"WebAssembly",
		"DataView",
		"Int8Array",
		"Uint8Array",
		"Uint8ClampedArray",
		"Int16Array",
		"Uint16Array",
		"Int32Array",
		"Uint32Array",
		"Float16Array",
		"Float32Array",
		"Float64Array",
		"BigInt64Array",
		"BigUint64Array",
	} {
		runtime.GlobalObject().Delete(name)
	}
	return nil
}

func (e *uiPluginEngine) turn(message uiPluginHostMessage) (messages []any, failure *uiPluginRuntimeError) {
	defer func() {
		if recovered := recover(); recovered != nil {
			messages = nil
			failure = e.failed(uiPluginErrExecutionFailed, fmt.Sprint(recovered))
		}
	}()
	switch message.Type {
	case "initialize":
		if err := e.load(message.Source); err != nil {
			return nil, err
		}
		return []any{uiPluginInitializedMessage()}, nil
	case "dispatch":
		return e.dispatch(message.ID, message.Export, message.ReturnMode)
	case "close":
		return nil, &uiPluginRuntimeError{Code: uiPluginErrInvalidProgram}
	default:
		return nil, &uiPluginRuntimeError{Code: uiPluginErrInvalidProgram}
	}
}

func (e *uiPluginEngine) load(source string) *uiPluginRuntimeError {
	if e.namespace != nil || len(source) > uiPluginMaxSourceBytes {
		return &uiPluginRuntimeError{Code: uiPluginErrInvalidProgram}
	}
	if uiPluginSourceHasDynamicImport(source) {
		return e.failed(uiPluginErrExecutionFailed, "Not supported")
	}
	resolve := func(interface{}, string) (sobek.ModuleRecord, error) {
		return nil, errors.New("Not supported")
	}
	module, err := sobek.ParseModule("plugin.mjs", source, resolve)
	if err != nil {
		return e.failed(uiPluginErrInvalidProgram, err.Error(), err)
	}
	if len(module.RequestedModules()) != 0 {
		return e.failed(uiPluginErrInvalidProgram, "module imports are unavailable")
	}
	if err := module.Link(); err != nil {
		return e.failed(uiPluginErrInvalidProgram, err.Error(), err)
	}
	evaluation := e.runtime.CyclicModuleRecordEvaluate(module, resolve)
	if evaluation.State() == sobek.PromiseStateRejected {
		return e.failedValue(uiPluginErrExecutionFailed, evaluation.Result())
	}
	if evaluation.State() != sobek.PromiseStateFulfilled {
		return &uiPluginRuntimeError{Code: uiPluginErrExecutionFailed}
	}
	e.namespace = e.runtime.NamespaceObjectFor(module)
	if failure := e.checkpoint(); failure != nil {
		return failure
	}
	return nil
}

func (e *uiPluginEngine) dispatch(id uint64, export string, returnMode string) ([]any, *uiPluginRuntimeError) {
	if e.namespace == nil {
		return nil, &uiPluginRuntimeError{Code: uiPluginErrInvalidProgram}
	}
	if export == "" || len(export) > uiPluginMaxMethodBytes {
		return nil, &uiPluginRuntimeError{Code: uiPluginErrQuotaExceeded}
	}
	if _, exists := e.pending[id]; exists {
		return nil, &uiPluginRuntimeError{Code: uiPluginErrQuotaExceeded}
	}
	value := e.namespace.Get(export)
	function, ok := sobek.AssertFunction(value)
	if !ok {
		return nil, &uiPluginRuntimeError{Code: uiPluginErrInvalidProgram}
	}
	result, err := function(sobek.Undefined())
	if err != nil {
		return nil, e.failed(uiPluginErrExecutionFailed, err.Error(), err)
	}
	var replies []any
	if promise, ok := result.Export().(*sobek.Promise); ok && returnMode == uiPluginReturnMaybeAsync {
		if len(e.pending) >= uiPluginMaxPromises {
			return nil, &uiPluginRuntimeError{Code: uiPluginErrQuotaExceeded}
		}
		e.pending[id] = promise
		e.order = append(e.order, id)
	} else {
		text, failure := uiPluginResultString(result)
		if failure != nil {
			return nil, failure
		}
		replies = append(replies, uiPluginReplyMessage(id, text))
	}
	if failure := e.checkpoint(); failure != nil {
		return nil, failure
	}
	remaining := e.order[:0]
	for _, pendingID := range e.order {
		promise := e.pending[pendingID]
		if promise.State() == sobek.PromiseStatePending {
			remaining = append(remaining, pendingID)
			continue
		}
		delete(e.pending, pendingID)
		if promise.State() != sobek.PromiseStateFulfilled {
			return nil, e.failedValue(uiPluginErrExecutionFailed, promise.Result())
		}
		text, failure := uiPluginResultString(promise.Result())
		if failure != nil {
			return nil, failure
		}
		replies = append(replies, uiPluginReplyMessage(pendingID, text))
	}
	e.order = remaining
	if _, stillPending := e.pending[id]; stillPending {
		replies = append(replies, uiPluginPendingMessage(id))
	}
	return replies, nil
}

func (e *uiPluginEngine) checkpoint() *uiPluginRuntimeError {
	if len(e.failures.rejected) > 0 {
		reason := e.failures.rejected[0].Result()
		return e.failedValue(uiPluginErrExecutionFailed, reason)
	}
	if e.failures.overflowed {
		return &uiPluginRuntimeError{Code: uiPluginErrQuotaExceeded}
	}
	return nil
}

func (e *uiPluginEngine) failed(code string, message string, causes ...error) *uiPluginRuntimeError {
	return &uiPluginRuntimeError{Code: code, ScriptError: uiPluginScriptErrorFrom(message, causes...)}
}

func (e *uiPluginEngine) failedValue(code string, value sobek.Value) *uiPluginRuntimeError {
	message := ""
	if value != nil {
		message = value.String()
	}
	return &uiPluginRuntimeError{Code: code, ScriptError: uiPluginScriptErrorFrom(message)}
}

func uiPluginResultString(value sobek.Value) (string, *uiPluginRuntimeError) {
	text, ok := value.Export().(string)
	if !ok {
		return "", &uiPluginRuntimeError{Code: uiPluginErrInvalidResult}
	}
	if len(text) > uiPluginMaxResultBytes {
		return "", &uiPluginRuntimeError{Code: uiPluginErrQuotaExceeded}
	}
	return text, nil
}

func uiPluginScriptErrorFrom(message string, causes ...error) *uiPluginScriptError {
	var line, column *int
	for _, cause := range causes {
		if cause == nil {
			continue
		}
		var syntax *sobek.CompilerSyntaxError
		if errors.As(cause, &syntax) {
			if syntax.File != nil {
				position := syntax.File.Position(syntax.Offset)
				lineValue, columnValue := position.Line, position.Column
				line, column = &lineValue, &columnValue
			} else if match := uiPluginScriptPosition.FindStringSubmatch(syntax.Message); match != nil {
				lineValue, lineErr := strconv.Atoi(match[1])
				columnValue, columnErr := strconv.Atoi(match[2])
				if lineErr == nil && columnErr == nil {
					line, column = &lineValue, &columnValue
				}
			}
			if syntax.Message != "" {
				message = syntax.Message
			}
			break
		}
		var exception *sobek.Exception
		if errors.As(cause, &exception) {
			if stack := exception.Stack(); len(stack) > 0 {
				position := stack[0].Position()
				if position.Line > 0 {
					lineValue, columnValue := position.Line, position.Column
					line, column = &lineValue, &columnValue
				}
			}
			break
		}
	}
	original := len(message)
	if original > uiPluginMaxScriptError {
		message = message[:uiPluginMaxScriptError]
		for !utf8.ValidString(message) && len(message) > 0 {
			message = message[:len(message)-1]
		}
	}
	return &uiPluginScriptError{
		Message:   message,
		Line:      line,
		Column:    column,
		Truncated: original > len(message),
	}
}

func (f *uiPluginFailures) track(promise *sobek.Promise, operation sobek.PromiseRejectionOperation) {
	switch operation {
	case sobek.PromiseRejectionReject:
		if len(f.rejected) >= uiPluginMaxRejections {
			f.overflowed = true
			return
		}
		f.rejected = append(f.rejected, promise)
	case sobek.PromiseRejectionHandle:
		for index, candidate := range f.rejected {
			if candidate == promise {
				f.rejected = append(f.rejected[:index], f.rejected[index+1:]...)
				return
			}
		}
	}
}

// uiPluginSourceHasDynamicImport rejects import() before Sobek's parser turns
// it into an opaque SyntaxError. Static imports are rejected after parsing via
// the module's requested-module list.
func uiPluginSourceHasDynamicImport(source string) bool {
	for index := 0; index < len(source); {
		switch {
		case strings.HasPrefix(source[index:], "//"):
			index = uiPluginSkipLineComment(source, index+2)
		case strings.HasPrefix(source[index:], "/*"):
			index = uiPluginSkipBlockComment(source, index+2)
		case source[index] == '\'' || source[index] == '"' || source[index] == '`':
			index = uiPluginSkipString(source, index)
		case uiPluginIsIdentifierStart(source[index]):
			start := index
			for index < len(source) && uiPluginIsIdentifierPart(source[index]) {
				index++
			}
			if source[start:index] != "import" {
				continue
			}
			if start > 0 && source[start-1] == '.' {
				continue
			}
			next := uiPluginSkipSpaceAndComments(source, index)
			if next < len(source) && source[next] == '(' {
				return true
			}
		default:
			index++
		}
	}
	return false
}

func uiPluginSkipLineComment(source string, index int) int {
	for index < len(source) && source[index] != '\n' {
		index++
	}
	return index
}

func uiPluginSkipBlockComment(source string, index int) int {
	if end := strings.Index(source[index:], "*/"); end >= 0 {
		return index + end + 2
	}
	return len(source)
}

func uiPluginSkipString(source string, index int) int {
	quote := source[index]
	index++
	for index < len(source) {
		switch source[index] {
		case '\\':
			index += 2
		case quote:
			return index + 1
		default:
			index++
		}
	}
	return len(source)
}

func uiPluginSkipSpaceAndComments(source string, index int) int {
	for index < len(source) {
		switch {
		case source[index] == ' ' || source[index] == '\t' || source[index] == '\r' || source[index] == '\n':
			index++
		case strings.HasPrefix(source[index:], "//"):
			index = uiPluginSkipLineComment(source, index+2)
		case strings.HasPrefix(source[index:], "/*"):
			index = uiPluginSkipBlockComment(source, index+2)
		default:
			return index
		}
	}
	return index
}

func uiPluginIsIdentifierStart(ch byte) bool {
	return ch == '_' || ch == '$' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
}

func uiPluginIsIdentifierPart(ch byte) bool {
	return uiPluginIsIdentifierStart(ch) || ch >= '0' && ch <= '9'
}
