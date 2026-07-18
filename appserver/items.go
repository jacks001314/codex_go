package appserver

import "encoding/json"

type CommandExecutionStatus string

const (
	CommandExecutionInProgress CommandExecutionStatus = "inProgress"
	CommandExecutionCompleted  CommandExecutionStatus = "completed"
	CommandExecutionFailed     CommandExecutionStatus = "failed"
	CommandExecutionDeclined   CommandExecutionStatus = "declined"
)

type CommandExecutionSource string

const (
	CommandExecutionSourceAgent                  CommandExecutionSource = "agent"
	CommandExecutionSourceUserShell              CommandExecutionSource = "userShell"
	CommandExecutionSourceUnifiedExecStartup     CommandExecutionSource = "unifiedExecStartup"
	CommandExecutionSourceUnifiedExecInteraction CommandExecutionSource = "unifiedExecInteraction"
)

type PatchApplyStatus string

const (
	PatchApplyInProgress PatchApplyStatus = "inProgress"
	PatchApplyCompleted  PatchApplyStatus = "completed"
	PatchApplyFailed     PatchApplyStatus = "failed"
	PatchApplyDeclined   PatchApplyStatus = "declined"
)

type PatchChangeKind struct {
	Type     string  `json:"type"`
	MovePath *string `json:"move_path,omitempty"`
}

type FileUpdateChange struct {
	Path string          `json:"path"`
	Kind PatchChangeKind `json:"kind"`
	Diff string          `json:"diff"`
}

type CommandAction struct {
	Type    string  `json:"type"`
	Command string  `json:"command"`
	Name    string  `json:"name,omitempty"`
	Path    *string `json:"path,omitempty"`
	Query   *string `json:"query,omitempty"`
}

func (a CommandAction) MarshalJSON() ([]byte, error) {
	switch a.Type {
	case "read":
		if a.Path == nil {
			return marshalUnknownCommandAction(a.Command)
		}
		return json.Marshal(struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Name    string `json:"name"`
			Path    string `json:"path"`
		}{
			Type:    "read",
			Command: a.Command,
			Name:    a.Name,
			Path:    *a.Path,
		})
	case "listFiles":
		return json.Marshal(struct {
			Type    string  `json:"type"`
			Command string  `json:"command"`
			Path    *string `json:"path"`
		}{
			Type:    "listFiles",
			Command: a.Command,
			Path:    cloneStringPtrAppserver(a.Path),
		})
	case "search":
		return json.Marshal(struct {
			Type    string  `json:"type"`
			Command string  `json:"command"`
			Query   *string `json:"query"`
			Path    *string `json:"path"`
		}{
			Type:    "search",
			Command: a.Command,
			Query:   cloneStringPtrAppserver(a.Query),
			Path:    cloneStringPtrAppserver(a.Path),
		})
	default:
		return marshalUnknownCommandAction(a.Command)
	}
}

func marshalUnknownCommandAction(command string) ([]byte, error) {
	return json.Marshal(struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}{
		Type:    "unknown",
		Command: command,
	})
}
