package cloudbackend

import (
	"context"
	"errors"
	"strings"

	"codex_go/appserver"
)

// Rust parity: codex-rs/app-server-client/src/cloud_backend/thread_title.rs
// (#61600). Cloud conversation titles are generated with the selected account,
// and saved names are read and written without replacing the active
// attachment. Automatic writes travel outside the conversation command queue.

// ThreadTitleGenerator generates a structured Cloud conversation title. The
// model package's CloudTitleClient implements it.
type ThreadTitleGenerator interface {
	Generate(ctx context.Context, model string, effort string, prompt string, schema map[string]any) (string, error)
}

// ConnectOptions configures the in-process Cloud app-server client.
type ConnectOptions struct {
	// IdleRecovery configures the quiet reopen of a lost reply stream.
	IdleRecovery IdleRecoveryOptions
	// TitleGenerator enables automatic Cloud conversation titles. nil keeps
	// title generation unavailable, so the TUI saves the first-message title.
	TitleGenerator ThreadTitleGenerator
}

// threadNameSaved applies a name written outside the command queue. The
// dispatch goroutine owns the loaded thread, so the update and its
// notification are serialized with every other request.
type threadNameSaved struct {
	params appserver.ThreadSetNameParams
	done   chan struct{}
}

// TitleGenerationAvailable reports whether this client can generate titles.
func (c *Client) TitleGenerationAvailable() bool {
	return c != nil && c.titleGenerator != nil
}

// GenerateThreadTitle asks the Cloud account for a structured title.
func (c *Client) GenerateThreadTitle(ctx context.Context, model string, effort string, prompt string, schema map[string]any) (string, error) {
	if c == nil || c.titleGenerator == nil {
		return "", errors.New("Cloud title generation is unavailable")
	}
	return c.titleGenerator.Generate(ctx, model, effort, prompt, schema)
}

// ReadThreadName reads the saved name without opening or replacing an
// attachment (Rust read_native_thread_name). The boolean reports whether the
// native client supports the read; callers fall back to thread/read when it is
// false.
func (c *Client) ReadThreadName(ctx context.Context, threadID string) (string, bool, error) {
	if c == nil {
		return "", false, errors.New("cloud client is unavailable")
	}
	source, ok := c.source.(interface {
		ReadThreadName(context.Context, string) (string, error)
	})
	if !ok {
		return "", false, nil
	}
	name, err := source.ReadThreadName(ctx, threadID)
	return name, true, err
}

// SaveThreadName saves a name without occupying the conversation command
// queue (Rust save_native_thread_name). The boolean reports whether the native
// client supports the write.
func (c *Client) SaveThreadName(ctx context.Context, threadID string, name string) (bool, error) {
	if c == nil {
		return false, errors.New("cloud client is unavailable")
	}
	threadID = strings.TrimSpace(threadID)
	name = strings.TrimSpace(name)
	if threadID == "" || name == "" {
		return false, errors.New("invalid cloud thread name")
	}
	source, ok := c.source.(interface {
		SetThreadName(context.Context, string, string) error
	})
	if !ok {
		return false, nil
	}
	if err := source.SetThreadName(ctx, threadID, name); err != nil {
		return true, err
	}
	saved := &threadNameSaved{
		params: appserver.ThreadSetNameParams{ThreadID: threadID, Name: name},
		done:   make(chan struct{}),
	}
	select {
	case c.nameSaves <- saved:
	case <-c.done:
		return true, errors.New("cloud client is closed")
	case <-ctx.Done():
		return true, ctx.Err()
	}
	select {
	case <-saved.done:
		return true, nil
	case <-c.done:
		return true, errors.New("cloud client is closed")
	case <-ctx.Done():
		return true, ctx.Err()
	}
}

// applyThreadNameSaved records the saved name and publishes
// thread/name/updated (Rust Requests::thread_name_saved).
func (c *Client) applyThreadNameSaved(params appserver.ThreadSetNameParams) {
	name := params.Name
	if c.loaded != nil && c.loaded.Thread != nil && c.loaded.Thread.ID == params.ThreadID {
		c.loaded.Thread.Name = &name
	}
	c.enqueue(Notification{
		Method: appserver.NotificationThreadNameUpdated,
		Params: &appserver.ThreadNameUpdatedNotification{ThreadID: params.ThreadID, ThreadName: &name},
	})
}
