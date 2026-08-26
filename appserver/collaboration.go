package appserver

import "encoding/json"

type CollaborationModeListParams struct{}

type CollabAgentStatus string

const (
	CollabAgentStatusPendingInit CollabAgentStatus = "pendingInit"
	CollabAgentStatusRunning     CollabAgentStatus = "running"
	CollabAgentStatusInterrupted CollabAgentStatus = "interrupted"
	CollabAgentStatusCompleted   CollabAgentStatus = "completed"
	CollabAgentStatusErrored     CollabAgentStatus = "errored"
	CollabAgentStatusShutdown    CollabAgentStatus = "shutdown"
	CollabAgentStatusNotFound    CollabAgentStatus = "notFound"
)

type CollabAgentState struct {
	Status  CollabAgentStatus `json:"status"`
	Message *string           `json:"message"`
}

type CollabAgentTool string

const (
	CollabAgentToolSpawnAgent  CollabAgentTool = "spawnAgent"
	CollabAgentToolSendInput   CollabAgentTool = "sendInput"
	CollabAgentToolResumeAgent CollabAgentTool = "resumeAgent"
	CollabAgentToolWait        CollabAgentTool = "wait"
	CollabAgentToolCloseAgent  CollabAgentTool = "closeAgent"
	CollabAgentToolSendMessage CollabAgentTool = "sendMessage"
	CollabAgentToolFollowup    CollabAgentTool = "followupTask"
	CollabAgentToolInterrupt   CollabAgentTool = "interruptAgent"
	CollabAgentToolListAgents  CollabAgentTool = "listAgents"
)

type CollabAgentToolCallStatus string

const (
	CollabAgentToolCallInProgress CollabAgentToolCallStatus = "inProgress"
	CollabAgentToolCallCompleted  CollabAgentToolCallStatus = "completed"
	CollabAgentToolCallFailed     CollabAgentToolCallStatus = "failed"
)

type CollaborationModeMask struct {
	Name            string  `json:"name"`
	Mode            string  `json:"mode"`
	Model           *string `json:"model"`
	ReasoningEffort *string `json:"reasoning_effort"`
}

func (m *CollaborationModeMask) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name            string  `json:"name"`
		Mode            *string `json:"mode"`
		Model           *string `json:"model"`
		ReasoningEffort *string `json:"reasoning_effort"`
	}{
		Name:            m.Name,
		Mode:            stringPtrIfNotEmpty(m.Mode),
		Model:           stringPtrIfNotEmpty(ptrStringValue(m.Model)),
		ReasoningEffort: stringPtrIfNotEmpty(ptrStringValue(m.ReasoningEffort)),
	})
}

type CollaborationModeListResponse struct {
	Data []CollaborationModeMask `json:"data"`
}

func (r *CollaborationModeListResponse) MarshalJSON() ([]byte, error) {
	data := cloneCollaborationModes(r.Data)
	if data == nil {
		data = []CollaborationModeMask{}
	}
	return json.Marshal(struct {
		Data []CollaborationModeMask `json:"data"`
	}{Data: data})
}

type CollaborationModeService struct {
	items []CollaborationModeMask
}

func NewCollaborationModeService(items []CollaborationModeMask) *CollaborationModeService {
	if len(items) == 0 {
		items = defaultCollaborationModes()
	}
	return &CollaborationModeService{items: cloneCollaborationModes(items)}
}

func (s *CollaborationModeService) List(params *CollaborationModeListParams) *CollaborationModeListResponse {
	_ = params
	if s == nil {
		return &CollaborationModeListResponse{Data: cloneCollaborationModes(defaultCollaborationModes())}
	}
	return &CollaborationModeListResponse{Data: cloneCollaborationModes(s.items)}
}

func defaultCollaborationModes() []CollaborationModeMask {
	medium := "medium"
	return []CollaborationModeMask{
		{Name: "Plan", Mode: "plan", ReasoningEffort: &medium},
		{Name: "Default", Mode: "default"},
	}
}

func cloneCollaborationModes(items []CollaborationModeMask) []CollaborationModeMask {
	out := make([]CollaborationModeMask, len(items))
	for i := range items {
		out[i] = CollaborationModeMask{
			Name:            items[i].Name,
			Mode:            items[i].Mode,
			Model:           stringPtrIfNotEmpty(ptrStringValue(items[i].Model)),
			ReasoningEffort: stringPtrIfNotEmpty(ptrStringValue(items[i].ReasoningEffort)),
		}
	}
	return out
}

func ptrStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type MockExperimentalMethodParams struct {
	Value any `json:"value"`
}

type MockExperimentalMethodResponse struct {
	Echoed any `json:"echoed"`
}
