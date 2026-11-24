package agentboard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// GuardianEvidenceResponseItem renders the admitted batch as the Responses
// function-call output a retained reviewer thread records: the tool-output name
// and namespace Rust guardian-v3 sampling::context_input assigns, with the
// routed originals as its plain-text body. The item carries no call id because
// no model turn called it; the conversation is retained and only receives
// evidence.
func (i GuardianEvidenceItem) ResponseItem() (json.RawMessage, error) {
	name := strings.TrimSpace(i.Name)
	if name == "" {
		return nil, errors.New("Guardian evidence item requires a name")
	}
	namespace := strings.TrimSpace(i.Namespace)
	if namespace == "" {
		namespace = GuardianEvidenceNamespace
	}
	if strings.TrimSpace(i.Text) == "" {
		return nil, errors.New("Guardian evidence item requires a body")
	}
	item := struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Output    string `json:"output"`
	}{
		Type:      "function_call_output",
		Name:      name,
		Namespace: namespace,
		Output:    i.Text,
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

// GuardianEvidenceInjector records admitted evidence in a retained reviewer
// thread without starting a model turn (Rust
// CodexThread::inject_response_items called from Conversation::deliver). A host
// implements it over its own thread store; the reviewer conversation stays free
// of that dependency.
type GuardianEvidenceInjector interface {
	InjectResponseItems(ctx context.Context, items []json.RawMessage) error
}

// GuardianInboxAdmit adapts an injector to the inbox admission callback. The
// item must be recorded before the callback reports success, because only then
// does the conversation advance its delivery cursor.
func GuardianInboxAdmit(injector GuardianEvidenceInjector) func(context.Context, GuardianEvidenceItem) error {
	return func(ctx context.Context, item GuardianEvidenceItem) error {
		if injector == nil {
			return errors.New("Guardian evidence injector is unavailable")
		}
		rendered, err := item.ResponseItem()
		if err != nil {
			return err
		}
		return injector.InjectResponseItems(ctx, []json.RawMessage{rendered})
	}
}
