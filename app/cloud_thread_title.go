package app

import (
	"context"
	"errors"
	"strings"
	"sync"

	"codex_go/appserverdaemon"
	"codex_go/config"
	codextea "codex_go/tui/tea"
)

// Rust parity: codex-rs/tui/src/app_app_server_session.rs + thread_title.rs
// (#61600). Automatic Cloud titles are generated and saved through the Cloud
// account itself, so a manual rename never waits behind a native Cloud write.

// automaticTitleSaveInFlightMessage is Rust's retryable manual-rename error.
const automaticTitleSaveInFlightMessage = "An automatic title is still being saved. Try renaming the thread again."

// automaticThreadNameGate serializes automatic Cloud name writes and lets a
// manual rename report the in-flight write instead of blocking the UI.
type automaticThreadNameGate struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newAutomaticThreadNameGate() *automaticThreadNameGate {
	return &automaticThreadNameGate{locks: map[string]*sync.Mutex{}}
}

// lockFor returns the per-thread write mutex.
func (g *automaticThreadNameGate) lockFor(threadID string) *sync.Mutex {
	threadID = strings.TrimSpace(threadID)
	g.mu.Lock()
	defer g.mu.Unlock()
	lock := g.locks[threadID]
	if lock == nil {
		lock = &sync.Mutex{}
		g.locks[threadID] = lock
	}
	return lock
}

// acquire waits for any automatic write for the thread and returns its release.
func (g *automaticThreadNameGate) acquire(threadID string) func() {
	if g == nil {
		return func() {}
	}
	lock := g.lockFor(threadID)
	lock.Lock()
	return lock.Unlock
}

// tryAcquire reports whether no automatic write for the thread is in flight.
func (g *automaticThreadNameGate) tryAcquire(threadID string) (func(), bool) {
	if g == nil {
		return func() {}, true
	}
	lock := g.lockFor(threadID)
	if !lock.TryLock() {
		return nil, false
	}
	return lock.Unlock, true
}

// cloudChatGPTBaseURLIsExplicit mirrors Rust's ChatgptBaseUrl::Configured: a
// configured backend must match the discovered workspace origin.
func cloudChatGPTBaseURLIsExplicit(loaded *config.Config) bool {
	if loaded == nil {
		return false
	}
	if _, ok := loaded.Values["chatgpt_base_url"]; !ok {
		return false
	}
	return strings.TrimSpace(loaded.ChatGPTBaseURL()) != config.DefaultChatGPTBaseURL
}

// cloudBackendTransport opens one in-process Cloud backend session. Each
// title/name operation is independent of the conversation attachment, so a
// short-lived session is enough.
func cloudBackendTransport(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) (*cloudAppServerTransport, func(), error) {
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		return nil, nil, err
	}
	transport, ok := client.transport.(*cloudAppServerTransport)
	if !ok || transport == nil || transport.client == nil {
		client.close()
		return nil, nil, errors.New("the Cloud backend is unavailable")
	}
	return transport, client.close, nil
}

// interactiveCloudTitleGenerator generates a Cloud title with the selected
// account's routing, without a temporary conversation (Rust #61600).
func interactiveCloudTitleGenerator(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.CloudTitleGenerateFunc {
	return func(_ string, options codextea.RecapThreadOptions, prompt string, schema map[string]any) (string, error) {
		transport, closeSession, err := cloudBackendTransport(ctx, endpoint)
		if err != nil {
			return "", err
		}
		defer closeSession()
		return transport.GenerateThreadTitle(ctx, options.Model, options.Effort, prompt, schema)
	}
}

// cloudThreadTitleTransport is the Cloud capability slice automatic titles use.
type cloudThreadTitleTransport interface {
	ReadThreadName(ctx context.Context, threadID string) (string, bool, error)
	SaveThreadName(ctx context.Context, threadID string, name string) (bool, error)
}

// interactiveCloudThreadNameSaver persists an automatic Cloud title outside
// the conversation command queue. An existing saved name is preserved.
func interactiveCloudThreadNameSaver(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, gate *automaticThreadNameGate) codextea.CloudThreadNameSaveFunc {
	return func(threadID string, title string, expected string) error {
		release := gate.acquire(threadID)
		defer release()
		transport, closeSession, err := cloudBackendTransport(ctx, endpoint)
		if err != nil {
			return err
		}
		defer closeSession()
		return saveAutomaticCloudThreadName(ctx, transport, threadID, title, expected)
	}
}

// saveAutomaticCloudThreadName writes the title unless the conversation name
// changed after generation started (Rust AutomaticThreadName::save).
func saveAutomaticCloudThreadName(ctx context.Context, transport cloudThreadTitleTransport, threadID string, title string, expected string) error {
	saved, supported, err := transport.ReadThreadName(ctx, threadID)
	if err != nil {
		return err
	}
	if supported && saved != expected {
		return nil
	}
	written, err := transport.SaveThreadName(ctx, threadID, title)
	if err != nil {
		return err
	}
	if !written {
		return errors.New("Cloud rename is unavailable")
	}
	return nil
}

// interactiveCloudAgentsOverviewRename gates dashboard renames the same way as
// the slash-command rename (Rust #61600 AppServerSession::thread_set_name).
func interactiveCloudAgentsOverviewRename(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, gate *automaticThreadNameGate) codextea.AgentsOverviewRenameFunc {
	manual := interactiveRemoteAgentsOverviewRename(ctx, endpoint)
	return func(threadID string, name string) error {
		release, ok := gate.tryAcquire(threadID)
		if !ok {
			return errors.New(automaticTitleSaveInFlightMessage)
		}
		defer release()
		return manual(threadID, name)
	}
}

// interactiveCloudRenameThreadHandler runs a manual Cloud rename. While an
// automatic save holds the thread, it reports the retryable error so the UI
// stays responsive (Rust #61600).
func interactiveCloudRenameThreadHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, gate *automaticThreadNameGate) codextea.ThreadRenameFunc {
	manual := interactiveRemoteRenameThreadHandler(ctx, endpoint)
	return func(threadID string, name string) error {
		release, ok := gate.tryAcquire(threadID)
		if !ok {
			return errors.New(automaticTitleSaveInFlightMessage)
		}
		defer release()
		return manual(threadID, name)
	}
}
