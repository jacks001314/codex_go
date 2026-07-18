package chatcomposer

import (
	"reflect"
	"testing"
)

func TestDraftStateInputPastesAndMentionsMatchRustCore(t *testing.T) {
	draft := NewDraftState()
	draft.Insert("hello")
	draft.Cursor = 1
	draft.Insert("i")
	if draft.Text != "hiello" || draft.Cursor != 2 {
		t.Fatalf("insert text=%q cursor=%d", draft.Text, draft.Cursor)
	}
	draft.SetInputEnabled(false, "Working")
	draft.Insert(" blocked")
	if draft.Text != "hiello" || draft.InputDisabledPlaceholder != "Working" {
		t.Fatalf("disabled insert changed draft: %#v", draft)
	}
	draft.SetInputEnabled(true, "")
	draft.AddPendingPaste("[paste-1]", "large")
	draft.AddMentionBinding(7, ComposerMentionBinding{Sigil: '@', Mention: "skill", Path: "plugin://skill"})
	draft.RecentSubmissionMentionBindings = []MentionBinding{{Sigil: '$', Mention: "figma", Path: "app://figma"}}
	if got := draft.TakePendingPastes(); !reflect.DeepEqual(got, []PendingPaste{{Placeholder: "[paste-1]", Content: "large"}}) {
		t.Fatalf("pending pastes = %#v", got)
	}
	if got := draft.TakeRecentSubmissionMentionBindings(); !reflect.DeepEqual(got, []MentionBinding{{Sigil: '$', Mention: "figma", Path: "app://figma"}}) {
		t.Fatalf("recent mention bindings = %#v", got)
	}
	draft.Clear()
	if !draft.IsEmpty() || draft.Cursor != 0 {
		t.Fatalf("clear failed: %#v", draft)
	}
}

func TestAttachmentStateLocalRemoteRelabelAndTakeMatchRustCore(t *testing.T) {
	draft := NewDraftState()
	var state AttachmentState
	state.SetRemoteImageURLs([]string{"https://example.test/1.png"}, draft)
	image := state.AttachImage(draft, "local-a.png")
	if image.Placeholder != "[Image #2]" || draft.Text != "[Image #2]" {
		t.Fatalf("attached image=%#v draft=%q", image, draft.Text)
	}
	state.SetRemoteImageURLs(nil, draft)
	if state.LocalImages[0].Placeholder != "[Image #1]" || draft.Text != "[Image #1]" {
		t.Fatalf("relabel after clearing remote = %#v draft=%q", state.LocalImages, draft.Text)
	}
	state.ResetLocalImages([]string{"a.png", "b.png"}, draft)
	if got := state.LocalImagePaths(); !reflect.DeepEqual(got, []string{"a.png", "b.png"}) {
		t.Fatalf("local paths = %#v", got)
	}
	if got := state.LocalImageAttachments(); !reflect.DeepEqual(got, []LocalImageAttachment{{Placeholder: "[Image #1]", Path: "a.png"}, {Placeholder: "[Image #2]", Path: "b.png"}}) {
		t.Fatalf("local attachments = %#v", got)
	}
	state.PruneLocalImagesForSubmission([]string{"[Image #2]"})
	if got := state.LocalImagePaths(); !reflect.DeepEqual(got, []string{"b.png"}) {
		t.Fatalf("pruned local paths = %#v", got)
	}
	taken := state.TakeRecentSubmissionImagesWithPlaceholders()
	if len(taken) != 1 || taken[0].Path != "b.png" || !state.IsEmpty() {
		t.Fatalf("taken=%#v state=%#v", taken, state)
	}
}

func TestAttachmentStateRemoteSelectionDeleteAndLinesMatchRustCore(t *testing.T) {
	draft := NewDraftState()
	var state AttachmentState
	state.SetRemoteImageURLs([]string{"one", "two"}, draft)
	if !state.HandleRemoteImageSelectionKey("up", draft) || state.SelectedRemoteImageIndex == nil || *state.SelectedRemoteImageIndex != 1 {
		t.Fatalf("up from cursor start should select last remote: %#v", state.SelectedRemoteImageIndex)
	}
	if got := state.RemoteImageLines(); !reflect.DeepEqual(got, []string{"  [Image #1]", "> [Image #2]"}) {
		t.Fatalf("remote lines = %#v", got)
	}
	if !state.HandleRemoteImageSelectionKey("delete", draft) || len(state.RemoteImageURLs) != 1 || state.RemoteImageURLs[0] != "one" {
		t.Fatalf("delete selected remote failed: %#v", state)
	}
	if state.SelectedRemoteImageIndex == nil || *state.SelectedRemoteImageIndex != 0 {
		t.Fatalf("selected index after delete = %#v", state.SelectedRemoteImageIndex)
	}
	if urls := state.TakeRemoteImageURLs(draft); !reflect.DeepEqual(urls, []string{"one"}) || len(state.RemoteImageURLs) != 0 {
		t.Fatalf("take remote urls=%#v state=%#v", urls, state)
	}
}
