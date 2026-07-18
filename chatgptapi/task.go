package chatgptapi

type GetTaskResponse struct {
	CurrentUserTurn      *CodeTaskTurn `json:"current_user_turn,omitempty"`
	CurrentAssistantTurn *CodeTaskTurn `json:"current_assistant_turn,omitempty"`
	CurrentDiffTaskTurn  *CodeTaskTurn `json:"current_diff_task_turn,omitempty"`
}

func (r *GetTaskResponse) Details() *CodeTaskDetailsResponse {
	if r == nil {
		return &CodeTaskDetailsResponse{}
	}
	return &CodeTaskDetailsResponse{
		CurrentUserTurn:      r.CurrentUserTurn,
		CurrentAssistantTurn: r.CurrentAssistantTurn,
		CurrentDiffTaskTurn:  r.CurrentDiffTaskTurn,
	}
}

func (r *GetTaskResponse) CurrentPRDiff() string {
	diff, _ := r.UnifiedDiff()
	return diff
}

func (r *GetTaskResponse) UnifiedDiff() (string, bool) {
	if r == nil {
		return "", false
	}
	return r.Details().UnifiedDiff()
}

func (r *GetTaskResponse) AssistantTextMessages() []string {
	if r == nil {
		return nil
	}
	return r.Details().AssistantTextMessages()
}

func (r *GetTaskResponse) UserTextPrompt() (string, bool) {
	if r == nil {
		return "", false
	}
	return r.Details().UserTextPrompt()
}

func (r *GetTaskResponse) AssistantErrorMessage() (string, bool) {
	if r == nil {
		return "", false
	}
	return r.Details().AssistantErrorMessage()
}
