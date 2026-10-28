package sandbox

import (
	"strings"
	"testing"
)

func TestApprovalHeadingValidationLikeRust(t *testing.T) {
	boundary := strings.Repeat("é", 250)
	if got := ValidApprovalHeading(&boundary); got != boundary {
		t.Fatalf("boundary heading = %q, want accepted", got)
	}
	invalid := []string{
		"",
		"  ",
		boundary + "a",
		PermissionInstructionsOpenTag,
		PermissionInstructionsCloseTag,
		"{{ policy }}",
		"title\nbody",
		"title\u2028body",
	}
	for _, value := range invalid {
		value := value
		if got := ValidApprovalHeading(&value); got != "" {
			t.Fatalf("ValidApprovalHeading(%q) = %q, want rejected", value, got)
		}
	}
	if got := ValidApprovalHeading(nil); got != "" {
		t.Fatalf("nil heading = %q", got)
	}
}

func TestApprovalHeadingsReplaceBundledFirstLineLikeRust(t *testing.T) {
	heading := "Catalog permissions"
	autoReview := "Catalog review policy"
	config := &PermissionPromptConfig{
		ApprovalPolicy:                PermissionPromptApprovalUnlessTrusted,
		RequestPermissionsToolEnabled: true,
		ApprovalMessages: &PermissionPromptApprovalMessages{
			RequestPermissions: &heading,
			AutoReviewSuffix:   &autoReview,
		},
	}
	text := BuildPermissionPrompt(config)
	if !strings.Contains(text, heading+"\n\nThe built-in `request_permissions` tool") {
		t.Fatalf("prompt does not use the catalog heading:\n%s", text)
	}
	if strings.Contains(text, "# request_permissions Tool") {
		t.Fatalf("prompt kept the bundled heading:\n%s", text)
	}

	config.ApprovalPolicy = PermissionPromptApprovalOnRequest
	config.ApprovalsReviewer = PermissionPromptAutoReview
	text = BuildPermissionPrompt(config)
	want := autoReview + "\n\n" + autoReviewText()
	if !strings.Contains(text, want) {
		t.Fatalf("prompt does not prepend the auto-review heading:\n%s", text)
	}

	// Invalid headings fall back to the bundled text.
	invalid := "bad\u2028heading"
	config.ApprovalMessages.RequestPermissions = &invalid
	config.ApprovalMessages.AutoReviewSuffix = &invalid
	config.ApprovalsReviewer = ""
	text = BuildPermissionPrompt(config)
	if !strings.Contains(text, requestPermissionsToolText()) {
		t.Fatalf("invalid heading did not fall back:\n%s", text)
	}
}

func TestSavedPrefixHeadingValidationLikeRust(t *testing.T) {
	valid := "Approved prefix"
	if got := ValidSavedPrefixHeading(&valid); got != valid {
		t.Fatalf("valid heading = %q", got)
	}
	for _, value := range []string{"", " ", "a<b", "a{b}", "a\nb", strings.Repeat("é", 251)} {
		value := value
		if got := ValidSavedPrefixHeading(&value); got != "" {
			t.Fatalf("ValidSavedPrefixHeading(%q) = %q, want rejected", value, got)
		}
	}
}
