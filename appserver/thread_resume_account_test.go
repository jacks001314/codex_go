package appserver

import (
	"testing"

	"codex_go/auth"
	"codex_go/session"
)

func TestResumeAccountMismatchComparesRecordedIDs(t *testing.T) {
	snapshot := &auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens:   map[string]any{"chatgpt_user_id": "user-1", "account_id": "account-1"},
	}
	cases := []struct {
		name    string
		userID  string
		account string
		want    bool
	}{
		{name: "untagged", want: false},
		{name: "same user", userID: "user-1", want: false},
		{name: "same account", account: "account-1", want: false},
		{name: "other user", userID: "user-2", want: true},
		{name: "other workspace", account: "account-2", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := &session.Record{Metadata: session.Metadata{CreatorUserID: tc.userID, CreatorAccountID: tc.account}}
			if got := resumeAccountMismatch(record, snapshot); got != tc.want {
				t.Fatalf("resumeAccountMismatch() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResumeAccountMismatchIgnoresAPIAuth(t *testing.T) {
	record := &session.Record{Metadata: session.Metadata{CreatorUserID: "user-2"}}
	snapshot := &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}
	if resumeAccountMismatch(record, snapshot) {
		t.Fatalf("API-key auth must not compare ChatGPT account tags")
	}
}
