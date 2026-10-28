package appserver

import (
	"strings"
	"testing"

	"codex_go/model"
)

func TestApprovedCommandPrefixSavedHeadingLikeRust(t *testing.T) {
	added := [][]string{{"echo", "amendment-ok"}}
	baseline := approvedCommandPrefixSavedText(added, "")
	if !strings.HasPrefix(baseline, approvedCommandPrefixSavedMessagePrefix+"\n") {
		t.Fatalf("baseline = %q", baseline)
	}
	heading := "Saved an allowed prefix:"
	withHeading := approvedCommandPrefixSavedText(added, heading)
	want := approvedCommandPrefixSavedMessagePrefix + "\n" + heading + "\n- [\"echo\", \"amendment-ok\"]"
	if withHeading != want {
		t.Fatalf("with heading = %q, want %q", withHeading, want)
	}
	// The marker itself is not repeated as a heading.
	if got := approvedCommandPrefixSavedText(added, approvedCommandPrefixSavedMessagePrefix); got != baseline {
		t.Fatalf("marker heading = %q, want baseline %q", got, baseline)
	}

	modelInfo := &model.ModelInfo{ModelMessages: &model.ModelMessages{Permissions: &model.PermissionMessages{
		ApprovedCommandPrefixSaved: &heading,
	}}}
	if got := savedPrefixHeadingForModel(modelInfo); got != heading {
		t.Fatalf("savedPrefixHeadingForModel() = %q", got)
	}
	invalid := "bad<heading>"
	modelInfo.ModelMessages.Permissions.ApprovedCommandPrefixSaved = &invalid
	if got := savedPrefixHeadingForModel(modelInfo); got != "" {
		t.Fatalf("invalid heading = %q, want empty", got)
	}
	if got := savedPrefixHeadingForModel(nil); got != "" {
		t.Fatalf("nil model heading = %q", got)
	}
}
