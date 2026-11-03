// Package fakeappserver is a deterministic, dependency-free stand-in for the
// Codex app server used by the Linux real-terminal tests. It speaks the same
// JSON-RPC-over-websocket protocol on a unix socket, so a real TUI process can
// be driven through resume, locked-view and fork paths without network access,
// credentials or a real backend.
//
// It is intentionally small: only the methods the TUI calls on startup and on
// the resume/fork paths are answered. Every request and response is written to
// the log so a test can prove *when* a request was sent and how many reached
// the server.
package fakeappserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Options configures a Server. The zero value is usable: a temporary unix
// socket, a single resumable thread and an instant successful fork.
type Options struct {
	// SocketPath is the unix socket to listen on. Empty picks a temporary path.
	SocketPath string
	// LogPath appends request/response lines to this file. Empty logs to
	// io.Discard (set LogWriter instead to capture them in-process).
	LogPath string
	// LogWriter receives the same lines as LogPath when set.
	LogWriter io.Writer
	// ForkDelay sleeps before answering thread/fork, so a caller can observe
	// the pending state of a real TUI.
	ForkDelay time.Duration
	// ForkFail answers thread/fork with a JSON-RPC error.
	ForkFail bool
	// ForkClose drops the connection instead of answering thread/fork, which
	// simulates an app server disappearing mid-request.
	ForkClose bool
	// ThreadID is the resumable thread served by thread/list. Empty uses a
	// fixed default.
	ThreadID string
	// ForkID is the thread id returned by thread/fork.
	ForkID string
	// ReadOnly makes thread/resume fail with the external-writer error the TUI
	// renders as the locked view ("already has an active writer").
	ReadOnly bool
}

const (
	defaultThreadID = "01910000-0000-7000-8000-000000000001"
	defaultForkID   = "01910000-0000-7000-8000-000000000002"
)

// Server is a running fake app server.
type Server struct {
	opts    Options
	socket  string
	ln      net.Listener
	srv     *http.Server
	logFile *os.File
	logOut  io.Writer
	logMu   sync.Mutex
	forks   atomic.Int32
	lastCWD atomic.Value
	done    chan struct{}
}

// New starts a fake app server. Call Close when done.
func New(opts Options) (*Server, error) {
	if opts.ThreadID == "" {
		opts.ThreadID = defaultThreadID
	}
	if opts.ForkID == "" {
		opts.ForkID = defaultForkID
	}
	s := &Server{opts: opts, socket: opts.SocketPath, done: make(chan struct{})}
	switch {
	case opts.LogWriter != nil:
		s.logOut = opts.LogWriter
	case opts.LogPath != "":
		f, err := os.OpenFile(opts.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		s.logFile = f
		s.logOut = f
	}
	s.lastCWD.Store("/tmp")
	if s.socket == "" {
		dir, err := os.MkdirTemp("", "fakeappserver")
		if err != nil {
			return nil, err
		}
		s.socket = dir + "/app.sock"
	}
	// A stale socket from a previous run would make Listen fail with
	// "address already in use"; the path is ours, so remove it first.
	_ = os.Remove(s.socket)
	ln, err := net.Listen("unix", s.socket)
	if err != nil {
		s.closeLog()
		return nil, err
	}
	s.ln = ln
	s.srv = &http.Server{Handler: http.HandlerFunc(s.handleConn)}
	go func() {
		defer close(s.done)
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logf("serve end: %v", err)
		}
	}()
	s.logf("listening on %s delay=%s", s.socket, s.opts.ForkDelay)
	return s, nil
}

// SocketPath is the unix socket the TUI should be pointed at with
// `--remote unix://<path>`.
func (s *Server) SocketPath() string { return s.socket }

// ForkCount reports how many thread/fork requests reached the server.
func (s *Server) ForkCount() int { return int(s.forks.Load()) }

// Close stops serving and removes the socket.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var err error
	if s.srv != nil {
		err = s.srv.Shutdown(ctx)
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
	_ = os.Remove(s.socket)
	s.closeLog()
	return err
}

func (s *Server) closeLog() {
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
}

func (s *Server) logf(format string, args ...any) {
	if s.logOut == nil {
		return
	}
	s.logMu.Lock()
	defer s.logMu.Unlock()
	fmt.Fprintf(s.logOut, "%s "+format+"\n", append([]any{time.Now().Format("15:04:05.000")}, args...)...)
	if f, ok := s.logOut.(*os.File); ok {
		_ = f.Sync()
	}
}

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcErr struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
}

func (s *Server) threadSnap() map[string]any {
	now := time.Now().Unix()
	cwd, _ := s.lastCWD.Load().(string)
	return map[string]any{
		"id":            s.opts.ThreadID,
		"sessionId":     s.opts.ThreadID,
		"preview":       "demo thread for linux harness",
		"ephemeral":     false,
		"historyMode":   "legacy",
		"modelProvider": "openai",
		"model":         "gpt-5",
		"createdAt":     now - 600,
		"updatedAt":     now - 60,
		"status":        map[string]any{"type": "idle"},
		"cwd":           cwd,
		"cliVersion":    "0.0.0-fake",
		"source":        "cli",
		"turns": []any{
			map[string]any{
				"id": "turn-1", "status": "completed",
				"items": []any{
					map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "text", "text": "hello from fake history"}}},
					map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": "fake assistant reply"}}},
				},
			},
		},
	}
}

func (s *Server) forkThread() map[string]any {
	t := s.threadSnap()
	t["id"] = s.opts.ForkID
	t["sessionId"] = s.opts.ForkID
	t["preview"] = "forked demo thread"
	t["forkedFromId"] = s.opts.ThreadID
	return t
}

// resultFor answers one request. forkClose reports that the caller must drop
// the connection instead of writing a response.
func (s *Server) resultFor(method string, params json.RawMessage) (any, *rpcErr, bool) {
	switch method {
	case "initialize":
		return map[string]any{
			"userAgent": "codex_cli_rs/0.0.0",
			"codexHome": "/tmp/fakehome",
			"events":    []any{},
		}, nil, false
	case "account/read":
		return map[string]any{
			"account":            map[string]any{"type": "chatgpt", "email": "harness@example.com", "planType": "plus", "workspaceName": nil},
			"requiresOpenaiAuth": false,
			"workspaceRouting":   nil,
		}, nil, false
	case "account/rateLimits/read":
		return map[string]any{
			"rateLimits":            map[string]any{"primary": map[string]any{"usedPercent": 12, "windowDurationMins": 300, "resetsAt": time.Now().Add(2 * time.Hour).Unix()}},
			"rateLimitsByLimitId":   map[string]any{},
			"rateLimitResetCredits": nil,
			"ordinaryUsageAllowed":  true,
		}, nil, false
	case "account/usage/read":
		now := time.Now().UTC()
		var buckets []any
		for i := 13; i >= 0; i-- {
			buckets = append(buckets, map[string]any{
				"startDate": now.AddDate(0, 0, -i).Format("2006-01-02"),
				"tokens":    1000 + i*370,
			})
		}
		return map[string]any{
			"summary": map[string]any{
				"lifetimeTokens":        123456,
				"peakDailyTokens":       5000,
				"longestRunningTurnSec": 300,
				"currentStreakDays":     4,
				"longestStreakDays":     9,
			},
			"dailyUsageBuckets": buckets,
		}, nil, false
	case "thread/list":
		var lp struct {
			CWD string `json:"cwd"`
		}
		_ = json.Unmarshal(params, &lp)
		if lp.CWD != "" {
			s.lastCWD.Store(lp.CWD)
		}
		return map[string]any{
			"data":       []any{s.threadSnap()},
			"nextCursor": nil,
			"readStates": map[string]any{},
		}, nil, false
	case "thread/start":
		// A fresh remote session learns its thread id here; without it `/fork`
		// (and the command-center fork) has no source thread to fork.
		thread := s.threadSnap()
		delete(thread, "turns")
		return map[string]any{
			"thread":        thread,
			"model":         "gpt-5",
			"modelProvider": "openai",
			"cwd":           s.currentCWD(),
		}, nil, false
	case "thread/read":
		var rp struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(params, &rp)
		if rp.ThreadID == s.opts.ForkID {
			return map[string]any{"thread": s.forkThread()}, nil, false
		}
		return map[string]any{"thread": s.threadSnap()}, nil, false
	case "thread/resume":
		if s.opts.ReadOnly {
			return nil, &rpcErr{Code: -32000, Message: fmt.Sprintf("thread %s already has an active writer", s.opts.ThreadID)}, false
		}
		var rp struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(params, &rp)
		thread := s.threadSnap()
		if rp.ThreadID == s.opts.ForkID {
			thread = s.forkThread()
			delete(thread, "turns")
		}
		return map[string]any{"thread": thread, "model": "gpt-5", "modelProvider": "openai"}, nil, false
	case "thread/fork":
		n := s.forks.Add(1)
		var fp struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(params, &fp)
		s.logf("thread/fork #%d parent=%s", n, fp.ThreadID)
		if s.opts.ForkDelay > 0 {
			time.Sleep(s.opts.ForkDelay)
		}
		if s.opts.ForkClose {
			return nil, nil, true
		}
		if s.opts.ForkFail {
			return nil, &rpcErr{Code: -32000, Message: "fork failed: simulated backend error"}, false
		}
		s.logf("thread/fork #%d answered -> %s", n, s.opts.ForkID)
		return map[string]any{
			"thread":        s.forkThread(),
			"model":         "gpt-5",
			"modelProvider": "openai",
			"cwd":           s.currentCWD(),
		}, nil, false
	case "thread/turns/list", "skills/list", "apps/list", "experimentalFeature/list", "plugin/list", "mcpServer/list":
		return map[string]any{"data": []any{}}, nil, false
	}
	return map[string]any{}, nil, false
}

func (s *Server) currentCWD() string {
	cwd, _ := s.lastCWD.Load().(string)
	return cwd
}

func (s *Server) handleConn(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		s.logf("accept error: %v", err)
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := r.Context()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			s.logf("read end: %v", err)
			return
		}
		var msg rpcMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			s.logf("bad json: %v", err)
			continue
		}
		if msg.Method == "" || len(msg.ID) == 0 {
			continue // a response to a server request, or a notification
		}
		s.logf("recv method=%q id=%s params=%s", msg.Method, string(msg.ID), truncate(string(msg.Params)))
		res, rerr, drop := s.resultFor(msg.Method, msg.Params)
		if drop {
			s.logf("dropping connection during %q", msg.Method)
			_ = c.Close(websocket.StatusGoingAway, "simulated app-server disconnect")
			return
		}
		out := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID)}
		if rerr != nil {
			out["error"] = rerr
		} else {
			out["result"] = res
		}
		blob, _ := json.Marshal(out)
		s.logf("send  method=%q ok=%v", msg.Method, rerr == nil)
		if err := c.Write(ctx, websocket.MessageText, blob); err != nil {
			s.logf("write error: %v", err)
			return
		}
	}
}

func truncate(s string) string {
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}
