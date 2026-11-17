package app

// Rust #61490: navigating to another Cloud task prepares the replacement and
// hydrates its initial history before committing, so a failed load leaves the
// displayed conversation untouched.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/cloudbackend"
	"codex_go/cloudclient"
)

type preparedCloudSource struct {
	attachErr error
	itemsErr  error
}

func (f *preparedCloudSource) Activate(_ context.Context, threadID string) (*cloudclient.ThreadActivation, error) {
	return &cloudclient.ThreadActivation{
		Thread:         cloudclient.ThreadSnapshot{ID: threadID, Status: map[string]any{"type": "idle"}},
		Model:          "gpt-5",
		ModelProvider:  "openai",
		CWD:            "/repo",
		ApprovalPolicy: "on-request",
	}, nil
}

func (f *preparedCloudSource) Attach(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
	if f.attachErr != nil {
		return nil, nil, f.attachErr
	}
	return make(chan cloudclient.Event), make(chan error), nil
}

func (f *preparedCloudSource) ArchiveThread(context.Context, string) error { return nil }

func (f *preparedCloudSource) UnarchiveThread(context.Context, string) (cloudclient.ThreadSnapshot, error) {
	return cloudclient.ThreadSnapshot{}, nil
}

func (f *preparedCloudSource) ListThreads(context.Context, cloudclient.ThreadListQuery) (cloudclient.ThreadPage, error) {
	return cloudclient.ThreadPage{}, nil
}

func (f *preparedCloudSource) PrewarmDefaultThread(context.Context, string, string) (*cloudclient.ThreadActivation, error) {
	return f.Activate(context.Background(), "started")
}

func (f *preparedCloudSource) PrewarmThread(context.Context, string, string, *cloudclient.EnvironmentSelection) (*cloudclient.ThreadActivation, error) {
	return f.Activate(context.Background(), "started")
}

func (f *preparedCloudSource) ListSavedEnvironments(context.Context) ([]cloudclient.SavedEnvironment, error) {
	return nil, nil
}

func (f *preparedCloudSource) ListTurns(_ context.Context, request cloudclient.ListTurnsRequest) (cloudclient.HistoryPage[cloudclient.HistoryTurn], error) {
	return cloudclient.HistoryPage[cloudclient.HistoryTurn]{
		ThreadID: request.ThreadID,
		Data: []cloudclient.HistoryTurn{{
			Summary: cloudclient.TurnSummary{ID: "turn-1", Status: cloudclient.TurnCompleted},
		}},
	}, nil
}

func (f *preparedCloudSource) ListItems(_ context.Context, request cloudclient.ListItemsRequest) (cloudclient.HistoryPage[cloudclient.HistoryItem], error) {
	if f.itemsErr != nil {
		return cloudclient.HistoryPage[cloudclient.HistoryItem]{}, f.itemsErr
	}
	return cloudclient.HistoryPage[cloudclient.HistoryItem]{
		ThreadID: request.ThreadID,
		Data: []cloudclient.HistoryItem{{
			TurnID: "turn-1",
			Summary: &cloudclient.ItemSummary{
				Kind: cloudclient.ItemUserMessage,
				User: cloudclient.UserMessageSummary{ID: "item-1", Text: []string{"hello from cloud"}},
			},
		}},
	}, nil
}

func (f *preparedCloudSource) AddUserMessage(context.Context, cloudclient.AddUserMessageRequest) (*cloudclient.Admission, error) {
	return nil, errors.New("no admission configured")
}

func (f *preparedCloudSource) InterruptTurn(context.Context, cloudclient.InterruptTurnRequest) error {
	return nil
}

func (f *preparedCloudSource) Close() error { return nil }

func cloudNavigationClient(t *testing.T, source *preparedCloudSource) (*remoteAppServerTUIClient, *cloudbackend.Client) {
	t.Helper()
	backend := cloudbackend.Connect(context.Background(), source, nil)
	t.Cleanup(backend.Close)
	transport := newCloudAppServerTransport(context.Background(), backend)
	t.Cleanup(transport.close)
	return &remoteAppServerTUIClient{
		transport:        transport,
		threadParamsMode: ThreadParamsModeCloud,
		root:             &cli.RootOptions{Remote: "cloud"},
	}, backend
}

func loadedCloudThreads(t *testing.T, backend *cloudbackend.Client) []string {
	t.Helper()
	raw, rpcErr := backend.Request(context.Background(), appserver.MethodThreadLoadedList, nil)
	if rpcErr != nil {
		t.Fatalf("thread/loaded/list: %#v", rpcErr)
	}
	var loaded appserver.ThreadLoadedListResponse
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("decode loaded threads: %v", err)
	}
	return loaded.Data
}

func TestCloudNavigationLoadsHistoryBeforeCommittingLikeRust(t *testing.T) {
	source := &preparedCloudSource{}
	client, backend := cloudNavigationClient(t, source)

	resumed, err := remoteTUIResumeThreadForNavigation(context.Background(), client, appserver.ThreadResumeParams{ThreadID: "new"})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Thread == nil || resumed.Thread.ID != "new" {
		t.Fatalf("resumed thread = %#v", resumed.Thread)
	}
	if len(resumed.Thread.Turns) != 1 || len(resumed.Thread.Turns[0].Items) != 1 {
		t.Fatalf("hydrated turns = %#v", resumed.Thread.Turns)
	}
	response := remoteTUIAgentSwitchResponseFromThread(resumed.Thread)
	found := false
	for _, message := range response.Messages {
		if strings.Contains(message.Text, "hello from cloud") {
			found = true
		}
	}
	if !found {
		t.Fatalf("hydrated transcript = %#v", response.Messages)
	}
	if loaded := loadedCloudThreads(t, backend); len(loaded) != 1 || loaded[0] != "new" {
		t.Fatalf("loaded threads = %#v, want the committed task", loaded)
	}
}

func TestCloudNavigationFailureKeepsCurrentConversationLikeRust(t *testing.T) {
	source := &preparedCloudSource{itemsErr: errors.New("history unavailable")}
	client, backend := cloudNavigationClient(t, source)

	if _, rpcErr := backend.Request(context.Background(), appserver.MethodThreadResume, json.RawMessage(`{"threadId":"old","excludeTurns":true}`)); rpcErr != nil {
		t.Fatalf("resume old: %#v", rpcErr)
	}
	if _, err := remoteTUIResumeThreadForNavigation(context.Background(), client, appserver.ThreadResumeParams{ThreadID: "new"}); err == nil {
		t.Fatal("failed history load was accepted")
	}
	if loaded := loadedCloudThreads(t, backend); len(loaded) != 1 || loaded[0] != "old" {
		t.Fatalf("loaded threads after failed navigation = %#v, want the displayed task", loaded)
	}
}

// pagedPreparedCloudSource paginates so the initial hydration must extend the
// turn window while reading the newest item pages.
type pagedPreparedCloudSource struct {
	preparedCloudSource
}

func (f *pagedPreparedCloudSource) ListTurns(_ context.Context, request cloudclient.ListTurnsRequest) (cloudclient.HistoryPage[cloudclient.HistoryTurn], error) {
	cursor := ""
	if request.Page.Cursor != nil {
		cursor = strings.TrimSpace(*request.Page.Cursor)
	}
	switch cursor {
	case "":
		next := "turns-2"
		return cloudclient.HistoryPage[cloudclient.HistoryTurn]{
			ThreadID:   request.ThreadID,
			Data:       []cloudclient.HistoryTurn{{Summary: cloudclient.TurnSummary{ID: "turn-2", Status: cloudclient.TurnCompleted}}},
			NextCursor: &next,
		}, nil
	case "turns-2":
		return cloudclient.HistoryPage[cloudclient.HistoryTurn]{
			ThreadID: request.ThreadID,
			Data:     []cloudclient.HistoryTurn{{Summary: cloudclient.TurnSummary{ID: "turn-1", Status: cloudclient.TurnCompleted}}},
		}, nil
	default:
		return cloudclient.HistoryPage[cloudclient.HistoryTurn]{ThreadID: request.ThreadID}, nil
	}
}

func (f *pagedPreparedCloudSource) ListItems(_ context.Context, request cloudclient.ListItemsRequest) (cloudclient.HistoryPage[cloudclient.HistoryItem], error) {
	cursor := ""
	if request.Page.Cursor != nil {
		cursor = strings.TrimSpace(*request.Page.Cursor)
	}
	switch cursor {
	case "":
		next := "items-2"
		return cloudclient.HistoryPage[cloudclient.HistoryItem]{
			ThreadID: request.ThreadID,
			Data: []cloudclient.HistoryItem{{
				TurnID: "turn-1",
				Summary: &cloudclient.ItemSummary{
					Kind: cloudclient.ItemUserMessage,
					User: cloudclient.UserMessageSummary{ID: "item-2", Text: []string{"newer"}},
				},
			}},
			NextCursor: &next,
		}, nil
	case "items-2":
		return cloudclient.HistoryPage[cloudclient.HistoryItem]{
			ThreadID: request.ThreadID,
			Data: []cloudclient.HistoryItem{{
				TurnID: "turn-1",
				Summary: &cloudclient.ItemSummary{
					Kind: cloudclient.ItemUserMessage,
					User: cloudclient.UserMessageSummary{ID: "item-1", Text: []string{"older"}},
				},
			}},
		}, nil
	default:
		return cloudclient.HistoryPage[cloudclient.HistoryItem]{ThreadID: request.ThreadID}, nil
	}
}

// Rust hydrate_initial_thread_history pages thread items first and fetches
// earlier turns when the item window reaches past the first turn page.
func TestCloudNavigationExtendsTurnWindowForItemPagesLikeRust(t *testing.T) {
	source := &pagedPreparedCloudSource{}
	backend := cloudbackend.Connect(context.Background(), source, nil)
	t.Cleanup(backend.Close)
	transport := newCloudAppServerTransport(context.Background(), backend)
	t.Cleanup(transport.close)
	client := &remoteAppServerTUIClient{
		transport:        transport,
		threadParamsMode: ThreadParamsModeCloud,
		root:             &cli.RootOptions{Remote: "cloud"},
	}

	resumed, err := remoteTUIResumeThreadForNavigation(context.Background(), client, appserver.ThreadResumeParams{ThreadID: "new"})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Thread == nil || len(resumed.Thread.Turns) != 2 {
		t.Fatalf("hydrated turns = %#v", resumed.Thread)
	}
	if resumed.Thread.Turns[0].ID != "turn-1" || resumed.Thread.Turns[1].ID != "turn-2" {
		t.Fatalf("turn order = %q, %q", resumed.Thread.Turns[0].ID, resumed.Thread.Turns[1].ID)
	}
	items := resumed.Thread.Turns[0].Items
	if len(items) != 2 || items[0].ID != "item-1" || items[1].ID != "item-2" {
		t.Fatalf("items = %#v, want chronological item-1 then item-2", items)
	}
	response := remoteTUIAgentSwitchResponseFromThread(resumed.Thread)
	older := -1
	newer := -1
	for index, message := range response.Messages {
		switch message.Text {
		case "older":
			older = index
		case "newer":
			newer = index
		}
	}
	if older < 0 || newer < 0 || older > newer {
		t.Fatalf("transcript order = %#v", response.Messages)
	}
}
