package appserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"codex_go/mcp"
	"codex_go/rollout"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

const (
	MethodInitialize                         Method = "initialize"
	MethodThreadStart                        Method = "thread/start"
	MethodThreadResume                       Method = "thread/resume"
	MethodThreadFork                         Method = "thread/fork"
	MethodThreadArchive                      Method = "thread/archive"
	MethodThreadUnarchive                    Method = "thread/unarchive"
	MethodThreadDelete                       Method = "thread/delete"
	MethodThreadIncrementElicitation         Method = "thread/increment_elicitation"
	MethodThreadDecrementElicitation         Method = "thread/decrement_elicitation"
	MethodThreadIncrementElicitationLegacy   Method = "thread/incrementElicitation"
	MethodThreadDecrementElicitationLegacy   Method = "thread/decrementElicitation"
	MethodThreadSetName                      Method = "thread/setName"
	MethodThreadNameSet                      Method = "thread/name/set"
	MethodThreadGoalSet                      Method = "thread/goal/set"
	MethodThreadGoalGet                      Method = "thread/goal/get"
	MethodThreadGoalClear                    Method = "thread/goal/clear"
	MethodThreadUnsubscribe                  Method = "thread/unsubscribe"
	MethodThreadMemoryModeSet                Method = "thread/memoryMode/set"
	MethodMemoryReset                        Method = "memory/reset"
	MethodThreadCompactStart                 Method = "thread/compact/start"
	MethodThreadApproveGuardianDeniedAction  Method = "thread/approveGuardianDeniedAction"
	MethodThreadMetadataUpdate               Method = "thread/metadata/update"
	MethodThreadSectionMove                  Method = "thread/section/move"
	MethodThreadSettingsUpdate               Method = "thread/settings/update"
	MethodThreadShellCommand                 Method = "thread/shellCommand"
	MethodThreadBackgroundTerminalsClean     Method = "thread/backgroundTerminals/clean"
	MethodThreadBackgroundTerminalsList      Method = "thread/backgroundTerminals/list"
	MethodThreadBackgroundTerminalsTerminate Method = "thread/backgroundTerminals/terminate"
	MethodThreadRollback                     Method = "thread/rollback"
	MethodThreadRevert                       Method = "thread/revert"
	MethodThreadQueueAdd                     Method = "thread/queue/add"
	MethodThreadQueueList                    Method = "thread/queue/list"
	MethodThreadQueueUpdate                  Method = "thread/queue/update"
	MethodThreadQueueDelete                  Method = "thread/queue/delete"
	MethodThreadQueueReorder                 Method = "thread/queue/reorder"
	MethodThreadQueueStart                   Method = "thread/queue/start"
	MethodThreadList                         Method = "thread/list"
	MethodThreadSectionList                  Method = "threadSection/list"
	MethodThreadSectionCreate                Method = "threadSection/create"
	MethodThreadSectionUpdate                Method = "threadSection/update"
	MethodThreadSectionDelete                Method = "threadSection/delete"
	MethodThreadSearch                       Method = "thread/search"
	MethodThreadSearchOccurrences            Method = "thread/searchOccurrences"
	MethodThreadLoadedList                   Method = "thread/loaded/list"
	MethodThreadRead                         Method = "thread/read"
	MethodThreadItemsList                    Method = "thread/items/list"
	MethodThreadTurnsList                    Method = "thread/turns/list"
	MethodThreadInjectItems                  Method = "thread/inject_items"
	MethodThreadRealtimeStart                Method = "thread/realtime/start"
	MethodThreadRealtimeAppendAudio          Method = "thread/realtime/appendAudio"
	MethodThreadRealtimeAppendText           Method = "thread/realtime/appendText"
	MethodThreadRealtimeAppendSpeech         Method = "thread/realtime/appendSpeech"
	MethodThreadRealtimeStop                 Method = "thread/realtime/stop"
	MethodThreadRealtimeListVoices           Method = "thread/realtime/listVoices"

	MethodTurnStart                              Method = "turn/start"
	MethodTurnSteer                              Method = "turn/steer"
	MethodTurnInterrupt                          Method = "turn/interrupt"
	MethodReviewStart                            Method = "review/start"
	MethodExperimentalFeatureList                Method = "experimentalFeature/list"
	MethodExperimentalFeatureSet                 Method = "experimentalFeature/enablement/set"
	MethodAppList                                Method = "app/list"
	MethodAppRead                                Method = "app/read"
	MethodAppInstalled                           Method = "app/installed"
	MethodGetAuthStatus                          Method = "getAuthStatus"
	MethodGetConversationSummary                 Method = "getConversationSummary"
	MethodGitDiffToRemote                        Method = "gitDiffToRemote"
	MethodFuzzyFileSearch                        Method = "fuzzyFileSearch"
	MethodFuzzyFileSearchStart                   Method = "fuzzyFileSearch/sessionStart"
	MethodFuzzyFileSearchUpdate                  Method = "fuzzyFileSearch/sessionUpdate"
	MethodFuzzyFileSearchStop                    Method = "fuzzyFileSearch/sessionStop"
	MethodHooksList                              Method = "hooks/list"
	MethodSkillsList                             Method = "skills/list"
	MethodSkillsExtraRootsSet                    Method = "skills/extraRoots/set"
	MethodSkillsConfigWrite                      Method = "skills/config/write"
	MethodMarketplaceAdd                         Method = "marketplace/add"
	MethodMarketplaceRemove                      Method = "marketplace/remove"
	MethodMarketplaceUpgrade                     Method = "marketplace/upgrade"
	MethodPluginList                             Method = "plugin/list"
	MethodPluginInstalled                        Method = "plugin/installed"
	MethodPluginRead                             Method = "plugin/read"
	MethodPluginSkillRead                        Method = "plugin/skill/read"
	MethodPluginShareSave                        Method = "plugin/share/save"
	MethodPluginShareUpdateTargets               Method = "plugin/share/updateTargets"
	MethodPluginShareList                        Method = "plugin/share/list"
	MethodPluginShareCheckout                    Method = "plugin/share/checkout"
	MethodPluginShareDelete                      Method = "plugin/share/delete"
	MethodPluginInstall                          Method = "plugin/install"
	MethodPluginUninstall                        Method = "plugin/uninstall"
	MethodModelList                              Method = "model/list"
	MethodModelProviderCapabilitiesRead          Method = "modelProvider/capabilities/read"
	MethodPermissionProfileList                  Method = "permissionProfile/list"
	MethodCollaborationModeList                  Method = "collaborationMode/list"
	MethodMockExperimentalMethod                 Method = "mock/experimentalMethod"
	MethodMCPServerOauthLogin                    Method = "mcpServer/oauth/login"
	MethodMCPServerOauthCancel                   Method = "mcpServer/oauth/cancel"
	MethodMCPServerRefresh                       Method = "mcpServer/refresh"
	MethodConfigMCPServerReload                  Method = "config/mcpServer/reload"
	MethodMCPServerStatusList                    Method = "mcpServerStatus/list"
	MethodMCPServerResourceRead                  Method = "mcpServer/resource/read"
	MethodMCPServerToolCall                      Method = "mcpServer/tool/call"
	MethodFSReadFile                             Method = "fs/readFile"
	MethodFSWriteFile                            Method = "fs/writeFile"
	MethodFSCreateDirectory                      Method = "fs/createDirectory"
	MethodFSGetMetadata                          Method = "fs/getMetadata"
	MethodFSReadDirectory                        Method = "fs/readDirectory"
	MethodFSRemove                               Method = "fs/remove"
	MethodFSCopy                                 Method = "fs/copy"
	MethodFSWatch                                Method = "fs/watch"
	MethodFSUnwatch                              Method = "fs/unwatch"
	MethodRemoteControlEnable                    Method = "remoteControl/enable"
	MethodRemoteControlDisable                   Method = "remoteControl/disable"
	MethodRemoteControlStatusRead                Method = "remoteControl/status/read"
	MethodRemoteControlPairingStart              Method = "remoteControl/pairing/start"
	MethodRemoteControlPairingStatus             Method = "remoteControl/pairing/status"
	MethodRemoteControlClientsList               Method = "remoteControl/client/list"
	MethodRemoteControlClientsRevoke             Method = "remoteControl/client/revoke"
	MethodEnvironmentAdd                         Method = "environment/add"
	MethodEnvironmentInfo                        Method = "environment/info"
	MethodEnvironmentStatus                      Method = "environment/status"
	MethodWindowsSandboxSetupStart               Method = "windowsSandbox/setupStart"
	MethodWindowsSandboxReadiness                Method = "windowsSandbox/readiness"
	MethodFeedbackUpload                         Method = "feedback/upload"
	MethodConfigRead                             Method = "config/read"
	MethodConfigValueWrite                       Method = "config/value/write"
	MethodConfigBatchWrite                       Method = "config/batchWrite"
	MethodConfigRequirementsRead                 Method = "configRequirements/read"
	MethodServerDiagnostics                      Method = "server/diagnostics"
	MethodExternalAgentConfigDetect              Method = "externalAgentConfig/detect"
	MethodExternalAgentConfigImport              Method = "externalAgentConfig/import"
	MethodExternalAgentConfigImportHistoryRecord Method = "externalAgentConfig/import/recordHistory"
	MethodExternalAgentConfigImportHistoriesRead Method = "externalAgentConfig/import/readHistories"
	MethodLoginAccount                           Method = "account/login/start"
	MethodCancelLoginAccount                     Method = "account/login/cancel"
	MethodAccountSessionsAdd                     Method = "account/sessions/add"
	MethodAccountSessionsList                    Method = "account/sessions/list"
	MethodAccountSessionsLogout                  Method = "account/sessions/logout"
	MethodAccountSessionsSwitch                  Method = "account/sessions/switch"
	MethodLogoutAccount                          Method = "account/logout"
	MethodGetAccount                             Method = "account/read"
	MethodGetAccountRateLimits                   Method = "account/rateLimits/read"
	MethodConsumeAccountRateLimitResetCredit     Method = "account/rateLimitResetCredit/consume"
	MethodGetAccountTokenUsage                   Method = "account/usage/read"
	MethodGetWorkspaceMessages                   Method = "account/workspaceMessages/read"
	MethodSendAddCreditsNudgeEmail               Method = "account/sendAddCreditsNudgeEmail"
	MethodProcessSpawn                           Method = "process/spawn"
	MethodProcessWriteStdin                      Method = "process/writeStdin"
	MethodProcessKill                            Method = "process/kill"
	MethodProcessResizePty                       Method = "process/resizePty"
	MethodCommandExec                            Method = "command/exec"
	MethodCommandExecWrite                       Method = "command/exec/write"
	MethodCommandExecTerminate                   Method = "command/exec/terminate"
	MethodCommandExecResize                      Method = "command/exec/resize"

	NotificationThreadStarted                   NotificationMethod = "thread/started"
	NotificationThreadStatusChanged             NotificationMethod = "thread/status/changed"
	NotificationThreadArchived                  NotificationMethod = "thread/archived"
	NotificationThreadDeleted                   NotificationMethod = "thread/deleted"
	NotificationThreadUnarchived                NotificationMethod = "thread/unarchived"
	NotificationThreadClosed                    NotificationMethod = "thread/closed"
	NotificationSkillsChanged                   NotificationMethod = "skills/changed"
	NotificationThreadNameUpdated               NotificationMethod = "thread/name/updated"
	NotificationThreadGoalUpdated               NotificationMethod = "thread/goal/updated"
	NotificationThreadGoalCleared               NotificationMethod = "thread/goal/cleared"
	NotificationThreadEnvironmentConnected      NotificationMethod = "thread/environment/connected"
	NotificationThreadEnvironmentDisconnected   NotificationMethod = "thread/environment/disconnected"
	NotificationThreadSettingsUpdated           NotificationMethod = "thread/settings/updated"
	NotificationThreadTokenUsageUpdated         NotificationMethod = "thread/tokenUsage/updated"
	NotificationTurnStarted                     NotificationMethod = "turn/started"
	NotificationTurnCompleted                   NotificationMethod = "turn/completed"
	NotificationThreadRealtimeStarted           NotificationMethod = "thread/realtime/started"
	NotificationThreadRealtimeItemAdded         NotificationMethod = "thread/realtime/itemAdded"
	NotificationThreadRealtimeTranscriptDelta   NotificationMethod = "thread/realtime/transcript/delta"
	NotificationThreadRealtimeTranscriptDone    NotificationMethod = "thread/realtime/transcript/done"
	NotificationThreadRealtimeOutputAudioDelta  NotificationMethod = "thread/realtime/outputAudio/delta"
	NotificationThreadRealtimeSDP               NotificationMethod = "thread/realtime/sdp"
	NotificationThreadRealtimeError             NotificationMethod = "thread/realtime/error"
	NotificationThreadRealtimeClosed            NotificationMethod = "thread/realtime/closed"
	NotificationError                           NotificationMethod = "error"
	NotificationAccountLoginCompleted           NotificationMethod = "account/login/completed"
	NotificationAccountUpdated                  NotificationMethod = "account/updated"
	NotificationAccountRateLimitsUpdated        NotificationMethod = "account/rateLimits/updated"
	NotificationAppListUpdated                  NotificationMethod = "app/list/updated"
	NotificationModelRerouted                   NotificationMethod = "model/rerouted"
	NotificationModelVerification               NotificationMethod = "model/verification"
	NotificationWarning                         NotificationMethod = "warning"
	NotificationDeprecationNotice               NotificationMethod = "deprecationNotice"
	NotificationConfigWarning                   NotificationMethod = "configWarning"
	NotificationFuzzyFileSearchSessionUpdated   NotificationMethod = "fuzzyFileSearch/sessionUpdated"
	NotificationFuzzyFileSearchSessionCompleted NotificationMethod = "fuzzyFileSearch/sessionCompleted"
)

const (
	defaultPageSize       = 50
	maxThreadListPageSize = 100
)

const (
	JSONRPCInvalidRequestErrorCode = -32600
	JSONRPCMethodNotFoundErrorCode = -32601
	JSONRPCInvalidParamsErrorCode  = -32602
	JSONRPCInternalErrorCode       = -32603
)

var (
	ErrUnknownMethod         = errors.New("unknown app-server method")
	ErrInvalidRequest        = errors.New("invalid app-server request")
	ErrJSONRPCInvalidRequest = errors.New("json-rpc invalid request")
	// ErrInvalidParams mirrors Rust's invalid_params (-32602) at the request
	// validation boundary (Rust message_processor deserialize_client_request).
	ErrInvalidParams = errors.New("invalid app-server params")
	// ErrSessionBudgetExceeded surfaces the Rust SessionBudgetExceeded
	// codexErrorInfo when the shared rollout budget is exhausted
	// (core/src/session/rollout_budget.rs).
	ErrSessionBudgetExceeded = errors.New("session budget exceeded")
)

type jsonRPCInvalidRequestError struct {
	message string
}

func (e *jsonRPCInvalidRequestError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *jsonRPCInvalidRequestError) Unwrap() error {
	return ErrJSONRPCInvalidRequest
}

func (e *jsonRPCInvalidRequestError) Is(target error) bool {
	return target == ErrJSONRPCInvalidRequest || target == ErrInvalidRequest
}

func jsonRPCInvalidRequest(message string) error {
	return &jsonRPCInvalidRequestError{message: message}
}

// threadRollbackFailedError carries the Rust ThreadRollbackFailed codexErrorInfo
// on thread/rollback request validation failures (num_turns < 1, active turn),
// mirroring codex-rs core/src/session/handlers.rs. The structured error data
// makes the app-server RPC response surface the codexErrorInfo like Rust's
// bespoke event handling (bespoke_event_handling.rs).
type threadRollbackFailedError struct {
	message string
}

func (e *threadRollbackFailedError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *threadRollbackFailedError) Unwrap() error {
	return ErrInvalidRequest
}

func (e *threadRollbackFailedError) Is(target error) bool {
	// Rust surfaces rollback failures via invalid_request (-32600) with the
	// ThreadRollbackFailed codexErrorInfo; the request-level classification
	// must stay invalid-request, not invalid-params.
	return target == ErrJSONRPCInvalidRequest || target == ErrInvalidRequest
}

func (e *threadRollbackFailedError) JSONRPCErrorData() map[string]any {
	return map[string]any{"codexErrorInfo": "threadRollbackFailed"}
}

func threadRollbackFailed(message string) error {
	return &threadRollbackFailedError{message: message}
}

// obsoletePermissionProfileError rejects the removed `permissionProfile`
// request field on the app-server methods that once accepted it (Rust
// #38919). Clients must select a named profile through `permissions` instead.
type obsoletePermissionProfileError struct {
	message string
}

func (e *obsoletePermissionProfileError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *obsoletePermissionProfileError) Unwrap() error {
	return ErrInvalidParams
}

func (e *obsoletePermissionProfileError) Is(target error) bool {
	return target == ErrInvalidParams
}

func rejectObsoleteRequestFields(r *Request) error {
	if r == nil {
		return nil
	}
	switch r.Method {
	case MethodThreadStart, MethodThreadResume, MethodThreadFork, MethodTurnStart:
	default:
		return nil
	}
	var params map[string]json.RawMessage
	if len(bytes.TrimSpace(r.Params)) == 0 {
		return nil
	}
	if err := json.Unmarshal(r.Params, &params); err != nil {
		return nil
	}
	if _, present := params["permissionProfile"]; !present {
		return nil
	}
	return &obsoletePermissionProfileError{
		message: fmt.Sprintf(
			"`permissionProfile` is no longer supported for `%s`; use `permissions` with a named profile id instead",
			string(r.Method),
		),
	}
}

type Method string

type NotificationMethod string

type RequestID struct {
	value any
}

func StringID(value string) RequestID {
	return RequestID{value: value}
}

func IntID(value int64) RequestID {
	return RequestID{value: value}
}

func (id *RequestID) IsZero() bool {
	return id == nil || id.value == nil
}

func (id *RequestID) String() string {
	if id == nil || id.value == nil {
		return ""
	}
	switch value := id.value.(type) {
	case string:
		return value
	case float64:
		intValue := int64(value)
		if value == float64(intValue) {
			return strconv.FormatInt(intValue, 10)
		}
		return strconv.FormatFloat(value, 'f', -1, 64)
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case json.Number:
		return value.String()
	default:
		return fmt.Sprint(value)
	}
}

func (id RequestID) MarshalJSON() ([]byte, error) {
	if id.value == nil {
		return []byte("null"), nil
	}
	switch value := id.value.(type) {
	case string:
		return json.Marshal(value)
	case int:
		return json.Marshal(value)
	case int64:
		return json.Marshal(value)
	case float64:
		intValue := int64(value)
		if value != float64(intValue) {
			return nil, fmt.Errorf("%w: request id must be an integer", ErrInvalidRequest)
		}
		return json.Marshal(intValue)
	case json.Number:
		intValue, err := value.Int64()
		if err != nil {
			return nil, fmt.Errorf("%w: request id must be an integer", ErrInvalidRequest)
		}
		return json.Marshal(intValue)
	default:
		return nil, fmt.Errorf("%w: unsupported request id %T", ErrInvalidRequest, value)
	}
}

func (id *RequestID) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("null")) || len(data) == 0 {
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
	intValue, err := value.Int64()
	if err != nil {
		return fmt.Errorf("%w: request id must be an integer", ErrInvalidRequest)
	}
	id.value = intValue
	return nil
}

type Request struct {
	JSONRPC      string          `json:"jsonrpc,omitempty"`
	ID           RequestID       `json:"id"`
	Method       Method          `json:"method"`
	Params       json.RawMessage `json:"params,omitempty"`
	ConnectionID string          `json:"-"`
	Internal     bool            `json:"-"`
}

const defaultRequestConnectionID = "default"

func (r *Request) normalizedConnectionID() string {
	if r == nil {
		return defaultRequestConnectionID
	}
	return normalizeConnectionID(r.ConnectionID)
}

func normalizeConnectionID(connectionID string) string {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		return defaultRequestConnectionID
	}
	return connectionID
}

func (r *Request) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: request is nil", ErrInvalidRequest)
	}
	if r.ID.IsZero() {
		return fmt.Errorf("%w: id is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(string(r.Method)) == "" {
		return fmt.Errorf("%w: method is required", ErrInvalidRequest)
	}
	if err := rejectObsoleteRequestFields(r); err != nil {
		return err
	}
	return nil
}

func (r *Request) DecodeParams(target any) error {
	if r == nil {
		return fmt.Errorf("%w: request is nil", ErrInvalidRequest)
	}
	if target == nil {
		return fmt.Errorf("%w: target is nil", ErrInvalidRequest)
	}
	if len(bytes.TrimSpace(r.Params)) == 0 {
		r.Params = []byte("{}")
	}
	if err := json.Unmarshal(r.Params, target); err != nil {
		return jsonRPCInvalidRequest(fmt.Sprintf("Invalid request: %v", err))
	}
	return nil
}

type Response struct {
	JSONRPC string         `json:"jsonrpc,omitempty"`
	ID      RequestID      `json:"id"`
	Result  any            `json:"result,omitempty"`
	Error   *ResponseError `json:"error,omitempty"`
}

type ResponseError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

func OK(id RequestID, result any) *Response {
	return &Response{ID: id, Result: result}
}

func ErrorResponse(id RequestID, code int, message string, data map[string]any) *Response {
	return &Response{
		ID:    id,
		Error: &ResponseError{Code: code, Message: message, Data: data},
	}
}

type jsonRPCErrorDataProvider interface {
	JSONRPCErrorData() map[string]any
}

func jsonRPCErrorData(err error) map[string]any {
	var provider jsonRPCErrorDataProvider
	if errors.As(err, &provider) {
		return provider.JSONRPCErrorData()
	}
	return nil
}

type Notification struct {
	JSONRPC string             `json:"jsonrpc,omitempty"`
	Method  NotificationMethod `json:"method"`
	Params  any                `json:"params,omitempty"`
}

func NewNotification(method NotificationMethod, params any) *Notification {
	return &Notification{Method: method, Params: params}
}

type SessionSource string

const (
	SessionSourceCli       SessionSource = "cli"
	SessionSourceVsCode    SessionSource = "vscode"
	SessionSourceExec      SessionSource = "exec"
	SessionSourceAppServer SessionSource = "appServer"
	SessionSourceUnknown   SessionSource = "unknown"
)

type ThreadSource string

const (
	ThreadSourceUser                ThreadSource = "user"
	ThreadSourceSubagent            ThreadSource = "subagent"
	ThreadSourceMemoryConsolidation ThreadSource = "memory_consolidation"
)

type ThreadHistoryMode string

const (
	ThreadHistoryLegacy    ThreadHistoryMode = "legacy"
	ThreadHistoryPaginated ThreadHistoryMode = "paginated"
)

type ThreadResumeHistoryItem json.RawMessage

type SortKey string

const (
	SortCreatedAt       SortKey = "created_at"
	SortUpdatedAt       SortKey = "updated_at"
	SortRecencyAt       SortKey = "recency_at"
	SortSectionPosition SortKey = "section_position"
)

type SortDirection string

const (
	SortAsc  SortDirection = "asc"
	SortDesc SortDirection = "desc"
)

type ThreadSourceKind string

const (
	ThreadSourceKindCli                 ThreadSourceKind = "cli"
	ThreadSourceKindVsCode              ThreadSourceKind = "vscode"
	ThreadSourceKindExec                ThreadSourceKind = "exec"
	ThreadSourceKindAppServer           ThreadSourceKind = "appServer"
	ThreadSourceKindSubAgent            ThreadSourceKind = "subAgent"
	ThreadSourceKindSubAgentReview      ThreadSourceKind = "subAgentReview"
	ThreadSourceKindSubAgentCompact     ThreadSourceKind = "subAgentCompact"
	ThreadSourceKindSubAgentThreadSpawn ThreadSourceKind = "subAgentThreadSpawn"
	ThreadSourceKindSubAgentOther       ThreadSourceKind = "subAgentOther"
	ThreadSourceKindUnknown             ThreadSourceKind = "unknown"
)

type ThreadListCwdFilter struct {
	Values []string
}

func (f *ThreadListCwdFilter) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		f.Values = nil
		return nil
	}
	if data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		f.Values = []string{value}
		return nil
	}
	var values []string
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	f.Values = values
	return nil
}

func (f *ThreadListCwdFilter) MarshalJSON() ([]byte, error) {
	if f == nil || len(f.Values) == 0 {
		return []byte("null"), nil
	}
	if len(f.Values) == 1 {
		return json.Marshal(f.Values[0])
	}
	return json.Marshal(f.Values)
}

type GitInfo struct {
	SHA       *string `json:"sha"`
	Branch    *string `json:"branch"`
	OriginURL *string `json:"originUrl"`
}

type ThreadSection struct {
	ID         string                   `json:"id"`
	Name       string                   `json:"name"`
	Appearance *ThreadSectionAppearance `json:"appearance,omitempty"`
}

// ThreadSectionAppearance is the optional visual presentation for a custom
// thread section. Both icon and color are limited to 64 bytes.
type ThreadSectionAppearance struct {
	Icon  *string `json:"icon,omitempty"`
	Color *string `json:"color,omitempty"`
}

type Thread struct {
	ID                   string            `json:"id"`
	Extra                map[string]any    `json:"extra,omitempty"`
	SessionID            string            `json:"sessionId"`
	ForkedFromID         *string           `json:"forkedFromId"`
	ParentThreadID       *string           `json:"parentThreadId"`
	Preview              string            `json:"preview"`
	Ephemeral            bool              `json:"ephemeral"`
	Section              *ThreadSection    `json:"section"`
	SectionEnteredAt     *int64            `json:"sectionEnteredAt"`
	HistoryMode          ThreadHistoryMode `json:"historyMode"`
	ModelProvider        string            `json:"modelProvider"`
	CreatedAt            int64             `json:"createdAt"`
	UpdatedAt            int64             `json:"updatedAt"`
	RecencyAt            *int64            `json:"recencyAt"`
	Status               ThreadStatus      `json:"status"`
	Path                 *string           `json:"path"`
	CWD                  string            `json:"cwd"`
	CLIVersion           string            `json:"cliVersion"`
	Source               SessionSource     `json:"source"`
	CanAcceptDirectInput *bool             `json:"canAcceptDirectInput"`
	ThreadSource         *ThreadSource     `json:"threadSource"`
	AgentNickname        *string           `json:"agentNickname"`
	AgentRole            *string           `json:"agentRole"`
	GitInfo              *GitInfo          `json:"gitInfo"`
	Name                 *string           `json:"name"`
	Turns                []Turn            `json:"turns"`
}

func (t *Thread) MarshalJSON() ([]byte, error) {
	turns := append([]Turn(nil), t.Turns...)
	if turns == nil {
		turns = []Turn{}
	}
	historyMode := t.HistoryMode
	if historyMode == "" {
		historyMode = ThreadHistoryLegacy
	}
	return json.Marshal(struct {
		ID                   string            `json:"id"`
		SessionID            string            `json:"sessionId"`
		ForkedFromID         *string           `json:"forkedFromId"`
		ParentThreadID       *string           `json:"parentThreadId"`
		Preview              string            `json:"preview"`
		Ephemeral            bool              `json:"ephemeral"`
		Section              *ThreadSection    `json:"section"`
		SectionEnteredAt     *int64            `json:"sectionEnteredAt"`
		HistoryMode          ThreadHistoryMode `json:"historyMode"`
		ModelProvider        string            `json:"modelProvider"`
		CreatedAt            int64             `json:"createdAt"`
		UpdatedAt            int64             `json:"updatedAt"`
		RecencyAt            *int64            `json:"recencyAt"`
		Status               ThreadStatus      `json:"status"`
		Path                 *string           `json:"path"`
		CWD                  string            `json:"cwd"`
		CLIVersion           string            `json:"cliVersion"`
		Source               SessionSource     `json:"source"`
		CanAcceptDirectInput *bool             `json:"canAcceptDirectInput"`
		ThreadSource         *ThreadSource     `json:"threadSource"`
		AgentNickname        *string           `json:"agentNickname"`
		AgentRole            *string           `json:"agentRole"`
		GitInfo              *GitInfo          `json:"gitInfo"`
		Name                 *string           `json:"name"`
		Turns                []Turn            `json:"turns"`
	}{
		ID:                   t.ID,
		SessionID:            t.SessionID,
		ForkedFromID:         t.ForkedFromID,
		ParentThreadID:       t.ParentThreadID,
		Preview:              t.Preview,
		Ephemeral:            t.Ephemeral,
		Section:              t.Section,
		SectionEnteredAt:     t.SectionEnteredAt,
		HistoryMode:          historyMode,
		ModelProvider:        t.ModelProvider,
		CreatedAt:            t.CreatedAt,
		UpdatedAt:            t.UpdatedAt,
		RecencyAt:            t.RecencyAt,
		Status:               t.Status,
		Path:                 t.Path,
		CWD:                  t.CWD,
		CLIVersion:           t.CLIVersion,
		Source:               t.Source,
		CanAcceptDirectInput: t.CanAcceptDirectInput,
		ThreadSource:         t.ThreadSource,
		AgentNickname:        t.AgentNickname,
		AgentRole:            t.AgentRole,
		GitInfo:              t.GitInfo,
		Name:                 t.Name,
		Turns:                turns,
	})
}

type ThreadStatus struct {
	Type        string             `json:"type"`
	ActiveFlags []ThreadActiveFlag `json:"activeFlags,omitempty"`
}

func (s *ThreadStatus) MarshalJSON() ([]byte, error) {
	statusType := strings.TrimSpace(s.Type)
	if statusType == "" {
		statusType = "notLoaded"
	}
	if statusType == "active" {
		flags := append([]ThreadActiveFlag(nil), s.ActiveFlags...)
		if flags == nil {
			flags = []ThreadActiveFlag{}
		}
		return json.Marshal(struct {
			Type        string             `json:"type"`
			ActiveFlags []ThreadActiveFlag `json:"activeFlags"`
		}{
			Type:        statusType,
			ActiveFlags: flags,
		})
	}
	return json.Marshal(struct {
		Type string `json:"type"`
	}{
		Type: statusType,
	})
}

type ThreadActiveFlag string

const (
	ThreadActiveFlagWaitingOnApproval  ThreadActiveFlag = "waitingOnApproval"
	ThreadActiveFlagWaitingOnUserInput ThreadActiveFlag = "waitingOnUserInput"
)

func NotLoadedStatus() ThreadStatus {
	return ThreadStatus{Type: "notLoaded"}
}

func IdleStatus() ThreadStatus {
	return ThreadStatus{Type: "idle"}
}

func ActiveStatus(flags ...ThreadActiveFlag) ThreadStatus {
	return ThreadStatus{Type: "active", ActiveFlags: flags}
}

func SystemErrorStatus() ThreadStatus {
	return ThreadStatus{Type: "systemError"}
}

type Turn struct {
	ID          string        `json:"id"`
	Items       []ThreadItem  `json:"items"`
	ItemsView   TurnItemsView `json:"itemsView"`
	Status      TurnStatus    `json:"status"`
	Error       *TurnError    `json:"error"`
	StartedAt   *int64        `json:"startedAt"`
	CompletedAt *int64        `json:"completedAt"`
	DurationMS  *int64        `json:"durationMs"`
}

func (t *Turn) MarshalJSON() ([]byte, error) {
	items := append([]ThreadItem(nil), t.Items...)
	if items == nil {
		items = []ThreadItem{}
	}
	itemsView := t.ItemsView
	if itemsView == "" {
		itemsView = TurnItemsFull
	}
	return json.Marshal(struct {
		ID          string        `json:"id"`
		Items       []ThreadItem  `json:"items"`
		ItemsView   TurnItemsView `json:"itemsView"`
		Status      TurnStatus    `json:"status"`
		Error       *TurnError    `json:"error"`
		StartedAt   *int64        `json:"startedAt"`
		CompletedAt *int64        `json:"completedAt"`
		DurationMS  *int64        `json:"durationMs"`
	}{
		ID:          t.ID,
		Items:       items,
		ItemsView:   itemsView,
		Status:      t.Status,
		Error:       t.Error,
		StartedAt:   t.StartedAt,
		CompletedAt: t.CompletedAt,
		DurationMS:  t.DurationMS,
	})
}

type TurnItemsView string

const (
	TurnItemsNotLoaded TurnItemsView = "notLoaded"
	TurnItemsSummary   TurnItemsView = "summary"
	TurnItemsFull      TurnItemsView = "full"
)

type TurnStatus string

const (
	TurnStatusInProgress  TurnStatus = "inProgress"
	TurnStatusCompleted   TurnStatus = "completed"
	TurnStatusInterrupted TurnStatus = "interrupted"
	TurnStatusFailed      TurnStatus = "failed"
	TurnStatusRunning     TurnStatus = TurnStatusInProgress
)

type TurnError struct {
	Message           string         `json:"message"`
	CodexErrorInfo    CodexErrorInfo `json:"codexErrorInfo"`
	AdditionalDetails *string        `json:"additionalDetails"`
}

func (e TurnError) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Message           string         `json:"message"`
		CodexErrorInfo    CodexErrorInfo `json:"codexErrorInfo"`
		AdditionalDetails *string        `json:"additionalDetails"`
	}{
		Message:           e.Message,
		CodexErrorInfo:    e.CodexErrorInfo,
		AdditionalDetails: cloneStringPtrAppserver(e.AdditionalDetails),
	})
}

type ThreadItem struct {
	ID         string              `json:"id"`
	Type       string              `json:"type"`
	Role       string              `json:"role,omitempty"`
	Text       string              `json:"text,omitempty"`
	Name       string              `json:"name,omitempty"`
	Namespace  string              `json:"namespace,omitempty"`
	CallID     string              `json:"callId,omitempty"`
	Status     string              `json:"status,omitempty"`
	TurnID     string              `json:"turnId,omitempty"`
	CreatedAt  int64               `json:"createdAt"`
	Content    []ThreadItemContent `json:"content,omitempty"`
	Data       map[string]any      `json:"data,omitempty"`
	Raw        json.RawMessage     `json:"raw,omitempty"`
	ResponseID string              `json:"responseId,omitempty"`
}

type fileChangeUpdate struct {
	Path string             `json:"path"`
	Kind fileChangeKindWire `json:"kind"`
	Diff string             `json:"diff"`
}

type HookPromptWire struct {
	Text      string `json:"text"`
	HookRunID string `json:"hookRunId"`
}

type fileChangeKindWire struct {
	Type     string  `json:"type"`
	MovePath *string `json:"move_path,omitempty"`
}

func (k fileChangeKindWire) MarshalJSON() ([]byte, error) {
	switch k.Type {
	case "update":
		return json.Marshal(struct {
			Type     string  `json:"type"`
			MovePath *string `json:"move_path"`
		}{Type: "update", MovePath: k.MovePath})
	case "delete":
		return json.Marshal(struct {
			Type string `json:"type"`
		}{Type: "delete"})
	default:
		return json.Marshal(struct {
			Type string `json:"type"`
		}{Type: "add"})
	}
}

func (i *ThreadItem) MarshalJSON() ([]byte, error) {
	if i == nil {
		return []byte("null"), nil
	}
	switch threadItemWireType(i) {
	case "userMessage":
		return json.Marshal(struct {
			Type     string           `json:"type"`
			ID       string           `json:"id"`
			ClientID *string          `json:"clientId"`
			Content  []map[string]any `json:"content"`
		}{
			Type:     "userMessage",
			ID:       i.ID,
			ClientID: threadItemStringPtrFromData(i.Data, "clientId", "client_id"),
			Content:  threadItemUserInputContent(i),
		})
	case "agentMessage":
		return json.Marshal(struct {
			Type           string `json:"type"`
			ID             string `json:"id"`
			Text           string `json:"text"`
			Phase          any    `json:"phase"`
			MemoryCitation any    `json:"memoryCitation"`
			ResponseID     string `json:"responseId,omitempty"`
		}{
			Type:           "agentMessage",
			ID:             i.ID,
			Text:           i.Text,
			Phase:          threadItemAnyFromData(i.Data, "phase", "messagePhase"),
			MemoryCitation: threadItemAnyFromData(i.Data, "memoryCitation", "memory_citation"),
			ResponseID:     i.ResponseID,
		})
	case "plan":
		return json.Marshal(struct {
			Type       string `json:"type"`
			ID         string `json:"id"`
			Text       string `json:"text"`
			ResponseID string `json:"responseId,omitempty"`
		}{
			Type:       "plan",
			ID:         i.ID,
			Text:       i.Text,
			ResponseID: i.ResponseID,
		})
	case "reasoning":
		return json.Marshal(struct {
			Type       string   `json:"type"`
			ID         string   `json:"id"`
			Summary    []string `json:"summary"`
			Content    []string `json:"content"`
			ResponseID string   `json:"responseId,omitempty"`
		}{
			Type:       "reasoning",
			ID:         i.ID,
			Summary:    threadItemStringSliceFromData(i.Data, "summary"),
			Content:    threadItemStringSliceFromData(i.Data, "reasoningContent", "content"),
			ResponseID: i.ResponseID,
		})
	case "hookPrompt":
		return json.Marshal(struct {
			Type      string           `json:"type"`
			ID        string           `json:"id"`
			Fragments []HookPromptWire `json:"fragments"`
		}{
			Type:      "hookPrompt",
			ID:        i.ID,
			Fragments: threadItemHookPromptFragments(i),
		})
	case "commandExecution":
		return json.Marshal(struct {
			Type             string                 `json:"type"`
			ID               string                 `json:"id"`
			PluginID         *string                `json:"pluginId"`
			ScriptPath       *string                `json:"scriptPath"`
			Command          string                 `json:"command"`
			CWD              string                 `json:"cwd"`
			ProcessID        *string                `json:"processId"`
			Source           CommandExecutionSource `json:"source"`
			Status           CommandExecutionStatus `json:"status"`
			CommandActions   []CommandAction        `json:"commandActions"`
			AggregatedOutput *string                `json:"aggregatedOutput"`
			ExitCode         *int64                 `json:"exitCode"`
			DurationMS       *int64                 `json:"durationMs"`
		}{
			Type:             "commandExecution",
			ID:               threadItemExternalID(i),
			PluginID:         threadItemStringPtrFromData(i.Data, "pluginId", "plugin_id"),
			ScriptPath:       threadItemStringPtrFromData(i.Data, "scriptPath", "script_path"),
			Command:          threadItemCommand(i),
			CWD:              threadItemCWD(i),
			ProcessID:        threadItemStringPtrFromData(i.Data, "processId", "process_id"),
			Source:           threadItemCommandSource(i),
			Status:           threadItemCommandStatus(i),
			CommandActions:   threadItemCommandActions(i),
			AggregatedOutput: threadItemAggregatedOutput(i),
			ExitCode:         threadItemInt64PtrFromData(i.Data, "exit_code", "exitCode"),
			DurationMS:       threadItemInt64PtrFromData(i.Data, "duration_ms", "durationMs"),
		})
	case "mcpToolCall":
		return json.Marshal(struct {
			Type              string  `json:"type"`
			ID                string  `json:"id"`
			Server            string  `json:"server"`
			Tool              string  `json:"tool"`
			Status            string  `json:"status"`
			Arguments         any     `json:"arguments"`
			AppContext        any     `json:"appContext"`
			MCPAppResourceURI *string `json:"mcpAppResourceUri,omitempty"`
			PluginID          *string `json:"pluginId"`
			ReadOnlyHint      *bool   `json:"readOnlyHint"`
			Result            any     `json:"result"`
			Error             any     `json:"error"`
			DurationMS        *int64  `json:"durationMs"`
		}{
			Type:              "mcpToolCall",
			ID:                threadItemExternalID(i),
			Server:            threadItemMCPServer(i),
			Tool:              threadItemMCPTool(i),
			Status:            threadItemMCPStatus(i),
			Arguments:         threadItemJSONValueFromData(i.Data, "arguments", "input", "rawArguments", "raw_arguments"),
			AppContext:        threadItemMCPAppContext(i),
			MCPAppResourceURI: threadItemStringPtrFromData(i.Data, "mcpAppResourceUri", "mcp_app_resource_uri"),
			PluginID:          threadItemStringPtrFromData(i.Data, "pluginId", "plugin_id"),
			ReadOnlyHint:      threadItemBoolPtrFromData(i.Data, "readOnlyHint", "read_only_hint"),
			Result:            threadItemMCPResult(i),
			Error:             threadItemMCPError(i),
			DurationMS:        threadItemInt64PtrFromData(i.Data, "duration_ms", "durationMs"),
		})
	case "dynamicToolCall":
		return json.Marshal(struct {
			Type         string  `json:"type"`
			ID           string  `json:"id"`
			Namespace    *string `json:"namespace"`
			Tool         string  `json:"tool"`
			Arguments    any     `json:"arguments"`
			Status       string  `json:"status"`
			ContentItems any     `json:"contentItems"`
			Success      *bool   `json:"success"`
			DurationMS   *int64  `json:"durationMs"`
		}{
			Type:         "dynamicToolCall",
			ID:           i.ID,
			Namespace:    threadItemDynamicNamespace(i),
			Tool:         threadItemDynamicTool(i),
			Arguments:    threadItemDynamicArguments(i),
			Status:       threadItemDynamicStatus(i),
			ContentItems: threadItemDynamicContentItems(i),
			Success:      threadItemBoolPtrFromData(i.Data, "success"),
			DurationMS:   threadItemInt64PtrFromData(i.Data, "duration_ms", "durationMs"),
		})
	case "collabAgentToolCall":
		return json.Marshal(struct {
			Type              string                      `json:"type"`
			ID                string                      `json:"id"`
			Tool              CollabAgentTool             `json:"tool"`
			Status            CollabAgentToolCallStatus   `json:"status"`
			SenderThreadID    string                      `json:"senderThreadId"`
			ReceiverThreadIDs []string                    `json:"receiverThreadIds"`
			Prompt            *string                     `json:"prompt"`
			Model             *string                     `json:"model"`
			ReasoningEffort   *string                     `json:"reasoningEffort"`
			AgentsStates      map[string]CollabAgentState `json:"agentsStates"`
		}{
			Type:              "collabAgentToolCall",
			ID:                i.ID,
			Tool:              threadItemCollabAgentTool(i),
			Status:            threadItemCollabAgentToolStatus(i),
			SenderThreadID:    threadItemStringFromData(i.Data, "senderThreadId", "sender_thread_id"),
			ReceiverThreadIDs: threadItemStringSliceFromData(i.Data, "receiverThreadIds", "receiver_thread_ids"),
			Prompt:            threadItemStringPtrFromData(i.Data, "prompt"),
			Model:             threadItemStringPtrFromData(i.Data, "model"),
			ReasoningEffort:   threadItemStringPtrFromData(i.Data, "reasoningEffort", "reasoning_effort"),
			AgentsStates:      threadItemCollabAgentStates(i),
		})
	case "subAgentActivity":
		return json.Marshal(struct {
			Type          string `json:"type"`
			ID            string `json:"id"`
			Kind          string `json:"kind"`
			AgentThreadID string `json:"agentThreadId"`
			AgentPath     string `json:"agentPath"`
		}{
			Type:          "subAgentActivity",
			ID:            i.ID,
			Kind:          threadItemStringFromData(i.Data, "kind"),
			AgentThreadID: threadItemStringFromData(i.Data, "agentThreadId", "agent_thread_id"),
			AgentPath:     threadItemStringFromData(i.Data, "agentPath", "agent_path", "path"),
		})
	case "webSearch":
		return json.Marshal(struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Query   string `json:"query"`
			Action  any    `json:"action"`
			Results []any  `json:"results,omitempty"`
		}{
			Type:    "webSearch",
			ID:      i.ID,
			Query:   threadItemWebSearchQuery(i),
			Action:  threadItemWebSearchAction(i),
			Results: threadItemWebSearchResults(i),
		})
	case "imageView":
		return json.Marshal(struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Path string `json:"path"`
		}{
			Type: "imageView",
			ID:   i.ID,
			Path: threadItemStringFromData(i.Data, "path"),
		})
	case "sleep":
		return json.Marshal(struct {
			Type       string `json:"type"`
			ID         string `json:"id"`
			DurationMS int64  `json:"durationMs"`
		}{
			Type:       "sleep",
			ID:         i.ID,
			DurationMS: threadItemInt64FromData(i.Data, "durationMs", "duration_ms"),
		})
	case "imageGeneration":
		return json.Marshal(struct {
			Type                  string  `json:"type"`
			ID                    string  `json:"id"`
			Status                string  `json:"status"`
			RevisedPrompt         *string `json:"revisedPrompt"`
			Result                string  `json:"result"`
			TransparentBackground *bool   `json:"transparentBackground"`
			SavedPath             *string `json:"savedPath,omitempty"`
		}{
			Type:                  "imageGeneration",
			ID:                    i.ID,
			Status:                firstNonEmpty(threadItemStringFromData(i.Data, "status"), i.Status),
			RevisedPrompt:         threadItemStringPtrFromData(i.Data, "revisedPrompt", "revised_prompt"),
			Result:                firstNonEmpty(threadItemStringFromData(i.Data, "result"), i.Text),
			TransparentBackground: threadItemBoolPtrFromData(i.Data, "transparentBackground", "transparent_background"),
			SavedPath:             threadItemStringPtrFromData(i.Data, "savedPath", "saved_path"),
		})
	case "enteredReviewMode":
		return json.Marshal(struct {
			Type   string `json:"type"`
			ID     string `json:"id"`
			Review string `json:"review"`
		}{
			Type:   "enteredReviewMode",
			ID:     i.ID,
			Review: threadItemReviewText(i),
		})
	case "exitedReviewMode":
		return json.Marshal(struct {
			Type   string `json:"type"`
			ID     string `json:"id"`
			Review string `json:"review"`
		}{
			Type:   "exitedReviewMode",
			ID:     i.ID,
			Review: threadItemReviewText(i),
		})
	case "contextCompaction":
		return json.Marshal(struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}{
			Type: "contextCompaction",
			ID:   i.ID,
		})
	case "fileChange":
		return json.Marshal(struct {
			Type    string             `json:"type"`
			ID      string             `json:"id"`
			Changes []fileChangeUpdate `json:"changes"`
			Status  PatchApplyStatus   `json:"status"`
		}{
			Type:    "fileChange",
			ID:      threadItemExternalID(i),
			Changes: threadItemFileChanges(i),
			Status:  threadItemFileChangeStatus(i),
		})
	default:
		return json.Marshal(threadItemEnvelope{
			ID:         i.ID,
			Type:       i.Type,
			Role:       i.Role,
			Text:       i.Text,
			Name:       i.Name,
			Namespace:  i.Namespace,
			CallID:     i.CallID,
			Status:     i.Status,
			TurnID:     i.TurnID,
			CreatedAt:  i.CreatedAt,
			Content:    i.Content,
			Data:       i.Data,
			Raw:        i.Raw,
			ResponseID: i.ResponseID,
		})
	}
}

type threadItemEnvelope ThreadItem

type ThreadItemContent struct {
	Type     string  `json:"type"`
	Text     string  `json:"text,omitempty"`
	ImageURL string  `json:"imageUrl,omitempty"`
	AudioURL string  `json:"audioUrl,omitempty"`
	Detail   *string `json:"detail,omitempty"`
}

func decodeServiceTierPresence(data []byte) (*string, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, err
	}
	serviceTierRaw, ok := raw["serviceTier"]
	if !ok {
		return nil, false, nil
	}
	if strings.TrimSpace(string(serviceTierRaw)) == "null" {
		return nil, true, nil
	}
	var serviceTier string
	if err := json.Unmarshal(serviceTierRaw, &serviceTier); err != nil {
		return nil, true, err
	}
	return &serviceTier, true, nil
}

type ThreadStartParams struct {
	CWD                        string         `json:"cwd,omitempty"`
	Prompt                     string         `json:"prompt,omitempty"`
	Model                      string         `json:"model,omitempty"`
	ModelProvider              string         `json:"modelProvider,omitempty"`
	ApprovalPolicy             any            `json:"approvalPolicy,omitempty"`
	ApprovalsReviewer          *string        `json:"approvalsReviewer,omitempty"`
	BaseInstructions           *string        `json:"baseInstructions,omitempty"`
	DeveloperInstructions      *string        `json:"developerInstructions,omitempty"`
	Config                     map[string]any `json:"config,omitempty"`
	Personality                *string        `json:"personality,omitempty"`
	Sandbox                    any            `json:"sandbox,omitempty"`
	Permissions                *string        `json:"permissions,omitempty"`
	ServiceName                *string        `json:"serviceName,omitempty"`
	ServiceTier                *string        `json:"serviceTier,omitempty"`
	ServiceTierSet             bool           `json:"-"`
	Ephemeral                  bool           `json:"ephemeral,omitempty"`
	SessionStartSource         *string        `json:"sessionStartSource,omitempty"`
	ExperimentalRawEvents      bool           `json:"experimentalRawEvents,omitempty"`
	AllowProviderModelFallback bool           `json:"allowProviderModelFallback,omitempty"`
	// Deprecated: accepted for old app-server clients, but ignored by runtime.
	MultiAgentMode          MultiAgentMode           `json:"multiAgentMode,omitempty"`
	HistoryMode             ThreadHistoryMode        `json:"historyMode,omitempty"`
	ThreadSource            *ThreadSource            `json:"threadSource,omitempty"`
	RuntimeWorkspaceRoots   []string                 `json:"runtimeWorkspaceRoots,omitempty"`
	SelectedCapabilityRoots []SelectedCapabilityRoot `json:"selectedCapabilityRoots,omitempty"`
	Environments            []map[string]any         `json:"environments,omitempty"`
	DynamicTools            []json.RawMessage        `json:"dynamicTools,omitempty"`
}

func (p *ThreadStartParams) UnmarshalJSON(data []byte) error {
	type threadStartParamsAlias ThreadStartParams
	var decoded threadStartParamsAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	serviceTier, serviceTierSet, err := decodeServiceTierPresence(data)
	if err != nil {
		return err
	}
	if serviceTierSet {
		decoded.ServiceTierSet = true
		decoded.ServiceTier = serviceTier
	}
	*p = ThreadStartParams(decoded)
	return nil
}

func (p *ThreadStartParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidRequest)
	}
	if err := validateThreadStartSessionStartSource(p.SessionStartSource); err != nil {
		return err
	}
	if err := validateThreadRuntimeWorkspaceRoots(p.RuntimeWorkspaceRoots); err != nil {
		return err
	}
	tools, err := normalizeThreadStartDynamicTools(p.DynamicTools)
	if err != nil {
		return err
	}
	if len(tools) > 0 {
		if err := turn.ValidateDynamicTools(tools); err != nil {
			return jsonRPCInvalidRequest(dynamicToolValidationMessage(err))
		}
		p.DynamicTools = rawMessagesFromDynamicToolSpecs(tools)
	}
	return nil
}

func validateThreadStartSessionStartSource(source *string) error {
	if source == nil {
		return nil
	}
	switch *source {
	case string(SessionStartSourceStartup), string(SessionStartSourceClear):
		return nil
	default:
		return jsonRPCInvalidRequest(`sessionStartSource must be "startup" or "clear"`)
	}
}

func validateThreadRuntimeWorkspaceRoots(roots []string) error {
	for _, root := range roots {
		if !isAbsoluteAppPath(root) {
			return jsonRPCInvalidRequest(fmt.Sprintf("runtimeWorkspaceRoots must contain absolute paths: %s", strings.TrimSpace(root)))
		}
	}
	return nil
}

type legacyDynamicToolSpec struct {
	Namespace       *string `json:"namespace,omitempty"`
	Name            string  `json:"name"`
	Description     string  `json:"description"`
	InputSchema     any     `json:"inputSchema"`
	DeferLoading    *bool   `json:"deferLoading,omitempty"`
	ExposeToContext *bool   `json:"exposeToContext,omitempty"`
}

func normalizeThreadStartDynamicTools(rawTools []json.RawMessage) ([]turn.DynamicToolSpec, error) {
	if len(rawTools) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(rawTools)
	if err != nil {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("invalid dynamicTools: %v", err))
	}
	var values []map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("invalid dynamicTools: %v", err))
	}
	hasLegacyFormat := false
	for _, value := range values {
		if dynamicToolHasLegacyFields(value) || dynamicToolNestedToolsHaveLegacyFields(value) {
			hasLegacyFormat = true
			break
		}
	}
	hasCanonicalFormat := false
	for _, value := range values {
		if _, ok := value["type"]; ok {
			hasCanonicalFormat = true
			break
		}
	}
	if hasLegacyFormat && hasCanonicalFormat {
		return nil, jsonRPCInvalidRequest("dynamic tools must use either canonical or legacy format consistently")
	}
	var tools []turn.DynamicToolSpec
	if !hasLegacyFormat {
		if err := json.Unmarshal(data, &tools); err != nil {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("invalid dynamicTools: %v", err))
		}
		return tools, nil
	}
	var legacyTools []legacyDynamicToolSpec
	if err := json.Unmarshal(data, &legacyTools); err != nil {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("invalid dynamicTools: %v", err))
	}
	return groupLegacyDynamicToolsByNamespace(legacyTools), nil
}

func dynamicToolHasLegacyFields(value map[string]any) bool {
	if value == nil {
		return true
	}
	if _, ok := value["namespace"]; ok {
		return true
	}
	if _, ok := value["exposeToContext"]; ok {
		return true
	}
	_, hasType := value["type"]
	return !hasType
}

func dynamicToolNestedToolsHaveLegacyFields(value map[string]any) bool {
	tools, _ := value["tools"].([]any)
	for _, toolValue := range tools {
		tool, _ := toolValue.(map[string]any)
		if dynamicToolHasLegacyFields(tool) {
			return true
		}
	}
	return false
}

func groupLegacyDynamicToolsByNamespace(legacyTools []legacyDynamicToolSpec) []turn.DynamicToolSpec {
	grouped := make([]turn.DynamicToolSpec, 0, len(legacyTools))
	namespaceIndexes := map[string]int{}
	for _, legacyTool := range legacyTools {
		function := turn.DynamicToolFunctionSpec{
			Name:         legacyTool.Name,
			Description:  legacyTool.Description,
			InputSchema:  legacyTool.InputSchema,
			DeferLoading: legacyDynamicToolDeferLoading(legacyTool),
		}
		if legacyTool.Namespace == nil {
			grouped = append(grouped, turn.DynamicToolSpec{Type: "function", Function: &function})
			continue
		}
		namespace := *legacyTool.Namespace
		if index, ok := namespaceIndexes[namespace]; ok {
			grouped[index].Namespace.Tools = append(grouped[index].Namespace.Tools, function)
			continue
		}
		namespaceIndexes[namespace] = len(grouped)
		grouped = append(grouped, turn.DynamicToolSpec{
			Type: "namespace",
			Namespace: &turn.DynamicToolNamespaceSpec{
				Name:  namespace,
				Tools: []turn.DynamicToolFunctionSpec{function},
			},
		})
	}
	return grouped
}

func legacyDynamicToolDeferLoading(tool legacyDynamicToolSpec) bool {
	if tool.DeferLoading != nil {
		return *tool.DeferLoading
	}
	if tool.ExposeToContext != nil {
		return !*tool.ExposeToContext
	}
	return false
}

func rawMessagesFromDynamicToolSpecs(tools []turn.DynamicToolSpec) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(tools))
	for i := range tools {
		data, err := json.Marshal(&tools[i])
		if err != nil {
			continue
		}
		out = append(out, json.RawMessage(data))
	}
	return out
}

func dynamicToolValidationMessage(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	prefix := turn.ErrInvalidTurnRequest.Error() + ":"
	if strings.HasPrefix(message, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(message, prefix))
	}
	return message
}

type ThreadStartResponse struct {
	Thread                  *Thread                          `json:"thread"`
	ApprovalPolicy          any                              `json:"approvalPolicy,omitempty"`
	ApprovalsReviewer       *string                          `json:"approvalsReviewer,omitempty"`
	CWD                     string                           `json:"cwd,omitempty"`
	RuntimeWorkspaceRoots   []string                         `json:"runtimeWorkspaceRoots,omitempty"`
	InstructionSources      []string                         `json:"instructionSources,omitempty"`
	Model                   string                           `json:"model,omitempty"`
	ModelProvider           string                           `json:"modelProvider,omitempty"`
	ReasoningEffort         *string                          `json:"reasoningEffort,omitempty"`
	Sandbox                 any                              `json:"sandbox,omitempty"`
	ActivePermissionProfile *sandbox.ActivePermissionProfile `json:"activePermissionProfile,omitempty"`
	MultiAgentMode          MultiAgentMode                   `json:"multiAgentMode,omitempty"`
	ServiceTier             *string                          `json:"serviceTier,omitempty"`
}

func (r *ThreadStartResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Thread                  *Thread                          `json:"thread"`
		ApprovalPolicy          any                              `json:"approvalPolicy"`
		ApprovalsReviewer       string                           `json:"approvalsReviewer"`
		CWD                     string                           `json:"cwd"`
		RuntimeWorkspaceRoots   []string                         `json:"runtimeWorkspaceRoots"`
		InstructionSources      []string                         `json:"instructionSources"`
		Model                   string                           `json:"model"`
		ModelProvider           string                           `json:"modelProvider"`
		ReasoningEffort         *string                          `json:"reasoningEffort"`
		Sandbox                 any                              `json:"sandbox"`
		ActivePermissionProfile *sandbox.ActivePermissionProfile `json:"activePermissionProfile"`
		MultiAgentMode          MultiAgentMode                   `json:"multiAgentMode"`
		ServiceTier             *string                          `json:"serviceTier"`
	}{
		Thread:                  r.Thread,
		ApprovalPolicy:          threadResponseApprovalPolicy(r.ApprovalPolicy),
		ApprovalsReviewer:       threadResponseApprovalsReviewer(r.ApprovalsReviewer),
		CWD:                     r.CWD,
		RuntimeWorkspaceRoots:   stringSliceForJSON(r.RuntimeWorkspaceRoots),
		InstructionSources:      stringSliceForJSON(r.InstructionSources),
		Model:                   r.Model,
		ModelProvider:           r.ModelProvider,
		ReasoningEffort:         cloneString(r.ReasoningEffort),
		Sandbox:                 threadResponseSandbox(r.Sandbox),
		ActivePermissionProfile: cloneActivePermissionProfile(r.ActivePermissionProfile),
		MultiAgentMode:          threadResponseMultiAgentMode(r.MultiAgentMode),
		ServiceTier:             cloneString(r.ServiceTier),
	})
}

type ThreadResumeParams struct {
	ThreadID              string                    `json:"threadId"`
	History               []ThreadResumeHistoryItem `json:"history,omitempty"`
	HistorySet            bool                      `json:"-"`
	Path                  *string                   `json:"path,omitempty"`
	ClientName            *string                   `json:"clientName,omitempty"`
	ExcludeTurns          bool                      `json:"excludeTurns,omitempty"`
	InitialTurnsPage      *ThreadInitialPageParams  `json:"initialTurnsPage,omitempty"`
	CWD                   *string                   `json:"cwd,omitempty"`
	Model                 *string                   `json:"model,omitempty"`
	ModelProvider         *string                   `json:"modelProvider,omitempty"`
	ApprovalPolicy        any                       `json:"approvalPolicy,omitempty"`
	ApprovalsReviewer     *string                   `json:"approvalsReviewer,omitempty"`
	BaseInstructions      *string                   `json:"baseInstructions,omitempty"`
	DeveloperInstructions *string                   `json:"developerInstructions,omitempty"`
	Config                map[string]any            `json:"config,omitempty"`
	Personality           *string                   `json:"personality,omitempty"`
	Sandbox               any                       `json:"sandbox,omitempty"`
	Permissions           *string                   `json:"permissions,omitempty"`
	ServiceTier           *string                   `json:"serviceTier,omitempty"`
	ServiceTierSet        bool                      `json:"-"`
	RuntimeWorkspaceRoots []string                  `json:"runtimeWorkspaceRoots,omitempty"`
}

func (p *ThreadResumeParams) UnmarshalJSON(data []byte) error {
	type threadResumeParamsAlias ThreadResumeParams
	var decoded threadResumeParamsAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	serviceTier, serviceTierSet, err := decodeServiceTierPresence(data)
	if err != nil {
		return err
	}
	if serviceTierSet {
		decoded.ServiceTierSet = true
		decoded.ServiceTier = serviceTier
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if historyRaw, ok := raw["history"]; ok && strings.TrimSpace(string(historyRaw)) != "null" {
		decoded.HistorySet = true
	}
	if decoded.Path != nil && *decoded.Path == "" {
		decoded.Path = nil
	}
	*p = ThreadResumeParams(decoded)
	return nil
}

func (p *ThreadResumeParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	if p.HistorySet && len(p.History) == 0 {
		return jsonRPCInvalidRequest("history must not be empty")
	}
	if err := p.InitialTurnsPage.Validate(); err != nil {
		return err
	}
	if err := validateThreadRuntimeWorkspaceRoots(p.RuntimeWorkspaceRoots); err != nil {
		return err
	}
	return nil
}

type ThreadInitialPageParams struct {
	Limit         *int          `json:"limit,omitempty"`
	SortDirection SortDirection `json:"sortDirection,omitempty"`
	ItemsView     TurnItemsView `json:"itemsView,omitempty"`
}

func (p *ThreadInitialPageParams) Validate() error {
	if p == nil {
		return nil
	}
	if p.Limit != nil && *p.Limit < 0 {
		return invalidLimitError()
	}
	if err := validateSortDirection(p.SortDirection); err != nil {
		return err
	}
	if err := validateTurnItemsView(p.ItemsView); err != nil {
		return err
	}
	return nil
}

type ThreadResumeResponse struct {
	Thread                  *Thread                          `json:"thread"`
	InitialTurnsPage        *TurnsPage                       `json:"initialTurnsPage,omitempty"`
	TurnsBackwardsCursor    *string                          `json:"turnsBackwardsCursor,omitempty"`
	ItemsBackwardsCursor    *string                          `json:"itemsBackwardsCursor,omitempty"`
	ApprovalPolicy          any                              `json:"approvalPolicy,omitempty"`
	ApprovalsReviewer       *string                          `json:"approvalsReviewer,omitempty"`
	CWD                     string                           `json:"cwd,omitempty"`
	RuntimeWorkspaceRoots   []string                         `json:"runtimeWorkspaceRoots,omitempty"`
	InstructionSources      []string                         `json:"instructionSources,omitempty"`
	Model                   string                           `json:"model,omitempty"`
	ModelProvider           string                           `json:"modelProvider,omitempty"`
	ReasoningEffort         *string                          `json:"reasoningEffort,omitempty"`
	Sandbox                 any                              `json:"sandbox,omitempty"`
	ActivePermissionProfile *sandbox.ActivePermissionProfile `json:"activePermissionProfile,omitempty"`
	MultiAgentMode          MultiAgentMode                   `json:"multiAgentMode,omitempty"`
	ServiceTier             *string                          `json:"serviceTier,omitempty"`
}

func (r *ThreadResumeResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Thread                  *Thread                          `json:"thread"`
		InitialTurnsPage        *TurnsPage                       `json:"initialTurnsPage"`
		TurnsBackwardsCursor    *string                          `json:"turnsBackwardsCursor"`
		ItemsBackwardsCursor    *string                          `json:"itemsBackwardsCursor"`
		ApprovalPolicy          any                              `json:"approvalPolicy"`
		ApprovalsReviewer       string                           `json:"approvalsReviewer"`
		CWD                     string                           `json:"cwd"`
		RuntimeWorkspaceRoots   []string                         `json:"runtimeWorkspaceRoots"`
		InstructionSources      []string                         `json:"instructionSources"`
		Model                   string                           `json:"model"`
		ModelProvider           string                           `json:"modelProvider"`
		ReasoningEffort         *string                          `json:"reasoningEffort"`
		Sandbox                 any                              `json:"sandbox"`
		ActivePermissionProfile *sandbox.ActivePermissionProfile `json:"activePermissionProfile"`
		MultiAgentMode          MultiAgentMode                   `json:"multiAgentMode"`
		ServiceTier             *string                          `json:"serviceTier"`
	}{
		Thread:                  r.Thread,
		InitialTurnsPage:        r.InitialTurnsPage,
		TurnsBackwardsCursor:    cloneString(r.TurnsBackwardsCursor),
		ItemsBackwardsCursor:    cloneString(r.ItemsBackwardsCursor),
		ApprovalPolicy:          threadResponseApprovalPolicy(r.ApprovalPolicy),
		ApprovalsReviewer:       threadResponseApprovalsReviewer(r.ApprovalsReviewer),
		CWD:                     r.CWD,
		RuntimeWorkspaceRoots:   stringSliceForJSON(r.RuntimeWorkspaceRoots),
		InstructionSources:      stringSliceForJSON(r.InstructionSources),
		Model:                   r.Model,
		ModelProvider:           r.ModelProvider,
		ReasoningEffort:         cloneString(r.ReasoningEffort),
		Sandbox:                 threadResponseSandbox(r.Sandbox),
		ActivePermissionProfile: cloneActivePermissionProfile(r.ActivePermissionProfile),
		MultiAgentMode:          threadResponseMultiAgentMode(r.MultiAgentMode),
		ServiceTier:             cloneString(r.ServiceTier),
	})
}

func threadResponseApprovalPolicy(value any) any {
	if value == nil {
		return string(sandbox.ApprovalOnRequest)
	}
	return value
}

func threadResponseApprovalsReviewer(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "user"
	}
	return *value
}

func threadResponseSandbox(value any) any {
	if value == nil {
		return sandbox.NewReadOnlyPolicy()
	}
	return value
}

func threadResponseMultiAgentMode(value MultiAgentMode) MultiAgentMode {
	if value == "" {
		return MultiAgentModeExplicitRequestOnly
	}
	return value
}

func cloneActivePermissionProfile(value *sandbox.ActivePermissionProfile) *sandbox.ActivePermissionProfile {
	if value == nil {
		return nil
	}
	return &sandbox.ActivePermissionProfile{
		ID:      value.ID,
		Extends: value.Extends,
	}
}

const RedactedThreadResumePayload = "[redacted]"

var chatGPTRemoteClientNames = map[string]bool{
	"codex_chatgpt_android_remote": true,
	"codex_chatgpt_ios_remote":     true,
}

func ShouldRedactThreadResumePayloads(clientName *string) bool {
	if clientName == nil {
		return false
	}
	return chatGPTRemoteClientNames[*clientName]
}

func RedactThreadResumePayloads(turns []Turn) []Turn {
	out := make([]Turn, len(turns))
	for i := range turns {
		out[i] = turns[i]
		out[i].Items = redactThreadItems(turns[i].Items)
	}
	return out
}

func RedactTurnsPagePayloads(page *TurnsPage) *TurnsPage {
	if page == nil {
		return nil
	}
	out := *page
	out.Data = RedactThreadResumePayloads(page.Data)
	return &out
}

func redactThreadItems(items []ThreadItem) []ThreadItem {
	out := make([]ThreadItem, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "imageGeneration", "image_generation", "image_generation_call":
			continue
		case "mcpToolCall", "mcp_tool_call":
			item = redactMCPThreadItem(item)
		}
		out = append(out, item)
	}
	return out
}

func redactMCPThreadItem(item ThreadItem) ThreadItem {
	if item.Data == nil {
		item.Data = map[string]any{}
	}
	item.Data = cloneAnyMap(item.Data)
	for _, key := range []string{"arguments", "input", "rawArguments", "raw_arguments"} {
		if _, ok := item.Data[key]; ok {
			item.Data[key] = RedactedThreadResumePayload
		}
	}
	if _, ok := item.Data["result"]; ok {
		item.Data["result"] = map[string]any{
			"content": []any{map[string]any{
				"type": "text",
				"text": RedactedThreadResumePayload,
			}},
		}
	}
	if rawError, ok := item.Data["error"].(map[string]any); ok {
		errorClone := cloneAnyMap(rawError)
		errorClone["message"] = RedactedThreadResumePayload
		item.Data["error"] = errorClone
	}
	return item
}

type ThreadForkParams struct {
	ThreadID              string           `json:"threadId"`
	Path                  *string          `json:"path,omitempty"`
	ParentItemID          *string          `json:"parentItemId,omitempty"`
	HistoryMode           session.ForkMode `json:"historyMode,omitempty"`
	LastN                 int              `json:"lastN,omitempty"`
	LastTurnID            string           `json:"lastTurnId,omitempty"`
	BeforeTurnID          string           `json:"beforeTurnId,omitempty"`
	CWD                   *string          `json:"cwd,omitempty"`
	Model                 *string          `json:"model,omitempty"`
	ModelProvider         *string          `json:"modelProvider,omitempty"`
	ApprovalPolicy        any              `json:"approvalPolicy,omitempty"`
	ApprovalsReviewer     *string          `json:"approvalsReviewer,omitempty"`
	BaseInstructions      *string          `json:"baseInstructions,omitempty"`
	DeveloperInstructions *string          `json:"developerInstructions,omitempty"`
	Config                map[string]any   `json:"config,omitempty"`
	Sandbox               any              `json:"sandbox,omitempty"`
	Permissions           *string          `json:"permissions,omitempty"`
	ServiceTier           *string          `json:"serviceTier,omitempty"`
	ServiceTierSet        bool             `json:"-"`
	RuntimeWorkspaceRoots []string         `json:"runtimeWorkspaceRoots,omitempty"`
	ThreadSource          *ThreadSource    `json:"threadSource,omitempty"`
	Ephemeral             bool             `json:"ephemeral,omitempty"`
	ExcludeTurns          bool             `json:"excludeTurns,omitempty"`
	DeferGoalContinuation bool             `json:"deferGoalContinuation,omitempty"`
}

func (p *ThreadForkParams) UnmarshalJSON(data []byte) error {
	type threadForkParamsAlias ThreadForkParams
	var decoded threadForkParamsAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	serviceTier, serviceTierSet, err := decodeServiceTierPresence(data)
	if err != nil {
		return err
	}
	if serviceTierSet {
		decoded.ServiceTierSet = true
		decoded.ServiceTier = serviceTier
	}
	if decoded.Path != nil && *decoded.Path == "" {
		decoded.Path = nil
	}
	*p = ThreadForkParams(decoded)
	return nil
}

func (p *ThreadForkParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: threadId or path is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(p.ThreadID) == "" && (p.Path == nil || strings.TrimSpace(*p.Path) == "") {
		return fmt.Errorf("%w: threadId or path is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(p.LastTurnID) != "" && strings.TrimSpace(p.BeforeTurnID) != "" {
		return jsonRPCInvalidRequest("`beforeTurnId` cannot be combined with `lastTurnId`")
	}
	if err := validateThreadRuntimeWorkspaceRoots(p.RuntimeWorkspaceRoots); err != nil {
		return err
	}
	return nil
}

type ThreadForkResponse struct {
	Thread                  *Thread                          `json:"thread"`
	ApprovalPolicy          any                              `json:"approvalPolicy,omitempty"`
	ApprovalsReviewer       *string                          `json:"approvalsReviewer,omitempty"`
	CWD                     string                           `json:"cwd,omitempty"`
	RuntimeWorkspaceRoots   []string                         `json:"runtimeWorkspaceRoots,omitempty"`
	InstructionSources      []string                         `json:"instructionSources,omitempty"`
	Model                   string                           `json:"model,omitempty"`
	ModelProvider           string                           `json:"modelProvider,omitempty"`
	ReasoningEffort         *string                          `json:"reasoningEffort,omitempty"`
	Sandbox                 any                              `json:"sandbox,omitempty"`
	ActivePermissionProfile *sandbox.ActivePermissionProfile `json:"activePermissionProfile,omitempty"`
	MultiAgentMode          MultiAgentMode                   `json:"multiAgentMode,omitempty"`
	ServiceTier             *string                          `json:"serviceTier,omitempty"`
}

func (r *ThreadForkResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Thread                  *Thread                          `json:"thread"`
		ApprovalPolicy          any                              `json:"approvalPolicy"`
		ApprovalsReviewer       string                           `json:"approvalsReviewer"`
		CWD                     string                           `json:"cwd"`
		RuntimeWorkspaceRoots   []string                         `json:"runtimeWorkspaceRoots"`
		InstructionSources      []string                         `json:"instructionSources"`
		Model                   string                           `json:"model"`
		ModelProvider           string                           `json:"modelProvider"`
		ReasoningEffort         *string                          `json:"reasoningEffort"`
		Sandbox                 any                              `json:"sandbox"`
		ActivePermissionProfile *sandbox.ActivePermissionProfile `json:"activePermissionProfile"`
		MultiAgentMode          MultiAgentMode                   `json:"multiAgentMode"`
		ServiceTier             *string                          `json:"serviceTier"`
	}{
		Thread:                  r.Thread,
		ApprovalPolicy:          threadResponseApprovalPolicy(r.ApprovalPolicy),
		ApprovalsReviewer:       threadResponseApprovalsReviewer(r.ApprovalsReviewer),
		CWD:                     r.CWD,
		RuntimeWorkspaceRoots:   stringSliceForJSON(r.RuntimeWorkspaceRoots),
		InstructionSources:      stringSliceForJSON(r.InstructionSources),
		Model:                   r.Model,
		ModelProvider:           r.ModelProvider,
		ReasoningEffort:         cloneString(r.ReasoningEffort),
		Sandbox:                 threadResponseSandbox(r.Sandbox),
		ActivePermissionProfile: cloneActivePermissionProfile(r.ActivePermissionProfile),
		MultiAgentMode:          threadResponseMultiAgentMode(r.MultiAgentMode),
		ServiceTier:             cloneString(r.ServiceTier),
	})
}

type ThreadArchiveParams struct {
	ThreadID string `json:"threadId"`
}

func (p *ThreadArchiveParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadArchiveResponse struct {
	archivedThreadIDs []session.ThreadID
}

type ThreadUnarchiveParams struct {
	ThreadID string `json:"threadId"`
}

func (p *ThreadUnarchiveParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadUnarchiveResponse struct {
	Thread *Thread `json:"thread"`
}

type ThreadDeleteParams struct {
	ThreadID string `json:"threadId"`
}

func (p *ThreadDeleteParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadDeleteResponse struct{}

type ThreadIncrementElicitationParams struct {
	ThreadID string `json:"threadId"`
}

func (p *ThreadIncrementElicitationParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadIncrementElicitationResponse struct {
	Count  int  `json:"count"`
	Paused bool `json:"paused"`
}

type ThreadDecrementElicitationParams struct {
	ThreadID string `json:"threadId"`
}

func (p *ThreadDecrementElicitationParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadDecrementElicitationResponse struct {
	Count  int  `json:"count"`
	Paused bool `json:"paused"`
}

type ThreadSetNameParams struct {
	ThreadID string `json:"threadId"`
	Name     string `json:"name"`
}

func (p *ThreadSetNameParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return jsonRPCInvalidRequest("thread name must not be empty")
	}
	return nil
}

type ThreadSetNameResponse struct{}

type ThreadUnsubscribeParams struct {
	ThreadID string `json:"threadId"`
}

func (p *ThreadUnsubscribeParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadUnsubscribeStatus string

const (
	ThreadUnsubscribeStatusNotLoaded     ThreadUnsubscribeStatus = "notLoaded"
	ThreadUnsubscribeStatusNotSubscribed ThreadUnsubscribeStatus = "notSubscribed"
	ThreadUnsubscribeStatusUnsubscribed  ThreadUnsubscribeStatus = "unsubscribed"
)

type ThreadUnsubscribeResponse struct {
	Status ThreadUnsubscribeStatus `json:"status"`
}

type ThreadMemoryMode string

const (
	ThreadMemoryModeEnabled  ThreadMemoryMode = "enabled"
	ThreadMemoryModeDisabled ThreadMemoryMode = "disabled"
)

type ThreadMemoryModeSetParams struct {
	ThreadID string           `json:"threadId"`
	Mode     ThreadMemoryMode `json:"mode"`
}

func (p *ThreadMemoryModeSetParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	switch p.Mode {
	case ThreadMemoryModeEnabled, ThreadMemoryModeDisabled:
		return nil
	default:
		return jsonRPCInvalidRequest(fmt.Sprintf("unsupported memory mode %q", p.Mode))
	}
}

type ThreadMemoryModeSetResponse struct{}

type MemoryResetResponse struct{}

type ThreadCompactStartParams struct {
	ThreadID string `json:"threadId"`
}

func (p *ThreadCompactStartParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadCompactStartResponse struct{}

type ThreadApproveGuardianDeniedActionParams struct {
	ThreadID string          `json:"threadId"`
	Event    json.RawMessage `json:"event,omitempty"`
	ActionID string          `json:"actionId,omitempty"`
}

func (p *ThreadApproveGuardianDeniedActionParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	if len(bytes.TrimSpace(p.Event)) == 0 && strings.TrimSpace(p.ActionID) == "" {
		return jsonRPCInvalidRequest("event is required")
	}
	return nil
}

type ThreadApproveGuardianDeniedActionResponse struct{}

type ThreadMetadataUpdateParams struct {
	ThreadID string                      `json:"threadId"`
	GitInfo  *ThreadMetadataGitInfoPatch `json:"gitInfo,omitempty"`
}

func (p *ThreadMetadataUpdateParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadMetadataGitInfoPatch struct {
	SHA       OptionalString `json:"sha,omitempty"`
	Branch    OptionalString `json:"branch,omitempty"`
	OriginURL OptionalString `json:"originUrl,omitempty"`
}

func (p ThreadMetadataGitInfoPatch) MarshalJSON() ([]byte, error) {
	fields := map[string]any{}
	if p.SHA.Set {
		fields["sha"] = &p.SHA
	}
	if p.Branch.Set {
		fields["branch"] = &p.Branch
	}
	if p.OriginURL.Set {
		fields["originUrl"] = &p.OriginURL
	}
	return json.Marshal(fields)
}

type OptionalString struct {
	Set   bool
	Value *string
}

func (o *OptionalString) UnmarshalJSON(data []byte) error {
	o.Set = true
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("null")) {
		o.Value = nil
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

func (o *OptionalString) MarshalJSON() ([]byte, error) {
	if o == nil || !o.Set || o.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*o.Value)
}

func (o *OptionalString) IsZero() bool {
	return o == nil || !o.Set
}

type ThreadMetadataUpdateResponse struct {
	Thread *Thread `json:"thread"`
}

type ThreadSectionMoveParams struct {
	ThreadID       string         `json:"threadId"`
	SectionID      OptionalString `json:"sectionId"`
	BeforeThreadID *string        `json:"beforeThreadId,omitempty"`
}

func (p ThreadSectionMoveParams) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ThreadID       string  `json:"threadId"`
		SectionID      *string `json:"sectionId"`
		BeforeThreadID *string `json:"beforeThreadId,omitempty"`
	}{ThreadID: p.ThreadID, SectionID: p.SectionID.Value, BeforeThreadID: p.BeforeThreadID})
}

func (p *ThreadSectionMoveParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return jsonRPCInvalidRequest("threadId is required")
	}
	if !p.SectionID.Set {
		return jsonRPCInvalidRequest("sectionId is required")
	}
	if p.SectionID.Value != nil && strings.TrimSpace(*p.SectionID.Value) == "" {
		return jsonRPCInvalidRequest("sectionId must not be empty")
	}
	if p.SectionID.Value == nil && p.BeforeThreadID != nil {
		return jsonRPCInvalidRequest("beforeThreadId requires a non-null sectionId")
	}
	return nil
}

type ThreadSectionMoveResponse struct{}

type ThreadListParams struct {
	Cursor           *string              `json:"cursor,omitempty"`
	Limit            *int                 `json:"limit,omitempty"`
	SortKey          SortKey              `json:"sortKey,omitempty"`
	SortDirection    SortDirection        `json:"sortDirection,omitempty"`
	ModelProviders   []string             `json:"modelProviders,omitempty"`
	SourceKinds      []ThreadSourceKind   `json:"sourceKinds,omitempty"`
	Archived         *bool                `json:"archived,omitempty"`
	SectionID        OptionalString       `json:"sectionId,omitempty"`
	CWD              *ThreadListCwdFilter `json:"cwd,omitempty"`
	UseStateDBOnly   bool                 `json:"useStateDbOnly,omitempty"`
	SearchTerm       *string              `json:"searchTerm,omitempty"`
	ParentThreadID   *string              `json:"parentThreadId,omitempty"`
	AncestorThreadID *string              `json:"ancestorThreadId,omitempty"`
}

func (p ThreadListParams) MarshalJSON() ([]byte, error) {
	type alias ThreadListParams
	data, err := json.Marshal(alias(p))
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if !p.SectionID.Set {
		delete(fields, "sectionId")
	} else {
		fields["sectionId"] = p.SectionID.Value
	}
	return json.Marshal(fields)
}

type ThreadSectionListParams struct {
	Cursor *string `json:"cursor,omitempty"`
	Limit  *int    `json:"limit,omitempty"`
}

func (p *ThreadSectionListParams) Validate() error {
	if p != nil && p.Limit != nil && *p.Limit < 0 {
		return invalidLimitError()
	}
	return nil
}

type ThreadSectionListResponse struct {
	Data       []ThreadSection `json:"data"`
	NextCursor *string         `json:"nextCursor"`
}

func (r *ThreadSectionListResponse) MarshalJSON() ([]byte, error) {
	data := append([]ThreadSection(nil), r.Data...)
	if data == nil {
		data = []ThreadSection{}
	}
	return json.Marshal(struct {
		Data       []ThreadSection `json:"data"`
		NextCursor *string         `json:"nextCursor"`
	}{Data: data, NextCursor: r.NextCursor})
}

type ThreadSectionCreateParams struct {
	Name       string                   `json:"name"`
	Appearance *ThreadSectionAppearance `json:"appearance,omitempty"`
}

func (p *ThreadSectionCreateParams) Validate() error {
	if p == nil || strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidRequest)
	}
	if err := validateThreadSectionAppearance(p.Appearance); err != nil {
		return err
	}
	return nil
}

type ThreadSectionCreateResponse struct {
	Section ThreadSection `json:"section"`
}

type ThreadSectionUpdateParams struct {
	SectionID     string                   `json:"sectionId"`
	Name          string                   `json:"name"`
	Appearance    *ThreadSectionAppearance `json:"-"`
	AppearanceSet bool                     `json:"-"`
}

func (p *ThreadSectionUpdateParams) UnmarshalJSON(data []byte) error {
	type threadSectionUpdateParamsAlias ThreadSectionUpdateParams
	var decoded threadSectionUpdateParamsAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if appearanceRaw, ok := raw["appearance"]; ok {
		decoded.AppearanceSet = true
		if strings.TrimSpace(string(appearanceRaw)) != "null" {
			var appearance ThreadSectionAppearance
			if err := json.Unmarshal(appearanceRaw, &appearance); err != nil {
				return err
			}
			decoded.Appearance = &appearance
		}
	}
	*p = ThreadSectionUpdateParams(decoded)
	return nil
}

func (p *ThreadSectionUpdateParams) Validate() error {
	if p == nil || strings.TrimSpace(p.SectionID) == "" {
		return fmt.Errorf("%w: sectionId is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidRequest)
	}
	if p.AppearanceSet {
		if err := validateThreadSectionAppearance(p.Appearance); err != nil {
			return err
		}
	}
	return nil
}

type ThreadSectionUpdateResponse struct {
	Section ThreadSection `json:"section"`
}

type ThreadSectionDeleteParams struct {
	SectionID string `json:"sectionId"`
}

func (p *ThreadSectionDeleteParams) Validate() error {
	if p == nil || strings.TrimSpace(p.SectionID) == "" {
		return fmt.Errorf("%w: sectionId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadSectionDeleteResponse struct {
	Deleted bool `json:"deleted"`
}

func validateThreadSectionAppearance(appearance *ThreadSectionAppearance) error {
	if appearance == nil {
		return nil
	}
	if appearance.Icon != nil && len(*appearance.Icon) > 64 {
		return fmt.Errorf("%w: section appearance icon must not exceed 64 bytes", ErrInvalidRequest)
	}
	if appearance.Color != nil && len(*appearance.Color) > 64 {
		return fmt.Errorf("%w: section appearance color must not exceed 64 bytes", ErrInvalidRequest)
	}
	return nil
}

func (p *ThreadListParams) Validate() error {
	if p == nil {
		return nil
	}
	if p.ParentThreadID != nil && p.AncestorThreadID != nil {
		return jsonRPCInvalidRequest("parentThreadId and ancestorThreadId are mutually exclusive")
	}
	if p.Limit != nil && *p.Limit < 0 {
		return invalidLimitError()
	}
	if err := validateThreadSourceKinds(p.SourceKinds); err != nil {
		return err
	}
	return nil
}

type ThreadListResponse struct {
	Data            []Thread `json:"data"`
	NextCursor      *string  `json:"nextCursor"`
	BackwardsCursor *string  `json:"backwardsCursor"`
}

func (r *ThreadListResponse) MarshalJSON() ([]byte, error) {
	data := append([]Thread(nil), r.Data...)
	if data == nil {
		data = []Thread{}
	}
	return json.Marshal(struct {
		Data            []Thread `json:"data"`
		NextCursor      *string  `json:"nextCursor"`
		BackwardsCursor *string  `json:"backwardsCursor"`
	}{
		Data:            data,
		NextCursor:      r.NextCursor,
		BackwardsCursor: r.BackwardsCursor,
	})
}

type ThreadSearchParams struct {
	Cursor        *string            `json:"cursor,omitempty"`
	Limit         *int               `json:"limit,omitempty"`
	SortKey       SortKey            `json:"sortKey,omitempty"`
	SortDirection SortDirection      `json:"sortDirection,omitempty"`
	SourceKinds   []ThreadSourceKind `json:"sourceKinds,omitempty"`
	Archived      *bool              `json:"archived,omitempty"`
	SearchTerm    string             `json:"searchTerm"`
}

type ThreadSearchOccurrencesParams struct {
	ThreadID   string  `json:"threadId"`
	SearchTerm string  `json:"searchTerm"`
	Cursor     *string `json:"cursor"`
	Limit      *uint32 `json:"limit"`
}

func (p *ThreadSearchOccurrencesParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return jsonRPCInvalidRequest("threadId is required")
	}
	if strings.TrimSpace(p.SearchTerm) == "" {
		return jsonRPCInvalidRequest("thread/searchOccurrences requires a non-empty searchTerm")
	}
	return nil
}

type ThreadSearchTextRange struct {
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
}

type ThreadSearchOccurrence struct {
	TurnID            string                `json:"turnId"`
	ItemID            string                `json:"itemId"`
	Snippet           string                `json:"snippet"`
	SnippetMatchRange ThreadSearchTextRange `json:"snippetMatchRange"`
	TurnCursor        string                `json:"turnCursor"`
}

type ThreadSearchOccurrencesResponse struct {
	Data       []ThreadSearchOccurrence `json:"data"`
	NextCursor *string                  `json:"nextCursor"`
}

func (r *ThreadSearchOccurrencesResponse) MarshalJSON() ([]byte, error) {
	data := append([]ThreadSearchOccurrence(nil), r.Data...)
	if data == nil {
		data = []ThreadSearchOccurrence{}
	}
	return json.Marshal(struct {
		Data       []ThreadSearchOccurrence `json:"data"`
		NextCursor *string                  `json:"nextCursor"`
	}{Data: data, NextCursor: cloneStringPtrAppserver(r.NextCursor)})
}

func buildThreadSearchOccurrences(record *session.Record, params *ThreadSearchOccurrencesParams) (*ThreadSearchOccurrencesResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	start, err := decodeOccurrenceCursor(params.Cursor)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(params.SearchTerm))
	all := make([]ThreadSearchOccurrence, 0)
	turns := turnsFromRecord(record)
	for turnIndex, turn := range turns {
		for _, item := range summarizeTurnItems(turn.Items, turn.Status) {
			text := visibleOccurrenceText(item)
			lower := strings.ToLower(text)
			searchFrom := 0
			for searchFrom <= len(lower) {
				rel := strings.Index(lower[searchFrom:], needle)
				if rel < 0 {
					break
				}
				byteStart := searchFrom + rel
				byteEnd := byteStart + len(needle)
				all = append(all, ThreadSearchOccurrence{
					TurnID: turn.ID, ItemID: item.ID, Snippet: text,
					SnippetMatchRange: ThreadSearchTextRange{Start: utf16Offset(text[:byteStart]), End: utf16Offset(text[:byteEnd])},
					TurnCursor:        strconv.Itoa(turnIndex),
				})
				searchFrom = byteEnd
			}
		}
	}
	if start > len(all) {
		return nil, jsonRPCInvalidRequest("invalid cursor")
	}
	limit := 50
	if params.Limit != nil {
		limit = int(*params.Limit)
	}
	end := start + limit
	if end > len(all) {
		end = len(all)
	}
	var next *string
	if end < len(all) {
		value := base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
		next = &value
	}
	return &ThreadSearchOccurrencesResponse{Data: append([]ThreadSearchOccurrence(nil), all[start:end]...), NextCursor: next}, nil
}

func decodeOccurrenceCursor(cursor *string) (int, error) {
	if cursor == nil || strings.TrimSpace(*cursor) == "" {
		return 0, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(*cursor))
	if err != nil {
		return 0, jsonRPCInvalidRequest("invalid cursor")
	}
	value, err := strconv.Atoi(string(data))
	if err != nil || value < 0 {
		return 0, jsonRPCInvalidRequest("invalid cursor")
	}
	return value, nil
}

func visibleOccurrenceText(item ThreadItem) string {
	if threadItemIsUserMessage(item) {
		if strings.TrimSpace(item.Text) != "" {
			return item.Text
		}
		parts := threadItemUserInputContent(&item)
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			if text, _ := part["text"].(string); text != "" {
				texts = append(texts, text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return item.Text
}

func utf16Offset(value string) uint32 {
	var count uint32
	for _, r := range value {
		if r > 0xffff {
			count += 2
		} else {
			count++
		}
	}
	return count
}

func (p *ThreadSearchParams) Validate() error {
	if p == nil || strings.TrimSpace(p.SearchTerm) == "" {
		return jsonRPCInvalidRequest("thread/search requires a non-empty searchTerm")
	}
	if err := validateThreadSourceKinds(p.SourceKinds); err != nil {
		return err
	}
	if p.SortKey == SortSectionPosition {
		return jsonRPCInvalidRequest(fmt.Sprintf("unsupported sortKey %q", p.SortKey))
	}
	return nil
}

func validateThreadSourceKinds(kinds []ThreadSourceKind) error {
	for _, kind := range kinds {
		switch kind {
		case ThreadSourceKindCli,
			ThreadSourceKindVsCode,
			ThreadSourceKindExec,
			ThreadSourceKindAppServer,
			ThreadSourceKindSubAgent,
			ThreadSourceKindSubAgentReview,
			ThreadSourceKindSubAgentCompact,
			ThreadSourceKindSubAgentThreadSpawn,
			ThreadSourceKindSubAgentOther,
			ThreadSourceKindUnknown:
			continue
		default:
			return jsonRPCInvalidRequest(fmt.Sprintf("unsupported sourceKind %q", kind))
		}
	}
	return nil
}

type ThreadSearchResult struct {
	Thread  Thread `json:"thread"`
	Snippet string `json:"snippet"`
}

type ThreadSearchResponse struct {
	Data            []ThreadSearchResult `json:"data"`
	NextCursor      *string              `json:"nextCursor"`
	BackwardsCursor *string              `json:"backwardsCursor"`
}

func (r *ThreadSearchResponse) MarshalJSON() ([]byte, error) {
	data := append([]ThreadSearchResult(nil), r.Data...)
	if data == nil {
		data = []ThreadSearchResult{}
	}
	return json.Marshal(struct {
		Data            []ThreadSearchResult `json:"data"`
		NextCursor      *string              `json:"nextCursor"`
		BackwardsCursor *string              `json:"backwardsCursor"`
	}{
		Data:            data,
		NextCursor:      r.NextCursor,
		BackwardsCursor: r.BackwardsCursor,
	})
}

type ThreadRollbackParams struct {
	ThreadID string `json:"threadId"`
	NumTurns int    `json:"numTurns"`
}

func (p *ThreadRollbackParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	if p.NumTurns < 1 {
		return jsonRPCInvalidRequest("numTurns must be >= 1")
	}
	return nil
}

type ThreadRollbackResponse struct {
	Thread *Thread `json:"thread"`
}

// ThreadRevertParams replaces a paginated thread's durable history with the
// prefix before beforeTurnId (Rust #38440). Local file changes are unaffected.
type ThreadRevertParams struct {
	ThreadID     string `json:"threadId"`
	BeforeTurnID string `json:"beforeTurnId"`
}

func (p *ThreadRevertParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(p.BeforeTurnID) == "" {
		return fmt.Errorf("%w: beforeTurnId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadRevertResponse struct {
	Thread               *Thread `json:"thread"`
	TurnsBackwardsCursor *string `json:"turnsBackwardsCursor,omitempty"`
	ItemsBackwardsCursor *string `json:"itemsBackwardsCursor,omitempty"`
}

type QueuedSubmission struct {
	ID                  string `json:"id"`
	Input               []any  `json:"input"`
	ClientUserMessageID string `json:"clientUserMessageId"`
}

type ThreadQueueAddParams struct {
	ThreadID            string `json:"threadId"`
	Input               []any  `json:"input"`
	ClientUserMessageID string `json:"clientUserMessageId"`
}

func (p *ThreadQueueAddParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	if len(p.Input) == 0 {
		return jsonRPCInvalidRequest("input is required")
	}
	return nil
}

type ThreadQueueAddResponse struct {
	QueuedSubmission *QueuedSubmission `json:"queuedSubmission"`
}

type ThreadQueueListParams struct {
	ThreadID string  `json:"threadId"`
	Cursor   *string `json:"cursor,omitempty"`
	Limit    *int    `json:"limit,omitempty"`
}

func (p *ThreadQueueListParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	if p.Limit != nil && *p.Limit < 0 {
		return invalidLimitError()
	}
	return nil
}

type ThreadQueueListResponse struct {
	Data       []QueuedSubmission `json:"data"`
	NextCursor *string            `json:"nextCursor,omitempty"`
}

type ThreadQueueUpdateParams struct {
	ThreadID           string `json:"threadId"`
	QueuedSubmissionID string `json:"queuedSubmissionId"`
	Input              []any  `json:"input"`
}

func (p *ThreadQueueUpdateParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(p.QueuedSubmissionID) == "" {
		return fmt.Errorf("%w: queuedSubmissionId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadQueueUpdateResponse struct {
	QueuedSubmission *QueuedSubmission `json:"queuedSubmission"`
}

type ThreadQueueDeleteParams struct {
	ThreadID           string `json:"threadId"`
	QueuedSubmissionID string `json:"queuedSubmissionId"`
}

func (p *ThreadQueueDeleteParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(p.QueuedSubmissionID) == "" {
		return fmt.Errorf("%w: queuedSubmissionId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadQueueDeleteResponse struct {
	Deleted bool `json:"deleted"`
}

type ThreadQueueReorderParams struct {
	ThreadID            string   `json:"threadId"`
	QueuedSubmissionIDs []string `json:"queuedSubmissionIds"`
}

func (p *ThreadQueueReorderParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	if len(p.QueuedSubmissionIDs) == 0 {
		return jsonRPCInvalidRequest("queuedSubmissionIds is required")
	}
	return nil
}

type ThreadQueueReorderResponse struct{}

type ThreadQueueStartParams struct {
	ThreadID           string  `json:"threadId"`
	QueuedSubmissionID *string `json:"queuedSubmissionId,omitempty"`
}

func (p *ThreadQueueStartParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadQueueStartResponse struct {
	Turn *turn.TurnRecord `json:"turn"`
}

type ThreadLoadedListParams struct {
	Cursor *string `json:"cursor,omitempty"`
	Limit  *int    `json:"limit,omitempty"`
}

func (p *ThreadLoadedListParams) Validate() error {
	if p == nil {
		return nil
	}
	if p.Cursor != nil && !validThreadLoadedListCursor(*p.Cursor) {
		return jsonRPCInvalidRequest(fmt.Sprintf("invalid cursor: %s", *p.Cursor))
	}
	if p.Limit != nil && *p.Limit < 0 {
		return invalidLimitError()
	}
	return nil
}

func validThreadLoadedListCursor(value string) bool {
	if validUUIDString(value) {
		return true
	}
	return validLegacyAppServerThreadCursor(value)
}

func validLegacyAppServerThreadCursor(value string) bool {
	if !strings.HasPrefix(value, "thread-") || len(value) == len("thread-") {
		return false
	}
	for _, ch := range value[len("thread-"):] {
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9':
		case ch == '-' || ch == '_':
		default:
			return false
		}
	}
	return true
}

type ThreadLoadedListResponse struct {
	Data       []string `json:"data"`
	NextCursor *string  `json:"nextCursor"`
}

func (r *ThreadLoadedListResponse) MarshalJSON() ([]byte, error) {
	data := append([]string(nil), r.Data...)
	if data == nil {
		data = []string{}
	}
	return json.Marshal(struct {
		Data       []string `json:"data"`
		NextCursor *string  `json:"nextCursor"`
	}{
		Data:       data,
		NextCursor: r.NextCursor,
	})
}

type ThreadReadParams struct {
	ThreadID     string `json:"threadId"`
	IncludeTurns bool   `json:"includeTurns,omitempty"`
}

func (p *ThreadReadParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadReadResponse struct {
	Thread *Thread `json:"thread"`
}

type ThreadItemsListParams struct {
	ThreadID      string        `json:"threadId"`
	TurnID        *string       `json:"turnId,omitempty"`
	Cursor        *string       `json:"cursor,omitempty"`
	Limit         *int          `json:"limit,omitempty"`
	SortDirection SortDirection `json:"sortDirection,omitempty"`
}

func (p *ThreadItemsListParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	if p.Limit != nil && *p.Limit < 0 {
		return invalidLimitError()
	}
	if err := validateSortDirection(p.SortDirection); err != nil {
		return err
	}
	return nil
}

type ThreadItemsListResponse struct {
	Data            []ThreadItemEntry `json:"data"`
	NextCursor      *string           `json:"nextCursor"`
	BackwardsCursor *string           `json:"backwardsCursor"`
}

type ThreadItemEntry struct {
	TurnID string     `json:"turnId"`
	Item   ThreadItem `json:"item"`
}

func (r *ThreadItemsListResponse) MarshalJSON() ([]byte, error) {
	data := append([]ThreadItemEntry(nil), r.Data...)
	if data == nil {
		data = []ThreadItemEntry{}
	}
	return json.Marshal(struct {
		Data            []ThreadItemEntry `json:"data"`
		NextCursor      *string           `json:"nextCursor"`
		BackwardsCursor *string           `json:"backwardsCursor"`
	}{
		Data:            data,
		NextCursor:      r.NextCursor,
		BackwardsCursor: r.BackwardsCursor,
	})
}

type ThreadInjectItemsParams struct {
	ThreadID string            `json:"threadId"`
	Items    []json.RawMessage `json:"items"`
}

func (p *ThreadInjectItemsParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	return nil
}

type ThreadInjectItemsResponse struct{}

type ThreadTurnsListParams struct {
	ThreadID      string        `json:"threadId"`
	Cursor        *string       `json:"cursor,omitempty"`
	Limit         *int          `json:"limit,omitempty"`
	SortDirection SortDirection `json:"sortDirection,omitempty"`
	ItemsView     TurnItemsView `json:"itemsView,omitempty"`
}

func (p *ThreadTurnsListParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	if p.Limit != nil && *p.Limit < 0 {
		return invalidLimitError()
	}
	if err := validateSortDirection(p.SortDirection); err != nil {
		return err
	}
	if err := validateTurnItemsView(p.ItemsView); err != nil {
		return err
	}
	return nil
}

func validateSortDirection(value SortDirection) error {
	if value != "" && value != SortAsc && value != SortDesc {
		return jsonRPCInvalidRequest(fmt.Sprintf("unsupported sortDirection %q", value))
	}
	return nil
}

func invalidLimitError() error {
	return jsonRPCInvalidRequest("limit must be non-negative")
}

func validateTurnItemsView(value TurnItemsView) error {
	if value != "" && value != TurnItemsNotLoaded && value != TurnItemsSummary && value != TurnItemsFull {
		return jsonRPCInvalidRequest(fmt.Sprintf("unsupported itemsView %q", value))
	}
	return nil
}

type TurnsPage struct {
	Data            []Turn  `json:"data"`
	NextCursor      *string `json:"nextCursor"`
	BackwardsCursor *string `json:"backwardsCursor"`
}

func (p *TurnsPage) MarshalJSON() ([]byte, error) {
	data := append([]Turn(nil), p.Data...)
	if data == nil {
		data = []Turn{}
	}
	return json.Marshal(struct {
		Data            []Turn  `json:"data"`
		NextCursor      *string `json:"nextCursor"`
		BackwardsCursor *string `json:"backwardsCursor"`
	}{
		Data:            data,
		NextCursor:      p.NextCursor,
		BackwardsCursor: p.BackwardsCursor,
	})
}

type ThreadStartedNotification struct {
	Thread *Thread `json:"thread"`
}

type ThreadStatusChangedNotification struct {
	ThreadID string       `json:"threadId"`
	Status   ThreadStatus `json:"status"`
}

type ThreadIDNotification struct {
	ThreadID string `json:"threadId"`
}

type ThreadNameUpdatedNotification struct {
	ThreadID   string  `json:"threadId"`
	ThreadName *string `json:"threadName"`
}

type EnvironmentConnectionNotification struct {
	ThreadID      string `json:"threadId"`
	EnvironmentID string `json:"environmentId"`
}

func BuildThread(record *session.Record, path string, includeTurns bool) *Thread {
	if record == nil {
		return nil
	}
	threadID := string(record.ID)
	sessionID := record.SessionID
	if sessionID == "" {
		sessionID = threadID
	}
	historyMode := ThreadHistoryMode(record.Metadata.HistoryMode)
	if historyMode == "" {
		historyMode = ThreadHistoryLegacy
	}
	modelProvider := record.Metadata.ModelProvider
	if modelProvider == "" {
		modelProvider = record.Metadata.Model
	}
	source := SessionSourceFromString(record.Metadata.Source)
	threadSource := threadSourcePtr(record.Metadata.ThreadSource)
	threadPath := stringPtrIfNotEmpty(path)
	var recencyAt *int64
	if !record.RecencyAt.IsZero() {
		value := record.RecencyAt.Unix()
		recencyAt = &value
	}
	var sectionEnteredAt *int64
	if record.SectionEnteredAt != nil && !record.SectionEnteredAt.IsZero() {
		value := record.SectionEnteredAt.Unix()
		sectionEnteredAt = &value
	}
	thread := &Thread{
		ID:               threadID,
		SessionID:        sessionID,
		ForkedFromID:     stringPtrIfNotEmpty(string(record.ForkedFromID)),
		ParentThreadID:   stringPtrIfNotEmpty(string(record.ParentThreadID)),
		Preview:          record.Preview,
		Ephemeral:        boolFromMap(record.Metadata.Extra, "ephemeral"),
		Section:          threadSectionFromRecord(record),
		SectionEnteredAt: sectionEnteredAt,
		HistoryMode:      historyMode,
		ModelProvider:    modelProvider,
		CreatedAt:        unixOrZero(record.CreatedAt),
		UpdatedAt:        unixOrZero(record.UpdatedAt),
		RecencyAt:        recencyAt,
		Status:           NotLoadedStatus(),
		Path:             threadPath,
		CWD:              record.Metadata.CWD,
		CLIVersion:       record.Metadata.CLIVersion,
		Source:           source,
		ThreadSource:     threadSource,
		AgentNickname:    stringPtrIfNotEmpty(record.Metadata.AgentNickname),
		AgentRole:        stringPtrIfNotEmpty(record.Metadata.AgentRole),
		GitInfo:          gitInfoFromMap(record.Metadata.Git),
		Name:             stringPtrIfNotEmpty(record.Title),
		Turns:            []Turn{},
	}
	if includeTurns {
		thread.Turns = turnsFromRecord(record)
		normalizeThreadTurnsStatus(thread.Turns, IdleStatus(), false)
	}
	return thread
}

func SessionSourceFromString(value string) SessionSource {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "cli":
		return SessionSourceCli
	case "vscode", "vs_code":
		return SessionSourceVsCode
	case "exec":
		return SessionSourceExec
	case "appserver", "app_server", "mcp":
		return SessionSourceAppServer
	case "":
		return SessionSourceUnknown
	default:
		return SessionSource(value)
	}
}

func ThreadSourceFromString(value string) ThreadSource {
	switch strings.TrimSpace(value) {
	case "":
		return ""
	case string(ThreadSourceUser):
		return ThreadSourceUser
	case string(ThreadSourceSubagent):
		return ThreadSourceSubagent
	case string(ThreadSourceMemoryConsolidation):
		return ThreadSourceMemoryConsolidation
	default:
		return ThreadSource(value)
	}
}

func SourceKindToSessionSource(kind ThreadSourceKind) string {
	switch kind {
	case ThreadSourceKindCli:
		return string(SessionSourceCli)
	case ThreadSourceKindVsCode:
		return string(SessionSourceVsCode)
	case ThreadSourceKindExec:
		return string(SessionSourceExec)
	case ThreadSourceKindAppServer:
		return string(SessionSourceAppServer)
	case ThreadSourceKindUnknown:
		return string(SessionSourceUnknown)
	default:
		return ""
	}
}

func ComputeSourceFilters(kinds []ThreadSourceKind) ([]string, []string) {
	if len(kinds) == 0 {
		return defaultThreadListSourceFilters(), nil
	}
	filter := make([]string, 0, len(kinds))
	requiresPostFilter := false
	for _, kind := range kinds {
		filter = append(filter, string(kind))
		if sourceKindRequiresPostFilter(kind) {
			requiresPostFilter = true
		}
	}
	if requiresPostFilter {
		return nil, filter
	}
	sources := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		if source := SourceKindToSessionSource(kind); source != "" {
			sources = append(sources, source)
		}
	}
	return sources, filter
}

func sourceKindRequiresPostFilter(kind ThreadSourceKind) bool {
	switch kind {
	case ThreadSourceKindExec,
		ThreadSourceKindAppServer,
		ThreadSourceKindSubAgent,
		ThreadSourceKindSubAgentReview,
		ThreadSourceKindSubAgentCompact,
		ThreadSourceKindSubAgentThreadSpawn,
		ThreadSourceKindSubAgentOther,
		ThreadSourceKindUnknown:
		return true
	default:
		return false
	}
}

func BuildListOptions(params *ThreadListParams) (session.ListOptions, error) {
	if err := params.Validate(); err != nil {
		return session.ListOptions{}, err
	}
	options := session.ListOptions{
		PageSize:      defaultPageSize,
		SortKey:       session.SortCreatedAt,
		SortDirection: session.SortDesc,
		Sources:       defaultThreadListSourceFilters(),
	}
	if params == nil {
		return options, nil
	}
	if params.Cursor != nil {
		if !validThreadListCursor(*params.Cursor) {
			return session.ListOptions{}, jsonRPCInvalidRequest(fmt.Sprintf("invalid cursor: %s", *params.Cursor))
		}
		options.Cursor = *params.Cursor
	}
	if params.Limit != nil {
		options.PageSize = *params.Limit
		if options.PageSize < 1 {
			options.PageSize = 1
		}
		if options.PageSize > maxThreadListPageSize {
			options.PageSize = maxThreadListPageSize
		}
	}
	switch params.SortKey {
	case SortUpdatedAt:
		options.SortKey = session.SortUpdatedAt
	case SortRecencyAt:
		options.SortKey = session.SortRecencyAt
	case SortSectionPosition:
		options.SortKey = session.SortSectionPosition
	case SortCreatedAt, "":
		options.SortKey = session.SortCreatedAt
	default:
		return session.ListOptions{}, jsonRPCInvalidRequest(fmt.Sprintf("unsupported sortKey %q", params.SortKey))
	}
	switch params.SortDirection {
	case SortAsc:
		options.SortDirection = session.SortAsc
	case SortDesc:
		options.SortDirection = session.SortDesc
	case "":
		if options.SortKey == session.SortSectionPosition {
			options.SortDirection = session.SortAsc
		} else {
			options.SortDirection = session.SortDesc
		}
	default:
		return session.ListOptions{}, jsonRPCInvalidRequest(fmt.Sprintf("unsupported sortDirection %q", params.SortDirection))
	}
	if params.Archived != nil {
		options.Archived = *params.Archived
	}
	if params.SectionID.Set {
		options.SectionSet = true
		options.SectionID = cloneString(params.SectionID.Value)
	}
	options.ModelProviders = append([]string(nil), params.ModelProviders...)
	if params.CWD != nil {
		options.CWDs = normalizeThreadListCWDs(params.CWD.Values)
	}
	if params.SearchTerm != nil {
		options.Search = *params.SearchTerm
	}
	if params.SourceKinds != nil {
		options.Sources, options.SourceKinds = ComputeSourceFilters(params.SourceKinds)
	}
	if params.ParentThreadID != nil {
		threadID, err := parseThreadListRelationThreadID(*params.ParentThreadID, "parent")
		if err != nil {
			return session.ListOptions{}, err
		}
		options.Relation = &session.RelationFilter{DirectChildrenOf: threadID}
	}
	if params.AncestorThreadID != nil {
		threadID, err := parseThreadListRelationThreadID(*params.AncestorThreadID, "ancestor")
		if err != nil {
			return session.ListOptions{}, err
		}
		options.Relation = &session.RelationFilter{DescendantsOf: threadID}
	}
	if options.Relation != nil && params.SourceKinds == nil {
		options.Sources = nil
	}
	return options, nil
}

func normalizeThreadListCWDs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, cleanRuntimeWorkspaceRoot(value))
	}
	return out
}

func parseThreadListRelationThreadID(value string, label string) (session.ThreadID, error) {
	if !validUUIDString(value) {
		return "", jsonRPCInvalidRequest(fmt.Sprintf("invalid %s thread id: invalid UUID", label))
	}
	return session.ThreadID(value), nil
}

func validUUIDString(value string) bool {
	if strings.HasPrefix(strings.ToLower(value), "urn:uuid:") {
		value = value[len("urn:uuid:"):]
	}
	if len(value) == 38 && value[0] == '{' && value[len(value)-1] == '}' {
		value = value[1 : len(value)-1]
	}
	switch len(value) {
	case 32:
		for i := 0; i < len(value); i++ {
			if !isHexDigit(value[i]) {
				return false
			}
		}
		return true
	case 36:
		for i := 0; i < len(value); i++ {
			switch i {
			case 8, 13, 18, 23:
				if value[i] != '-' {
					return false
				}
			default:
				if !isHexDigit(value[i]) {
					return false
				}
			}
		}
		return true
	default:
		return false
	}
}

func isHexDigit(ch byte) bool {
	return ('0' <= ch && ch <= '9') || ('a' <= ch && ch <= 'f') || ('A' <= ch && ch <= 'F')
}

func defaultThreadListSourceFilters() []string {
	return []string{
		string(SessionSourceCli),
		string(SessionSourceVsCode),
		"atlas",
		"chatgpt",
	}
}

func validThreadListCursor(cursor string) bool {
	if cursor == "" {
		return false
	}
	allDigits := true
	for _, ch := range cursor {
		if ch < '0' || ch > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return true
	}
	if position, threadID, ok := strings.Cut(cursor, "|"); ok && strings.TrimSpace(threadID) != "" {
		_, err := strconv.ParseInt(position, 10, 64)
		return err == nil
	}
	_, err := time.Parse(time.RFC3339Nano, cursor)
	return err == nil
}

func BuildListResponse(page *session.Page, store *session.Store, includeTurns bool) (*ThreadListResponse, error) {
	if page == nil {
		return &ThreadListResponse{Data: []Thread{}}, nil
	}
	data := make([]Thread, 0, len(page.Records))
	for i := range page.Records {
		record := &page.Records[i]
		path, _ := record.Metadata.Extra["rollout_path"].(string)
		if store != nil {
			if value, err := rollout.FindThreadPath(codexHomeForProtocolStore(store), string(record.ID), record.Archived); strings.TrimSpace(path) == "" && err == nil {
				path = value
			}
		}
		if thread := BuildThread(record, path, includeTurns); thread != nil {
			data = append(data, *thread)
		}
	}
	var next *string
	if page.NextCursor != "" {
		next = &page.NextCursor
	}
	var backwards *string
	if page.BackwardsCursor != "" {
		backwards = &page.BackwardsCursor
	}
	return &ThreadListResponse{Data: data, NextCursor: next, BackwardsCursor: backwards}, nil
}

func codexHomeForProtocolStore(store *session.Store) string {
	if store == nil {
		return ""
	}
	root := store.Root()
	if filepath.Base(root) == "sessions" {
		return filepath.Dir(root)
	}
	return root
}

func BuildItemsResponse(record *session.Record, params *ThreadItemsListParams) (*ThreadItemsListResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if record == nil {
		return &ThreadItemsListResponse{Data: []ThreadItemEntry{}}, nil
	}
	items := make([]ThreadItemEntry, 0, len(record.Items))
	for _, item := range record.Items {
		if sessionItemIsHiddenThreadItem(&item) {
			continue
		}
		threadItem := BuildThreadItem(item)
		if params.TurnID != nil && threadItem.TurnID != *params.TurnID {
			continue
		}
		items = append(items, ThreadItemEntry{TurnID: threadItem.TurnID, Item: threadItem})
	}
	if params.SortDirection == SortDesc {
		for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
			items[left], items[right] = items[right], items[left]
		}
	}
	start, err := parseCursor(params.Cursor)
	if err != nil {
		return nil, err
	}
	limit := threadItemsDefaultLimit
	if params.Limit != nil {
		limit = *params.Limit
		if limit < 1 {
			limit = 1
		}
		if limit > threadItemsMaxLimit {
			limit = threadItemsMaxLimit
		}
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := append([]ThreadItemEntry(nil), items[start:end]...)
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return &ThreadItemsListResponse{
		Data:            page,
		NextCursor:      stringPtrIfNotEmpty(next),
		BackwardsCursor: itemEntryCursor(page),
	}, nil
}

func itemEntryCursor(items []ThreadItemEntry) *string {
	if len(items) == 0 {
		return nil
	}
	value := "0"
	return &value
}

func BuildTurnsResponse(record *session.Record, params *ThreadTurnsListParams) (*TurnsPage, error) {
	return buildTurnsResponse(record, params, nil)
}

type turnsResponseOptions struct {
	ActiveTurn           *Turn
	LoadedStatus         ThreadStatus
	HasLiveRunningThread bool
}

func buildTurnsResponse(record *session.Record, params *ThreadTurnsListParams, options *turnsResponseOptions) (*TurnsPage, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if record == nil {
		return &TurnsPage{Data: []Turn{}}, nil
	}
	turns := turnsFromRecord(record)
	if options != nil {
		hasLiveInProgressTurn := options.HasLiveRunningThread
		if options.ActiveTurn != nil && options.ActiveTurn.Status == TurnStatusInProgress {
			hasLiveInProgressTurn = true
		}
		normalizeThreadTurnsStatus(turns, options.LoadedStatus, hasLiveInProgressTurn)
		if options.ActiveTurn != nil {
			turns = mergeTurnHistoryWithActiveTurn(turns, *options.ActiveTurn)
		}
	} else {
		normalizeThreadTurnsStatus(turns, IdleStatus(), false)
	}
	itemsView := params.ItemsView
	if itemsView == "" {
		itemsView = TurnItemsSummary
	}
	applyTurnItemsView(turns, itemsView)
	limit := threadTurnsDefaultLimit
	if params.Limit != nil {
		limit = *params.Limit
		if limit < 1 {
			limit = 1
		}
	}
	sortDirection := params.SortDirection
	if sortDirection == "" {
		sortDirection = SortDesc
	}
	page, next, backwards, err := paginateTurns(turns, params.Cursor, limit, sortDirection)
	if err != nil {
		return nil, err
	}
	return &TurnsPage{
		Data:            page,
		NextCursor:      stringPtrIfNotEmpty(next),
		BackwardsCursor: stringPtrIfNotEmpty(backwards),
	}, nil
}

func normalizeThreadTurnsStatus(turns []Turn, loadedStatus ThreadStatus, hasLiveInProgressTurn bool) {
	status := ResolveThreadStatus(loadedStatus, hasLiveInProgressTurn)
	if status.Type == "active" {
		return
	}
	for i := range turns {
		if turns[i].Status == TurnStatusInProgress {
			turns[i].Status = TurnStatusInterrupted
		}
	}
}

func mergeTurnHistoryWithActiveTurn(turns []Turn, activeTurn Turn) []Turn {
	if strings.TrimSpace(activeTurn.ID) == "" {
		return turns
	}
	merged := make([]Turn, 0, len(turns)+1)
	for _, turn := range turns {
		if turn.ID != activeTurn.ID {
			merged = append(merged, turn)
		}
	}
	merged = append(merged, activeTurn)
	return merged
}

func applyTurnItemsView(turns []Turn, view TurnItemsView) {
	if view == "" {
		view = TurnItemsSummary
	}
	for i := range turns {
		switch view {
		case TurnItemsNotLoaded:
			turns[i].Items = []ThreadItem{}
			turns[i].ItemsView = TurnItemsNotLoaded
		case TurnItemsFull:
			turns[i].ItemsView = TurnItemsFull
		default:
			turns[i].Items = summarizeTurnItems(turns[i].Items, turns[i].Status)
			turns[i].ItemsView = TurnItemsSummary
		}
	}
}

func summarizeTurnItems(items []ThreadItem, status TurnStatus) []ThreadItem {
	if len(items) == 0 {
		return []ThreadItem{}
	}
	var firstUser *ThreadItem
	for i := range items {
		if threadItemIsUserMessage(items[i]) {
			firstUser = &items[i]
			break
		}
	}
	var finalAgent *ThreadItem
	for i := len(items) - 1; i >= 0; i-- {
		if threadItemIsAgentMessage(items[i]) && threadItemMessagePhase(items[i]) == string(MessagePhaseFinalAnswer) {
			finalAgent = &items[i]
			break
		}
	}
	if finalAgent == nil && turnStatusIsTerminal(status) {
		for i := len(items) - 1; i >= 0; i-- {
			if threadItemIsAgentMessage(items[i]) && threadItemMessagePhase(items[i]) == "" {
				finalAgent = &items[i]
				break
			}
		}
	}
	switch {
	case firstUser != nil && finalAgent != nil && firstUser.ID != finalAgent.ID:
		return []ThreadItem{*firstUser, *finalAgent}
	case firstUser != nil:
		return []ThreadItem{*firstUser}
	case finalAgent != nil:
		return []ThreadItem{*finalAgent}
	default:
		return []ThreadItem{}
	}
}

func threadItemMessagePhase(item ThreadItem) string {
	value := threadItemAnyFromData(item.Data, "phase", "messagePhase")
	phase, _ := value.(string)
	return strings.TrimSpace(phase)
}

func turnStatusIsTerminal(status TurnStatus) bool {
	return status == TurnStatusCompleted || status == TurnStatusInterrupted || status == TurnStatusFailed
}

func threadItemIsUserMessage(item ThreadItem) bool {
	if strings.TrimSpace(item.Role) == "assistant" {
		return false
	}
	return normalizeThreadItemType(item.Type) == "userMessage"
}

func threadItemIsAgentMessage(item ThreadItem) bool {
	if strings.TrimSpace(item.Role) == "assistant" && normalizeThreadItemType(item.Type) == "userMessage" {
		return true
	}
	return normalizeThreadItemType(item.Type) == "agentMessage" || strings.TrimSpace(item.Type) == "assistant_message"
}

func BuildThreadItem(item session.Item) ThreadItem {
	data := map[string]any{}
	for key, value := range item.Data {
		data[key] = value
	}
	for key, value := range item.Metadata {
		data[key] = value
	}
	if item.CallID != "" {
		data["callId"] = item.CallID
		data["call_id"] = item.CallID
	}
	if item.Name != "" {
		data["name"] = item.Name
	}
	if item.Namespace != "" {
		data["namespace"] = item.Namespace
	}
	if item.ResponseID != "" {
		data["responseId"] = item.ResponseID
		data["response_id"] = item.ResponseID
	}
	turnID := ""
	if value, ok := data["turn_id"].(string); ok {
		turnID = value
	}
	if value, ok := data["turnId"].(string); ok && turnID == "" {
		turnID = value
	}
	return ThreadItem{
		ID:         item.ID,
		Type:       item.Type,
		Role:       item.Role,
		Text:       item.Text,
		Name:       item.Name,
		Namespace:  item.Namespace,
		CallID:     item.CallID,
		Status:     item.Status,
		TurnID:     turnID,
		CreatedAt:  unixOrZero(item.CreatedAt),
		Content:    threadItemContentFromSession(item.Content),
		Data:       data,
		Raw:        append(json.RawMessage(nil), item.Raw...),
		ResponseID: item.ResponseID,
	}
}

func threadItemContentFromSession(content []session.ContentPart) []ThreadItemContent {
	if content == nil {
		return nil
	}
	out := make([]ThreadItemContent, len(content))
	for i := range content {
		out[i] = ThreadItemContent{
			Type:     content[i].Type,
			Text:     content[i].Text,
			ImageURL: content[i].ImageURL,
			AudioURL: content[i].AudioURL,
			Detail:   cloneString(content[i].Detail),
		}
	}
	return out
}

func normalizeThreadItemType(itemType string) string {
	switch strings.TrimSpace(itemType) {
	case "message", "user_message", "userMessage":
		return "userMessage"
	case "agent_message", "agentMessage":
		return "agentMessage"
	case "external_session_import_marker":
		return "agentMessage"
	case "hook_prompt", "hookPrompt":
		return "hookPrompt"
	case "plan":
		return "plan"
	case "reasoning":
		return "reasoning"
	case "collab_agent_tool_call", "collabAgentToolCall":
		return "collabAgentToolCall"
	case "sub_agent_activity", "subAgentActivity":
		return "subAgentActivity"
	case "web_search", "webSearch", "web_search_call":
		return "webSearch"
	case "image_view", "imageView":
		return "imageView"
	case "sleep":
		return "sleep"
	case "image_generation", "imageGeneration", "image_generation_call":
		return "imageGeneration"
	case "entered_review_mode", "enteredReviewMode":
		return "enteredReviewMode"
	case "exited_review_mode", "exitedReviewMode":
		return "exitedReviewMode"
	case "context_compaction", "contextCompaction":
		return "contextCompaction"
	default:
		return itemType
	}
}

func threadItemWireType(item *ThreadItem) string {
	if item == nil {
		return ""
	}
	normalized := normalizeThreadItemType(item.Type)
	switch normalized {
	case "userMessage", "agentMessage", "hookPrompt", "plan", "reasoning",
		"collabAgentToolCall", "subAgentActivity", "webSearch", "imageView",
		"sleep", "imageGeneration", "enteredReviewMode", "exitedReviewMode", "contextCompaction":
		return normalized
	}
	switch strings.TrimSpace(item.Type) {
	case "commandExecution", "fileChange", "mcpToolCall", "dynamicToolCall":
		return item.Type
	case "command_execution":
		return "commandExecution"
	case "file_change":
		return "fileChange"
	case "mcp_tool_call":
		return "mcpToolCall"
	case "dynamic_tool_call":
		return "dynamicToolCall"
	}
	switch strings.TrimSpace(item.Type) {
	case "custom_tool_call", "function_call", "tool_search_call",
		"function_call_output", "custom_tool_call_output", "tool_search_output", "tool_output":
		if threadItemLooksLikeFileChange(item) {
			return "fileChange"
		}
		if threadItemLooksLikeMCP(item) {
			return "mcpToolCall"
		}
		if threadItemLooksLikeDynamic(item) {
			return "dynamicToolCall"
		}
		if strings.TrimSpace(threadItemCommand(item)) != "" {
			return "commandExecution"
		}
	}
	return item.Type
}

func threadItemExternalID(item *ThreadItem) string {
	if item == nil {
		return ""
	}
	if id := threadItemStringFromData(item.Data, "itemId", "item_id", "callId", "call_id"); strings.TrimSpace(id) != "" {
		return id
	}
	if strings.TrimSpace(item.CallID) != "" {
		return strings.TrimSpace(item.CallID)
	}
	return item.ID
}

func threadItemUserInputContent(item *ThreadItem) []map[string]any {
	if item == nil {
		return []map[string]any{}
	}
	if content := threadItemUserInputContentFromData(item.Data); content != nil {
		return content
	}
	if len(item.Content) == 0 {
		return []map[string]any{{"type": "text", "text": item.Text, "text_elements": []any{}}}
	}
	content := make([]map[string]any, 0, len(item.Content))
	for _, part := range item.Content {
		switch part.Type {
		case "input_image", "image":
			entry := map[string]any{"type": "image", "url": part.ImageURL}
			if part.Detail != nil {
				entry["detail"] = *part.Detail
			}
			content = append(content, entry)
		case "localImage", "local_image":
			entry := map[string]any{"type": "localImage", "path": part.ImageURL}
			if part.Detail != nil {
				entry["detail"] = *part.Detail
			}
			content = append(content, entry)
		default:
			text := part.Text
			if text == "" {
				text = item.Text
			}
			content = append(content, map[string]any{"type": "text", "text": text, "text_elements": []any{}})
		}
	}
	if len(content) == 0 {
		return []map[string]any{}
	}
	return content
}

func threadItemUserInputContentFromData(data map[string]any) []map[string]any {
	raw := threadItemAnyFromData(data, "content")
	switch content := raw.(type) {
	case []map[string]any:
		out := make([]map[string]any, 0, len(content))
		for _, entry := range content {
			out = append(out, cloneAnyMap(entry))
		}
		return out
	case []any:
		out := make([]map[string]any, 0, len(content))
		for _, rawEntry := range content {
			if entry, ok := rawEntry.(map[string]any); ok {
				out = append(out, cloneAnyMap(entry))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func threadItemAnyFromData(data map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := data[key]; ok {
			return value
		}
	}
	return nil
}

func threadItemStringFromData(data map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := data[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			return typed
		case *string:
			if typed != nil {
				return *typed
			}
		}
	}
	return ""
}

func threadItemJSONValueFromData(data map[string]any, keys ...string) any {
	for _, key := range keys {
		value, ok := data[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok {
			decoded := threadItemJSONMap(text)
			if decoded != nil {
				return decoded
			}
			return text
		}
		return value
	}
	return nil
}

func threadItemCommand(item *ThreadItem) string {
	if item == nil {
		return ""
	}
	if command := threadItemStringFromData(item.Data, "command", "hook_command", "cmd"); strings.TrimSpace(command) != "" {
		return command
	}
	for _, candidate := range []string{threadItemStringFromData(item.Data, "arguments"), item.Text} {
		if command := threadItemCommandFromJSON(candidate); strings.TrimSpace(command) != "" {
			return command
		}
	}
	return ""
}

func threadItemCWD(item *ThreadItem) string {
	if item == nil {
		return ""
	}
	if cwd := threadItemStringFromData(item.Data, "cwd"); strings.TrimSpace(cwd) != "" {
		return cwd
	}
	for _, candidate := range []string{threadItemStringFromData(item.Data, "arguments"), item.Text} {
		if cwd := threadItemCWDFromJSON(candidate); strings.TrimSpace(cwd) != "" {
			return cwd
		}
	}
	return ""
}

func threadItemCommandFromJSON(value string) string {
	args := threadItemJSONMap(value)
	return threadItemStringFromData(args, "cmd", "command")
}

func threadItemCWDFromJSON(value string) string {
	args := threadItemJSONMap(value)
	return firstNonEmpty(threadItemStringFromData(args, "cwd"), threadItemStringFromData(args, "workdir"))
}

func threadItemJSONMap(value string) map[string]any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(value), &args); err != nil {
		return nil
	}
	return args
}

func threadItemStringPtrFromData(data map[string]any, keys ...string) *string {
	for _, key := range keys {
		value, ok := data[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if typed != "" {
				return &typed
			}
		case *string:
			if typed != nil && *typed != "" {
				value := *typed
				return &value
			}
		}
	}
	return nil
}

func threadItemBoolPtrFromData(data map[string]any, keys ...string) *bool {
	for _, key := range keys {
		value, ok := data[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			v := typed
			return &v
		case *bool:
			if typed != nil {
				v := *typed
				return &v
			}
		}
	}
	return nil
}

func threadItemInt64PtrFromData(data map[string]any, keys ...string) *int64 {
	for _, key := range keys {
		value, ok := data[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int:
			v := int64(typed)
			return &v
		case int64:
			v := typed
			return &v
		case int32:
			v := int64(typed)
			return &v
		case uint64:
			v := int64(typed)
			return &v
		case float64:
			v := int64(typed)
			return &v
		case json.Number:
			if v, err := typed.Int64(); err == nil {
				return &v
			}
		}
	}
	return nil
}

func threadItemStringSliceFromData(data map[string]any, keys ...string) []string {
	for _, key := range keys {
		value, ok := data[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case []string:
			return append([]string(nil), typed...)
		case []any:
			out := make([]string, 0, len(typed))
			for _, entry := range typed {
				text, ok := entry.(string)
				if ok {
					out = append(out, text)
				}
			}
			return out
		case string:
			if typed != "" {
				return []string{typed}
			}
		}
	}
	return []string{}
}

func threadItemHookPromptFragments(item *ThreadItem) []HookPromptWire {
	if item == nil {
		return []HookPromptWire{}
	}
	if fragments := threadItemHookPromptFragmentsFromAny(threadItemAnyFromData(item.Data, "fragments", "hookPromptFragments", "hook_prompt_fragments")); len(fragments) > 0 {
		return fragments
	}
	if strings.TrimSpace(item.Text) == "" {
		return []HookPromptWire{}
	}
	return []HookPromptWire{{
		Text:      item.Text,
		HookRunID: threadItemStringFromData(item.Data, "hookRunId", "hook_run_id", "runId", "run_id"),
	}}
}

func threadItemHookPromptFragmentsFromAny(value any) []HookPromptWire {
	switch typed := value.(type) {
	case nil:
		return nil
	case []HookPromptWire:
		return append([]HookPromptWire(nil), typed...)
	case []*HookPromptWire:
		out := make([]HookPromptWire, 0, len(typed))
		for _, fragment := range typed {
			if fragment != nil {
				out = append(out, *fragment)
			}
		}
		return out
	case []map[string]any:
		out := make([]HookPromptWire, 0, len(typed))
		for _, fragment := range typed {
			out = append(out, threadItemHookPromptFragmentFromMap(fragment))
		}
		return out
	case []any:
		out := make([]HookPromptWire, 0, len(typed))
		for _, fragment := range typed {
			if mapped, ok := fragment.(map[string]any); ok {
				out = append(out, threadItemHookPromptFragmentFromMap(mapped))
				continue
			}
			out = append(out, HookPromptWire{Text: fmt.Sprint(fragment)})
		}
		return out
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		var decoded []map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return nil
		}
		return threadItemHookPromptFragmentsFromAny(decoded)
	}
}

func threadItemHookPromptFragmentFromMap(value map[string]any) HookPromptWire {
	return HookPromptWire{
		Text:      threadItemStringFromAnyMap(value, "text"),
		HookRunID: threadItemStringFromAnyMap(value, "hookRunId", "hook_run_id"),
	}
}

func threadItemCommandSource(item *ThreadItem) CommandExecutionSource {
	if item == nil {
		return CommandExecutionSourceAgent
	}
	source := CommandExecutionSource(threadItemStringFromData(item.Data, "source"))
	switch source {
	case CommandExecutionSourceAgent, CommandExecutionSourceUserShell, CommandExecutionSourceUnifiedExecStartup, CommandExecutionSourceUnifiedExecInteraction:
		return source
	default:
		return CommandExecutionSourceAgent
	}
}

func threadItemCommandStatus(item *ThreadItem) CommandExecutionStatus {
	if item == nil {
		return CommandExecutionCompleted
	}
	if item.Type == "function_call" {
		return CommandExecutionInProgress
	}
	if status := CommandExecutionStatus(threadItemStringFromData(item.Data, "status")); status != "" {
		switch status {
		case CommandExecutionInProgress, CommandExecutionCompleted, CommandExecutionFailed, CommandExecutionDeclined:
			return status
		}
	}
	switch strings.TrimSpace(threadItemStringFromData(item.Data, "approvalDecision", "approval_decision")) {
	case "deny", string(CommandExecutionApprovalDecline), string(CommandExecutionApprovalCancel):
		return CommandExecutionDeclined
	}
	if success, ok := item.Data["success"].(bool); ok && !success {
		return CommandExecutionFailed
	}
	if item.Data["error"] != nil {
		return CommandExecutionFailed
	}
	return CommandExecutionCompleted
}

func threadItemCommandActions(item *ThreadItem) []CommandAction {
	if item == nil {
		return []CommandAction{}
	}
	if actions := threadItemCommandActionsFromAny(threadItemAnyFromData(item.Data, "commandActions", "command_actions")); len(actions) > 0 {
		return actions
	}
	command := threadItemStringFromData(item.Data, "command", "hook_command", "cmd")
	if strings.TrimSpace(command) == "" {
		return []CommandAction{}
	}
	return []CommandAction{{Type: "unknown", Command: command}}
}

func threadItemCommandActionsFromAny(value any) []CommandAction {
	switch typed := value.(type) {
	case nil:
		return nil
	case []CommandAction:
		return append([]CommandAction(nil), typed...)
	case []*CommandAction:
		out := make([]CommandAction, 0, len(typed))
		for _, action := range typed {
			if action != nil {
				out = append(out, *action)
			}
		}
		return out
	case []map[string]any:
		out := make([]CommandAction, 0, len(typed))
		for _, action := range typed {
			out = append(out, threadItemCommandActionFromMap(action))
		}
		return out
	case []any:
		out := make([]CommandAction, 0, len(typed))
		for _, action := range typed {
			if mapped, ok := action.(map[string]any); ok {
				out = append(out, threadItemCommandActionFromMap(mapped))
			}
		}
		return out
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		var actions []CommandAction
		if err := json.Unmarshal(data, &actions); err == nil {
			return actions
		}
		var maps []map[string]any
		if err := json.Unmarshal(data, &maps); err == nil {
			return threadItemCommandActionsFromAny(maps)
		}
		return nil
	}
}

func threadItemCommandActionFromMap(value map[string]any) CommandAction {
	actionType := threadItemCommandActionType(threadItemStringFromAnyMap(value, "type"))
	action := CommandAction{
		Type:    actionType,
		Command: threadItemStringFromAnyMap(value, "command", "cmd"),
	}
	switch actionType {
	case "read":
		action.Name = threadItemStringFromAnyMap(value, "name")
		action.Path = threadItemStringPtrFromAnyMap(value, "path")
	case "listFiles":
		action.Path = threadItemStringPtrFromAnyMap(value, "path")
	case "search":
		action.Query = threadItemStringPtrFromAnyMap(value, "query")
		action.Path = threadItemStringPtrFromAnyMap(value, "path")
	}
	return action
}

func threadItemCommandActionType(value string) string {
	switch strings.TrimSpace(value) {
	case "read":
		return "read"
	case "listFiles", "list_files":
		return "listFiles"
	case "search":
		return "search"
	default:
		return "unknown"
	}
}

func threadItemAggregatedOutput(item *ThreadItem) *string {
	if item == nil {
		return nil
	}
	if threadItemCommandStatus(item) == CommandExecutionDeclined {
		return nil
	}
	if output := threadItemStringFromData(item.Data, "aggregatedOutput", "aggregated_output", "hook_response", "output", "formattedOutput", "formatted_output"); output != "" {
		return &output
	}
	if strings.TrimSpace(item.Text) != "" {
		output := item.Text
		return &output
	}
	return nil
}

func threadItemLooksLikeMCP(item *ThreadItem) bool {
	if item == nil {
		return false
	}
	if marker, ok := item.Data["mcpToolCall"].(bool); ok && marker {
		return true
	}
	if marker, ok := item.Data["mcp_tool_call"].(bool); ok && marker {
		return true
	}
	return false
}

func threadItemLooksLikeFileChange(item *ThreadItem) bool {
	if item == nil {
		return false
	}
	if marker, ok := item.Data["fileChange"].(bool); ok && marker {
		return true
	}
	if marker, ok := item.Data["file_change"].(bool); ok && marker {
		return true
	}
	return strings.TrimSpace(item.Name) == "apply_patch" && strings.TrimSpace(item.Type) == "custom_tool_call"
}

func threadItemFileChangeStatus(item *ThreadItem) PatchApplyStatus {
	if item == nil {
		return PatchApplyCompleted
	}
	switch strings.TrimSpace(item.Type) {
	case "custom_tool_call", "function_call":
		return PatchApplyInProgress
	}
	switch status := threadItemStringFromData(item.Data, "status"); status {
	case string(PatchApplyInProgress):
		return PatchApplyInProgress
	case string(PatchApplyFailed):
		return PatchApplyFailed
	case string(PatchApplyDeclined):
		return PatchApplyDeclined
	}
	if success, ok := item.Data["success"].(bool); ok && !success {
		return PatchApplyFailed
	}
	if item.Data["error"] != nil {
		return PatchApplyFailed
	}
	return PatchApplyCompleted
}

func threadItemFileChanges(item *ThreadItem) []fileChangeUpdate {
	if item == nil {
		return []fileChangeUpdate{}
	}
	value := threadItemAnyFromData(item.Data, "changes", "fileChanges", "file_changes")
	return threadItemFileChangesFromAny(value)
}

func threadItemFileChangesFromAny(value any) []fileChangeUpdate {
	switch typed := value.(type) {
	case nil:
		return []fileChangeUpdate{}
	case []fileChangeUpdate:
		return append([]fileChangeUpdate(nil), typed...)
	case []FileUpdateChange:
		changes := make([]fileChangeUpdate, 0, len(typed))
		for index := range typed {
			changes = append(changes, threadItemFileChangeFromProtocol(&typed[index]))
		}
		return changes
	case []*FileUpdateChange:
		changes := make([]fileChangeUpdate, 0, len(typed))
		for _, change := range typed {
			changes = append(changes, threadItemFileChangeFromProtocol(change))
		}
		return changes
	case []map[string]any:
		changes := make([]fileChangeUpdate, 0, len(typed))
		for _, change := range typed {
			changes = append(changes, threadItemFileChangeFromMap(change))
		}
		return changes
	case []any:
		changes := make([]fileChangeUpdate, 0, len(typed))
		for _, change := range typed {
			changes = append(changes, threadItemFileChangeFromAny(change))
		}
		return changes
	case map[string]any:
		changes := make([]fileChangeUpdate, 0, len(typed))
		for path, rawChange := range typed {
			change := threadItemFileChangeFromAny(rawChange)
			if strings.TrimSpace(change.Path) == "" {
				change.Path = path
			}
			changes = append(changes, change)
		}
		return changes
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return []fileChangeUpdate{}
		}
		var decoded []map[string]any
		if err := json.Unmarshal(data, &decoded); err == nil {
			return threadItemFileChangesFromAny(decoded)
		}
		var decodedMap map[string]any
		if err := json.Unmarshal(data, &decodedMap); err == nil {
			return threadItemFileChangesFromAny(decodedMap)
		}
		return []fileChangeUpdate{}
	}
}

func threadItemFileChangeFromAny(value any) fileChangeUpdate {
	switch typed := value.(type) {
	case map[string]any:
		return threadItemFileChangeFromMap(typed)
	case FileUpdateChange:
		return threadItemFileChangeFromProtocol(&typed)
	case *FileUpdateChange:
		return threadItemFileChangeFromProtocol(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fileChangeUpdate{Kind: fileChangeKindWire{Type: "update"}}
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fileChangeUpdate{Kind: fileChangeKindWire{Type: "update"}}
		}
		return threadItemFileChangeFromMap(decoded)
	}
}

func threadItemFileChangeFromProtocol(change *FileUpdateChange) fileChangeUpdate {
	if change == nil {
		return fileChangeUpdate{Kind: fileChangeKindWire{Type: "update"}}
	}
	return fileChangeUpdate{
		Path: change.Path,
		Kind: fileChangeKindWire{
			Type:     threadItemPatchChangeKindType(change.Kind.Type),
			MovePath: change.Kind.MovePath,
		},
		Diff: change.Diff,
	}
}

func threadItemFileChangeFromMap(change map[string]any) fileChangeUpdate {
	if change == nil {
		return fileChangeUpdate{Kind: fileChangeKindWire{Type: "update"}}
	}
	return fileChangeUpdate{
		Path: threadItemStringFromAnyMap(change, "path", "filePath", "file_path", "file"),
		Kind: threadItemPatchChangeKindFromChange(change),
		Diff: threadItemStringFromAnyMap(change, "diff", "unifiedDiff", "unified_diff", "content"),
	}
}

func threadItemPatchChangeKindFromChange(change map[string]any) fileChangeKindWire {
	if change == nil {
		return fileChangeKindWire{Type: "update"}
	}
	if kind, ok := change["kind"]; ok {
		return threadItemPatchChangeKindFromAny(kind)
	}
	if changeType := threadItemStringFromAnyMap(change, "type"); changeType != "" {
		return fileChangeKindWire{
			Type:     threadItemPatchChangeKindType(changeType),
			MovePath: threadItemStringPtrFromAnyMap(change, "move_path", "movePath"),
		}
	}
	if _, ok := change["Add"]; ok {
		return fileChangeKindWire{Type: "add"}
	}
	if _, ok := change["add"]; ok {
		return fileChangeKindWire{Type: "add"}
	}
	if _, ok := change["Delete"]; ok {
		return fileChangeKindWire{Type: "delete"}
	}
	if _, ok := change["delete"]; ok {
		return fileChangeKindWire{Type: "delete"}
	}
	return fileChangeKindWire{
		Type:     "update",
		MovePath: threadItemStringPtrFromAnyMap(change, "move_path", "movePath"),
	}
}

func threadItemPatchChangeKindFromAny(value any) fileChangeKindWire {
	switch typed := value.(type) {
	case PatchChangeKind:
		return fileChangeKindWire{
			Type:     threadItemPatchChangeKindType(typed.Type),
			MovePath: typed.MovePath,
		}
	case *PatchChangeKind:
		if typed == nil {
			return fileChangeKindWire{Type: "update"}
		}
		return fileChangeKindWire{
			Type:     threadItemPatchChangeKindType(typed.Type),
			MovePath: typed.MovePath,
		}
	case map[string]any:
		if _, ok := typed["Add"]; ok {
			return fileChangeKindWire{Type: "add"}
		}
		if _, ok := typed["add"]; ok {
			return fileChangeKindWire{Type: "add"}
		}
		if _, ok := typed["Delete"]; ok {
			return fileChangeKindWire{Type: "delete"}
		}
		if _, ok := typed["delete"]; ok {
			return fileChangeKindWire{Type: "delete"}
		}
		return fileChangeKindWire{
			Type:     threadItemPatchChangeKindType(threadItemStringFromAnyMap(typed, "type")),
			MovePath: threadItemStringPtrFromAnyMap(typed, "move_path", "movePath"),
		}
	case string:
		return fileChangeKindWire{Type: threadItemPatchChangeKindType(typed)}
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fileChangeKindWire{Type: "update"}
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fileChangeKindWire{Type: "update"}
		}
		return threadItemPatchChangeKindFromAny(decoded)
	}
}

func threadItemPatchChangeKindType(value string) string {
	switch strings.TrimSpace(value) {
	case "add":
		return "add"
	case "delete":
		return "delete"
	default:
		return "update"
	}
}

func threadItemMCPServer(item *ThreadItem) string {
	if item == nil {
		return ""
	}
	if server := threadItemStringFromData(item.Data, "server"); server != "" {
		return server
	}
	name := threadItemStringFromData(item.Data, "name", "toolName")
	if index := strings.Index(name, "."); index > 0 {
		return name[:index]
	}
	return ""
}

func threadItemMCPTool(item *ThreadItem) string {
	if item == nil {
		return ""
	}
	if tool := threadItemStringFromData(item.Data, "tool"); tool != "" {
		return tool
	}
	name := threadItemStringFromData(item.Data, "name", "toolName")
	if index := strings.Index(name, "."); index >= 0 && index+1 < len(name) {
		return name[index+1:]
	}
	return ""
}

func threadItemMCPStatus(item *ThreadItem) string {
	if item == nil {
		return "completed"
	}
	switch status := threadItemStringFromData(item.Data, "status"); status {
	case "inProgress", "completed", "failed":
		return status
	}
	if item.Type == "function_call" {
		return "inProgress"
	}
	if success, ok := item.Data["success"].(bool); ok && !success {
		return "failed"
	}
	if item.Data["error"] != nil {
		return "failed"
	}
	return "completed"
}

func threadItemMCPAppContext(item *ThreadItem) any {
	if item == nil {
		return nil
	}
	value := threadItemAnyFromData(item.Data, "appContext", "app_context")
	switch typed := value.(type) {
	case nil:
		return nil
	case mcp.McpToolCallAppContext:
		return typed
	case *mcp.McpToolCallAppContext:
		if typed == nil {
			return nil
		}
		return typed
	case map[string]any:
		return map[string]any{
			"connectorId": threadItemStringFromAnyMap(typed, "connectorId", "connector_id", "connectorID"),
			"linkId":      threadItemStringPtrFromAnyMap(typed, "linkId", "link_id"),
			"resourceUri": threadItemStringPtrFromAnyMap(typed, "resourceUri", "resource_uri", "mcpAppResourceUri", "mcp_app_resource_uri"),
			"appName":     threadItemStringPtrFromAnyMap(typed, "appName", "app_name"),
			"templateId":  threadItemStringPtrFromAnyMap(typed, "templateId", "template_id"),
			"actionName":  threadItemStringPtrFromAnyMap(typed, "actionName", "action_name"),
		}
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return value
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return value
		}
		return threadItemMCPAppContext(&ThreadItem{Data: map[string]any{"appContext": decoded}})
	}
}

func threadItemMCPResult(item *ThreadItem) any {
	if item == nil || item.Type == "function_call" {
		return nil
	}
	if result := threadItemAnyFromData(item.Data, "result"); result != nil {
		return threadItemMCPResultFromAny(result)
	}
	content := threadItemAnyFromData(item.Data, "content")
	if content == nil {
		return nil
	}
	return threadItemMCPResultFromParts(content, threadItemAnyFromData(item.Data, "structuredContent", "structured_content"), threadItemAnyFromData(item.Data, "_meta", "meta"))
}

func threadItemMCPResultFromAny(value any) map[string]any {
	if values, ok := value.(map[string]any); ok {
		structuredContent, _ := threadItemMapValue(values, "structuredContent", "structured_content")
		meta, _ := threadItemMapValue(values, "_meta", "meta")
		content, _ := threadItemMapValue(values, "content")
		return threadItemMCPResultFromParts(content, structuredContent, meta)
	}
	data, err := json.Marshal(value)
	if err == nil {
		var values map[string]any
		if err := json.Unmarshal(data, &values); err == nil {
			return threadItemMCPResultFromAny(values)
		}
		var content []any
		if err := json.Unmarshal(data, &content); err == nil {
			return threadItemMCPResultFromParts(content, nil, nil)
		}
	}
	return threadItemMCPResultFromParts([]any{value}, nil, nil)
}

func threadItemMCPResultFromParts(content any, structuredContent any, meta any) map[string]any {
	return map[string]any{
		"content":           threadItemMCPResultContent(content),
		"structuredContent": structuredContent,
		"_meta":             meta,
	}
}

func threadItemMCPResultContent(content any) []any {
	switch typed := content.(type) {
	case nil:
		return []any{}
	case []any:
		return append([]any(nil), typed...)
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, entry := range typed {
			out = append(out, cloneAnyMap(entry))
		}
		return out
	default:
		data, err := json.Marshal(typed)
		if err == nil {
			var decoded []any
			if err := json.Unmarshal(data, &decoded); err == nil {
				return decoded
			}
		}
		return []any{typed}
	}
}

func threadItemMCPError(item *ThreadItem) any {
	if item == nil {
		return nil
	}
	if success, ok := item.Data["success"].(bool); ok && success {
		return nil
	}
	message := threadItemMCPErrorMessage(threadItemAnyFromData(item.Data, "error"))
	if message == "" {
		return nil
	}
	return map[string]any{"message": message}
}

func threadItemMCPErrorMessage(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case map[string]any:
		if message := threadItemStringFromAnyMap(typed, "message", "error", "text"); message != "" {
			return message
		}
		if raw, ok := threadItemMapValue(typed, "message", "error", "text"); ok && raw != nil {
			return fmt.Sprint(raw)
		}
		data, err := json.Marshal(typed)
		if err == nil {
			return string(data)
		}
		return fmt.Sprint(typed)
	default:
		data, err := json.Marshal(typed)
		if err == nil {
			var values map[string]any
			if err := json.Unmarshal(data, &values); err == nil {
				return threadItemMCPErrorMessage(values)
			}
		}
		return fmt.Sprint(typed)
	}
}

func threadItemMapValue(values map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		value, ok := values[key]
		if ok {
			return value, true
		}
	}
	return nil, false
}

func threadItemLooksLikeDynamic(item *ThreadItem) bool {
	if item == nil {
		return false
	}
	if marker, ok := item.Data["dynamicToolCall"].(bool); ok && marker {
		return true
	}
	if marker, ok := item.Data["dynamic_tool_call"].(bool); ok && marker {
		return true
	}
	return false
}

func threadItemDynamicNamespace(item *ThreadItem) *string {
	if item == nil {
		return nil
	}
	return threadItemStringPtrFromData(item.Data, "namespace")
}

func threadItemDynamicTool(item *ThreadItem) string {
	if item == nil {
		return ""
	}
	if value := threadItemStringFromData(item.Data, "tool"); value != "" {
		return value
	}
	if item.Name != "" {
		if index := strings.Index(item.Name, "."); index >= 0 && index+1 < len(item.Name) {
			return item.Name[index+1:]
		}
		return item.Name
	}
	return threadItemStringFromData(item.Data, "name", "toolName")
}

func threadItemDynamicArguments(item *ThreadItem) any {
	if item == nil {
		return map[string]any{}
	}
	value := threadItemJSONValueFromData(item.Data, "arguments", "input", "rawArguments", "raw_arguments")
	if value == nil {
		return map[string]any{}
	}
	return value
}

func threadItemDynamicStatus(item *ThreadItem) string {
	if item == nil {
		return "completed"
	}
	if item.Type == "function_call" {
		return "inProgress"
	}
	if success, ok := item.Data["success"].(bool); ok && !success {
		return "failed"
	}
	if item.Data["error"] != nil {
		return "failed"
	}
	switch status := threadItemStringFromData(item.Data, "status"); status {
	case "inProgress", "completed", "failed":
		return status
	default:
		return "completed"
	}
}

func threadItemDynamicContentItems(item *ThreadItem) any {
	if item == nil || item.Type == "function_call" {
		return nil
	}
	value := threadItemAnyFromData(item.Data, "contentItems", "content_items")
	items := threadItemDynamicContentItemsAny(value)
	if items != nil {
		return items
	}
	if output := threadItemStringFromData(item.Data, "output"); strings.TrimSpace(output) != "" {
		return []any{map[string]any{"type": "inputText", "text": output}}
	}
	return []any{}
}

func threadItemDynamicContentItemsAny(value any) []any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []any:
		out := make([]any, 0, len(typed))
		for _, entry := range typed {
			out = append(out, threadItemDynamicContentItem(entry))
		}
		return out
	case []DynamicToolCallOutputContent:
		out := make([]any, 0, len(typed))
		for i := range typed {
			out = append(out, threadItemDynamicContentItem(&typed[i]))
		}
		return out
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		var decoded []any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return nil
		}
		out := make([]any, 0, len(decoded))
		for _, entry := range decoded {
			out = append(out, threadItemDynamicContentItem(entry))
		}
		return out
	}
}

func threadItemDynamicContentItem(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		itemType := threadItemStringFromAnyMap(typed, "type")
		if itemType == "inputImage" || itemType == "input_image" || itemType == "image" {
			return map[string]any{"type": "inputImage", "imageUrl": threadItemStringFromAnyMap(typed, "imageUrl", "image_url", "url")}
		}
		if itemType == "inputAudio" || itemType == "input_audio" || itemType == "audio" {
			return map[string]any{"type": "inputAudio", "audioUrl": threadItemStringFromAnyMap(typed, "audioUrl", "audio_url", "url")}
		}
		return map[string]any{"type": "inputText", "text": threadItemStringFromAnyMap(typed, "text", "input_text", "inputText")}
	case *DynamicToolCallOutputContent:
		if typed == nil {
			return map[string]any{"type": "inputText", "text": ""}
		}
		if typed.Type == "inputImage" || typed.Type == "input_image" {
			return map[string]any{"type": "inputImage", "imageUrl": typed.ImageURL}
		}
		if typed.Type == "inputAudio" || typed.Type == "input_audio" {
			return map[string]any{"type": "inputAudio", "audioUrl": typed.AudioURL}
		}
		return map[string]any{"type": "inputText", "text": typed.Text}
	case DynamicToolCallOutputContent:
		if typed.Type == "inputImage" || typed.Type == "input_image" {
			return map[string]any{"type": "inputImage", "imageUrl": typed.ImageURL}
		}
		if typed.Type == "inputAudio" || typed.Type == "input_audio" {
			return map[string]any{"type": "inputAudio", "audioUrl": typed.AudioURL}
		}
		return map[string]any{"type": "inputText", "text": typed.Text}
	default:
		return map[string]any{"type": "inputText", "text": fmt.Sprint(value)}
	}
}

func threadItemCollabAgentTool(item *ThreadItem) CollabAgentTool {
	if item == nil {
		return CollabAgentToolSendInput
	}
	tool := CollabAgentTool(threadItemStringFromData(item.Data, "tool"))
	switch tool {
	case CollabAgentToolSpawnAgent, CollabAgentToolSendInput, CollabAgentToolResumeAgent, CollabAgentToolWait, CollabAgentToolCloseAgent:
		return tool
	default:
		return CollabAgentToolSendInput
	}
}

func threadItemCollabAgentToolStatus(item *ThreadItem) CollabAgentToolCallStatus {
	if item == nil {
		return CollabAgentToolCallCompleted
	}
	status := CollabAgentToolCallStatus(threadItemStringFromData(item.Data, "status"))
	switch status {
	case CollabAgentToolCallInProgress, CollabAgentToolCallCompleted, CollabAgentToolCallFailed:
		return status
	default:
		return CollabAgentToolCallCompleted
	}
}

func threadItemCollabAgentStates(item *ThreadItem) map[string]CollabAgentState {
	if item == nil {
		return map[string]CollabAgentState{}
	}
	value := threadItemAnyFromData(item.Data, "agentsStates", "agents_states")
	switch typed := value.(type) {
	case nil:
		return map[string]CollabAgentState{}
	case map[string]CollabAgentState:
		out := make(map[string]CollabAgentState, len(typed))
		for key, state := range typed {
			out[key] = CollabAgentState{Status: state.Status, Message: cloneStringPtrAppserver(state.Message)}
		}
		return out
	case map[string]any:
		out := make(map[string]CollabAgentState, len(typed))
		for key, state := range typed {
			out[key] = threadItemCollabAgentStateFromAny(state)
		}
		return out
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return map[string]CollabAgentState{}
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return map[string]CollabAgentState{}
		}
		return threadItemCollabAgentStates(&ThreadItem{Data: map[string]any{"agentsStates": decoded}})
	}
}

func threadItemCollabAgentStateFromAny(value any) CollabAgentState {
	switch typed := value.(type) {
	case CollabAgentState:
		return CollabAgentState{Status: typed.Status, Message: cloneStringPtrAppserver(typed.Message)}
	case *CollabAgentState:
		if typed == nil {
			return CollabAgentState{Status: CollabAgentStatusNotFound}
		}
		return CollabAgentState{Status: typed.Status, Message: cloneStringPtrAppserver(typed.Message)}
	case map[string]any:
		return CollabAgentState{
			Status:  CollabAgentStatus(threadItemStringFromAnyMap(typed, "status")),
			Message: threadItemStringPtrFromAnyMap(typed, "message"),
		}
	default:
		return CollabAgentState{Status: CollabAgentStatusNotFound}
	}
}

func threadItemWebSearchQuery(item *ThreadItem) string {
	if item == nil {
		return ""
	}
	return firstNonEmpty(threadItemStringFromData(item.Data, "query"), item.Text)
}

func threadItemWebSearchAction(item *ThreadItem) any {
	if item == nil {
		return nil
	}
	if action := threadItemAnyFromData(item.Data, "action", "webSearchAction", "web_search_action"); action != nil {
		return threadItemWebSearchActionFromAny(action)
	}
	if query := threadItemWebSearchQuery(item); strings.TrimSpace(query) != "" {
		return map[string]any{"type": "search", "query": query, "queries": nil}
	}
	return nil
}

func threadItemWebSearchResults(item *ThreadItem) []any {
	if item == nil || item.Data == nil {
		return nil
	}
	for _, key := range []string{"results", "webSearchResults", "web_search_results"} {
		if values, ok := item.Data[key].([]any); ok {
			return append([]any(nil), values...)
		}
	}
	return nil
}

func threadItemWebSearchActionFromAny(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case map[string]any:
		actionType := threadItemStringFromAnyMap(typed, "type")
		switch actionType {
		case "search":
			return map[string]any{
				"type":    "search",
				"query":   threadItemNullableStringFromAnyMap(typed, "query"),
				"queries": threadItemNullableStringSliceFromAnyMap(typed, "queries"),
			}
		case "openPage", "open_page":
			return map[string]any{"type": "openPage", "url": threadItemNullableStringFromAnyMap(typed, "url")}
		case "findInPage", "find_in_page":
			return map[string]any{
				"type":    "findInPage",
				"url":     threadItemNullableStringFromAnyMap(typed, "url"),
				"pattern": threadItemNullableStringFromAnyMap(typed, "pattern"),
			}
		default:
			return map[string]any{"type": "other"}
		}
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return map[string]any{"type": "search", "query": typed, "queries": nil}
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return map[string]any{"type": "other"}
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return map[string]any{"type": "other"}
		}
		return threadItemWebSearchActionFromAny(decoded)
	}
}

func threadItemNullableStringFromAnyMap(data map[string]any, keys ...string) *string {
	if value := threadItemStringPtrFromAnyMap(data, keys...); value != nil {
		return value
	}
	return nil
}

func threadItemNullableStringSliceFromAnyMap(data map[string]any, keys ...string) any {
	for _, key := range keys {
		value, ok := data[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case []string:
			return append([]string(nil), typed...)
		case []any:
			out := make([]string, 0, len(typed))
			for _, entry := range typed {
				if text, ok := entry.(string); ok {
					out = append(out, text)
				}
			}
			return out
		}
	}
	return nil
}

func threadItemReviewText(item *ThreadItem) string {
	if item == nil {
		return ""
	}
	return firstNonEmpty(threadItemStringFromData(item.Data, "review"), item.Text)
}

func threadItemInt64FromData(data map[string]any, keys ...string) int64 {
	if value := threadItemInt64PtrFromData(data, keys...); value != nil {
		return *value
	}
	return 0
}

func threadItemStringFromAnyMap(data map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := data[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			return typed
		case *string:
			if typed != nil {
				return *typed
			}
		}
	}
	return ""
}

func threadItemStringPtrFromAnyMap(data map[string]any, keys ...string) *string {
	for _, key := range keys {
		value, ok := data[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			return &typed
		case *string:
			return typed
		}
	}
	return nil
}

func MetadataPatchToSession(params *ThreadMetadataUpdateParams) (session.MetadataPatch, error) {
	return MetadataPatchToSessionWithExisting(params, nil)
}

func MetadataPatchToSessionWithExisting(params *ThreadMetadataUpdateParams, existingGit map[string]string) (session.MetadataPatch, error) {
	if err := params.Validate(); err != nil {
		return session.MetadataPatch{}, err
	}
	patch := session.MetadataPatch{}
	if params.GitInfo == nil {
		return session.MetadataPatch{}, jsonRPCInvalidRequest("thread metadata update must include at least one field")
	}
	if !params.GitInfo.SHA.Set && !params.GitInfo.Branch.Set && !params.GitInfo.OriginURL.Set {
		return session.MetadataPatch{}, jsonRPCInvalidRequest("gitInfo must include at least one field")
	}
	patch.Git = cloneGitMap(existingGit)
	if params.GitInfo.SHA.Set {
		value, err := metadataOptionalStringValue(params.GitInfo.SHA.Value, "gitInfo.sha")
		if err != nil {
			return session.MetadataPatch{}, err
		}
		patch.Git["sha"] = value
	}
	if params.GitInfo.Branch.Set {
		value, err := metadataOptionalStringValue(params.GitInfo.Branch.Value, "gitInfo.branch")
		if err != nil {
			return session.MetadataPatch{}, err
		}
		patch.Git["branch"] = value
	}
	if params.GitInfo.OriginURL.Set {
		value, err := metadataOptionalStringValue(params.GitInfo.OriginURL.Value, "gitInfo.originUrl")
		if err != nil {
			return session.MetadataPatch{}, err
		}
		patch.Git["origin_url"] = value
	}
	return patch, nil
}

func threadSectionFromRecord(record *session.Record) *ThreadSection {
	if record == nil {
		return nil
	}
	section := record.Section
	if section == nil && record.IsPinned {
		section = &session.ThreadSection{ID: session.PinnedThreadSectionID, Name: session.PinnedThreadSectionName}
	}
	if section == nil {
		return nil
	}
	return &ThreadSection{ID: section.ID, Name: section.Name}
}

func parseCursor(cursor *string) (int, error) {
	if cursor == nil || *cursor == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(*cursor)
	if err != nil || value < 0 {
		return 0, jsonRPCInvalidRequest("invalid cursor")
	}
	return value, nil
}

func paginateItems(items []ThreadItem, start int, limit int) ([]ThreadItem, string) {
	if start >= len(items) {
		return []ThreadItem{}, ""
	}
	if limit <= 0 {
		limit = threadItemsDefaultLimit
	}
	if limit > threadItemsMaxLimit {
		limit = threadItemsMaxLimit
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return append([]ThreadItem(nil), items[start:end]...), next
}

const (
	threadItemsDefaultLimit             = 25
	threadItemsMaxLimit                 = 100
	threadTurnsDefaultLimit             = 25
	threadTurnsMaxLimit                 = 100
	threadSearchOccurrencesDefaultLimit = 50
	threadSearchOccurrencesMaxLimit     = 250
)

type threadTurnsCursor struct {
	TurnID        string `json:"turnId"`
	IncludeAnchor bool   `json:"includeAnchor"`
}

func paginateTurns(turns []Turn, cursor *string, limit int, sortDirection SortDirection) ([]Turn, string, string, error) {
	if len(turns) == 0 {
		return []Turn{}, "", "", nil
	}
	anchor, err := parseThreadTurnsCursor(cursor)
	if err != nil {
		return nil, "", "", err
	}
	pageSize := limit
	if pageSize <= 0 {
		pageSize = threadTurnsDefaultLimit
	}
	if pageSize > threadTurnsMaxLimit {
		pageSize = threadTurnsMaxLimit
	}
	anchorIndex := -1
	if anchor != nil {
		for i := range turns {
			if turns[i].ID == anchor.TurnID {
				anchorIndex = i
				break
			}
		}
		if anchorIndex < 0 {
			return nil, "", "", jsonRPCInvalidRequest("invalid cursor: anchor turn is no longer present")
		}
	}
	type keyedTurn struct {
		index int
		turn  Turn
	}
	keyed := make([]keyedTurn, 0, len(turns))
	for i, turn := range turns {
		keyed = append(keyed, keyedTurn{index: i, turn: turn})
	}
	if sortDirection == SortDesc {
		for i, j := 0, len(keyed)-1; i < j; i, j = i+1, j-1 {
			keyed[i], keyed[j] = keyed[j], keyed[i]
		}
	}
	if anchor != nil {
		filtered := keyed[:0]
		for _, entry := range keyed {
			include := false
			if sortDirection == SortDesc {
				if anchor.IncludeAnchor {
					include = entry.index <= anchorIndex
				} else {
					include = entry.index < anchorIndex
				}
			} else if anchor.IncludeAnchor {
				include = entry.index >= anchorIndex
			} else {
				include = entry.index > anchorIndex
			}
			if include {
				filtered = append(filtered, entry)
			}
		}
		keyed = filtered
	}
	moreTurnsAvailable := len(keyed) > pageSize
	if moreTurnsAvailable {
		keyed = keyed[:pageSize]
	}
	if len(keyed) == 0 {
		return []Turn{}, "", "", nil
	}
	backwards, err := serializeThreadTurnsCursor(keyed[0].turn.ID, true)
	if err != nil {
		return nil, "", "", err
	}
	next := ""
	if moreTurnsAvailable {
		next, err = serializeThreadTurnsCursor(keyed[len(keyed)-1].turn.ID, false)
		if err != nil {
			return nil, "", "", err
		}
	}
	page := make([]Turn, 0, len(keyed))
	for _, entry := range keyed {
		page = append(page, entry.turn)
	}
	return page, next, backwards, nil
}

func parseThreadTurnsCursor(cursor *string) (*threadTurnsCursor, error) {
	if cursor == nil {
		return nil, nil
	}
	if strings.TrimSpace(*cursor) == "" {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("invalid cursor: %s", *cursor))
	}
	var parsed threadTurnsCursor
	if err := json.Unmarshal([]byte(*cursor), &parsed); err != nil {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("invalid cursor: %s", *cursor))
	}
	return &parsed, nil
}

func serializeThreadTurnsCursor(turnID string, includeAnchor bool) (string, error) {
	data, err := json.Marshal(threadTurnsCursor{TurnID: turnID, IncludeAnchor: includeAnchor})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func turnsFromItems(items []session.Item) []Turn {
	turnsByID := map[string]*Turn{}
	var order []string
	for index, item := range items {
		if sessionItemIsHiddenThreadItem(&item) {
			continue
		}
		threadItem := BuildThreadItem(item)
		turnID := threadItem.TurnID
		if turnID == "" {
			turnID = fallbackTurnID(index)
			threadItem.TurnID = turnID
		}
		turn := turnsByID[turnID]
		if turn == nil {
			startedAt := threadItem.CreatedAt
			turn = &Turn{
				ID:        turnID,
				Items:     []ThreadItem{},
				ItemsView: TurnItemsFull,
				Status:    TurnStatusCompleted,
				StartedAt: &startedAt,
			}
			turnsByID[turnID] = turn
			order = append(order, turnID)
		}
		turn.Items = append(turn.Items, threadItem)
		completedAt := threadItem.CreatedAt
		turn.CompletedAt = &completedAt
	}
	turns := make([]Turn, 0, len(order))
	for _, id := range order {
		turns = append(turns, *turnsByID[id])
	}
	return turns
}

func turnsFromRecord(record *session.Record) []Turn {
	if record == nil {
		return []Turn{}
	}
	base := turnsFromItems(record.Items)
	if len(record.Metadata.RolloutTurns) == 0 {
		return base
	}
	snapshotsByID := make(map[string]session.TurnSnapshot, len(record.Metadata.RolloutTurns))
	snapshotOrder := make([]string, 0, len(record.Metadata.RolloutTurns))
	for _, snapshot := range record.Metadata.RolloutTurns {
		turnID := strings.TrimSpace(snapshot.ID)
		if turnID == "" {
			continue
		}
		if _, ok := snapshotsByID[turnID]; !ok {
			snapshotOrder = append(snapshotOrder, turnID)
		}
		snapshot.ID = turnID
		snapshotsByID[turnID] = snapshot
	}
	if len(snapshotsByID) == 0 {
		return base
	}
	used := make(map[string]bool, len(snapshotsByID))
	turns := make([]Turn, 0, len(base)+len(snapshotsByID))
	for _, turn := range base {
		turnID := strings.TrimSpace(turn.ID)
		if snapshot, ok := snapshotsByID[turnID]; ok {
			applyTurnSnapshot(&turn, snapshot)
			used[turnID] = true
		}
		turns = append(turns, turn)
	}
	for _, turnID := range snapshotOrder {
		if used[turnID] {
			continue
		}
		turn := Turn{
			ID:        turnID,
			Items:     []ThreadItem{},
			ItemsView: TurnItemsFull,
		}
		applyTurnSnapshot(&turn, snapshotsByID[turnID])
		turns = append(turns, turn)
	}
	return turns
}

func applyTurnSnapshot(turn *Turn, snapshot session.TurnSnapshot) {
	if turn == nil {
		return
	}
	if turnID := strings.TrimSpace(snapshot.ID); turnID != "" {
		turn.ID = turnID
	}
	turn.Status = turnStatusFromSnapshot(snapshot.Status)
	turn.StartedAt = cloneInt64PtrAppserver(snapshot.StartedAt)
	turn.CompletedAt = cloneInt64PtrAppserver(snapshot.CompletedAt)
	turn.DurationMS = cloneInt64PtrAppserver(snapshot.DurationMS)
	turn.Error = nil
	if turn.Status == TurnStatusFailed && strings.TrimSpace(snapshot.ErrorMessage) != "" {
		turn.Error = &TurnError{
			Message:        strings.TrimSpace(snapshot.ErrorMessage),
			CodexErrorInfo: snapshot.CodexErrorInfo,
		}
	}
}

func turnStatusFromSnapshot(value string) TurnStatus {
	switch strings.TrimSpace(value) {
	case string(TurnStatusInProgress), "in_progress":
		return TurnStatusInProgress
	case string(TurnStatusInterrupted):
		return TurnStatusInterrupted
	case string(TurnStatusFailed):
		return TurnStatusFailed
	case string(TurnStatusCompleted), "":
		return TurnStatusCompleted
	default:
		return TurnStatus(value)
	}
}

func rollbackItems(items []session.Item, numTurns int) []session.Item {
	if numTurns <= 0 || len(items) == 0 {
		return append([]session.Item(nil), items...)
	}
	turnIDs := []string{}
	seen := map[string]bool{}
	for index, item := range items {
		threadItem := BuildThreadItem(item)
		turnID := threadItem.TurnID
		if turnID == "" {
			turnID = fallbackTurnID(index)
		}
		if !seen[turnID] {
			seen[turnID] = true
			turnIDs = append(turnIDs, turnID)
		}
	}
	if numTurns >= len(turnIDs) {
		return []session.Item{}
	}
	drop := map[string]bool{}
	for _, id := range turnIDs[len(turnIDs)-numTurns:] {
		drop[id] = true
	}
	result := make([]session.Item, 0, len(items))
	for index, item := range items {
		threadItem := BuildThreadItem(item)
		turnID := threadItem.TurnID
		if turnID == "" {
			turnID = fallbackTurnID(index)
		}
		if drop[turnID] {
			continue
		}
		result = append(result, item)
	}
	return result
}

func fallbackTurnID(index int) string {
	return "turn-" + strconv.Itoa(index+1)
}

func gitInfoFromMap(values map[string]string) *GitInfo {
	if len(values) == 0 {
		return nil
	}
	info := &GitInfo{
		SHA:       stringPtrIfNotEmpty(values["sha"]),
		Branch:    stringPtrIfNotEmpty(values["branch"]),
		OriginURL: stringPtrIfNotEmpty(firstNonEmpty(values["origin_url"], values["originUrl"])),
	}
	if info.SHA == nil && info.Branch == nil && info.OriginURL == nil {
		return nil
	}
	return info
}

func threadSourcePtr(value string) *ThreadSource {
	source := ThreadSourceFromString(value)
	if source == "" {
		return nil
	}
	return &source
}

func firstThreadCursor(data []Thread) *string {
	if len(data) == 0 {
		return nil
	}
	return stringPtrIfNotEmpty("0")
}

func searchCursor(data []ThreadSearchResult) *string {
	if len(data) == 0 {
		return nil
	}
	return stringPtrIfNotEmpty("0")
}

func turnCursor(data []Turn) *string {
	if len(data) == 0 {
		return nil
	}
	return &data[0].ID
}

func itemCursor(data []ThreadItem) *string {
	if len(data) == 0 {
		return nil
	}
	return stringPtrIfNotEmpty("0")
}

func reverseItems(items []ThreadItem) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func reverseTurns(turns []Turn) {
	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}
}

func searchSnippet(record *session.Record, term string) string {
	if record == nil {
		return ""
	}
	haystacks := []string{record.Title, record.Preview}
	for _, item := range record.Items {
		haystacks = append(haystacks, item.Text)
	}
	needle := strings.ToLower(strings.TrimSpace(term))
	for _, value := range haystacks {
		lower := strings.ToLower(value)
		index := strings.Index(lower, needle)
		if index < 0 {
			continue
		}
		start := index - 40
		if start < 0 {
			start = 0
		}
		end := index + len(term) + 40
		if end > len(value) {
			end = len(value)
		}
		return strings.TrimSpace(value[start:end])
	}
	return record.Preview
}

func sessionItemFromRaw(raw json.RawMessage, now time.Time, index int) (session.Item, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return session.Item{}, fmt.Errorf("%w: invalid item JSON", ErrInvalidRequest)
	}
	itemType := stringFromMap(payload, "type")
	if itemType == "" {
		itemType = "raw_response_item"
	}
	id := stringFromMap(payload, "id")
	if id == "" {
		id = fmt.Sprintf("injected-%d-%d", now.UnixNano(), index)
	}
	role := stringFromMap(payload, "role")
	text := firstNonEmpty(stringFromMap(payload, "text"), contentText(payload["content"]))
	turnID := firstNonEmpty(stringFromMap(payload, "turnId"), stringFromMap(payload, "turn_id"))
	if turnID == "" {
		turnID = fmt.Sprintf("turn-injected-%d", now.UnixNano())
	}
	metadata := map[string]any{
		"rawResponseItem": payload,
		"turnId":          turnID,
	}
	return session.Item{
		ID:        id,
		Type:      itemType,
		Role:      role,
		Text:      text,
		CreatedAt: now,
		Metadata:  metadata,
	}, nil
}

func sessionItemsFromResumeHistory(history []ThreadResumeHistoryItem, now time.Time) ([]session.Item, error) {
	items := make([]session.Item, 0, len(history))
	for i := range history {
		item, err := sessionItemFromResumeHistory(&history[i], now, i)
		if err != nil {
			return nil, err
		}
		if item.ID == "" {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

func sessionItemFromResumeHistory(raw *ThreadResumeHistoryItem, now time.Time, index int) (session.Item, error) {
	if raw == nil {
		return session.Item{}, nil
	}
	var payload map[string]any
	data := json.RawMessage(*raw)
	if err := json.Unmarshal(data, &payload); err != nil {
		return session.Item{}, fmt.Errorf("%w: invalid history item JSON", ErrInvalidRequest)
	}
	itemType := stringFromMap(payload, "type")
	id := stringFromMap(payload, "id")
	if id == "" {
		id = fmt.Sprintf("history-%d-%d", now.UnixNano(), index)
	}
	turnID := resumeHistoryTurnID(payload, id, index)
	metadata := map[string]any{
		"rawResponseItem": payload,
		"turnId":          turnID,
	}
	item := session.Item{
		ID:        id,
		CreatedAt: now.Add(time.Duration(index) * time.Nanosecond),
		Metadata:  metadata,
		Raw:       append(json.RawMessage(nil), data...),
	}
	switch itemType {
	case "message", "userMessage", "user_message":
		item.Type = "message"
		item.Role = firstNonEmpty(stringFromMap(payload, "role"), "user")
		item.Text = contentText(payload["content"])
		item.Content = sessionContentPartsFromResponseContent(payload["content"], item.Role)
		if item.Role == "assistant" {
			item.Type = "agent_message"
		}
		item.Data = cloneAnyMap(payload)
	case "agentMessage", "agent_message":
		item.Type = "agent_message"
		item.Role = "assistant"
		item.Text = stringFromMap(payload, "text")
		item.Data = cloneAnyMap(payload)
	case "plan":
		item.Type = "plan"
		item.Text = stringFromMap(payload, "text")
		item.Data = cloneAnyMap(payload)
	case "reasoning":
		item.Type = "reasoning"
		item.Text = firstNonEmpty(contentText(payload["summary"]), contentText(payload["content"]))
		if item.Text == "" {
			item.Text = strings.Join(stringSliceFromAny(payload["summary"]), "\n")
		}
		item.Data = cloneAnyMap(payload)
	case "hookPrompt", "hook_prompt":
		item.Type = "hookPrompt"
		item.Text = hookPromptFragmentsText(payload["fragments"])
		item.Data = normalizeHistoryUnionData(payload, "")
	case "function_call", "custom_tool_call", "tool_search_call":
		item.Type = itemType
		item.Name = stringFromMap(payload, "name")
		item.CallID = firstNonEmpty(stringFromMap(payload, "call_id"), stringFromMap(payload, "callId"), id)
		item.Text = firstNonEmpty(stringFromMap(payload, "arguments"), stringFromMap(payload, "input"))
		item.Data = cloneAnyMap(payload)
	case "function_call_output", "custom_tool_call_output", "tool_search_output":
		item.Type = itemType
		item.CallID = firstNonEmpty(stringFromMap(payload, "call_id"), stringFromMap(payload, "callId"), id)
		item.Text = firstNonEmpty(stringFromMap(payload, "output"), contentText(payload["output"]))
		item.Data = cloneAnyMap(payload)
	case "mcpToolCall", "mcp_tool_call":
		item.Type = "mcpToolCall"
		item.Name = joinHistoryNamespaceTool(stringFromMap(payload, "server"), stringFromMap(payload, "tool"))
		item.Text = firstNonEmpty(stringFromMap(payload, "aggregatedOutput"), stringFromMap(payload, "output"))
		item.Data = normalizeHistoryUnionData(payload, "mcpToolCall")
	case "dynamicToolCall", "dynamic_tool_call":
		item.Type = "dynamicToolCall"
		item.Name = joinHistoryNamespaceTool(stringFromMap(payload, "namespace"), stringFromMap(payload, "tool"))
		item.Text = firstNonEmpty(stringFromMap(payload, "output"), contentText(payload["contentItems"]), contentText(payload["content_items"]))
		item.Data = normalizeHistoryUnionData(payload, "dynamicToolCall")
	case "fileChange", "file_change":
		item.Type = "fileChange"
		item.Name = "apply_patch"
		item.Data = normalizeHistoryUnionData(payload, "fileChange")
	case "commandExecution", "command_execution":
		item.Type = "commandExecution"
		item.Text = firstNonEmpty(stringFromMap(payload, "aggregatedOutput"), stringFromMap(payload, "aggregated_output"), stringFromMap(payload, "output"))
		item.Data = normalizeHistoryUnionData(payload, "")
	case "collabAgentToolCall", "collab_agent_tool_call":
		item.Type = "collabAgentToolCall"
		item.Text = firstNonEmpty(stringFromMap(payload, "prompt"), stringFromMap(payload, "tool"))
		item.Data = normalizeHistoryUnionData(payload, "")
	case "subAgentActivity", "sub_agent_activity":
		item.Type = "subAgentActivity"
		item.Text = firstNonEmpty(stringFromMap(payload, "kind"), stringFromMap(payload, "agentPath"), stringFromMap(payload, "agent_path"))
		item.Data = normalizeHistoryUnionData(payload, "")
	case "webSearch", "web_search":
		item.Type = "webSearch"
		item.Text = stringFromMap(payload, "query")
		item.Data = normalizeHistoryUnionData(payload, "")
	case "imageView", "image_view":
		item.Type = "imageView"
		item.Text = stringFromMap(payload, "path")
		item.Data = normalizeHistoryUnionData(payload, "")
	case "sleep":
		item.Type = "sleep"
		item.Text = stringFromMap(payload, "durationMs")
		item.Data = normalizeHistoryUnionData(payload, "")
	case "imageGeneration", "image_generation", "image_generation_call":
		item.Type = "imageGeneration"
		item.Status = stringFromMap(payload, "status")
		item.Text = firstNonEmpty(stringFromMap(payload, "result"), stringFromMap(payload, "revisedPrompt"), stringFromMap(payload, "revised_prompt"))
		item.Data = normalizeHistoryUnionData(payload, "")
	case "enteredReviewMode", "entered_review_mode", "exitedReviewMode", "exited_review_mode":
		item.Type = normalizeThreadItemType(itemType)
		item.Text = stringFromMap(payload, "review")
		item.Data = normalizeHistoryUnionData(payload, "")
	case "contextCompaction", "context_compaction":
		item.Type = "contextCompaction"
		item.Data = normalizeHistoryUnionData(payload, "")
	default:
		item.Type = firstNonEmpty(itemType, "raw_response_item")
		item.Role = stringFromMap(payload, "role")
		item.Text = firstNonEmpty(stringFromMap(payload, "text"), contentText(payload["content"]))
		item.Data = cloneAnyMap(payload)
	}
	return item, nil
}

func normalizeHistoryUnionData(payload map[string]any, marker string) map[string]any {
	data := cloneAnyMap(payload)
	if data == nil {
		data = map[string]any{}
	}
	if marker != "" {
		data[marker] = true
	}
	copyHistoryAlias(data, "contentItems", "content_items")
	copyHistoryAlias(data, "durationMs", "duration_ms")
	copyHistoryAlias(data, "exitCode", "exit_code")
	copyHistoryAlias(data, "aggregatedOutput", "aggregated_output")
	copyHistoryAlias(data, "processId", "process_id")
	copyHistoryAlias(data, "commandActions", "command_actions")
	copyHistoryAlias(data, "mcpAppResourceUri", "mcp_app_resource_uri")
	copyHistoryAlias(data, "pluginId", "plugin_id")
	copyHistoryAlias(data, "appContext", "app_context")
	copyHistoryAlias(data, "hookRunId", "hook_run_id")
	copyHistoryAlias(data, "senderThreadId", "sender_thread_id")
	copyHistoryAlias(data, "receiverThreadIds", "receiver_thread_ids")
	copyHistoryAlias(data, "agentsStates", "agents_states")
	copyHistoryAlias(data, "agentThreadId", "agent_thread_id")
	copyHistoryAlias(data, "agentPath", "agent_path")
	copyHistoryAlias(data, "webSearchAction", "web_search_action")
	copyHistoryAlias(data, "revisedPrompt", "revised_prompt")
	copyHistoryAlias(data, "savedPath", "saved_path")
	copyHistoryAlias(data, "transparentBackground", "transparent_background")
	copyHistoryAlias(data, "arguments", "input", "rawArguments", "raw_arguments")
	return data
}

func copyHistoryAlias(data map[string]any, target string, aliases ...string) {
	if data == nil {
		return
	}
	if _, ok := data[target]; ok {
		return
	}
	for _, alias := range aliases {
		if value, ok := data[alias]; ok {
			data[target] = value
			return
		}
	}
}

func joinHistoryNamespaceTool(namespace string, tool string) string {
	namespace = strings.TrimSpace(namespace)
	tool = strings.TrimSpace(tool)
	if namespace == "" {
		return tool
	}
	if tool == "" {
		return namespace
	}
	return namespace + "." + tool
}

func sessionContentPartsFromResponseContent(value any, role string) []session.ContentPart {
	content, ok := value.([]any)
	if !ok {
		return nil
	}
	parts := make([]session.ContentPart, 0, len(content))
	for _, entry := range content {
		part, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		partType := stringFromMap(part, "type")
		switch partType {
		case "input_image", "image":
			detail := stringPtrIfNotEmpty(stringFromMap(part, "detail"))
			parts = append(parts, session.ContentPart{Type: "input_image", ImageURL: firstNonEmpty(stringFromMap(part, "image_url"), stringFromMap(part, "url")), Detail: detail})
		case "localImage", "local_image":
			detail := stringPtrIfNotEmpty(stringFromMap(part, "detail"))
			parts = append(parts, session.ContentPart{Type: "local_image", ImageURL: stringFromMap(part, "path"), Detail: detail})
		case "input_audio", "audio":
			parts = append(parts, session.ContentPart{Type: "input_audio", AudioURL: firstNonEmpty(stringFromMap(part, "audio_url"), stringFromMap(part, "url"))})
		case "localAudio", "local_audio":
			parts = append(parts, session.ContentPart{Type: "local_audio", AudioURL: stringFromMap(part, "path")})
		default:
			text := firstNonEmpty(stringFromMap(part, "text"), stringFromMap(part, "input_text"), stringFromMap(part, "output_text"))
			if text == "" {
				continue
			}
			contentType := "input_text"
			if role == "assistant" || partType == "output_text" {
				contentType = "output_text"
			}
			parts = append(parts, session.ContentPart{Type: contentType, Text: text})
		}
	}
	return parts
}

func hookPromptFragmentsText(value any) string {
	fragments := threadItemHookPromptFragmentsFromAny(value)
	if len(fragments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fragments))
	for _, fragment := range fragments {
		if strings.TrimSpace(fragment.Text) != "" {
			parts = append(parts, fragment.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func resumeHistoryTurnID(payload map[string]any, id string, index int) string {
	if turnID := stringFromMap(payload, "turnId"); turnID != "" {
		return turnID
	}
	if turnID := stringFromMap(payload, "turn_id"); turnID != "" {
		return turnID
	}
	if metadata, ok := payload["internal_chat_message_metadata_passthrough"].(map[string]any); ok {
		if turnID := stringFromMap(metadata, "turn_id"); turnID != "" {
			return turnID
		}
		if turnID := stringFromMap(metadata, "turnId"); turnID != "" {
			return turnID
		}
	}
	if id != "" {
		return "turn-" + safeIdentifier(id)
	}
	return fallbackTurnID(index)
}

func historyPreview(items []session.Item) string {
	for i := range items {
		if sessionItemIsHiddenThreadItem(&items[i]) {
			continue
		}
		if items[i].Role == "user" && strings.TrimSpace(items[i].Text) != "" {
			return strings.TrimSpace(items[i].Text)
		}
	}
	for i := range items {
		if sessionItemIsHiddenThreadItem(&items[i]) {
			continue
		}
		if strings.TrimSpace(items[i].Text) != "" {
			return strings.TrimSpace(items[i].Text)
		}
	}
	return ""
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return text
}

func boolFromMap(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	return boolFromAny(values[key])
}

func contentText(value any) string {
	content, ok := value.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(content))
	for _, entry := range content {
		part, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		text := firstNonEmpty(stringFromMap(part, "text"), stringFromMap(part, "input_text"), stringFromMap(part, "output_text"))
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func stringSliceFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		values := make([]string, 0, len(typed))
		for _, entry := range typed {
			if text, ok := entry.(string); ok {
				values = append(values, text)
			}
		}
		return values
	default:
		return nil
	}
}

func stringPtrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func metadataOptionalStringValue(value *string, name string) (string, error) {
	if value == nil {
		return "", nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return "", jsonRPCInvalidRequest(fmt.Sprintf("%s must not be empty", name))
	}
	return trimmed, nil
}

func cloneGitMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func stringSliceForJSON(values []string) []string {
	out := append([]string(nil), values...)
	if out == nil {
		return []string{}
	}
	return out
}

func unixOrZero(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().Unix()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
