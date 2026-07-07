package execserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"codex_go/internal/shell"
	"codex_go/internal/utils"
)

const (
	MethodInitialize        = "initialize"
	MethodInitialized       = "initialized"
	MethodEnvironmentInfo   = "environment/info"
	MethodProcessStart      = "process/start"
	MethodProcessRead       = "process/read"
	MethodProcessWrite      = "process/write"
	MethodProcessTerminate  = "process/terminate"
	MethodProcessSignal     = "process/signal"
	MethodFSReadFile        = "fs/readFile"
	MethodFSOpen            = "fs/open"
	MethodFSReadBlock       = "fs/readBlock"
	MethodFSClose           = "fs/close"
	MethodFSWriteFile       = "fs/writeFile"
	MethodFSCreateDirectory = "fs/createDirectory"
	MethodFSGetMetadata     = "fs/getMetadata"
	MethodFSCanonicalize    = "fs/canonicalize"
	MethodFSReadDirectory   = "fs/readDirectory"
	MethodFSWalk            = "fs/walk"
	MethodFSRemove          = "fs/remove"
	MethodFSCopy            = "fs/copy"
	MethodHTTPRequest       = "http/request"
)

const (
	maxWalkDepth                  = 64
	maxWalkDirectories            = 10000
	maxWalkEntries                = 50000
	maxWalkResponseBytes          = 4 * 1024 * 1024
	walkResponseItemOverhead      = 64
	retainedOutputBytesPerProcess = 10 * 1024 * 1024
)

type RequestID struct {
	value any
}

func (id *RequestID) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		id.value = nil
		return nil
	}
	if data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		id.value = value
		return nil
	}
	var value json.Number
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	id.value = value
	return nil
}

func (id RequestID) MarshalJSON() ([]byte, error) {
	switch value := id.value.(type) {
	case nil:
		return []byte("null"), nil
	case string:
		return json.Marshal(value)
	case json.Number:
		return []byte(value.String()), nil
	case int:
		return json.Marshal(value)
	case int64:
		return json.Marshal(value)
	default:
		return json.Marshal(fmt.Sprint(value))
	}
}

func (id *RequestID) IsZero() bool {
	return id == nil || id.value == nil
}

type request struct {
	ID     RequestID       `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type response struct {
	ID     RequestID `json:"id"`
	Result any       `json:"result,omitempty"`
	Error  *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type requestFailure struct {
	code    int
	message string
}

func (e *requestFailure) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

type Server struct {
	mu        sync.Mutex
	sessionID string
	processes map[string]*processState
	handles   map[string]*os.File
}

type processState struct {
	id              string
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	pipeStdin       bool
	mu              sync.Mutex
	cond            *sync.Cond
	chunks          []outputChunk
	retainedBytes   int
	nextSeq         uint64
	exited          bool
	exitSequenced   bool
	exitCode        *int
	failure         string
	closed          bool
	closedSequenced bool
	openStreams     int
	seenWriteIDs    map[string]bool
}

type outputChunk struct {
	Seq    uint64 `json:"seq"`
	Stream string `json:"stream"`
	Chunk  string `json:"chunk"`
}

type InitializeParams struct {
	ClientName      string  `json:"clientName"`
	ResumeSessionID *string `json:"resumeSessionId,omitempty"`
}

type InitializeResponse struct {
	SessionID string `json:"sessionId"`
}

type EnvironmentInfo struct {
	Shell ShellInfo `json:"shell"`
	CWD   *string   `json:"cwd"`
}

type ShellInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type ExecParams struct {
	ProcessID             string            `json:"processId"`
	Argv                  []string          `json:"argv"`
	CWD                   string            `json:"cwd"`
	EnvPolicy             *ExecEnvPolicy    `json:"envPolicy,omitempty"`
	Env                   map[string]string `json:"env"`
	TTY                   bool              `json:"tty"`
	PipeStdin             bool              `json:"pipeStdin"`
	Arg0                  *string           `json:"arg0"`
	Sandbox               json.RawMessage   `json:"sandbox,omitempty"`
	EnforceManagedNetwork bool              `json:"enforceManagedNetwork,omitempty"`
	ManagedNetwork        json.RawMessage   `json:"managedNetwork,omitempty"`
}

type ExecEnvPolicy struct {
	Inherit               string            `json:"inherit"`
	IgnoreDefaultExcludes bool              `json:"ignoreDefaultExcludes"`
	Exclude               []string          `json:"exclude"`
	Set                   map[string]string `json:"set"`
	IncludeOnly           []string          `json:"includeOnly"`
}

type ExecResponse struct {
	ProcessID string `json:"processId"`
}

type ReadParams struct {
	ProcessID string  `json:"processId"`
	AfterSeq  *uint64 `json:"afterSeq"`
	MaxBytes  *int    `json:"maxBytes"`
	WaitMS    *uint64 `json:"waitMs"`
}

type ReadResponse struct {
	Chunks        []outputChunk `json:"chunks"`
	NextSeq       uint64        `json:"nextSeq"`
	Exited        bool          `json:"exited"`
	ExitCode      *int          `json:"exitCode"`
	Closed        bool          `json:"closed"`
	Failure       *string       `json:"failure"`
	SandboxDenied bool          `json:"sandboxDenied"`
}

type WriteParams struct {
	ProcessID string `json:"processId"`
	Chunk     string `json:"chunk"`
	WriteID   string `json:"writeId"`
}

type WriteResponse struct {
	Status string `json:"status"`
}

type TerminateParams struct {
	ProcessID string `json:"processId"`
}

type TerminateResponse struct {
	Running bool `json:"running"`
}

type SignalParams struct {
	ProcessID string `json:"processId"`
	Signal    string `json:"signal"`
}

type SignalResponse struct{}

type FSReadFileParams struct {
	Path string `json:"path"`
}

type FSReadFileResponse struct {
	DataBase64 string `json:"dataBase64"`
}

type FSOpenParams struct {
	HandleID string `json:"handleId"`
	Path     string `json:"path"`
}

type FSOpenResponse struct {
	HandleID string `json:"handleId"`
}

type FSReadBlockParams struct {
	HandleID string `json:"handleId"`
	Offset   int64  `json:"offset"`
	Len      int    `json:"len"`
}

type FSReadBlockResponse struct {
	Chunk string `json:"chunk"`
	EOF   bool   `json:"eof"`
}

type FSCloseParams struct {
	HandleID string `json:"handleId"`
}

type FSCloseResponse struct{}

type FSWriteFileParams struct {
	Path       string `json:"path"`
	DataBase64 string `json:"dataBase64"`
}

type FSWriteFileResponse struct{}

type FSCreateDirectoryParams struct {
	Path      string `json:"path"`
	Recursive *bool  `json:"recursive,omitempty"`
}

type FSCreateDirectoryResponse struct{}

type FSGetMetadataParams struct {
	Path string `json:"path"`
}

type FSGetMetadataResponse struct {
	IsDirectory  bool  `json:"isDirectory"`
	IsFile       bool  `json:"isFile"`
	IsSymlink    bool  `json:"isSymlink"`
	Size         int64 `json:"size"`
	CreatedAtMS  int64 `json:"createdAtMs"`
	ModifiedAtMS int64 `json:"modifiedAtMs"`
}

type FSCanonicalizeParams struct {
	Path string `json:"path"`
}

type FSCanonicalizeResponse struct {
	Path string `json:"path"`
}

type FSReadDirectoryParams struct {
	Path string `json:"path"`
}

type FSReadDirectoryEntry struct {
	FileName    string `json:"fileName"`
	IsDirectory bool   `json:"isDirectory"`
	IsFile      bool   `json:"isFile"`
}

type FSReadDirectoryResponse struct {
	Entries []FSReadDirectoryEntry `json:"entries"`
}

type FSWalkParams struct {
	Path    string        `json:"path"`
	Options FSWalkOptions `json:"options"`
}

type FSWalkOptions struct {
	MaxDepth                int  `json:"maxDepth"`
	MaxDirectories          int  `json:"maxDirectories"`
	MaxEntries              int  `json:"maxEntries"`
	FollowDirectorySymlinks bool `json:"followDirectorySymlinks"`
}

type FSWalkEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type FSWalkError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type FSWalkResponse struct {
	Entries   []FSWalkEntry `json:"entries"`
	Errors    []FSWalkError `json:"errors"`
	Truncated bool          `json:"truncated"`
}

type FSRemoveParams struct {
	Path      string `json:"path"`
	Recursive *bool  `json:"recursive,omitempty"`
	Force     *bool  `json:"force,omitempty"`
}

type FSRemoveResponse struct{}

type FSCopyParams struct {
	SourcePath      string `json:"sourcePath"`
	DestinationPath string `json:"destinationPath"`
	Recursive       bool   `json:"recursive,omitempty"`
}

type FSCopyResponse struct{}

type HTTPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HTTPRequestParams struct {
	Method         string       `json:"method"`
	URL            string       `json:"url"`
	Headers        []HTTPHeader `json:"headers"`
	BodyBase64     *string      `json:"bodyBase64,omitempty"`
	TimeoutMS      *uint64      `json:"timeoutMs,omitempty"`
	RedirectPolicy string       `json:"redirectPolicy,omitempty"`
	RequestID      string       `json:"requestId"`
	StreamResponse bool         `json:"streamResponse"`
}

type HTTPRequestResponse struct {
	Status     int          `json:"status"`
	Headers    []HTTPHeader `json:"headers"`
	BodyBase64 string       `json:"bodyBase64"`
}

func NewServer() *Server {
	return &Server{
		sessionID: "exec-session-" + time.Now().UTC().Format("20060102T150405.000000000"),
		processes: map[string]*processState{},
		handles:   map[string]*os.File{},
	}
}

func (s *Server) Serve(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	encoder := json.NewEncoder(stdout)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		out, ok := s.handleLine(ctx, line)
		if !ok {
			continue
		}
		if err := encoder.Encode(out); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) handleLine(ctx context.Context, line []byte) (any, bool) {
	hasID, err := lineHasTopLevelID(line)
	if err != nil {
		return errorResponse(RequestID{}, -32600, "invalid request: "+err.Error()), true
	}
	if hasID {
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			return errorResponse(RequestID{}, -32600, "invalid request: "+err.Error()), true
		}
		if req.ID.IsZero() {
			return errorResponse(req.ID, -32600, "id is required"), true
		}
		result, err := s.handleRequest(ctx, &req)
		if err != nil {
			return errorResponseForRequest(req.ID, err), true
		}
		return response{ID: req.ID, Result: result}, true
	}
	var note notification
	if err := json.Unmarshal(line, &note); err != nil {
		return errorResponse(RequestID{}, -32600, "invalid notification: "+err.Error()), true
	}
	_ = note
	return nil, false
}

func lineHasTopLevelID(line []byte) (bool, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(line, &envelope); err != nil {
		return false, err
	}
	_, ok := envelope["id"]
	return ok, nil
}

func (s *Server) handleRequest(ctx context.Context, req *request) (any, error) {
	switch req.Method {
	case MethodInitialize:
		var params InitializeParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if params.ResumeSessionID != nil && *params.ResumeSessionID != "" {
			s.mu.Lock()
			s.sessionID = *params.ResumeSessionID
			s.mu.Unlock()
		}
		return InitializeResponse{SessionID: s.sessionID}, nil
	case MethodEnvironmentInfo:
		return localEnvironmentInfo(), nil
	case MethodProcessStart:
		var params ExecParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.startProcess(ctx, &params)
	case MethodProcessRead:
		var params ReadParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.readProcess(&params)
	case MethodProcessWrite:
		var params WriteParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.writeProcess(&params)
	case MethodProcessSignal:
		var params SignalParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.signalProcess(&params)
	case MethodProcessTerminate:
		var params TerminateParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.terminateProcess(&params)
	case MethodFSReadFile:
		var params FSReadFileParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return readFile(&params)
	case MethodFSOpen:
		var params FSOpenParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.openFile(&params)
	case MethodFSReadBlock:
		var params FSReadBlockParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.readBlock(&params)
	case MethodFSClose:
		var params FSCloseParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.closeFile(&params)
	case MethodFSWriteFile:
		var params FSWriteFileParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return writeFile(&params)
	case MethodFSCreateDirectory:
		var params FSCreateDirectoryParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return createDirectory(&params)
	case MethodFSGetMetadata:
		var params FSGetMetadataParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return getMetadata(&params)
	case MethodFSCanonicalize:
		var params FSCanonicalizeParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return canonicalize(&params)
	case MethodFSReadDirectory:
		var params FSReadDirectoryParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return readDirectory(&params)
	case MethodFSWalk:
		var params FSWalkParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return walkPath(&params)
	case MethodFSRemove:
		var params FSRemoveParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return removePath(&params)
	case MethodFSCopy:
		var params FSCopyParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return copyPath(&params)
	case MethodHTTPRequest:
		var params HTTPRequestParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return doHTTPRequest(ctx, &params)
	default:
		return nil, requestError(-32601, fmt.Sprintf("unknown exec-server method %s", req.Method))
	}
}

func (s *Server) startProcess(ctx context.Context, params *ExecParams) (*ExecResponse, error) {
	if params == nil {
		return nil, errors.New("process/start params are required")
	}
	if strings.TrimSpace(params.ProcessID) == "" {
		return nil, errors.New("processId is required")
	}
	if len(params.Argv) == 0 || strings.TrimSpace(params.Argv[0]) == "" {
		return nil, errors.New("argv is required")
	}
	if hasJSONValue(params.Sandbox) {
		return nil, requestError(-32600, "process/start sandbox is not supported by this exec-server backend")
	}
	if params.EnforceManagedNetwork || hasJSONValue(params.ManagedNetwork) {
		return nil, requestError(-32600, "process/start managed network is not supported by this exec-server backend")
	}
	cwd := strings.TrimSpace(params.CWD)
	if strings.HasPrefix(cwd, "file://") {
		uri, err := utils.Parse(cwd)
		if err != nil {
			return nil, err
		}
		cwd = uri.NativePathString()
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	cmd := exec.CommandContext(ctx, params.Argv[0], params.Argv[1:]...)
	if params.Arg0 != nil {
		cmd.Args[0] = *params.Arg0
	}
	cmd.Dir = cwd
	cmd.Env = envPairs(childEnv(params))
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	var stdin io.WriteCloser
	if params.PipeStdin {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
	}
	state := &processState{
		id:           params.ProcessID,
		cmd:          cmd,
		stdin:        stdin,
		pipeStdin:    params.PipeStdin,
		nextSeq:      1,
		openStreams:  2,
		seenWriteIDs: map[string]bool{},
	}
	state.cond = sync.NewCond(&state.mu)
	s.mu.Lock()
	if existing := s.processes[params.ProcessID]; existing != nil {
		existing.mu.Lock()
		closed := existing.closed
		existing.mu.Unlock()
		if !closed {
			s.mu.Unlock()
			return nil, fmt.Errorf("process %s already exists", params.ProcessID)
		}
	}
	s.processes[params.ProcessID] = state
	s.mu.Unlock()
	if err := cmd.Start(); err != nil {
		state.failStart(err)
	} else {
		go state.capture("stdout", stdoutPipe)
		go state.capture("stderr", stderrPipe)
		go func() {
			err := cmd.Wait()
			state.finish(err)
		}()
	}
	return &ExecResponse{ProcessID: params.ProcessID}, nil
}

func (s *Server) readProcess(params *ReadParams) (*ReadResponse, error) {
	state := s.lookup(params.ProcessID)
	if state == nil {
		return nil, fmt.Errorf("unknown process %s", params.ProcessID)
	}
	after := uint64(0)
	if params.AfterSeq != nil {
		after = *params.AfterSeq
	}
	return state.read(after, params.MaxBytes, params.WaitMS), nil
}

func (s *Server) writeProcess(params *WriteParams) (*WriteResponse, error) {
	state := s.lookup(params.ProcessID)
	if state == nil {
		return &WriteResponse{Status: "unknownProcess"}, nil
	}
	return state.write(params)
}

func (s *Server) terminateProcess(params *TerminateParams) (*TerminateResponse, error) {
	state := s.lookup(params.ProcessID)
	if state == nil {
		return &TerminateResponse{Running: false}, nil
	}
	return state.terminate(), nil
}

func (s *Server) signalProcess(params *SignalParams) (*SignalResponse, error) {
	state := s.lookup(params.ProcessID)
	if state == nil {
		return &SignalResponse{}, nil
	}
	return state.signal(params)
}

func (s *Server) lookup(processID string) *processState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.processes[processID]
}

func readFile(params *FSReadFileParams) (*FSReadFileResponse, error) {
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &FSReadFileResponse{DataBase64: base64.StdEncoding.EncodeToString(data)}, nil
}

func (s *Server) openFile(params *FSOpenParams) (*FSOpenResponse, error) {
	if params == nil || strings.TrimSpace(params.HandleID) == "" {
		return nil, errors.New("handleId is required")
	}
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if old := s.handles[params.HandleID]; old != nil {
		_ = old.Close()
	}
	s.handles[params.HandleID] = file
	s.mu.Unlock()
	return &FSOpenResponse{HandleID: params.HandleID}, nil
}

func (s *Server) readBlock(params *FSReadBlockParams) (*FSReadBlockResponse, error) {
	if params == nil || strings.TrimSpace(params.HandleID) == "" {
		return nil, errors.New("handleId is required")
	}
	if params.Offset < 0 {
		return nil, errors.New("offset must be non-negative")
	}
	if params.Len < 0 {
		return nil, errors.New("len must be non-negative")
	}
	s.mu.Lock()
	file := s.handles[params.HandleID]
	s.mu.Unlock()
	if file == nil {
		return nil, fmt.Errorf("unknown file handle %s", params.HandleID)
	}
	buffer := make([]byte, params.Len)
	n, err := file.ReadAt(buffer, params.Offset)
	eof := errors.Is(err, io.EOF)
	if err != nil && !eof {
		return nil, err
	}
	return &FSReadBlockResponse{
		Chunk: base64.StdEncoding.EncodeToString(buffer[:n]),
		EOF:   eof,
	}, nil
}

func (s *Server) closeFile(params *FSCloseParams) (*FSCloseResponse, error) {
	if params == nil || strings.TrimSpace(params.HandleID) == "" {
		return nil, errors.New("handleId is required")
	}
	s.mu.Lock()
	file := s.handles[params.HandleID]
	delete(s.handles, params.HandleID)
	s.mu.Unlock()
	if file != nil {
		if err := file.Close(); err != nil {
			return nil, err
		}
	}
	return &FSCloseResponse{}, nil
}

func writeFile(params *FSWriteFileParams) (*FSWriteFileResponse, error) {
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(params.DataBase64)
	if err != nil {
		return nil, fmt.Errorf("fs/writeFile requires valid base64 dataBase64: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, err
	}
	return &FSWriteFileResponse{}, nil
}

func createDirectory(params *FSCreateDirectoryParams) (*FSCreateDirectoryResponse, error) {
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	recursive := true
	if params.Recursive != nil {
		recursive = *params.Recursive
	}
	if recursive {
		err = os.MkdirAll(path, 0o755)
	} else {
		err = os.Mkdir(path, 0o755)
	}
	if err != nil {
		return nil, err
	}
	return &FSCreateDirectoryResponse{}, nil
}

func getMetadata(params *FSGetMetadataParams) (*FSGetMetadataResponse, error) {
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		info = linkInfo
	}
	return &FSGetMetadataResponse{
		IsDirectory:  info.IsDir(),
		IsFile:       info.Mode().IsRegular(),
		IsSymlink:    linkInfo.Mode()&os.ModeSymlink != 0,
		Size:         info.Size(),
		CreatedAtMS:  createdAtMillis(info),
		ModifiedAtMS: info.ModTime().UnixMilli(),
	}, nil
}

func canonicalize(params *FSCanonicalizeParams) (*FSCanonicalizeResponse, error) {
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved, err = filepath.Abs(path)
	}
	if err != nil {
		return nil, err
	}
	uri, err := utils.FromHostNativePath(resolved)
	if err != nil {
		return nil, err
	}
	return &FSCanonicalizeResponse{Path: uri.String()}, nil
}

func readDirectory(params *FSReadDirectoryParams) (*FSReadDirectoryResponse, error) {
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]FSReadDirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, FSReadDirectoryEntry{
			FileName:    entry.Name(),
			IsDirectory: info.IsDir(),
			IsFile:      info.Mode().IsRegular(),
		})
	}
	return &FSReadDirectoryResponse{Entries: out}, nil
}

func removePath(params *FSRemoveParams) (*FSRemoveResponse, error) {
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	recursive := true
	if params.Recursive != nil {
		recursive = *params.Recursive
	}
	force := true
	if params.Force != nil {
		force = *params.Force
	}
	if recursive {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}
	if err != nil && !(force && os.IsNotExist(err)) {
		return nil, err
	}
	return &FSRemoveResponse{}, nil
}

func copyPath(params *FSCopyParams) (*FSCopyResponse, error) {
	source, err := resolvePath(params.SourcePath)
	if err != nil {
		return nil, err
	}
	destination, err := resolvePath(params.DestinationPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		if !params.Recursive {
			return nil, errors.New("fs/copy requires recursive: true when sourcePath is a directory")
		}
		descendant, err := destinationIsSameOrDescendant(source, destination)
		if err != nil {
			return nil, err
		}
		if descendant {
			return nil, errors.New("fs/copy cannot copy a directory to itself or one of its descendants")
		}
		if err := copyDirectory(source, destination); err != nil {
			return nil, err
		}
		return &FSCopyResponse{}, nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if err := copySymlink(source, destination); err != nil {
			return nil, err
		}
		return &FSCopyResponse{}, nil
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("fs/copy only supports regular files, directories, and symlinks")
	}
	if err := copyFile(source, destination, info.Mode()); err != nil {
		return nil, err
	}
	return &FSCopyResponse{}, nil
}

func walkPath(params *FSWalkParams) (*FSWalkResponse, error) {
	path, err := resolvePath(params.Path)
	if err != nil {
		return nil, err
	}
	options := params.Options
	if options.MaxDepth == 0 && options.MaxDirectories == 0 && options.MaxEntries == 0 {
		options = FSWalkOptions{
			MaxDepth:       maxWalkDepth,
			MaxDirectories: maxWalkDirectories,
			MaxEntries:     maxWalkEntries,
		}
	}
	if options.MaxDirectories <= 0 || options.MaxEntries <= 0 {
		return nil, errors.New("filesystem walk limits must be greater than zero")
	}
	if options.MaxDepth > maxWalkDepth || options.MaxDirectories > maxWalkDirectories || options.MaxEntries > maxWalkEntries {
		return nil, fmt.Errorf("filesystem walk limits exceed maximums: depth=%d, directories=%d, entries=%d", maxWalkDepth, maxWalkDirectories, maxWalkEntries)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	linkInfo, linkErr := os.Lstat(path)
	if linkErr != nil {
		return nil, linkErr
	}
	if !info.IsDir() || (linkInfo.Mode()&os.ModeSymlink != 0 && !options.FollowDirectorySymlinks) {
		return &FSWalkResponse{Entries: []FSWalkEntry{}, Errors: []FSWalkError{}}, nil
	}
	walker := &walkState{
		options: options,
		seen:    map[string]bool{},
	}
	identity := path
	if options.FollowDirectorySymlinks {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			identity = resolved
		}
	}
	walker.seen[filepath.Clean(identity)] = true
	walker.directoryCount = 1
	walker.walkDirectory(path, 0)
	entries := walker.entries
	if entries == nil {
		entries = []FSWalkEntry{}
	}
	errors := walker.errors
	if errors == nil {
		errors = []FSWalkError{}
	}
	return &FSWalkResponse{
		Entries:   entries,
		Errors:    errors,
		Truncated: walker.truncated,
	}, nil
}

func doHTTPRequest(ctx context.Context, params *HTTPRequestParams) (*HTTPRequestResponse, error) {
	if params == nil {
		return nil, errors.New("http/request params are required")
	}
	method := strings.TrimSpace(params.Method)
	if method == "" {
		method = http.MethodGet
	}
	if params.StreamResponse {
		return nil, requestError(-32600, "http/request streamResponse is not supported by this exec-server backend")
	}
	var body io.Reader
	if params.BodyBase64 != nil {
		data, err := base64.StdEncoding.DecodeString(*params.BodyBase64)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, params.URL, body)
	if err != nil {
		return nil, err
	}
	for _, header := range params.Headers {
		name := strings.TrimSpace(header.Name)
		if !validHTTPHeaderName(name) || !validHTTPHeaderValue(header.Value) {
			continue
		}
		req.Header.Add(name, header.Value)
	}
	client := &http.Client{}
	if params.TimeoutMS != nil && *params.TimeoutMS > 0 {
		client.Timeout = time.Duration(*params.TimeoutMS) * time.Millisecond
	}
	if strings.EqualFold(params.RedirectPolicy, "stop") {
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	headers := make([]HTTPHeader, 0, len(resp.Header))
	for name, values := range resp.Header {
		for _, value := range values {
			headers = append(headers, HTTPHeader{Name: name, Value: value})
		}
	}
	sort.Slice(headers, func(i, j int) bool {
		leftName := strings.ToLower(headers[i].Name)
		rightName := strings.ToLower(headers[j].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		if headers[i].Name != headers[j].Name {
			return headers[i].Name < headers[j].Name
		}
		return headers[i].Value < headers[j].Value
	})
	return &HTTPRequestResponse{
		Status:     resp.StatusCode,
		Headers:    headers,
		BodyBase64: base64.StdEncoding.EncodeToString(data),
	}, nil
}

func validHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

func validHTTPHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func (p *processState) failStart(startErr error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failure = startErr.Error()
	p.nextSeq++
	p.exitSequenced = true
	p.closedSequenced = true
	code := -1
	p.exitCode = &code
	p.exited = true
	p.closed = true
	p.openStreams = 0
	p.cond.Broadcast()
}

func (p *processState) capture(stream string, reader io.Reader) {
	buffer := make([]byte, 32*1024)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			data := append([]byte(nil), buffer[:n]...)
			p.mu.Lock()
			p.appendLocked(stream, data)
			p.mu.Unlock()
		}
		if err != nil {
			p.finishStream()
			return
		}
	}
}

func (p *processState) finish(waitErr error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if waitErr != nil {
		p.failure = waitErr.Error()
	}
	if p.cmd != nil && p.cmd.ProcessState != nil {
		code := p.cmd.ProcessState.ExitCode()
		p.exitCode = &code
	}
	p.exited = true
	if !p.exitSequenced {
		p.nextSeq++
		p.exitSequenced = true
	}
	if p.openStreams == 0 {
		p.closed = true
		if !p.closedSequenced {
			p.nextSeq++
			p.closedSequenced = true
		}
	}
	p.cond.Broadcast()
}

func (p *processState) finishStream() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.openStreams > 0 {
		p.openStreams--
	}
	if p.exited && p.openStreams == 0 {
		p.closed = true
		if !p.closedSequenced {
			p.nextSeq++
			p.closedSequenced = true
		}
	}
	p.cond.Broadcast()
}

func (p *processState) appendLocked(stream string, data []byte) {
	seq := p.nextSeq
	p.nextSeq++
	p.chunks = append(p.chunks, outputChunk{
		Seq:    seq,
		Stream: stream,
		Chunk:  base64.StdEncoding.EncodeToString(data),
	})
	p.retainedBytes += len(data)
	for p.retainedBytes > retainedOutputBytesPerProcess && len(p.chunks) > 0 {
		decoded, _ := base64.StdEncoding.DecodeString(p.chunks[0].Chunk)
		p.retainedBytes -= len(decoded)
		p.chunks = p.chunks[1:]
	}
	p.cond.Broadcast()
}

func (p *processState) read(after uint64, maxBytes *int, waitMS *uint64) *ReadResponse {
	deadline := time.Time{}
	if waitMS != nil && *waitMS > 0 {
		deadline = time.Now().Add(time.Duration(*waitMS) * time.Millisecond)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		response := p.readLocked(after, maxBytes)
		hasTerminalEvent := response.Exited && after < response.NextSeq-1
		if len(response.Chunks) > 0 || response.Closed || response.Failure != nil || hasTerminalEvent || waitMS == nil || *waitMS == 0 || !time.Now().Before(deadline) {
			return response
		}
		remaining := time.Until(deadline)
		timer := time.AfterFunc(remaining, func() {
			p.mu.Lock()
			p.cond.Broadcast()
			p.mu.Unlock()
		})
		p.cond.Wait()
		timer.Stop()
	}
}

func (p *processState) readLocked(after uint64, maxBytes *int) *ReadResponse {
	chunks := []outputChunk{}
	remaining := -1
	if maxBytes != nil && *maxBytes >= 0 {
		remaining = *maxBytes
	}
	for _, chunk := range p.chunks {
		if chunk.Seq <= after {
			continue
		}
		if remaining == 0 {
			break
		}
		if remaining > 0 {
			decoded, _ := base64.StdEncoding.DecodeString(chunk.Chunk)
			if len(decoded) > remaining {
				break
			}
			remaining -= len(decoded)
		}
		chunks = append(chunks, chunk)
	}
	nextSeq := p.nextSeq
	if maxBytes != nil && len(chunks) > 0 {
		nextSeq = chunks[len(chunks)-1].Seq + 1
	}
	var failure *string
	if p.failure != "" {
		value := p.failure
		failure = &value
	}
	return &ReadResponse{
		Chunks:   chunks,
		NextSeq:  nextSeq,
		Exited:   p.exited,
		ExitCode: p.exitCode,
		Closed:   p.closed,
		Failure:  failure,
	}
}

func (p *processState) write(params *WriteParams) (*WriteResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.TrimSpace(params.WriteID) == "" {
		return nil, errors.New("writeId must not be empty")
	}
	if p.exited {
		return &WriteResponse{Status: "stdinClosed"}, nil
	}
	if !p.pipeStdin || p.stdin == nil {
		return &WriteResponse{Status: "stdinClosed"}, nil
	}
	if p.seenWriteIDs[params.WriteID] {
		return &WriteResponse{Status: "accepted"}, nil
	}
	data, err := base64.StdEncoding.DecodeString(params.Chunk)
	if err != nil {
		return nil, err
	}
	if _, err := p.stdin.Write(data); err != nil {
		return &WriteResponse{Status: "stdinClosed"}, nil
	}
	p.seenWriteIDs[params.WriteID] = true
	return &WriteResponse{Status: "accepted"}, nil
}

func (p *processState) terminate() *TerminateResponse {
	p.mu.Lock()
	cmd := p.cmd
	running := !p.exited && cmd != nil && cmd.Process != nil
	p.mu.Unlock()
	if running {
		_ = cmd.Process.Kill()
	}
	return &TerminateResponse{Running: running}
}

func (p *processState) signal(params *SignalParams) (*SignalResponse, error) {
	p.mu.Lock()
	cmd := p.cmd
	running := !p.exited && cmd != nil && cmd.Process != nil
	p.mu.Unlock()
	if !running {
		return &SignalResponse{}, nil
	}
	if params != nil && params.Signal != "" && !strings.EqualFold(params.Signal, "interrupt") {
		return nil, fmt.Errorf("unsupported process signal %s", params.Signal)
	}
	if runtime.GOOS == "windows" {
		return nil, errors.New("process interrupt is not supported by this process backend")
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		return nil, fmt.Errorf("failed to signal process: %w", err)
	}
	return &SignalResponse{}, nil
}

func decodeParams(data json.RawMessage, target any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return requestError(-32602, "invalid params: "+err.Error())
	}
	return nil
}

func errorResponse(id RequestID, code int, message string) response {
	return response{ID: id, Error: &rpcError{Code: code, Message: message}}
}

func errorResponseForRequest(id RequestID, err error) response {
	var failure *requestFailure
	if errors.As(err, &failure) {
		return errorResponse(id, failure.code, failure.message)
	}
	return errorResponse(id, -32603, err.Error())
}

func requestError(code int, message string) error {
	return &requestFailure{code: code, message: message}
}

func hasJSONValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func localEnvironmentInfo() *EnvironmentInfo {
	detected := shell.UltimateFallbackShell()
	cwd := ""
	if current, err := os.Getwd(); err == nil {
		if uri, err := utils.FromHostNativePath(current); err == nil {
			cwd = uri.String()
		} else {
			cwd = current
		}
	}
	return &EnvironmentInfo{
		Shell: ShellInfo{Name: detected.Name(), Path: detected.ShellPath},
		CWD:   stringPtr(cwd),
	}
}

func envPairs(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for key, value := range env {
		if strings.TrimSpace(key) == "" {
			continue
		}
		out = append(out, key+"="+value)
	}
	return out
}

func childEnv(params *ExecParams) map[string]string {
	if params == nil {
		return map[string]string{}
	}
	if params.EnvPolicy == nil {
		return copyEnv(params.Env)
	}
	env := populateEnv(params.EnvPolicy, os.Environ())
	for key, value := range params.Env {
		env[key] = value
	}
	if runtime.GOOS == "windows" {
		if !hasEnvKey(env, "PATHEXT") {
			env["PATHEXT"] = ".COM;.EXE;.BAT;.CMD"
		}
	}
	return env
}

func populateEnv(policy *ExecEnvPolicy, environ []string) map[string]string {
	env := map[string]string{}
	inherit := "all"
	if policy != nil && strings.TrimSpace(policy.Inherit) != "" {
		inherit = strings.ToLower(strings.TrimSpace(policy.Inherit))
	}
	switch inherit {
	case "none":
	case "core":
		for _, pair := range environ {
			key, value, ok := splitEnv(pair)
			if ok && isCoreEnv(key) {
				env[key] = value
			}
		}
	default:
		for _, pair := range environ {
			key, value, ok := splitEnv(pair)
			if ok {
				env[key] = value
			}
		}
	}
	if policy == nil {
		return env
	}
	if !policy.IgnoreDefaultExcludes {
		for key := range env {
			if wildcardMatchAny([]string{"*KEY*", "*SECRET*", "*TOKEN*"}, key) {
				delete(env, key)
			}
		}
	}
	if len(policy.Exclude) > 0 {
		for key := range env {
			if wildcardMatchAny(policy.Exclude, key) {
				delete(env, key)
			}
		}
	}
	for key, value := range policy.Set {
		env[key] = value
	}
	if len(policy.IncludeOnly) > 0 {
		for key := range env {
			if !wildcardMatchAny(policy.IncludeOnly, key) {
				delete(env, key)
			}
		}
	}
	return env
}

func copyEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for key, value := range env {
		out[key] = value
	}
	return out
}

func splitEnv(pair string) (string, string, bool) {
	index := strings.Index(pair, "=")
	if index <= 0 {
		return "", "", false
	}
	return pair[:index], pair[index+1:], true
}

func hasEnvKey(env map[string]string, key string) bool {
	for existing := range env {
		if strings.EqualFold(existing, key) {
			return true
		}
	}
	return false
}

func isCoreEnv(key string) bool {
	var core []string
	if runtime.GOOS == "windows" {
		core = []string{
			"PATH", "PATHEXT", "SHELL", "COMSPEC", "SYSTEMROOT", "SYSTEMDRIVE",
			"USERNAME", "USERDOMAIN", "USERPROFILE", "HOMEDRIVE", "HOMEPATH",
			"PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432", "PROGRAMDATA",
			"LOCALAPPDATA", "APPDATA", "TEMP", "TMP", "TMPDIR", "POWERSHELL", "PWSH",
		}
	} else {
		core = []string{"PATH", "SHELL", "TMPDIR", "TEMP", "TMP", "HOME", "LANG", "LC_ALL", "LC_CTYPE", "LOGNAME", "USER"}
	}
	for _, allowed := range core {
		if strings.EqualFold(allowed, key) {
			return true
		}
	}
	return false
}

func wildcardMatchAny(patterns []string, key string) bool {
	for _, pattern := range patterns {
		if wildcardMatch(strings.ToLower(pattern), strings.ToLower(key)) {
			return true
		}
	}
	return false
}

func wildcardMatch(pattern string, value string) bool {
	if pattern == "" {
		return value == ""
	}
	ok, err := filepath.Match(pattern, value)
	if err == nil {
		return ok
	}
	return pattern == value
}

func resolvePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("path is required")
	}
	if strings.HasPrefix(raw, "file://") {
		uri, err := utils.Parse(raw)
		if err != nil {
			return "", err
		}
		raw = uri.NativePathString()
	}
	if filepath.IsAbs(raw) {
		return raw, nil
	}
	return filepath.Abs(raw)
}

func copyFile(source string, destination string, mode os.FileMode) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	return os.WriteFile(destination, data, mode.Perm())
}

func copyDirectory(source string, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return copySymlink(path, target)
		}
		if info.Mode().IsRegular() {
			return copyFile(path, target, info.Mode())
		}
		return nil
	})
}

func copySymlink(source string, destination string) error {
	target, err := os.Readlink(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	return os.Symlink(target, destination)
}

func destinationIsSameOrDescendant(source string, destination string) (bool, error) {
	resolvedSource, err := filepath.Abs(source)
	if err != nil {
		return false, err
	}
	if canonical, err := filepath.EvalSymlinks(resolvedSource); err == nil {
		resolvedSource = canonical
	}
	resolvedDestination, err := resolveExistingPath(destination)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(resolvedSource, resolvedDestination)
	if err != nil {
		return false, err
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
}

func resolveExistingPath(path string) (string, error) {
	clean := filepath.Clean(path)
	suffix := []string{}
	for {
		if _, err := os.Lstat(clean); err == nil {
			resolved, err := filepath.EvalSymlinks(clean)
			if err != nil {
				resolved, err = filepath.Abs(clean)
			}
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(clean)
		if parent == clean {
			return filepath.Abs(path)
		}
		suffix = append(suffix, filepath.Base(clean))
		clean = parent
	}
}

type walkState struct {
	options        FSWalkOptions
	entries        []FSWalkEntry
	errors         []FSWalkError
	truncated      bool
	seen           map[string]bool
	directoryCount int
	entryCount     int
	responseBytes  int
}

func (w *walkState) walkDirectory(directory string, depth int) {
	if w.truncated {
		return
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		w.pushError(directory, err)
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		if w.entryCount == w.options.MaxEntries {
			w.truncated = true
			return
		}
		w.entryCount++
		path := filepath.Join(directory, entry.Name())
		info, statErr := os.Stat(path)
		linkInfo, linkErr := os.Lstat(path)
		if statErr != nil {
			w.pushError(path, statErr)
			continue
		}
		if linkErr != nil {
			w.pushError(path, linkErr)
			continue
		}
		if linkInfo.Mode()&os.ModeSymlink != 0 && (!w.options.FollowDirectorySymlinks || !info.IsDir()) {
			continue
		}
		kind := ""
		if info.IsDir() {
			kind = "directory"
		} else if info.Mode().IsRegular() {
			kind = "file"
		} else {
			continue
		}
		uri := pathToURI(path)
		if !w.reserve(len(uri)) {
			return
		}
		w.entries = append(w.entries, FSWalkEntry{Path: uri, Kind: kind})
		if kind == "directory" && depth < w.options.MaxDepth {
			identity := path
			if w.options.FollowDirectorySymlinks {
				if resolved, err := filepath.EvalSymlinks(path); err == nil {
					identity = resolved
				} else {
					w.pushError(path, err)
					continue
				}
			}
			identity = filepath.Clean(identity)
			if w.seen[identity] {
				continue
			}
			w.seen[identity] = true
			if w.directoryCount == w.options.MaxDirectories {
				w.truncated = true
				continue
			}
			w.directoryCount++
			w.walkDirectory(path, depth+1)
		}
	}
}

func (w *walkState) pushError(path string, err error) {
	message := err.Error()
	uri := pathToURI(path)
	if !w.reserve(len(uri) + len(message)) {
		return
	}
	w.errors = append(w.errors, FSWalkError{Path: uri, Message: message})
}

func (w *walkState) reserve(contentBytes int) bool {
	total := w.responseBytes + contentBytes + walkResponseItemOverhead
	if total > maxWalkResponseBytes {
		w.truncated = true
		return false
	}
	w.responseBytes = total
	return true
}

func pathToURI(path string) string {
	uri, err := utils.FromHostNativePath(path)
	if err != nil {
		return path
	}
	return uri.String()
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func ListenURL(listen string) string {
	if strings.TrimSpace(listen) == "" || listen == DefaultListenURL {
		return DefaultListenURL
	}
	if listen == "stdio" || listen == "stdio://" {
		return "stdio"
	}
	if runtime.GOOS == "windows" && strings.EqualFold(listen, "unix://") {
		return "stdio"
	}
	return listen
}

func ResolveCWD(cwd string) string {
	if cwd == "" {
		current, _ := os.Getwd()
		return current
	}
	if strings.HasPrefix(cwd, "file://") {
		uri, err := utils.Parse(cwd)
		if err == nil {
			return uri.NativePathString()
		}
	}
	if filepath.IsAbs(cwd) {
		return cwd
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	return abs
}
