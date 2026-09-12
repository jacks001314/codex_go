package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	bottompane "codex_go/tui/bottom_pane"
)

func newAgentsOverviewAttachmentModel(t *testing.T, dispatch func(SubmitRequest, string) (string, error)) (*Model, *[]SubmitRequest) {
	t.Helper()
	requests := &[]SubmitRequest{}
	model := NewModel(codextui.NewState(nil), Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return []agentsoverview.Row{{ThreadID: "root-1", Name: "task"}}, nil
		},
		OnAgentsOverviewDispatch: func(request SubmitRequest, cwd string) (string, error) {
			*requests = append(*requests, cloneSubmitRequest(request))
			if dispatch != nil {
				return dispatch(request, cwd)
			}
			return "new-1", nil
		},
	})
	model.applyAgentsCommand()
	model.Update(agentsOverviewListMsg{rows: []agentsoverview.Row{{ThreadID: "root-1", Name: "task"}}})
	return model, requests
}

// TestAgentsOverviewDispatchCarriesAttachments covers Rust #44027: the
// dashboard's background-task request carries the pending image attachments.
func TestAgentsOverviewDispatchCarriesAttachments(t *testing.T) {
	model, requests := newAgentsOverviewAttachmentModel(t, nil)
	model.setAgentsOverviewAttachments([]bottompane.ComposerAttachment{
		{Kind: bottompane.AttachmentImage, Path: `D:\tmp\chart.png`},
		{Kind: bottompane.AttachmentRemoteImage, URL: "https://example.com/a.png"},
	})
	if labels := model.agentsOverview.AttachmentLabels(); len(labels) != 2 {
		t.Fatalf("rendered attachment labels = %#v", labels)
	}

	typeText(t, model, "inspect the chart")
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("enter with input returned no dispatch command")
	}
	updated, _ = model.Update(command())
	model = updated.(*Model)

	if len(*requests) != 1 {
		t.Fatalf("dispatch requests = %#v", *requests)
	}
	request := (*requests)[0]
	if request.Prompt != "inspect the chart" || len(request.Attachments) != 2 {
		t.Fatalf("dispatch request = %#v", request)
	}
	if request.Attachments[0].Kind != bottompane.AttachmentImage || request.Attachments[1].Kind != bottompane.AttachmentRemoteImage {
		t.Fatalf("attachment kinds = %#v", request.Attachments)
	}
	// A successful dispatch consumes the attachments.
	if labels := model.agentsOverview.AttachmentLabels(); len(labels) != 0 {
		t.Fatalf("attachments after success = %#v", labels)
	}
	if len(model.agentsOverviewAttachments) != 0 {
		t.Fatalf("model attachments after success = %#v", model.agentsOverviewAttachments)
	}
}

// TestAgentsOverviewDispatchFailureRestoresPromptAndAttachments covers Rust
// #44027: a failed dispatch restores the unsent prompt with its images, or
// reports the reattachment paths when a newer draft exists.
func TestAgentsOverviewDispatchFailureRestoresPromptAndAttachments(t *testing.T) {
	model, _ := newAgentsOverviewAttachmentModel(t, func(SubmitRequest, string) (string, error) {
		return "", errTestDispatch
	})
	model.setAgentsOverviewAttachments([]bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: `D:\tmp\chart.png`}})
	typeText(t, model, "inspect the chart")
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	updated, _ = model.Update(command())
	model = updated.(*Model)

	if got := model.agentsOverview.State.Input; got != "inspect the chart" {
		t.Fatalf("restored input = %q", got)
	}
	if len(model.agentsOverviewAttachments) != 1 || model.agentsOverviewAttachments[0].Path != `D:\tmp\chart.png` {
		t.Fatalf("restored attachments = %#v", model.agentsOverviewAttachments)
	}

	// A newer draft typed while the task was starting is preserved, and the
	// failed request's image is reported for reattachment.
	model2, _ := newAgentsOverviewAttachmentModel(t, func(SubmitRequest, string) (string, error) {
		return "", errTestDispatch
	})
	model2.setAgentsOverviewAttachments([]bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: `D:\tmp\pending.png`}})
	typeText(t, model2, "first draft")
	updated, command = model2.Update(key(bubbletea.KeyEnter))
	model2 = updated.(*Model)
	// The dispatch command runs later; the user types a newer draft meanwhile.
	typeText(t, model2, "second draft")
	updated, _ = model2.Update(command())
	model2 = updated.(*Model)
	if got := model2.agentsOverview.State.Input; got != "second draft" {
		t.Fatalf("newer draft = %q, want it preserved", got)
	}
	if notice := model2.agentsOverviewNotice; !strings.Contains(notice, "Reattach image(s): D:\\tmp\\pending.png") {
		t.Fatalf("notice = %q", notice)
	}
}

var errTestDispatch = &testDispatchError{}

type testDispatchError struct{}

func (e *testDispatchError) Error() string { return "dispatch failed" }
