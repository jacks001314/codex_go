package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/filesearch"
)

func TestAuthStatusReturnsSnapshot(t *testing.T) {
	service := NewMiscService()
	service.SetAuthStatus(AuthStatusResponse{Authenticated: true, Mode: "api-key"})
	if !service.HasAuthStatus() {
		t.Fatalf("HasAuthStatus() = false, want true")
	}
	status := service.AuthStatus(&AuthStatusParams{})
	if !status.Authenticated || status.Mode != "api-key" {
		t.Fatalf("unexpected auth status: %#v", status)
	}
}

func TestAuthStatusResponseJSONMatchesRustSchema(t *testing.T) {
	token := "sk-test"
	response := AuthStatusResponse{
		AuthToken:     &token,
		Authenticated: true,
		Mode:          "apikey",
		AccountID:     "account-1",
	}
	data, err := json.Marshal(&response)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	text := string(data)
	for _, unexpected := range []string{"authenticated", "mode", "accountId"} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("JSON contains legacy field %q: %s", unexpected, text)
		}
	}
	if !strings.Contains(text, `"authMethod":"apikey"`) || !strings.Contains(text, `"authToken":"sk-test"`) || !strings.Contains(text, `"requiresOpenaiAuth":true`) {
		t.Fatalf("JSON = %s", text)
	}
}

func TestMiscStableWireShapesMatchRustSchema(t *testing.T) {
	authParams, err := json.Marshal(&AuthStatusParams{})
	if err != nil {
		t.Fatalf("Marshal(AuthStatusParams) error = %v", err)
	}
	if string(authParams) != `{"includeToken":null,"refreshToken":null}` {
		t.Fatalf("AuthStatusParams JSON = %s", authParams)
	}

	summaryParams, err := json.Marshal(ConversationSummaryParams{ThreadID: "thread-1"})
	if err != nil {
		t.Fatalf("Marshal(ConversationSummaryParams) error = %v", err)
	}
	if string(summaryParams) != `{"conversationId":"thread-1"}` {
		t.Fatalf("ConversationSummaryParams JSON = %s", summaryParams)
	}

	rolloutParams, err := json.Marshal(ConversationSummaryParams{ThreadID: "thread-1", RolloutPath: "/repo/rollout.jsonl"})
	if err != nil {
		t.Fatalf("Marshal(ConversationSummaryParams rollout) error = %v", err)
	}
	if string(rolloutParams) != `{"rolloutPath":"/repo/rollout.jsonl"}` {
		t.Fatalf("ConversationSummaryParams rollout JSON = %s", rolloutParams)
	}

	updatedAt := "2025-01-02T12:00:00.000Z"
	sha := "abc123"
	summaryResponse, err := json.Marshal(&ConversationSummaryResponse{
		Summary: "legacy text",
		SummaryData: &ConversationSummary{
			ConversationID: "thread-1",
			Path:           "/repo/rollout.jsonl",
			Preview:        "preview",
			Timestamp:      nil,
			UpdatedAt:      &updatedAt,
			ModelProvider:  "openai",
			CWD:            "/repo",
			CLIVersion:     "0.1.0",
			Source:         SessionSourceCli,
			GitInfo:        &ConversationGitInfo{SHA: &sha},
		},
	})
	if err != nil {
		t.Fatalf("Marshal(ConversationSummaryResponse) error = %v", err)
	}
	wantSummaryResponse := `{"summary":{"conversationId":"thread-1","path":"/repo/rollout.jsonl","preview":"preview","timestamp":null,"updatedAt":"2025-01-02T12:00:00.000Z","modelProvider":"openai","cwd":"/repo","cliVersion":"0.1.0","source":"cli","gitInfo":{"sha":"abc123","branch":null,"origin_url":null}}}`
	if string(summaryResponse) != wantSummaryResponse {
		t.Fatalf("ConversationSummaryResponse JSON = %s", summaryResponse)
	}

	gitDiff, err := json.Marshal(GitDiffToRemoteParams{CWD: "/repo", Remote: "upstream"})
	if err != nil {
		t.Fatalf("Marshal(GitDiffToRemoteParams) error = %v", err)
	}
	if string(gitDiff) != `{"cwd":"/repo"}` {
		t.Fatalf("GitDiffToRemoteParams JSON = %s", gitDiff)
	}
}

func TestConversationSummaryRequiresThreadID(t *testing.T) {
	service := NewMiscService()
	if _, err := service.ConversationSummary(&ConversationSummaryParams{}); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestFuzzyFileSearch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	service := NewMiscService()
	response, err := service.FuzzyFileSearch(context.Background(), &FuzzyFileSearchParams{Query: "readme", CWD: dir, Limit: 5})
	if err != nil {
		t.Fatalf("FuzzyFileSearch() error = %v", err)
	}
	if len(response.Files) != 1 || response.Files[0].Path != "README.md" {
		t.Fatalf("unexpected files: %#v", response.Files)
	}
}

func TestFuzzyFileSearchEmptyQueryReturnsEmptyResults(t *testing.T) {
	service := NewMiscService()
	response, err := service.FuzzyFileSearch(context.Background(), &FuzzyFileSearchParams{Query: "", Roots: []string{t.TempDir()}})
	if err != nil {
		t.Fatalf("FuzzyFileSearch(empty query) error = %v", err)
	}
	if len(response.Files) != 0 {
		t.Fatalf("files = %#v, want empty", response.Files)
	}
}

func TestFuzzyFileSearchCancellationTokenCancelsPreviousSearch(t *testing.T) {
	service := NewMiscService()
	token := "search-1"
	canceled := false
	service.pendingFuzzySearches[token] = &fuzzySearchCancellation{cancel: func() {
		canceled = true
	}}

	response, err := service.FuzzyFileSearch(context.Background(), &FuzzyFileSearchParams{
		Query:             "readme",
		CancellationToken: &token,
	})
	if err != nil {
		t.Fatalf("FuzzyFileSearch() error = %v", err)
	}
	if !canceled {
		t.Fatalf("previous search was not canceled")
	}
	if len(response.Files) != 0 {
		t.Fatalf("files = %#v, want empty roots result", response.Files)
	}
	if _, ok := service.pendingFuzzySearches[token]; ok {
		t.Fatalf("pending cancellation token still registered after search")
	}
}

func TestFuzzyFileSearchParamsJSONMatchesRustSchema(t *testing.T) {
	token := "search-1"
	data, err := json.Marshal(&FuzzyFileSearchParams{
		Query:             "readme",
		CWD:               "/legacy-cwd",
		Roots:             []string{"/repo"},
		Limit:             5,
		Exclude:           []string{"vendor/**"},
		CancellationToken: &token,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	output := string(data)
	for _, legacy := range []string{"cwd", "limit", "exclude"} {
		if strings.Contains(output, legacy) {
			t.Fatalf("fuzzyFileSearch params leaked %q: %s", legacy, output)
		}
	}
	if output != `{"query":"readme","roots":["/repo"],"cancellationToken":"search-1"}` {
		t.Fatalf("JSON = %s", output)
	}

	data, err = json.Marshal(&FuzzyFileSearchParams{Query: "readme"})
	if err != nil {
		t.Fatalf("Marshal(empty roots) error = %v", err)
	}
	if string(data) != `{"query":"readme","roots":[],"cancellationToken":null}` {
		t.Fatalf("empty roots JSON = %s", data)
	}
}

func TestFuzzyFileSearchSessionWireMatchesRustSchema(t *testing.T) {
	updateParams, err := json.Marshal(&FuzzyFileSearchSessionUpdateParams{SessionID: "session-1", Query: "read", Limit: 10})
	if err != nil {
		t.Fatalf("Marshal(update params) error = %v", err)
	}
	if string(updateParams) != `{"sessionId":"session-1","query":"read"}` {
		t.Fatalf("update params JSON = %s", updateParams)
	}

	updateResponse, err := json.Marshal(&FuzzyFileSearchSessionUpdateResponse{
		Files: []filesearch.FileMatch{{Root: "/repo", Path: "README.md", MatchType: filesearch.MatchFile}},
	})
	if err != nil {
		t.Fatalf("Marshal(update response) error = %v", err)
	}
	if string(updateResponse) != `{}` {
		t.Fatalf("update response JSON = %s", updateResponse)
	}

	startParams, err := json.Marshal(&FuzzyFileSearchSessionStartParams{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("Marshal(start params) error = %v", err)
	}
	if string(startParams) != `{"sessionId":"session-1","roots":[]}` {
		t.Fatalf("start params JSON = %s", startParams)
	}
}

func TestFuzzyFileSearchAllowsEmptyRoots(t *testing.T) {
	service := NewMiscService()
	response, err := service.FuzzyFileSearch(context.Background(), &FuzzyFileSearchParams{Query: "readme"})
	if err != nil {
		t.Fatalf("FuzzyFileSearch(empty roots) error = %v", err)
	}
	if len(response.Files) != 0 {
		t.Fatalf("files = %#v, want empty", response.Files)
	}
}

func TestMiscServiceNilParamsValidateAsEmptyRequests(t *testing.T) {
	service := NewMiscService()
	if status := service.AuthStatus(nil); status == nil {
		t.Fatalf("AuthStatus(nil) returned nil")
	}
	if _, err := service.GitDiffToRemote(nil); !errors.Is(err, ErrInvalidMiscRequest) {
		t.Fatalf("GitDiffToRemote(nil) error = %v, want ErrInvalidMiscRequest", err)
	}
	if _, err := service.FuzzyFileSearch(context.Background(), nil); !errors.Is(err, ErrInvalidMiscRequest) {
		t.Fatalf("FuzzyFileSearch(nil) error = %v, want ErrInvalidMiscRequest", err)
	}
	if _, err := service.FuzzyFileSearchSessionStart(nil); !errors.Is(err, ErrInvalidMiscRequest) {
		t.Fatalf("FuzzyFileSearchSessionStart(nil) error = %v, want ErrInvalidMiscRequest", err)
	}
}
