package tea

import (
	"strings"
	"sync"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	tuiapp "codex_go/tui/app"
)

// cloudTitleSavesState bounds shutdown: saves already in flight get a short
// chance to finish, and a save that starts after shutdown is skipped (Rust
// #61600 cloud_title_shutdown).
type cloudTitleSavesState struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// begin admits one save. It reports false once shutdown started.
func (s *cloudTitleSavesState) begin() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.wg.Add(1)
	return true
}

func (s *cloudTitleSavesState) end() {
	if s != nil {
		s.wg.Done()
	}
}

// shutdown stops admitting saves and waits up to timeout for the pending ones.
func (s *cloudTitleSavesState) shutdown(timeout time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func (m *Model) resetAutomaticThreadTitle() {
	if m == nil {
		return
	}
	m.automaticThreadTitleEpoch.Add(1)
	m.automaticThreadTitlePending = false
	m.automaticThreadTitleThreadID = ""
	m.automaticThreadTitleExpected = ""
}

// startAutomaticThreadTitle applies the provisional name and starts one bounded
// structured title request (Rust #40492). Cloud sessions generate the title with
// their own account instead of a temporary thread (Rust #61600).
func (m *Model) startAutomaticThreadTitle(text string) bubbletea.Cmd {
	if m == nil || m.State == nil || m.automaticThreadTitlePending {
		return nil
	}
	if m.readOnlyThread || m.threadUnavailable || m.inSideConversation() {
		return nil
	}
	if strings.TrimSpace(m.State.ThreadName) != "" {
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" {
		return nil
	}
	prompt := strings.TrimSpace(text)
	// Rust #60842: desktop shows a single line of the first message until the
	// generated title arrives, and keeps it when generation fails.
	provisional := tuiapp.FallbackThreadTitle(prompt)
	if provisional == "" {
		return nil
	}
	cloud := m.onGenerateCloudThreadTitle != nil
	m.automaticThreadTitleEpoch.Add(1)
	epoch := m.automaticThreadTitleEpoch.Load()
	m.automaticThreadTitlePending = true
	m.automaticThreadTitleThreadID = threadID
	m.automaticThreadTitleExpected = provisional
	m.State.SetThreadName(provisional)
	m.refreshTranscript()

	requestID := m.nextThreadNameSuggestionRequest()
	rename := m.onRenameThread
	generate := m.onSuggestThreadName
	if cloud {
		// A Cloud title is saved only after generation (or its fallback), so the
		// display here stays local until the writer runs (Rust #61600).
		generate = m.onGenerateCloudThreadTitle
	}
	options := RecapThreadOptions{
		Model:             m.threadTitleModel(),
		ModelProvider:     strings.TrimSpace(m.State.Provider),
		CWD:               strings.TrimSpace(m.State.CWD),
		PermissionProfile: strings.TrimSpace(m.State.Sandbox),
		ThreadSource:      "thread_title",
	}
	if options.Model == threadTitleSuggestionModel {
		options.Effort = "low"
	}
	titlePrompt := tuiapp.ThreadTitlePrompt(prompt)
	schema := tuiapp.ThreadTitleOutputSchema()
	return func() bubbletea.Msg {
		if m.automaticThreadTitleEpoch.Load() != epoch {
			return nil
		}
		if rename != nil && !cloud {
			if err := rename(threadID, provisional); err != nil {
				return ThreadNameSuggestedMsg{
					ThreadID:      threadID,
					RequestID:     requestID,
					ExpectedTitle: provisional,
					Automatic:     true,
					RenameFailed:  true,
				}
			}
		}
		if generate == nil {
			return ThreadNameSuggestedMsg{
				ThreadID:      threadID,
				RequestID:     requestID,
				ExpectedTitle: provisional,
				FallbackTitle: provisional,
				Automatic:     true,
			}
		}
		response, err := generate(threadID, options, titlePrompt, schema)
		message := ThreadNameSuggestedMsg{
			ThreadID:      threadID,
			RequestID:     requestID,
			ExpectedTitle: provisional,
			FallbackTitle: provisional,
			Automatic:     true,
		}
		if err == nil {
			message.Suggestion, message.OK = tuiapp.ParseGeneratedThreadTitle(response)
		}
		return message
	}
}

// applyAutomaticThreadTitle applies the generated name only while the
// provisional name is still current and the thread is still active.
func (m *Model) applyAutomaticThreadTitle(message ThreadNameSuggestedMsg) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	threadID := strings.TrimSpace(message.ThreadID)
	if threadID != m.automaticThreadTitleThreadID || threadID != strings.TrimSpace(m.State.ThreadID) {
		return nil
	}
	expected := strings.TrimSpace(message.ExpectedTitle)
	if expected == "" || strings.TrimSpace(m.State.ThreadName) != expected {
		m.automaticThreadTitlePending = false
		return nil
	}
	m.automaticThreadTitlePending = false
	if m.onSaveCloudThreadName != nil {
		return m.saveCloudAutomaticTitle(message, expected)
	}
	if message.RenameFailed {
		m.State.SetThreadName("")
		m.refreshTranscript()
		return m.refreshStatusControlsCmd()
	}
	if !message.OK || strings.TrimSpace(message.Suggestion) == "" {
		return nil
	}
	finalTitle := strings.TrimSpace(message.Suggestion)
	rename := m.onRenameThread
	epoch := m.automaticThreadTitleEpoch.Load()
	// Keep the event loop free while the final title is persisted. The epoch
	// prevents a slow save from overwriting a manual rename (Rust #58970).
	return func() bubbletea.Msg {
		if m.automaticThreadTitleEpoch.Load() != epoch {
			return nil
		}
		var err error
		if rename != nil {
			err = rename(threadID, finalTitle)
		}
		return AutomaticThreadNameSavedMsg{
			ThreadID: threadID,
			Title:    finalTitle,
			Expected: expected,
			Epoch:    epoch,
			Err:      err,
		}
	}
}

// saveCloudAutomaticTitle persists the generated title, or the first-message
// fallback, through the Cloud writer. The writer preserves a saved name that no
// longer matches the expected empty name (Rust AutomaticThreadName::save).
func (m *Model) saveCloudAutomaticTitle(message ThreadNameSuggestedMsg, expected string) bubbletea.Cmd {
	title := ""
	if message.OK {
		title = strings.TrimSpace(message.Suggestion)
	}
	if title == "" {
		title = strings.TrimSpace(message.FallbackTitle)
	}
	if title == "" {
		return nil
	}
	save := m.onSaveCloudThreadName
	threadID := strings.TrimSpace(message.ThreadID)
	saves := m.cloudTitleSaves
	epoch := m.automaticThreadTitleEpoch.Load()
	return func() bubbletea.Msg {
		if m.automaticThreadTitleEpoch.Load() != epoch {
			return nil
		}
		if !saves.begin() {
			return nil
		}
		defer saves.end()
		// The thread had no saved name when generation started, so any name the
		// writer finds means a manual rename won the race.
		err := save(threadID, title, "")
		return AutomaticThreadNameSavedMsg{
			ThreadID: threadID,
			Title:    title,
			Expected: expected,
			Epoch:    epoch,
			Err:      err,
		}
	}
}

// AutomaticThreadNameSavedMsg reports the background save of a generated title.
type AutomaticThreadNameSavedMsg struct {
	ThreadID string
	Title    string
	Expected string
	Epoch    uint64
	Err      error
}

func (m *Model) applyAutomaticThreadNameSaved(message AutomaticThreadNameSavedMsg) bubbletea.Cmd {
	if m == nil || m.State == nil || message.Err != nil {
		return nil
	}
	if message.Epoch != m.automaticThreadTitleEpoch.Load() {
		return nil
	}
	if strings.TrimSpace(message.ThreadID) != strings.TrimSpace(m.State.ThreadID) {
		return nil
	}
	if strings.TrimSpace(m.State.ThreadName) != strings.TrimSpace(message.Expected) {
		return nil
	}
	m.State.SetThreadName(message.Title)
	m.refreshTranscript()
	return m.refreshStatusControlsCmd()
}
