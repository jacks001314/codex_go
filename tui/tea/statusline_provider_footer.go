package tea

// Rust parity: codex-rs/tui/src/chatwidget/statusline_reader.rs (#61979).
//
// With `tui_extensions_statusline` enabled, a saved MCP provider renders the
// footer from a JSON UI tree. The widget keeps one read per displayed thread
// and drops every result whose thread, saved selection, or working directory no
// longer matches, so a late response can never paint another thread's footer.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/config"
	"codex_go/features"
	bottompane "codex_go/tui/bottom_pane"
)

const (
	statuslineProviderLookupTimeout = 5 * time.Second
	statuslineProviderInitializing  = "Initializing…"
	// Rust #61990 replaces the generic failure copy with short status line
	// messages that point at /statusline.
	statuslineProviderUnavailableCopy = "Status line unavailable · /statusline"
	statuslineProviderTimedOutCopy    = "Status line timed out · /statusline"
	statuslineProviderFailedCopy      = "Status line failed · /statusline"
)

// Rust #61981 reads the provider again one second after each successful read and
// bounds a read with a one-second deadline. Both are variables only so tests can
// compress them the way Rust's tests pause the tokio clock.
var (
	statuslineProviderPollInterval = 1 * time.Second
	statuslineProviderReadDeadline = 1 * time.Second
)

var errStatuslineProviderReadTimeout = errors.New("resource read timed out")

// StatuslineProviderReadRequest identifies the saved provider a host resolves
// for one displayed thread and working directory.
type StatuslineProviderReadRequest struct {
	ThreadID         string
	WorkingDirectory string
	Server           string
	Tool             string
}

// StatuslineProvider is one resolved provider advertisement.
type StatuslineProvider struct {
	Found bool
	Title string
	URI   string
}

// StatuslineProviderContent is one resource returned by the provider read.
// Rust's decoder accepts exactly one text content whose URI and media type match
// the advertisement; IsText distinguishes text from blob contents.
type StatuslineProviderContent struct {
	URI      string
	MIMEType string
	Text     string
	IsText   bool
}

// StatuslineProviderResourceRequest asks the host to render the provider's
// advertised resource with the displayed thread's statusline snapshot.
type StatuslineProviderResourceRequest struct {
	ThreadID string
	Server   string
	URI      string
	Snapshot map[string]any
}

// activeStatuslineProviderRead is one footer read bound to a thread, selection,
// and working directory. Rust stores the same tuple in ActiveStatuslineRead.
type activeStatuslineProviderRead struct {
	token    uint64
	threadID string
	server   string
	tool     string
	hostCWD  string
	// name is the provider title once resolved; the saved tool name until then.
	// Rust uses it verbatim in the failure copy.
	name    string
	uri     string
	started bool
	// failed records a lookup, RPC, deadline, or content failure. Rust #61986
	// cancels the reader on failure, which stops provider snapshot consumers
	// (thread usage, workspace headline) until the reader is reselected.
	failed bool
	// ctx is cancelled when the reader is retired (thread switch, selection
	// change, disconnect), mirroring Rust's CancellationToken in
	// ActiveStatuslineRead's Drop.
	ctx    context.Context
	cancel context.CancelFunc
}

func (r *activeStatuslineProviderRead) isCurrent(m *Model) bool {
	if r == nil || m == nil || m.State == nil {
		return false
	}
	selection := m.selectedStatuslineProvider()
	if selection == nil {
		return false
	}
	return r.threadID == strings.TrimSpace(m.State.ThreadID) &&
		r.server == selection.Server &&
		r.tool == selection.Tool &&
		r.hostCWD == m.statusLineCWD()
}

type statuslineProviderResolveMsg struct {
	token    uint64
	threadID string
	server   string
	tool     string
	provider StatuslineProvider
	err      error
}

type statuslineProviderReadMsg struct {
	token    uint64
	threadID string
	server   string
	contents []StatuslineProviderContent
	err      error
}

// statuslineProviderPollMsg wakes one polling iteration after the delay Rust
// sleeps between successful reads.
type statuslineProviderPollMsg struct {
	token uint64
}

// statuslineReadyGate broadcasts MCP server readiness to the resolving
// goroutine. Rust uses a watch channel: a cold catalog can arrive before the
// selected server finishes starting, and each readiness notification wakes the
// pending lookup.
type statuslineReadyGate struct {
	mu sync.Mutex
	ch chan struct{}
}

func newStatuslineReadyGate() *statuslineReadyGate {
	return &statuslineReadyGate{ch: make(chan struct{})}
}

func (g *statuslineReadyGate) wait() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.ch
}

func (g *statuslineReadyGate) signal() {
	g.mu.Lock()
	defer g.mu.Unlock()
	close(g.ch)
	g.ch = make(chan struct{})
}

// selectedStatuslineProvider returns the saved MCP provider that owns the
// footer, or nil for the built-in status line. Rust's
// ChatWidget::selected_statusline_provider gates on read-only history and the
// extensions feature.
func (m *Model) selectedStatuslineProvider() *config.StatusLineProviderSelection {
	if m == nil || m.statuslineDisconnected || m.readOnlyThread {
		return nil
	}
	if !features.Enabled(m.featureSettings, "tui_extensions_statusline") {
		return nil
	}
	// A host without the resolve/read hooks cannot serve the provider, so the
	// built-in status line keeps the footer instead of showing a permanent
	// initialization row.
	if m.onResolveStatuslineProvider == nil || m.onReadStatuslineProvider == nil {
		return nil
	}
	selection := m.statusLineProvider
	if selection == nil || selection.Default {
		return nil
	}
	if strings.TrimSpace(selection.Server) == "" || strings.TrimSpace(selection.Tool) == "" {
		return nil
	}
	return selection
}

// statuslineProviderSelected reports whether a provider owns the footer.
func (m *Model) statuslineProviderSelected() bool {
	return m.selectedStatuslineProvider() != nil
}

// providerNeedsSnapshot reports whether the provider still consumes the
// statusline snapshot. Rust #61986 provider_needs_snapshot: a failed reader that
// is still current keeps its error footer but stops asking for provider-specific
// usage and workspace-headline data until /statusline reselects it.
func (m *Model) providerNeedsSnapshot() bool {
	if read := m.statuslineProviderRead; read != nil && read.failed && read.isCurrent(m) {
		return false
	}
	return m.statuslineProviderSelected()
}

// statusLineCWD mirrors Rust ChatWidget::status_line_cwd: the thread's current
// working directory, or the launch directory before an attachment.
func (m *Model) statusLineCWD() string {
	if m == nil {
		return ""
	}
	if cwd := strings.TrimSpace(m.sessionCWD); cwd != "" {
		return cwd
	}
	if m.State != nil {
		if cwd := strings.TrimSpace(m.State.CWD); cwd != "" {
			return cwd
		}
	}
	return ""
}

// syncProviderFooterSelection keeps the footer read bound to the live
// selection. It is safe on the render path: no host request starts here and no
// transcript refresh is requested. Rust's refresh_provider_footer performs the
// same reset and leaves the start request to the app event loop.
func (m *Model) syncProviderFooterSelection() {
	if m == nil || m.State == nil {
		return
	}
	selection := m.selectedStatuslineProvider()
	if selection == nil {
		if m.statuslineProviderRead != nil {
			m.retireStatuslineProviderRead()
			// Only provider-owned content is cleared; a host that installed
			// extension content directly keeps it.
			m.statusLineExtension = nil
		}
		return
	}
	if m.statuslineProviderRead != nil && m.statuslineProviderRead.isCurrent(m) {
		return
	}
	m.statuslineProviderToken++
	if m.statuslineProviderToken == 0 {
		m.statuslineProviderToken = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	read := &activeStatuslineProviderRead{
		token:    m.statuslineProviderToken,
		threadID: strings.TrimSpace(m.State.ThreadID),
		server:   selection.Server,
		tool:     selection.Tool,
		hostCWD:  m.statusLineCWD(),
		name:     selection.Tool,
		ctx:      ctx,
		cancel:   cancel,
	}
	if m.statuslineProviderRead != nil {
		m.statuslineProviderRead.cancel()
	}
	m.statuslineProviderRead = read
	m.setProviderFooterText(statuslineProviderInitializing)
}

// retireStatuslineProviderRead cancels the outstanding resolve/read work and
// forgets it. Rust drops ActiveStatuslineRead, whose Drop cancels the token.
func (m *Model) retireStatuslineProviderRead() {
	if m == nil || m.statuslineProviderRead == nil {
		return
	}
	if m.statuslineProviderRead.cancel != nil {
		m.statuslineProviderRead.cancel()
	}
	m.statuslineProviderRead = nil
}

// ensureProviderFooterCmd starts the read whose selection was staged by
// syncProviderFooterSelection. Rust sends ReadStep::Start from
// refresh_provider_footer; the bubbletea Update loop is that event loop.
func (m *Model) ensureProviderFooterCmd() bubbletea.Cmd {
	if m == nil || m.statuslineProviderRead == nil {
		return nil
	}
	read := m.statuslineProviderRead
	if read.started || read.threadID == "" {
		return nil
	}
	if m.onResolveStatuslineProvider == nil {
		return nil
	}
	read.started = true
	return m.statuslineProviderResolveCmd(read)
}

// statuslineProviderResolveCmd resolves the saved provider within five seconds,
// waiting for MCP readiness when the catalog is still cold. The timeout starts
// when the command runs, matching Rust's tokio::time::timeout around lookup.
func (m *Model) statuslineProviderResolveCmd(read *activeStatuslineProviderRead) bubbletea.Cmd {
	resolve := m.onResolveStatuslineProvider
	gate := m.ensureStatuslineReadyGate()
	token := read.token
	request := StatuslineProviderReadRequest{
		ThreadID:         read.threadID,
		WorkingDirectory: read.hostCWD,
		Server:           read.server,
		Tool:             read.tool,
	}
	parent := read.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() bubbletea.Msg {
		ctx, cancel := context.WithTimeout(parent, statuslineProviderLookupTimeout)
		defer cancel()
		for {
			provider, err := resolve(ctx, request)
			if err != nil || provider.Found {
				return statuslineProviderResolveMsg{
					token:    token,
					threadID: request.ThreadID,
					server:   request.Server,
					tool:     request.Tool,
					provider: provider,
					err:      err,
				}
			}
			ready := gate.wait()
			select {
			case <-ctx.Done():
				return statuslineProviderResolveMsg{
					token:    token,
					threadID: request.ThreadID,
					server:   request.Server,
					tool:     request.Tool,
					err:      ctx.Err(),
				}
			case <-ready:
			}
		}
	}
}

// statuslineProviderReadCmd asks the host to render the resolved resource with
// the displayed thread's statusline snapshot.
func (m *Model) statuslineProviderReadCmd(read *activeStatuslineProviderRead) bubbletea.Cmd {
	readResource := m.onReadStatuslineProvider
	if readResource == nil {
		return nil
	}
	// Rust captures the snapshot on the widget thread, then reads it under a
	// one-second deadline that starts after the snapshot is produced.
	snapshot := m.statuslineState()
	token := read.token
	ctx := read.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	request := StatuslineProviderResourceRequest{
		ThreadID: read.threadID,
		Server:   read.server,
		URI:      read.uri,
		Snapshot: snapshot,
	}
	return func() bubbletea.Msg {
		readCtx, cancel := context.WithTimeout(ctx, statuslineProviderReadDeadline)
		defer cancel()
		started := time.Now()
		contents, err := readResource(readCtx, request)
		// A reply can arrive just after the deadline; Rust treats that as a
		// timeout so a slow provider cannot keep the footer on stale content.
		if err == nil && time.Since(started) >= statuslineProviderReadDeadline {
			err = errStatuslineProviderReadTimeout
		}
		return statuslineProviderReadMsg{
			token:    token,
			threadID: request.ThreadID,
			server:   request.Server,
			contents: contents,
			err:      err,
		}
	}
}

// statuslineProviderPollCmd schedules the next read one second after a
// successful one (Rust #61981). The delay runs as a bubbletea command so the
// next read captures the then-current statusline state.
func (m *Model) statuslineProviderPollCmd(read *activeStatuslineProviderRead) bubbletea.Cmd {
	if m == nil || read == nil || m.onReadStatuslineProvider == nil {
		return nil
	}
	token := read.token
	return bubbletea.Tick(statuslineProviderPollInterval, func(time.Time) bubbletea.Msg {
		return statuslineProviderPollMsg{token: token}
	})
}

// applyStatuslineProviderPoll starts the read that follows a successful poll
// using the state the thread has right now (Rust rebuilds the snapshot for
// every read).
func (m *Model) applyStatuslineProviderPoll(msg statuslineProviderPollMsg) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	read := m.statuslineProviderRead
	if read == nil || msg.token != read.token {
		return nil
	}
	if !read.isCurrent(m) {
		m.syncProviderFooterSelection()
		return m.ensureProviderFooterCmd()
	}
	return m.statuslineProviderReadCmd(read)
}

// applyStatuslineProviderResolve continues a read once the provider is known:
// Rust snapshots the widget state on the main thread and then reads the
// resource.
func (m *Model) applyStatuslineProviderResolve(msg statuslineProviderResolveMsg) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	read := m.statuslineProviderRead
	if read == nil || msg.token != read.token || msg.threadID != read.threadID ||
		msg.server != read.server || msg.tool != read.tool {
		return nil
	}
	if !read.isCurrent(m) {
		m.syncProviderFooterSelection()
		return m.ensureProviderFooterCmd()
	}
	if msg.err != nil || !msg.provider.Found {
		read.failed = true
		m.setProviderFooterFailure(statuslineProviderUnavailableCopy)
		return nil
	}
	if title := strings.TrimSpace(msg.provider.Title); title != "" {
		read.name = title
	}
	read.uri = strings.TrimSpace(msg.provider.URI)
	return m.statuslineProviderReadCmd(read)
}

// applyStatuslineProviderRead installs the provider's UI tree, or the Rust
// failure copy when the response is missing, malformed, or for another backend.
// A successful read schedules the next poll (Rust #61981).
func (m *Model) applyStatuslineProviderRead(msg statuslineProviderReadMsg) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	read := m.statuslineProviderRead
	if read == nil || msg.token != read.token || msg.threadID != read.threadID ||
		msg.server != read.server {
		return nil
	}
	if !read.isCurrent(m) {
		m.syncProviderFooterSelection()
		return m.ensureProviderFooterCmd()
	}
	if msg.err != nil {
		read.failed = true
		m.setProviderFooterFailure(statuslineProviderReadFailureCopy(msg.err))
		return nil
	}
	tree, err := decodeStatuslineProviderTree(read.uri, msg.contents)
	if err != nil {
		read.failed = true
		m.setProviderFooterFailure(statuslineProviderFailedCopy)
		return nil
	}
	m.SetExtensionStatusLine(&tree)
	return m.statuslineProviderPollCmd(read)
}

// decodeStatuslineProviderTree mirrors Rust's decode_tree: exactly one text
// content, an advertised URI match, an application/json media type, and a
// parseable UI tree.
func decodeStatuslineProviderTree(uri string, contents []StatuslineProviderContent) (bottompane.StatusLine, error) {
	if len(contents) != 1 {
		return bottompane.StatusLine{}, errors.New("expected one text resource")
	}
	content := contents[0]
	if !content.IsText || strings.TrimSpace(content.MIMEType) == "" {
		return bottompane.StatusLine{}, errors.New("expected one text resource")
	}
	mediaType := strings.TrimSpace(strings.SplitN(content.MIMEType, ";", 2)[0])
	if uri != content.URI || !strings.EqualFold(mediaType, "application/json") {
		return bottompane.StatusLine{}, errors.New("expected application/json at the advertised resource URI")
	}
	line, err := bottompane.StatusLineFromUITreeJSON([]byte(content.Text))
	if err != nil {
		return bottompane.StatusLine{}, err
	}
	return line, nil
}

func (m *Model) setProviderFooterText(text string) {
	if m == nil {
		return
	}
	encoded, err := json.Marshal(text)
	if err != nil {
		return
	}
	line, err := bottompane.StatusLineFromUITreeJSON(encoded)
	if err != nil {
		return
	}
	m.statusLineExtension = &line
}

// statuslineProviderReadFailureCopy mirrors Rust's ReadIssue to footer mapping:
// a read that misses its one-second deadline reports a timeout, every other
// transport, RPC, or response failure is a generic status line failure.
func statuslineProviderReadFailureCopy(err error) string {
	if errors.Is(err, errStatuslineProviderReadTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return statuslineProviderTimedOutCopy
	}
	return statuslineProviderFailedCopy
}

func (m *Model) setProviderFooterFailure(message string) {
	m.setProviderFooterText(message)
}

// statuslineProviderServerReady wakes a pending lookup when the selected MCP
// server finishes starting (Rust ChatWidget::server_ready).
func (m *Model) statuslineProviderServerReady(server string) {
	if m == nil {
		return
	}
	read := m.statuslineProviderRead
	if read == nil || read.server != server {
		return
	}
	m.ensureStatuslineReadyGate().signal()
}

func (m *Model) ensureStatuslineReadyGate() *statuslineReadyGate {
	if m.statuslineProviderReady == nil {
		m.statuslineProviderReady = newStatuslineReadyGate()
	}
	return m.statuslineProviderReady
}
