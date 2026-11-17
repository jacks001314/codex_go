package cloudbackend

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"codex_go/appserver"
	"codex_go/cloudclient"
)

// Rust #61956: selecting a saved environment replaces the host project for the
// next start, and the prewarm carries only the saved configuration.
func TestCloudThreadStartUsesSavedEnvironmentLikeRust(t *testing.T) {
	fake := &fakeCloud{
		attach: func(context.Context, string) (<-chan cloudclient.Event, <-chan error, error) {
			return pendingStream()
		},
		prewarm: func(context.Context, string, string) (*cloudclient.ThreadActivation, error) {
			return &cloudclient.ThreadActivation{
				Thread: cloudclient.ThreadSnapshot{ID: "cloud-thread", HistoryMode: "legacy", ModelProvider: "openai", CWD: "/repo", Status: map[string]any{"type": "idle"}, Source: "cli"},
				CWD:    "/repo",
			}, nil
		},
	}
	client := Connect(context.Background(), fake, json.RawMessage("{}"))
	defer client.Close()

	client.SetLocalProject(&cloudclient.ProjectEnvironment{
		Name:       "repo",
		OriginURLs: []string{"https://github.com/org/repo"},
	})
	client.SetEnvironmentSelection(cloudclient.SavedEnvironmentSelection("ws~asenvcfg_saved"))
	if _, rpcErr := client.Request(context.Background(), appserver.MethodThreadStart, json.RawMessage(`{"ephemeral":false}`)); rpcErr != nil {
		t.Fatalf("thread/start error = %#v", rpcErr)
	}
	selection := fake.prewarmEnvironment
	if selection == nil || selection.SavedID != "ws~asenvcfg_saved" || selection.Project != nil {
		t.Fatalf("prewarm environment = %#v", selection)
	}
}

// Rust #61956: saved environment discovery is scoped to the bound account, so
// a read that completes after the account changed is discarded.
func TestSavedEnvironmentCatalogRequiresCurrentAccountLikeRust(t *testing.T) {
	current := true
	calls := 0
	fake := &fakeCloud{
		listEnvironments: func(context.Context) ([]cloudclient.SavedEnvironment, error) {
			calls++
			return []cloudclient.SavedEnvironment{{ID: "ws~asenvcfg_saved", Name: "Team", WorkspaceShared: true}}, nil
		},
	}
	client := newRecoveryTestClient(fake, IdleRecoveryOptions{LoginCurrent: func() bool { return current }})

	environments, err := client.ListSavedEnvironments(context.Background())
	if err != nil || len(environments) != 1 || environments[0].ID != "ws~asenvcfg_saved" {
		t.Fatalf("ListSavedEnvironments() = %#v, %v", environments, err)
	}

	current = false
	environments, err = client.ListSavedEnvironments(context.Background())
	if !errors.Is(err, cloudclient.ErrInvalidCredentials) || environments != nil {
		t.Fatalf("stale account read = %#v, %v", environments, err)
	}
	if calls != 2 {
		t.Fatalf("catalog reads = %d", calls)
	}
}
