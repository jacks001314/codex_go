package cloudclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

const threadServiceUpdateConfigMethod = "/oaiproto.codex_app_server_backend.ThreadService/UpdateConfig"

// ThreadActivation contains the resolved settings and metadata returned when a
// cloud thread is deliberately loaded without hydrating its turns (Rust #59343).
type ThreadActivation struct {
	Thread                  ThreadSnapshot           `json:"thread"`
	CollaborationMode       *CollaborationMode       `json:"collaborationMode,omitempty"`
	Model                   string                   `json:"model"`
	ModelProvider           string                   `json:"modelProvider"`
	ServiceTier             *string                  `json:"serviceTier,omitempty"`
	CWD                     string                   `json:"cwd"`
	InstructionSources      []string                 `json:"instructionSources"`
	RuntimeWorkspaceRoots   []string                 `json:"runtimeWorkspaceRoots"`
	ApprovalPolicy          any                      `json:"approvalPolicy"`
	ApprovalsReviewer       string                   `json:"approvalsReviewer"`
	Sandbox                 any                      `json:"sandbox"`
	ActivePermissionProfile *ActivePermissionProfile `json:"activePermissionProfile,omitempty"`
	ReasoningEffort         *string                  `json:"reasoningEffort,omitempty"`
	TurnsBackwardsCursor    *string                  `json:"turnsBackwardsCursor,omitempty"`
	ItemsBackwardsCursor    *string                  `json:"itemsBackwardsCursor,omitempty"`
}

type CollaborationMode struct {
	Mode     string                `json:"mode"`
	Settings CollaborationSettings `json:"settings"`
}

type CollaborationSettings struct {
	Model                 string  `json:"model"`
	ReasoningEffort       *string `json:"reasoning_effort,omitempty"`
	DeveloperInstructions *string `json:"developer_instructions,omitempty"`
}

type ThreadSnapshot struct {
	ID                   string               `json:"id"`
	SessionID            string               `json:"sessionId"`
	ForkedFromID         *string              `json:"forkedFromId,omitempty"`
	ParentThreadID       *string              `json:"parentThreadId,omitempty"`
	Preview              string               `json:"preview"`
	Ephemeral            bool                 `json:"ephemeral"`
	DaybreakEnabled      *bool                `json:"daybreakEnabled,omitempty"`
	ProjectID            *string              `json:"projectId,omitempty"`
	HistoryMode          string               `json:"historyMode"`
	ModelProvider        string               `json:"modelProvider"`
	Model                *string              `json:"model,omitempty"`
	ReasoningEffort      *string              `json:"reasoningEffort,omitempty"`
	CreatedAt            int64                `json:"createdAt"`
	UpdatedAt            int64                `json:"updatedAt"`
	RecencyAt            *int64               `json:"recencyAt,omitempty"`
	Status               any                  `json:"status"`
	Path                 *string              `json:"path,omitempty"`
	CWD                  string               `json:"cwd"`
	CLIVersion           string               `json:"cliVersion"`
	Originator           *string              `json:"originator,omitempty"`
	Source               any                  `json:"source"`
	CanAcceptDirectInput *bool                `json:"canAcceptDirectInput,omitempty"`
	ThreadSource         *string              `json:"threadSource,omitempty"`
	AgentNickname        *string              `json:"agentNickname,omitempty"`
	AgentRole            *string              `json:"agentRole,omitempty"`
	Name                 *string              `json:"name,omitempty"`
	Environments         *[]ThreadEnvironment `json:"environments,omitempty"`
	Turns                []any                `json:"turns"`
	RawProtobuf          []byte               `json:"-"`
}

func (s ThreadSnapshot) Proto() []byte { return append([]byte(nil), s.RawProtobuf...) }

type ThreadEnvironment struct {
	EnvironmentID         string   `json:"environmentId"`
	CWD                   string   `json:"cwd"`
	RuntimeWorkspaceRoots []string `json:"runtimeWorkspaceRoots"`
}

type ActivePermissionProfile struct {
	ID      string  `json:"id"`
	Extends *string `json:"extends,omitempty"`
}

// Activate loads a thread with exclude_turns=true and returns its resolved
// settings. The RPC is a mutation and is never retried; transport failures leave
// the outcome unknown.
func (c *Client) Activate(ctx context.Context, threadID string) (*ThreadActivation, error) {
	if c == nil || c.conn == nil {
		return nil, &MutationError{Kind: MutationOutcomeUnknown, Err: ErrClientBuild}
	}
	if strings.TrimSpace(threadID) == "" {
		return nil, &MutationError{Kind: MutationInvalidRequest}
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var response rawMessage
	if err := c.conn.Invoke(c.outgoingContext(requestCtx), threadServiceUpdateConfigMethod, rawMessage(encodeUpdateConfigRequest(threadID)), &response); err != nil {
		return nil, &MutationError{Kind: MutationOutcomeUnknown, Err: mapCallError(requestCtx, err)}
	}
	activation, err := decodeThreadActivation(response, threadID)
	if err != nil {
		return nil, &MutationError{Kind: MutationOutcomeUnknown, Err: err}
	}
	return activation, nil
}

func encodeUpdateConfigRequest(threadID string) []byte {
	out := protowire.AppendTag(nil, 7, protowire.VarintType)
	out = protowire.AppendVarint(out, 1)
	out = protowire.AppendTag(out, 18, protowire.BytesType)
	return protowire.AppendString(out, threadID)
}

type wireField struct {
	number protowire.Number
	typ    protowire.Type
	bytes  []byte
	value  uint64
}

func parseWireFields(data []byte) ([]wireField, error) {
	fields := []wireField{}
	for len(data) > 0 {
		number, typ, consumed := protowire.ConsumeTag(data)
		if consumed < 0 {
			return nil, ErrInvalidResponse
		}
		data = data[consumed:]
		field := wireField{number: number, typ: typ}
		switch typ {
		case protowire.VarintType:
			value, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return nil, ErrInvalidResponse
			}
			field.value = value
			data = data[n:]
		case protowire.BytesType:
			value, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return nil, ErrInvalidResponse
			}
			field.bytes = append([]byte(nil), value...)
			data = data[n:]
		case protowire.Fixed32Type:
			_, n := protowire.ConsumeFixed32(data)
			if n < 0 {
				return nil, ErrInvalidResponse
			}
			data = data[n:]
		case protowire.Fixed64Type:
			_, n := protowire.ConsumeFixed64(data)
			if n < 0 {
				return nil, ErrInvalidResponse
			}
			data = data[n:]
		default:
			n := protowire.ConsumeFieldValue(number, typ, data)
			if n < 0 {
				return nil, ErrInvalidResponse
			}
			data = data[n:]
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func firstField(fields []wireField, number protowire.Number) (wireField, bool) {
	for _, field := range fields {
		if field.number == number {
			return field, true
		}
	}
	return wireField{}, false
}

// lastField follows protobuf scalar semantics when a peer repeats a field.
func lastField(fields []wireField, number protowire.Number) (wireField, bool) {
	for index := len(fields) - 1; index >= 0; index-- {
		if fields[index].number == number {
			return fields[index], true
		}
	}
	return wireField{}, false
}

func fieldBytes(fields []wireField, number protowire.Number) ([]byte, bool) {
	field, ok := lastField(fields, number)
	if !ok || field.typ != protowire.BytesType {
		return nil, false
	}
	return field.bytes, true
}

func fieldString(fields []wireField, number protowire.Number) (*string, error) {
	value, ok := fieldBytes(fields, number)
	if !ok {
		return nil, nil
	}
	text := string(value)
	return &text, nil
}

func requiredString(fields []wireField, number protowire.Number, nonempty bool) (string, error) {
	text, err := fieldString(fields, number)
	if err != nil || text == nil || (nonempty && *text == "") {
		return "", ErrInvalidResponse
	}
	return *text, nil
}

func fieldBool(fields []wireField, number protowire.Number) (*bool, error) {
	field, ok := lastField(fields, number)
	if !ok {
		return nil, nil
	}
	if field.typ != protowire.VarintType {
		return nil, ErrInvalidResponse
	}
	value := field.value != 0
	return &value, nil
}

func requiredBool(fields []wireField, number protowire.Number) (bool, error) {
	value, err := fieldBool(fields, number)
	if err != nil || value == nil {
		return false, ErrInvalidResponse
	}
	return *value, nil
}

func fieldInt64(fields []wireField, number protowire.Number) (*int64, error) {
	field, ok := lastField(fields, number)
	if !ok {
		return nil, nil
	}
	if field.typ != protowire.VarintType {
		return nil, ErrInvalidResponse
	}
	value := int64(field.value)
	return &value, nil
}

func requiredInt64(fields []wireField, number protowire.Number) (int64, error) {
	value, err := fieldInt64(fields, number)
	if err != nil || value == nil {
		return 0, ErrInvalidResponse
	}
	return *value, nil
}

func decodeThreadActivation(data []byte, requestedID string) (*ThreadActivation, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return nil, err
	}
	rawThread, ok := fieldBytes(fields, 17)
	if !ok || len(rawThread) == 0 {
		return nil, ErrInvalidResponse
	}
	thread, err := decodeThreadSnapshot(rawThread)
	if err != nil || (requestedID != "" && thread.ID != requestedID) {
		return nil, ErrInvalidResponse
	}
	if thread.SessionID == "" || thread.ModelProvider == "" {
		return nil, ErrInvalidResponse
	}
	model, err := requiredString(fields, 10, true)
	if err != nil {
		return nil, err
	}
	provider, err := requiredString(fields, 11, true)
	if err != nil {
		return nil, err
	}
	cwd, err := requiredString(fields, 5, true)
	if err != nil {
		return nil, err
	}
	approvalBytes, ok := fieldBytes(fields, 2)
	if !ok {
		return nil, ErrInvalidResponse
	}
	approval, err := decodeApprovalPolicy(approvalBytes)
	if err != nil {
		return nil, err
	}
	reviewerField, ok := lastField(fields, 3)
	if !ok || reviewerField.typ != protowire.VarintType {
		return nil, ErrInvalidResponse
	}
	reviewer := ""
	switch reviewerField.value {
	case 1:
		reviewer = "user"
	case 2, 3:
		reviewer = "auto_review"
	default:
		return nil, fmt.Errorf("%w: approvals reviewer", ErrInvalidResponse)
	}
	sandboxBytes, ok := fieldBytes(fields, 15)
	if !ok {
		return nil, ErrInvalidResponse
	}
	sandbox, err := decodeSandboxPolicy(sandboxBytes)
	if err != nil {
		return nil, err
	}
	activation := &ThreadActivation{
		Thread:                *thread,
		Model:                 model,
		ModelProvider:         provider,
		CWD:                   cwd,
		InstructionSources:    []string{},
		RuntimeWorkspaceRoots: []string{},
		ApprovalPolicy:        approval,
		ApprovalsReviewer:     reviewer,
		Sandbox:               sandbox,
	}
	if modeBytes, ok := fieldBytes(fields, 4); ok {
		mode, err := decodeCollaborationMode(modeBytes)
		if err != nil {
			return nil, err
		}
		activation.CollaborationMode = mode
	}
	if list, ok := fieldBytes(fields, 8); ok {
		activation.InstructionSources, err = decodeStringList(list)
		if err != nil {
			return nil, err
		}
	}
	if list, ok := fieldBytes(fields, 14); ok {
		activation.RuntimeWorkspaceRoots, err = decodeStringList(list)
		if err != nil {
			return nil, err
		}
	}
	if profileBytes, ok := fieldBytes(fields, 1); ok {
		profile, err := decodeActivePermissionProfile(profileBytes)
		if err != nil {
			return nil, err
		}
		activation.ActivePermissionProfile = profile
	}
	activation.ReasoningEffort, _ = fieldString(fields, 13)
	activation.TurnsBackwardsCursor, _ = fieldString(fields, 18)
	activation.ItemsBackwardsCursor, _ = fieldString(fields, 9)
	if tierBytes, ok := fieldBytes(fields, 16); ok {
		value, isNull, err := decodeNullableString(tierBytes)
		if err != nil {
			return nil, err
		}
		if !isNull {
			activation.ServiceTier = &value
		}
	}
	return activation, nil
}

func decodeThreadSnapshot(data []byte) (*ThreadSnapshot, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return nil, err
	}
	id, err := requiredString(fields, 14, true)
	if err != nil {
		return nil, err
	}
	sessionID, err := requiredString(fields, 27, false)
	if err != nil {
		return nil, err
	}
	preview, err := requiredString(fields, 21, false)
	if err != nil {
		return nil, err
	}
	ephemeral, err := requiredBool(fields, 9)
	if err != nil {
		return nil, err
	}
	provider, err := requiredString(fields, 16, false)
	if err != nil {
		return nil, err
	}
	created, err := requiredInt64(fields, 5)
	if err != nil {
		return nil, err
	}
	updated, err := requiredInt64(fields, 32)
	if err != nil {
		return nil, err
	}
	cwd, err := requiredString(fields, 6, true)
	if err != nil {
		return nil, err
	}
	cliVersion, err := requiredString(fields, 4, false)
	if err != nil {
		return nil, err
	}
	statusBytes, ok := fieldBytes(fields, 29)
	if !ok {
		return nil, ErrInvalidResponse
	}
	status, err := decodeThreadStatus(statusBytes)
	if err != nil {
		return nil, err
	}
	sourceBytes, ok := fieldBytes(fields, 28)
	if !ok {
		return nil, ErrInvalidResponse
	}
	source, err := decodeSessionSource(sourceBytes)
	if err != nil {
		return nil, err
	}
	historyMode := "legacy"
	if value, err := fieldInt64(fields, 13); err != nil {
		return nil, err
	} else if value != nil {
		switch *value {
		case 1:
		case 2:
			historyMode = "paginated"
		default:
			return nil, fmt.Errorf("%w: thread history mode", ErrInvalidResponse)
		}
	}
	thread := &ThreadSnapshot{
		ID: id, SessionID: sessionID, Preview: preview, Ephemeral: ephemeral,
		HistoryMode: historyMode, ModelProvider: provider, CreatedAt: created,
		UpdatedAt: updated, Status: status, CWD: cwd, CLIVersion: cliVersion,
		Source: source, Turns: []any{}, RawProtobuf: append([]byte(nil), data...),
	}
	thread.ForkedFromID, _ = fieldString(fields, 11)
	thread.ParentThreadID, _ = fieldString(fields, 19)
	thread.DaybreakEnabled, _ = fieldBool(fields, 7)
	thread.ProjectID, _ = fieldString(fields, 22)
	thread.Model, _ = fieldString(fields, 15)
	thread.ReasoningEffort, _ = fieldString(fields, 23)
	thread.RecencyAt, _ = fieldInt64(fields, 24)
	thread.Path, _ = fieldString(fields, 20)
	thread.Originator, _ = fieldString(fields, 18)
	thread.CanAcceptDirectInput, _ = fieldBool(fields, 3)
	thread.ThreadSource, _ = fieldString(fields, 30)
	thread.AgentNickname, _ = fieldString(fields, 1)
	thread.AgentRole, _ = fieldString(fields, 2)
	thread.Name, _ = fieldString(fields, 17)
	if environmentsBytes, ok := fieldBytes(fields, 8); ok {
		environmentFields, err := parseWireFields(environmentsBytes)
		if err != nil {
			return nil, err
		}
		environments := []ThreadEnvironment{}
		for _, field := range environmentFields {
			if field.number != 1 {
				continue
			}
			if field.typ != protowire.BytesType {
				return nil, ErrInvalidResponse
			}
			environment, err := decodeThreadEnvironment(field.bytes)
			if err != nil {
				return nil, err
			}
			environments = append(environments, environment)
		}
		thread.Environments = &environments
	}
	return thread, nil
}

func decodeThreadEnvironment(data []byte) (ThreadEnvironment, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return ThreadEnvironment{}, err
	}
	cwd, err := requiredString(fields, 1, true)
	if err != nil {
		return ThreadEnvironment{}, err
	}
	id, err := requiredString(fields, 2, true)
	if err != nil {
		return ThreadEnvironment{}, err
	}
	rootsBytes, ok := fieldBytes(fields, 3)
	if !ok {
		return ThreadEnvironment{}, ErrInvalidResponse
	}
	roots, err := decodeStringList(rootsBytes)
	if err != nil {
		return ThreadEnvironment{}, err
	}
	return ThreadEnvironment{EnvironmentID: id, CWD: cwd, RuntimeWorkspaceRoots: roots}, nil
}

func decodeStringList(data []byte) ([]string, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return nil, err
	}
	values := []string{}
	for _, field := range fields {
		if field.number == 1 && field.typ == protowire.BytesType {
			values = append(values, string(field.bytes))
		}
	}
	return values, nil
}

func decodeCollaborationMode(data []byte) (*CollaborationMode, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return nil, err
	}
	modeField, ok := firstField(fields, 1)
	if !ok || modeField.typ != protowire.VarintType {
		return nil, ErrInvalidResponse
	}
	mode := ""
	switch modeField.value {
	case 1:
		mode = "plan"
	case 2:
		mode = "default"
	default:
		return nil, fmt.Errorf("%w: collaboration mode", ErrInvalidResponse)
	}
	settingsBytes, ok := fieldBytes(fields, 2)
	if !ok {
		return nil, ErrInvalidResponse
	}
	settingsFields, err := parseWireFields(settingsBytes)
	if err != nil {
		return nil, err
	}
	model, err := requiredString(settingsFields, 2, true)
	if err != nil {
		return nil, err
	}
	settings := CollaborationSettings{Model: model}
	settings.DeveloperInstructions, _ = fieldString(settingsFields, 1)
	settings.ReasoningEffort, _ = fieldString(settingsFields, 3)
	return &CollaborationMode{Mode: mode, Settings: settings}, nil
}

func decodeApprovalPolicy(data []byte) (any, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return nil, err
	}
	if simple, ok := firstField(fields, 1); ok {
		if simple.typ != protowire.VarintType {
			return nil, ErrInvalidResponse
		}
		switch simple.value {
		case 1:
			return "untrusted", nil
		case 2:
			return "on-request", nil
		case 3:
			return "never", nil
		default:
			return nil, fmt.Errorf("%w: approval policy", ErrInvalidResponse)
		}
	}
	granularBytes, ok := fieldBytes(fields, 2)
	if !ok {
		return nil, ErrInvalidResponse
	}
	wrapperFields, err := parseWireFields(granularBytes)
	if err != nil {
		return nil, err
	}
	valueBytes, ok := fieldBytes(wrapperFields, 1)
	if !ok {
		return nil, ErrInvalidResponse
	}
	valueFields, err := parseWireFields(valueBytes)
	if err != nil {
		return nil, err
	}
	mcp, err := requiredBool(valueFields, 1)
	if err != nil {
		return nil, err
	}
	requestPermissions, err := requiredBool(valueFields, 2)
	if err != nil {
		return nil, err
	}
	rules, err := requiredBool(valueFields, 3)
	if err != nil {
		return nil, err
	}
	sandboxApproval, err := requiredBool(valueFields, 4)
	if err != nil {
		return nil, err
	}
	skillApproval, _ := fieldBool(valueFields, 5)
	skill := false
	if skillApproval != nil {
		skill = *skillApproval
	}
	return map[string]any{"granular": map[string]any{
		"sandbox_approval": sandboxApproval, "rules": rules, "skill_approval": skill,
		"request_permissions": requestPermissions, "mcp_elicitations": mcp,
	}}, nil
}

func decodeSandboxPolicy(data []byte) (any, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return nil, err
	}
	selected := 0
	var selectedBytes []byte
	for _, field := range fields {
		if field.number < 1 || field.number > 4 {
			continue
		}
		if field.typ != protowire.BytesType {
			return nil, ErrInvalidResponse
		}
		selected = int(field.number)
		selectedBytes = field.bytes
	}
	switch selected {
	case 0:
		return nil, ErrInvalidResponse
	case 1:
		return map[string]any{"type": "dangerFullAccess"}, nil
	case 2:
		valueFields, err := parseWireFields(selectedBytes)
		if err != nil {
			return nil, err
		}
		network, err := fieldBool(valueFields, 1)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "readOnly", "networkAccess": network != nil && *network}, nil
	case 3:
		valueFields, err := parseWireFields(selectedBytes)
		if err != nil {
			return nil, err
		}
		networkField, ok := lastField(valueFields, 1)
		if ok && networkField.typ != protowire.VarintType {
			return nil, ErrInvalidResponse
		}
		name := "restricted"
		if ok {
			switch networkField.value {
			case 0, 1:
			case 2:
				name = "enabled"
			default:
				return nil, fmt.Errorf("%w: external network policy", ErrInvalidResponse)
			}
		}
		return map[string]any{"type": "externalSandbox", "networkAccess": name}, nil
	case 4:
		valueFields, err := parseWireFields(selectedBytes)
		if err != nil {
			return nil, err
		}
		network, err := fieldBool(valueFields, 3)
		if err != nil {
			return nil, err
		}
		excludeSlash, err := fieldBool(valueFields, 1)
		if err != nil {
			return nil, err
		}
		excludeTmpdir, err := fieldBool(valueFields, 2)
		if err != nil {
			return nil, err
		}
		roots := []string{}
		if rootsBytes, ok := fieldBytes(valueFields, 4); ok {
			roots, err = decodeStringList(rootsBytes)
			if err != nil {
				return nil, err
			}
		}
		return map[string]any{
			"type": "workspaceWrite", "writableRoots": roots,
			"networkAccess":       network != nil && *network,
			"excludeTmpdirEnvVar": excludeTmpdir != nil && *excludeTmpdir,
			"excludeSlashTmp":     excludeSlash != nil && *excludeSlash,
		}, nil
	}
	return nil, ErrInvalidResponse
}

func decodeNullableString(data []byte) (string, bool, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return "", false, err
	}
	if value, ok := fieldBytes(fields, 1); ok {
		return string(value), false, nil
	}
	if _, ok := fieldBytes(fields, 2); ok {
		return "", true, nil
	}
	return "", true, nil
}

func decodeActivePermissionProfile(data []byte) (*ActivePermissionProfile, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return nil, err
	}
	id, err := requiredString(fields, 2, true)
	if err != nil {
		return nil, err
	}
	profile := &ActivePermissionProfile{ID: id}
	profile.Extends, _ = fieldString(fields, 1)
	return profile, nil
}

func decodeSessionSource(data []byte) (any, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return nil, err
	}
	if simple, ok := firstField(fields, 1); ok {
		if simple.typ != protowire.VarintType {
			return nil, ErrInvalidResponse
		}
		switch simple.value {
		case 1:
			return "cli", nil
		case 2:
			return "vscode", nil
		case 3:
			return "exec", nil
		case 4:
			return "appServer", nil
		case 0:
			return nil, fmt.Errorf("%w: session source", ErrInvalidResponse)
		default:
			return "unknown", nil
		}
	}
	if customBytes, ok := fieldBytes(fields, 2); ok {
		customFields, err := parseWireFields(customBytes)
		if err != nil {
			return nil, err
		}
		custom, err := requiredString(customFields, 1, true)
		if err != nil {
			return nil, err
		}
		return map[string]any{"custom": custom}, nil
	}
	subAgentBytes, ok := fieldBytes(fields, 3)
	if !ok {
		return nil, ErrInvalidResponse
	}
	wrapperFields, err := parseWireFields(subAgentBytes)
	if err != nil {
		return nil, err
	}
	valueBytes, ok := fieldBytes(wrapperFields, 1)
	if !ok {
		return nil, ErrInvalidResponse
	}
	source, err := decodeSubAgentSource(valueBytes)
	if err != nil {
		return nil, err
	}
	return map[string]any{"subAgent": source}, nil
}

func decodeSubAgentSource(data []byte) (any, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return nil, err
	}
	if simple, ok := firstField(fields, 1); ok && simple.typ == protowire.VarintType {
		switch simple.value {
		case 1:
			return "review", nil
		case 2:
			return "compact", nil
		case 3:
			return "memory_consolidation", nil
		default:
			return nil, fmt.Errorf("%w: sub-agent source", ErrInvalidResponse)
		}
	}
	if spawnBytes, ok := fieldBytes(fields, 2); ok {
		wrapperFields, err := parseWireFields(spawnBytes)
		if err != nil {
			return nil, err
		}
		sourceBytes, ok := fieldBytes(wrapperFields, 1)
		if !ok {
			return nil, ErrInvalidResponse
		}
		sourceFields, err := parseWireFields(sourceBytes)
		if err != nil {
			return nil, err
		}
		parent, err := requiredString(sourceFields, 5, true)
		if err != nil {
			return nil, err
		}
		depth, err := requiredInt64(sourceFields, 4)
		if err != nil || depth < -1<<31 || depth > 1<<31-1 {
			return nil, ErrInvalidResponse
		}
		spawn := map[string]any{"parent_thread_id": parent, "depth": int32(depth)}
		if path, _ := fieldString(sourceFields, 2); path != nil {
			spawn["agent_path"] = *path
		}
		if nickname, _ := fieldString(sourceFields, 1); nickname != nil {
			spawn["agent_nickname"] = *nickname
		}
		if role, _ := fieldString(sourceFields, 3); role != nil {
			spawn["agent_role"] = *role
		}
		return map[string]any{"thread_spawn": spawn}, nil
	}
	if otherBytes, ok := fieldBytes(fields, 3); ok {
		otherFields, err := parseWireFields(otherBytes)
		if err != nil {
			return nil, err
		}
		other, err := requiredString(otherFields, 1, true)
		if err != nil {
			return nil, err
		}
		return map[string]any{"other": other}, nil
	}
	return nil, ErrInvalidResponse
}

func decodeThreadStatus(data []byte) (any, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return nil, err
	}
	if _, ok := fieldBytes(fields, 1); ok {
		return map[string]any{"type": "notLoaded"}, nil
	}
	if _, ok := fieldBytes(fields, 2); ok {
		return map[string]any{"type": "idle"}, nil
	}
	if _, ok := fieldBytes(fields, 3); ok {
		return map[string]any{"type": "systemError"}, nil
	}
	activeBytes, ok := fieldBytes(fields, 4)
	if !ok {
		return nil, ErrInvalidResponse
	}
	activeFields, err := parseWireFields(activeBytes)
	if err != nil {
		return nil, err
	}
	flags := []any{}
	listBytes, ok := fieldBytes(activeFields, 1)
	if !ok {
		return nil, ErrInvalidResponse
	}
	listFields, err := parseWireFields(listBytes)
	if err != nil {
		return nil, err
	}
	for _, field := range listFields {
		if field.number != 1 {
			continue
		}
		if field.typ != protowire.VarintType {
			return nil, ErrInvalidResponse
		}
		switch field.value {
		case 1:
			flags = append(flags, "waitingOnApproval")
		case 2:
			flags = append(flags, "waitingOnUserInput")
		default:
			return nil, fmt.Errorf("%w: active flag", ErrInvalidResponse)
		}
	}
	return map[string]any{"type": "active", "activeFlags": flags}, nil
}

// MarshalJSON preserves the serializable activation contract without exposing
// raw protobuf bytes.
func (a ThreadActivation) MarshalJSON() ([]byte, error) {
	type alias ThreadActivation
	return json.Marshal(alias(a))
}
