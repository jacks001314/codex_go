package bottompane

import (
	"strings"

	"codex_go/internal/sandbox"
	"codex_go/internal/tui"
)

// Rust parity: codex-rs/tui/src/bottom_pane/approval_overlay.rs.

type ApprovalRequestKind string

const (
	ApprovalRequestExec           ApprovalRequestKind = "exec"
	ApprovalRequestPermissions    ApprovalRequestKind = "permissions"
	ApprovalRequestApplyPatch     ApprovalRequestKind = "apply_patch"
	ApprovalRequestMcpElicitation ApprovalRequestKind = "mcp_elicitation"
)

type ApprovalCommandDecisionKind string

const (
	ApprovalCommandAccept                        ApprovalCommandDecisionKind = "accept"
	ApprovalCommandAcceptForSession              ApprovalCommandDecisionKind = "accept_for_session"
	ApprovalCommandAcceptWithExecpolicyAmendment ApprovalCommandDecisionKind = "accept_with_execpolicy_amendment"
	ApprovalCommandApplyNetworkPolicyAmendment   ApprovalCommandDecisionKind = "apply_network_policy_amendment"
	ApprovalCommandDecline                       ApprovalCommandDecisionKind = "decline"
	ApprovalCommandCancel                        ApprovalCommandDecisionKind = "cancel"
)

type ApprovalNetworkPolicyAction string

const (
	ApprovalNetworkPolicyAllow ApprovalNetworkPolicyAction = "allow"
	ApprovalNetworkPolicyDeny  ApprovalNetworkPolicyAction = "deny"
)

type ApprovalPermissionsDecision string

const (
	ApprovalPermissionsGrantForTurn                 ApprovalPermissionsDecision = "grant_for_turn"
	ApprovalPermissionsGrantForTurnStrictAutoReview ApprovalPermissionsDecision = "grant_for_turn_strict_auto_review"
	ApprovalPermissionsGrantForSession              ApprovalPermissionsDecision = "grant_for_session"
	ApprovalPermissionsDeny                         ApprovalPermissionsDecision = "deny"
)

type ApprovalFileChangeDecision string

const (
	ApprovalFileChangeAccept           ApprovalFileChangeDecision = "accept"
	ApprovalFileChangeAcceptForSession ApprovalFileChangeDecision = "accept_for_session"
	ApprovalFileChangeCancel           ApprovalFileChangeDecision = "cancel"
)

type ApprovalMcpElicitationDecision string

const (
	ApprovalMcpElicitationAccept  ApprovalMcpElicitationDecision = "accept"
	ApprovalMcpElicitationDecline ApprovalMcpElicitationDecision = "decline"
	ApprovalMcpElicitationCancel  ApprovalMcpElicitationDecision = "cancel"
)

type ApprovalCommandDecision struct {
	Kind                ApprovalCommandDecisionKind
	ExecpolicyCommand   []string
	NetworkPolicyHost   string
	NetworkPolicyAction ApprovalNetworkPolicyAction
}

type ApprovalNetworkContext struct {
	Host     string
	Protocol string
}

type ApprovalRequest struct {
	Kind                  ApprovalRequestKind
	ThreadID              string
	ThreadLabel           string
	ID                    string
	CallID                string
	EnvironmentID         string
	Command               []string
	Reason                string
	AvailableDecisions    []ApprovalCommandDecision
	NetworkContext        *ApprovalNetworkContext
	AdditionalPermissions *sandbox.RequestPermissionProfile
	Permissions           *sandbox.RequestPermissionProfile
	CWD                   string
	Changes               []string
	ServerName            string
	RequestID             string
	Message               string
}

type ApprovalDecision struct {
	Kind           ApprovalRequestKind
	Command        ApprovalCommandDecision
	Permissions    ApprovalPermissionsDecision
	FileChange     ApprovalFileChangeDecision
	McpElicitation ApprovalMcpElicitationDecision
}

type ApprovalOption struct {
	ID       string
	Label    string
	Shortcut string
	Decision ApprovalDecision
}

type ApprovalEventKind string

const (
	ApprovalEventExecDecision           ApprovalEventKind = "exec_decision"
	ApprovalEventPermissionsDecision    ApprovalEventKind = "permissions_decision"
	ApprovalEventFileChangeDecision     ApprovalEventKind = "file_change_decision"
	ApprovalEventMcpElicitationDecision ApprovalEventKind = "mcp_elicitation_decision"
	ApprovalEventSelectThread           ApprovalEventKind = "select_thread"
)

type ApprovalEvent struct {
	Kind            ApprovalEventKind
	ThreadID        string
	ID              string
	CallID          string
	ServerName      string
	RequestID       string
	CommandDecision ApprovalCommandDecision
	Permissions     ApprovalPermissionsDecision
	FileChange      ApprovalFileChangeDecision
	McpElicitation  ApprovalMcpElicitationDecision
}

type ApprovalOverlay struct {
	Title   string
	Message string
	Options []string

	CurrentRequest *ApprovalRequest
	Queue          []ApprovalRequest
	CurrentOptions []ApprovalOption
	Selected       int

	currentComplete bool
	done            bool
	events          []ApprovalEvent
}

func NewApprovalOverlay(request ApprovalRequest) *ApprovalOverlay {
	overlay := &ApprovalOverlay{}
	overlay.setCurrent(request)
	return overlay
}

func (o *ApprovalOverlay) EnqueueRequest(request ApprovalRequest) {
	if o == nil {
		return
	}
	if o.CurrentRequest == nil || o.done {
		o.setCurrent(request)
		o.done = false
		return
	}
	o.Queue = append(o.Queue, request)
}

func (o *ApprovalOverlay) HandleKey(key string) {
	if o == nil || o.done {
		return
	}
	key = normalizeApprovalKey(key)
	switch key {
	case "up", "k", "left", "ctrl-h", "backtab":
		if o.Selected > 0 {
			o.Selected--
		}
		return
	case "down", "j", "right", "ctrl-l", "tab":
		if o.Selected < len(o.CurrentOptions)-1 {
			o.Selected++
		}
		return
	case "o":
		if o.CurrentRequest != nil && o.CurrentRequest.ThreadLabel != "" {
			o.events = append(o.events, ApprovalEvent{Kind: ApprovalEventSelectThread, ThreadID: o.CurrentRequest.ThreadID})
		}
		return
	case "enter":
		o.ApplySelection(o.Selected)
		return
	case "esc", "escape", "ctrl-c":
		o.cancelCurrentRequest()
		return
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		index := int(key[0] - '1')
		if index >= 0 && index < len(o.CurrentOptions) {
			o.Selected = index
			o.ApplySelection(index)
		}
		return
	}
	for idx, option := range o.CurrentOptions {
		if option.Shortcut != "" && normalizeApprovalKey(option.Shortcut) == key {
			o.Selected = idx
			o.ApplySelection(idx)
			return
		}
	}
}

func (o *ApprovalOverlay) ApplySelection(index int) {
	if o == nil || o.currentComplete || o.CurrentRequest == nil || index < 0 || index >= len(o.CurrentOptions) {
		return
	}
	option := o.CurrentOptions[index]
	request := o.CurrentRequest
	switch request.Kind {
	case ApprovalRequestExec:
		o.events = append(o.events, ApprovalEvent{
			Kind:            ApprovalEventExecDecision,
			ThreadID:        request.ThreadID,
			ID:              request.ID,
			CommandDecision: option.Decision.Command,
		})
	case ApprovalRequestPermissions:
		o.events = append(o.events, ApprovalEvent{
			Kind:        ApprovalEventPermissionsDecision,
			ThreadID:    request.ThreadID,
			CallID:      request.CallID,
			Permissions: option.Decision.Permissions,
		})
	case ApprovalRequestApplyPatch:
		o.events = append(o.events, ApprovalEvent{
			Kind:       ApprovalEventFileChangeDecision,
			ThreadID:   request.ThreadID,
			ID:         request.ID,
			FileChange: option.Decision.FileChange,
		})
	case ApprovalRequestMcpElicitation:
		o.events = append(o.events, ApprovalEvent{
			Kind:           ApprovalEventMcpElicitationDecision,
			ThreadID:       request.ThreadID,
			ServerName:     request.ServerName,
			RequestID:      request.RequestID,
			McpElicitation: option.Decision.McpElicitation,
		})
	}
	o.currentComplete = true
	o.advanceQueue()
}

func (o *ApprovalOverlay) IsComplete() bool {
	return o != nil && o.done
}

func (o *ApprovalOverlay) Events() []ApprovalEvent {
	if o == nil {
		return nil
	}
	return append([]ApprovalEvent(nil), o.events...)
}

func (o *ApprovalOverlay) Rows(width int) []string {
	if o == nil || o.CurrentRequest == nil {
		return nil
	}
	rows := []string{ApprovalTitleForRequest(*o.CurrentRequest)}
	rows = append(rows, "")
	rows = append(rows, BuildApprovalHeaderRows(*o.CurrentRequest, width)...)
	rows = trimTrailingBlankRows(rows)
	rows = append(rows, "")
	for idx, option := range o.CurrentOptions {
		selected := idx == o.Selected
		row := tui.NumberedSelectionPrefix(idx, selected) + option.Label
		if option.Shortcut != "" {
			row += " (" + option.Shortcut + ")"
		}
		if selected {
			row = tui.RenderSelectedRow(row)
		}
		rows = append(rows, row)
	}
	rows = append(rows, "", "Press enter to confirm or esc to cancel")
	return rows
}

func (o *ApprovalOverlay) DismissResolvedRequest(kind ApprovalRequestKind, id string, serverName string, requestID string) bool {
	if o == nil || o.CurrentRequest == nil {
		return false
	}
	if !ApprovalRequestMatchesResolved(*o.CurrentRequest, kind, id, serverName, requestID) {
		return false
	}
	o.currentComplete = true
	o.advanceQueue()
	return true
}

func (o *ApprovalOverlay) setCurrent(request ApprovalRequest) {
	o.CurrentRequest = cloneApprovalRequest(request)
	o.CurrentOptions = ApprovalOptionsForRequest(request)
	o.Options = make([]string, len(o.CurrentOptions))
	for i, option := range o.CurrentOptions {
		o.Options[i] = option.Label
	}
	o.Title = ApprovalTitleForRequest(request)
	o.Message = strings.Join(BuildApprovalHeaderRows(request, 80), "\n")
	o.Selected = 0
	o.currentComplete = false
}

func (o *ApprovalOverlay) advanceQueue() {
	if len(o.Queue) == 0 {
		o.CurrentRequest = nil
		o.CurrentOptions = nil
		o.Options = nil
		o.done = true
		return
	}
	next := o.Queue[0]
	o.Queue = o.Queue[1:]
	o.setCurrent(next)
}

func (o *ApprovalOverlay) cancelCurrentRequest() {
	if o == nil || o.done {
		return
	}
	if o.CurrentRequest != nil && !o.currentComplete {
		switch o.CurrentRequest.Kind {
		case ApprovalRequestExec:
			o.events = append(o.events, ApprovalEvent{
				Kind:            ApprovalEventExecDecision,
				ThreadID:        o.CurrentRequest.ThreadID,
				ID:              o.CurrentRequest.ID,
				CommandDecision: ApprovalCommandDecision{Kind: ApprovalCommandCancel},
			})
		case ApprovalRequestPermissions:
			o.events = append(o.events, ApprovalEvent{
				Kind:        ApprovalEventPermissionsDecision,
				ThreadID:    o.CurrentRequest.ThreadID,
				CallID:      o.CurrentRequest.CallID,
				Permissions: ApprovalPermissionsDeny,
			})
		case ApprovalRequestApplyPatch:
			o.events = append(o.events, ApprovalEvent{
				Kind:       ApprovalEventFileChangeDecision,
				ThreadID:   o.CurrentRequest.ThreadID,
				ID:         o.CurrentRequest.ID,
				FileChange: ApprovalFileChangeCancel,
			})
		case ApprovalRequestMcpElicitation:
			o.events = append(o.events, ApprovalEvent{
				Kind:           ApprovalEventMcpElicitationDecision,
				ThreadID:       o.CurrentRequest.ThreadID,
				ServerName:     o.CurrentRequest.ServerName,
				RequestID:      o.CurrentRequest.RequestID,
				McpElicitation: ApprovalMcpElicitationCancel,
			})
		}
	}
	o.Queue = nil
	o.done = true
}

func ApprovalRequestMatchesResolved(request ApprovalRequest, kind ApprovalRequestKind, id string, serverName string, requestID string) bool {
	if request.Kind != kind {
		return false
	}
	switch request.Kind {
	case ApprovalRequestExec, ApprovalRequestApplyPatch:
		return request.ID == id
	case ApprovalRequestPermissions:
		return request.CallID == id
	case ApprovalRequestMcpElicitation:
		return request.ServerName == serverName && request.RequestID == requestID
	default:
		return false
	}
}

func ApprovalOptionsForRequest(request ApprovalRequest) []ApprovalOption {
	switch request.Kind {
	case ApprovalRequestExec:
		return ExecApprovalOptions(request.AvailableDecisions, request.NetworkContext, request.AdditionalPermissions)
	case ApprovalRequestPermissions:
		return PermissionsApprovalOptions()
	case ApprovalRequestApplyPatch:
		return PatchApprovalOptions()
	case ApprovalRequestMcpElicitation:
		return McpElicitationApprovalOptions()
	default:
		return nil
	}
}

func ApprovalTitleForRequest(request ApprovalRequest) string {
	switch request.Kind {
	case ApprovalRequestExec:
		if request.NetworkContext != nil {
			return `Do you want to approve network access to "` + request.NetworkContext.Host + `"?`
		}
		return "Would you like to run the following command?"
	case ApprovalRequestPermissions:
		return "Would you like to grant these permissions?"
	case ApprovalRequestApplyPatch:
		return "Would you like to make the following edits?"
	case ApprovalRequestMcpElicitation:
		return request.ServerName + " needs your approval."
	default:
		return "Approval request"
	}
}

func ExecApprovalOptions(available []ApprovalCommandDecision, network *ApprovalNetworkContext, additional *sandbox.RequestPermissionProfile) []ApprovalOption {
	options := []ApprovalOption{}
	for _, decision := range available {
		switch decision.Kind {
		case ApprovalCommandAccept:
			label := "Yes, proceed"
			if network != nil {
				label = "Yes, just this once"
			}
			options = append(options, ApprovalOption{ID: "accept", Label: label, Shortcut: "y", Decision: ApprovalDecision{Kind: ApprovalRequestExec, Command: decision}})
		case ApprovalCommandAcceptWithExecpolicyAmendment:
			prefix := StripBashLCAndEscape(decision.ExecpolicyCommand)
			if prefix == "" || strings.ContainsAny(prefix, "\r\n") || network != nil || additional != nil {
				continue
			}
			label := "Yes, and don't ask again for commands that start with `" + prefix + "`"
			options = append(options, ApprovalOption{ID: "accept_prefix", Label: label, Shortcut: "p", Decision: ApprovalDecision{Kind: ApprovalRequestExec, Command: decision}})
		case ApprovalCommandAcceptForSession:
			label := "Yes, and don't ask again for this command in this session"
			if network != nil {
				label = "Yes, and allow this host for this conversation"
			} else if additional != nil {
				label = "Yes, and allow these permissions for this session"
			}
			options = append(options, ApprovalOption{ID: "accept_session", Label: label, Shortcut: "a", Decision: ApprovalDecision{Kind: ApprovalRequestExec, Command: decision}})
		case ApprovalCommandApplyNetworkPolicyAmendment:
			label := "Yes, and allow this host in the future"
			shortcut := "p"
			if decision.NetworkPolicyAction == ApprovalNetworkPolicyDeny {
				label = "No, and block this host in the future"
				shortcut = "d"
			}
			options = append(options, ApprovalOption{ID: "network_policy", Label: label, Shortcut: shortcut, Decision: ApprovalDecision{Kind: ApprovalRequestExec, Command: decision}})
		case ApprovalCommandDecline:
			options = append(options, ApprovalOption{ID: "decline", Label: "No, continue without running it", Shortcut: "d", Decision: ApprovalDecision{Kind: ApprovalRequestExec, Command: decision}})
		case ApprovalCommandCancel:
			options = append(options, ApprovalOption{ID: "cancel", Label: "No, and tell Codex what to do differently", Shortcut: "esc", Decision: ApprovalDecision{Kind: ApprovalRequestExec, Command: decision}})
		}
	}
	return options
}

func PermissionsApprovalOptions() []ApprovalOption {
	return []ApprovalOption{
		{ID: "grant_turn", Label: "Yes, grant these permissions for this turn", Shortcut: "y", Decision: ApprovalDecision{Kind: ApprovalRequestPermissions, Permissions: ApprovalPermissionsGrantForTurn}},
		{ID: "grant_turn_strict", Label: "Yes, grant for this turn with strict auto review", Shortcut: "r", Decision: ApprovalDecision{Kind: ApprovalRequestPermissions, Permissions: ApprovalPermissionsGrantForTurnStrictAutoReview}},
		{ID: "grant_session", Label: "Yes, grant these permissions for this session", Shortcut: "a", Decision: ApprovalDecision{Kind: ApprovalRequestPermissions, Permissions: ApprovalPermissionsGrantForSession}},
		{ID: "deny", Label: "No, continue without permissions", Shortcut: "d", Decision: ApprovalDecision{Kind: ApprovalRequestPermissions, Permissions: ApprovalPermissionsDeny}},
	}
}

func PatchApprovalOptions() []ApprovalOption {
	return []ApprovalOption{
		{ID: "accept", Label: "Yes, proceed", Shortcut: "y", Decision: ApprovalDecision{Kind: ApprovalRequestApplyPatch, FileChange: ApprovalFileChangeAccept}},
		{ID: "accept_session", Label: "Yes, and don't ask again for these files", Shortcut: "a", Decision: ApprovalDecision{Kind: ApprovalRequestApplyPatch, FileChange: ApprovalFileChangeAcceptForSession}},
		{ID: "cancel", Label: "No, and tell Codex what to do differently", Shortcut: "esc", Decision: ApprovalDecision{Kind: ApprovalRequestApplyPatch, FileChange: ApprovalFileChangeCancel}},
	}
}

func McpElicitationApprovalOptions() []ApprovalOption {
	return []ApprovalOption{
		{ID: "accept", Label: "Yes, provide the requested info", Shortcut: "y", Decision: ApprovalDecision{Kind: ApprovalRequestMcpElicitation, McpElicitation: ApprovalMcpElicitationAccept}},
		{ID: "decline", Label: "No, but continue without it", Shortcut: "n", Decision: ApprovalDecision{Kind: ApprovalRequestMcpElicitation, McpElicitation: ApprovalMcpElicitationDecline}},
		{ID: "cancel", Label: "Cancel this request", Shortcut: "esc", Decision: ApprovalDecision{Kind: ApprovalRequestMcpElicitation, McpElicitation: ApprovalMcpElicitationCancel}},
	}
}

func BuildApprovalHeaderRows(request ApprovalRequest, width int) []string {
	rows := []string{}
	appendCommon := func() {
		if request.ThreadLabel != "" {
			rows = append(rows, "Thread: "+request.ThreadLabel, "")
		}
		if request.EnvironmentID != "" {
			rows = append(rows, "Environment: "+request.EnvironmentID, "")
		}
		if request.Reason != "" {
			rows = append(rows, "Reason: "+request.Reason, "")
		}
	}
	appendCommon()
	switch request.Kind {
	case ApprovalRequestExec:
		if rule := FormatApprovalPermissionsRule(request.AdditionalPermissions); rule != "" {
			rows = append(rows, "Permission rule: "+rule, "")
		}
		if request.NetworkContext == nil {
			command := StripBashLCAndEscape(request.Command)
			if command != "" {
				rows = appendWrappedApprovalRows(rows, "$ "+command, width)
			}
		}
	case ApprovalRequestPermissions:
		if rule := FormatApprovalPermissionsRule(request.Permissions); rule != "" {
			rows = append(rows, "Permission rule: "+rule)
		}
	case ApprovalRequestApplyPatch:
		for _, change := range request.Changes {
			if strings.TrimSpace(change) != "" {
				rows = append(rows, change)
			}
		}
	case ApprovalRequestMcpElicitation:
		rows = append(rows, "Server: "+request.ServerName, "")
		if request.Message != "" {
			rows = appendWrappedApprovalRows(rows, request.Message, width)
		}
	}
	return trimTrailingBlankRows(rows)
}

func FormatApprovalPermissionsRule(permissions *sandbox.RequestPermissionProfile) string {
	if permissions == nil {
		return ""
	}
	parts := []string{}
	if permissions.Network != nil && permissions.Network.Enabled != nil && *permissions.Network.Enabled {
		parts = append(parts, "network")
	}
	if permissions.FileSystem != nil {
		reads := approvalFileSystemEntryPaths(permissions.FileSystem.Read, permissions.FileSystem.Entries, sandbox.FileSystemAccessRead)
		if reads != "" {
			parts = append(parts, "read "+reads)
		}
		writes := approvalFileSystemEntryPaths(permissions.FileSystem.Write, permissions.FileSystem.Entries, sandbox.FileSystemAccessWrite)
		if writes != "" {
			parts = append(parts, "write "+writes)
		}
		denies := approvalFileSystemEntryPaths(nil, permissions.FileSystem.Entries, sandbox.FileSystemAccessDeny)
		if denies != "" {
			parts = append(parts, "deny read "+denies)
		}
	}
	return strings.Join(parts, "; ")
}

func StripBashLCAndEscape(command []string) string {
	if len(command) == 0 {
		return ""
	}
	if len(command) == 3 && command[0] == "bash" && command[1] == "-lc" {
		return command[2]
	}
	if len(command) == 2 && command[0] == "sh" && command[1] != "" {
		return command[1]
	}
	return strings.Join(command, " ")
}

func approvalFileSystemEntryPaths(paths []string, entries []sandbox.FileSystemSandboxEntry, access sandbox.FileSystemAccessMode) string {
	formatted := []string{}
	for _, path := range paths {
		if strings.TrimSpace(path) != "" {
			formatted = append(formatted, "`"+path+"`")
		}
	}
	for _, entry := range entries {
		if entry.Access == access {
			formatted = append(formatted, approvalFileSystemPathLabel(entry.Path))
		}
	}
	return strings.Join(formatted, ", ")
}

func approvalFileSystemPathLabel(path sandbox.FileSystemPath) string {
	switch path.Type {
	case "glob_pattern":
		return "glob `" + path.Pattern + "`"
	case "special":
		if path.Value != nil {
			return "`" + approvalSpecialPathLabel(*path.Value) + "`"
		}
		return "``"
	default:
		return "`" + path.Path + "`"
	}
}

func approvalSpecialPathLabel(value sandbox.FileSystemSpecialPath) string {
	switch value.Kind {
	case "root":
		return ":root"
	case "minimal":
		return ":minimal"
	case "project_roots":
		return approvalPathLabel(":workspace_roots", value.Subpath)
	case "tmpdir":
		return ":tmpdir"
	case "slash_tmp":
		return "/tmp"
	case "unknown":
		return approvalPathLabel(value.Path, value.Subpath)
	default:
		return value.Kind
	}
}

func approvalPathLabel(base string, subpath *string) string {
	if subpath == nil || strings.TrimSpace(*subpath) == "" {
		return base
	}
	return strings.TrimRight(base, "/\\") + "/" + strings.TrimLeft(*subpath, "/\\")
}

func appendWrappedApprovalRows(rows []string, text string, width int) []string {
	if width <= 0 {
		width = 80
	}
	return append(rows, tui.AdaptiveWrapLine(text, tui.WrapOptions{Width: width, BreakWords: true})...)
}

func normalizeApprovalKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "+", "-")
	return key
}

func cloneApprovalRequest(request ApprovalRequest) *ApprovalRequest {
	copyRequest := request
	copyRequest.Command = append([]string(nil), request.Command...)
	copyRequest.AvailableDecisions = append([]ApprovalCommandDecision(nil), request.AvailableDecisions...)
	copyRequest.Changes = append([]string(nil), request.Changes...)
	if request.NetworkContext != nil {
		network := *request.NetworkContext
		copyRequest.NetworkContext = &network
	}
	return &copyRequest
}
