package codemode

import (
	"encoding/json"
	"fmt"

	"github.com/grafana/sobek"
)

func installSobekHelpers(runtime *sobek.Runtime, items *[]ContentItem) error {
	if err := runtime.Set("text", func(call sobek.FunctionCall) sobek.Value {
		value := sobek.Undefined()
		if len(call.Arguments) > 0 {
			value = call.Argument(0)
		}
		*items = append(*items, InputText(renderSobekText(value)))
		return sobek.Undefined()
	}); err != nil {
		return fmt.Errorf("install text helper: %w", err)
	}
	if err := runtime.Set("exit", func() {
		runtime.Interrupt(errEngineExit)
	}); err != nil {
		return fmt.Errorf("install exit helper: %w", err)
	}
	return nil
}

func renderSobekText(value sobek.Value) string {
	if sobek.IsUndefined(value) {
		return "undefined"
	}
	if sobek.IsNull(value) {
		return "null"
	}
	if _, ok := value.Export().(string); ok {
		return value.String()
	}
	encoded, err := json.Marshal(value.Export())
	if err == nil {
		return string(encoded)
	}
	return value.String()
}
