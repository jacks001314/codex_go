package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	execserverclient "codex_go/execserver"

	"github.com/coder/websocket"
)

var ErrInvalidEnvironmentRequest = errors.New("invalid environment request")

const defaultEnvironmentConnectTimeout = 10 * time.Second

type EnvironmentAddParams struct {
	EnvironmentID    string  `json:"environmentId"`
	ExecServerURL    string  `json:"execServerUrl"`
	ConnectTimeoutMS *uint64 `json:"connectTimeoutMs,omitempty"`
}

func (p *EnvironmentAddParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidEnvironmentRequest)
	}
	if strings.TrimSpace(p.EnvironmentID) == "" {
		return fmt.Errorf("%w: environmentId is required", ErrInvalidEnvironmentRequest)
	}
	parsed, err := url.Parse(strings.TrimSpace(p.ExecServerURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%w: execServerUrl must be absolute", ErrInvalidEnvironmentRequest)
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return fmt.Errorf("%w: execServerUrl must use ws or wss", ErrInvalidEnvironmentRequest)
	}
	return nil
}

type EnvironmentAddResponse struct{}

type EnvironmentInfoParams struct {
	EnvironmentID string `json:"environmentId"`
}

func (p *EnvironmentInfoParams) Validate() error {
	if p == nil || strings.TrimSpace(p.EnvironmentID) == "" {
		return fmt.Errorf("%w: environmentId is required", ErrInvalidEnvironmentRequest)
	}
	return nil
}

type EnvironmentInfoResponse struct {
	Shell EnvironmentShellInfo `json:"shell"`
	CWD   *string              `json:"cwd"`
}

type EnvironmentStatusParams struct {
	EnvironmentID string `json:"environmentId"`
}

func (p *EnvironmentStatusParams) Validate() error {
	if p == nil || strings.TrimSpace(p.EnvironmentID) == "" {
		return fmt.Errorf("%w: environmentId is required", ErrInvalidEnvironmentRequest)
	}
	return nil
}

type EnvironmentStatusKind string

const (
	EnvironmentStatusReady        EnvironmentStatusKind = "ready"
	EnvironmentStatusPending      EnvironmentStatusKind = "pending"
	EnvironmentStatusDisconnected EnvironmentStatusKind = "disconnected"
	EnvironmentStatusUnknown      EnvironmentStatusKind = "unknown"
)

type EnvironmentStatusResponse struct {
	Status EnvironmentStatusKind `json:"status"`
	Error  *string               `json:"error,omitempty"`
}

type EnvironmentShellInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (s *EnvironmentShellInfo) Validate() error {
	if s == nil {
		return fmt.Errorf("%w: shell is nil", ErrInvalidEnvironmentRequest)
	}
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("%w: shell name is required", ErrInvalidEnvironmentRequest)
	}
	if strings.TrimSpace(s.Path) == "" {
		return fmt.Errorf("%w: shell path is required", ErrInvalidEnvironmentRequest)
	}
	return nil
}

type EnvironmentRecord struct {
	EnvironmentID    string
	ExecServerURL    string
	NoiseProvider    execserverclient.NoiseRendezvousConnectProvider
	ConnectTimeoutMS *uint64
	Shell            EnvironmentShellInfo
	CWD              *string
	InfoOverride     bool
}

type EnvironmentManager struct {
	mu           sync.Mutex
	defaultShell EnvironmentShellInfo
	defaultCWD   *string
	records      map[string]EnvironmentRecord
}

func NewEnvironmentManager(defaultShell EnvironmentShellInfo, defaultCWD string) *EnvironmentManager {
	return &EnvironmentManager{
		defaultShell: defaultShell,
		defaultCWD:   pathURI(defaultCWD),
		records:      map[string]EnvironmentRecord{},
	}
}

func (m *EnvironmentManager) Add(params *EnvironmentAddParams) (*EnvironmentAddResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record := EnvironmentRecord{
		EnvironmentID:    strings.TrimSpace(params.EnvironmentID),
		ExecServerURL:    strings.TrimSpace(params.ExecServerURL),
		ConnectTimeoutMS: cloneUint64Ptr(params.ConnectTimeoutMS),
		Shell:            m.defaultShell,
		CWD:              cloneString(m.defaultCWD),
	}
	m.records[record.EnvironmentID] = record
	return &EnvironmentAddResponse{}, nil
}

func (m *EnvironmentManager) AddNoise(environmentID string, provider execserverclient.NoiseRendezvousConnectProvider) error {
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return fmt.Errorf("%w: environmentId is required", ErrInvalidEnvironmentRequest)
	}
	if provider == nil {
		return fmt.Errorf("%w: Noise rendezvous provider is required", ErrInvalidEnvironmentRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[environmentID] = EnvironmentRecord{
		EnvironmentID: environmentID,
		NoiseProvider: provider,
		Shell:         m.defaultShell,
		CWD:           cloneString(m.defaultCWD),
	}
	return nil
}

func (m *EnvironmentManager) SetInfo(environmentID string, shell EnvironmentShellInfo, cwd string) error {
	if strings.TrimSpace(environmentID) == "" {
		return fmt.Errorf("%w: environmentId is required", ErrInvalidEnvironmentRequest)
	}
	if err := shell.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record := m.records[environmentID]
	if record.EnvironmentID == "" {
		record.EnvironmentID = environmentID
	}
	record.Shell = shell
	record.CWD = pathURI(cwd)
	record.InfoOverride = true
	m.records[environmentID] = record
	return nil
}

func (m *EnvironmentManager) Info(params *EnvironmentInfoParams) (*EnvironmentInfoResponse, error) {
	return m.InfoContext(context.Background(), params)
}

func (m *EnvironmentManager) InfoContext(ctx context.Context, params *EnvironmentInfoParams) (*EnvironmentInfoResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	record, ok := m.records[params.EnvironmentID]
	defaultShell := m.defaultShell
	defaultCWD := cloneString(m.defaultCWD)
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: unknown environment id `%s`", ErrInvalidEnvironmentRequest, params.EnvironmentID)
	}
	if !record.InfoOverride {
		info, err := fetchRemoteEnvironmentInfo(ctx, &record)
		if err != nil {
			return nil, fmt.Errorf("failed to get info for environment `%s`: %w", params.EnvironmentID, err)
		}
		return info, nil
	}
	shell := record.Shell
	if strings.TrimSpace(shell.Name) == "" {
		shell = defaultShell
	}
	cwd := record.CWD
	if cwd == nil {
		cwd = defaultCWD
	}
	return &EnvironmentInfoResponse{Shell: shell, CWD: cloneString(cwd)}, nil
}

func (m *EnvironmentManager) Status(params *EnvironmentStatusParams) (*EnvironmentStatusResponse, error) {
	return m.StatusContext(context.Background(), params)
}

func (m *EnvironmentManager) StatusContext(ctx context.Context, params *EnvironmentStatusParams) (*EnvironmentStatusResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	record, ok := m.records[params.EnvironmentID]
	m.mu.Unlock()
	if !ok {
		return &EnvironmentStatusResponse{
			Status: EnvironmentStatusUnknown,
			Error:  environmentStringPtr(fmt.Sprintf("unknown environment id `%s`", params.EnvironmentID)),
		}, nil
	}
	if record.InfoOverride {
		return &EnvironmentStatusResponse{Status: EnvironmentStatusReady}, nil
	}
	if strings.TrimSpace(record.ExecServerURL) == "" && record.NoiseProvider == nil {
		return &EnvironmentStatusResponse{Status: EnvironmentStatusPending}, nil
	}
	status, err := fetchRemoteEnvironmentStatus(ctx, &record)
	if err != nil {
		return &EnvironmentStatusResponse{
			Status: EnvironmentStatusDisconnected,
			Error:  environmentStringPtr(err.Error()),
		}, nil
	}
	if status == nil || status.Status == "" {
		return &EnvironmentStatusResponse{Status: EnvironmentStatusReady}, nil
	}
	return status, nil
}

func (m *EnvironmentManager) Remove(environmentID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.records[environmentID]; !ok {
		return false
	}
	delete(m.records, environmentID)
	return true
}

func (m *EnvironmentManager) List() []EnvironmentRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]EnvironmentRecord, 0, len(m.records))
	for _, record := range m.records {
		record.ConnectTimeoutMS = cloneUint64Ptr(record.ConnectTimeoutMS)
		record.CWD = cloneString(record.CWD)
		out = append(out, record)
	}
	for i := 1; i < len(out); i++ {
		current := out[i]
		j := i - 1
		for j >= 0 && out[j].EnvironmentID > current.EnvironmentID {
			out[j+1] = out[j]
			j--
		}
		out[j+1] = current
	}
	return out
}

func (m *EnvironmentManager) Record(environmentID string) (*EnvironmentRecord, bool) {
	if m == nil {
		return nil, false
	}
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.records[environmentID]
	if !ok {
		return nil, false
	}
	record.ConnectTimeoutMS = cloneUint64Ptr(record.ConnectTimeoutMS)
	record.CWD = cloneString(record.CWD)
	return &record, true
}

func pathURI(path string) *string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if strings.HasPrefix(path, "file://") {
		return &path
	}
	cleaned, err := filepath.Abs(path)
	if err != nil {
		cleaned = filepath.Clean(path)
	}
	cleaned = filepath.ToSlash(cleaned)
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	value := "file://" + cleaned
	return &value
}

func cloneUint64Ptr(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func environmentStringPtr(value string) *string {
	return &value
}

type execServerJSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc,omitempty"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type execServerJSONRPCResponse struct {
	ID     RequestID        `json:"id"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *ResponseError   `json:"error,omitempty"`
	Method string           `json:"method,omitempty"`
	Params *json.RawMessage `json:"params,omitempty"`
}

func fetchRemoteEnvironmentInfo(ctx context.Context, record *EnvironmentRecord) (*EnvironmentInfoResponse, error) {
	if record == nil {
		return nil, errors.New("environment record is nil")
	}
	ctx, cancel := context.WithTimeout(ctx, environmentConnectTimeout(record.ConnectTimeoutMS))
	defer cancel()
	if record.NoiseProvider != nil {
		client, err := execserverclient.DialNoiseRendezvousClient(ctx, record.NoiseProvider, execserverclient.DialClientOptions{ClientName: "codex-go"})
		if err != nil {
			return nil, err
		}
		defer client.Close()
		info, err := client.EnvironmentInfo(ctx)
		if err != nil {
			return nil, err
		}
		response := &EnvironmentInfoResponse{
			Shell: EnvironmentShellInfo{Name: info.Shell.Name, Path: info.Shell.Path},
			CWD:   cloneString(info.CWD),
		}
		if err := response.Shell.Validate(); err != nil {
			return nil, err
		}
		return response, nil
	}
	conn, _, err := websocket.Dial(ctx, record.ExecServerURL, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"clientName": "codex-go",
		},
	}); err != nil {
		return nil, err
	}
	if _, err := readExecServerResponse(ctx, conn, 1); err != nil {
		return nil, err
	}
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialized",
	}); err != nil {
		return nil, err
	}
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "environment/info",
	}); err != nil {
		return nil, err
	}
	result, err := readExecServerResponse(ctx, conn, 2)
	if err != nil {
		return nil, err
	}
	var info EnvironmentInfoResponse
	if err := json.Unmarshal(result, &info); err != nil {
		return nil, err
	}
	if err := info.Shell.Validate(); err != nil {
		return nil, err
	}
	return &info, nil
}

func fetchRemoteEnvironmentStatus(ctx context.Context, record *EnvironmentRecord) (*EnvironmentStatusResponse, error) {
	if record == nil {
		return nil, errors.New("environment record is nil")
	}
	ctx, cancel := context.WithTimeout(ctx, environmentConnectTimeout(record.ConnectTimeoutMS))
	defer cancel()
	if record.NoiseProvider != nil {
		client, err := execserverclient.DialNoiseRendezvousClient(ctx, record.NoiseProvider, execserverclient.DialClientOptions{ClientName: "codex-go"})
		if err != nil {
			return nil, err
		}
		defer client.Close()
		status, err := client.EnvironmentStatus(ctx)
		if err != nil {
			return nil, err
		}
		return &EnvironmentStatusResponse{Status: EnvironmentStatusKind(status.Status)}, nil
	}
	conn, _, err := websocket.Dial(ctx, record.ExecServerURL, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"clientName": "codex-go",
		},
	}); err != nil {
		return nil, err
	}
	if _, err := readExecServerResponse(ctx, conn, 1); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &EnvironmentStatusResponse{Status: EnvironmentStatusPending}, nil
		}
		return nil, err
	}
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialized",
	}); err != nil {
		return nil, err
	}
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "environment/status",
	}); err != nil {
		return nil, err
	}
	result, err := readExecServerResponse(ctx, conn, 2)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &EnvironmentStatusResponse{Status: EnvironmentStatusPending}, nil
		}
		return nil, err
	}
	var status EnvironmentStatusResponse
	if err := json.Unmarshal(result, &status); err != nil {
		return nil, err
	}
	if status.Status == "" {
		status.Status = EnvironmentStatusReady
	}
	return &status, nil
}

func environmentConnectTimeout(connectTimeoutMS *uint64) time.Duration {
	if connectTimeoutMS == nil {
		return defaultEnvironmentConnectTimeout
	}
	timeout := time.Duration(*connectTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		return defaultEnvironmentConnectTimeout
	}
	return timeout
}

func writeExecServerJSON(ctx context.Context, conn *websocket.Conn, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

func readExecServerResponse(ctx context.Context, conn *websocket.Conn, id int) (json.RawMessage, error) {
	for {
		messageType, data, err := conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		if messageType != websocket.MessageText && messageType != websocket.MessageBinary {
			continue
		}
		var response execServerJSONRPCResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return nil, err
		}
		if response.ID.String() != fmt.Sprint(id) {
			continue
		}
		if response.Error != nil {
			if strings.TrimSpace(response.Error.Message) != "" {
				return nil, errors.New(response.Error.Message)
			}
			return nil, fmt.Errorf("exec-server request %d failed with code %d", id, response.Error.Code)
		}
		if len(response.Result) == 0 {
			return []byte("{}"), nil
		}
		return response.Result, nil
	}
}
