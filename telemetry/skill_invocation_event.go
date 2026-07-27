package telemetry

import "context"

const SkillInvocationEventType = "skill_invocation"

const (
	SkillInvocationTypeExplicit = "explicit"
	SkillInvocationTypeImplicit = "implicit"
)

type SkillInvocationEventSink interface {
	TrackSkillInvocationEvent(context.Context, SkillInvocationEventRequest)
}

type SkillInvocationEventRequest struct {
	EventType   string                     `json:"event_type"`
	SkillID     string                     `json:"skill_id"`
	SkillName   string                     `json:"skill_name"`
	EventParams SkillInvocationEventParams `json:"event_params"`
}

type SkillInvocationEventParams struct {
	ProductClientID *string `json:"product_client_id"`
	SkillScope      *string `json:"skill_scope"`
	PluginID        *string `json:"plugin_id"`
	RemotePluginID  *string `json:"remote_plugin_id"`
	RepoURL         *string `json:"repo_url"`
	ThreadID        *string `json:"thread_id"`
	TurnID          *string `json:"turn_id"`
	InvokeType      *string `json:"invoke_type"`
	ModelSlug       *string `json:"model_slug"`
}
