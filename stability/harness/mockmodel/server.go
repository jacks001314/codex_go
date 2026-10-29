// Package mockmodel provides a deterministic in-process stand-in for the
// OpenAI Responses API. Stability scenarios use it to hold a stream at a
// specific chunk, inject transport faults, and capture request bodies without
// touching a real model.
package mockmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrAborted reports that a gate was aborted before it was released.
var ErrAborted = errors.New("mockmodel: gated chunk aborted")

// Gate blocks one chunk until Release or Abort is called.
type Gate struct {
	ch      chan struct{}
	once    sync.Once
	aborted atomic.Bool
}

// NewGate returns a closed-later gate.
func NewGate() *Gate { return &Gate{ch: make(chan struct{})} }

// Release unblocks the gate. It is safe to call more than once.
func (g *Gate) Release() { g.once.Do(func() { close(g.ch) }) }

// Abort unblocks the gate and reports ErrAborted to the waiting writer.
func (g *Gate) Abort() {
	g.aborted.Store(true)
	g.Release()
}

// Wait blocks until the gate is released, aborted, or the context ends.
func (g *Gate) Wait(ctx context.Context) error {
	select {
	case <-g.ch:
		if g.aborted.Load() {
			return ErrAborted
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Chunk is one SSE frame.
type Chunk struct {
	Data  string
	Event string
	Delay time.Duration
	Gate  *Gate
}

// Response is one scripted HTTP response.
type Response struct {
	Status      int
	Headers     map[string]string
	HeaderDelay time.Duration
	Chunks      []Chunk
	// PlainText is used for non-streaming requests.
	PlainText string
	// AbortAfter writes this many chunks and then closes the connection.
	AbortAfter int
	// Partial writes a prefix of the chunk at PartialAt and then closes.
	Partial      bool
	PartialAt    int
	PartialBytes int
	Body         []byte
	// Stream writes the SSE body directly. Multi-megabyte fixtures use it so
	// the harness never keeps its own copy of the payload: scenarios measure
	// the client's heap, not the fixture's. AbortAfter/Partial/GateAt do not
	// apply to streamed responses.
	Stream func(w io.Writer, flush func()) error
}

// Request is one captured request.
type Request struct {
	Method string      `json:"method"`
	Path   string      `json:"path"`
	Header http.Header `json:"header"`
	Body   []byte      `json:"body"`
}

// Server serves scripted Responses API requests.
type Server struct {
	http      *httptest.Server
	mu        sync.Mutex
	responses []*Response
	// requests keeps only the most recent maxCaptured bodies; a 24h soak
	// serves millions of requests and must not grow the harness heap.
	requests     []Request
	requestCount int
	maxCaptured  int
	fallback     *Response
	connOpened   int
	connClosed   int
	connActive   int
}

// maxCapturedRequests bounds the request-capture window.
const maxCapturedRequests = 256

// New starts a server with the given script. Requests beyond the script use a
// successful "ok" response so background calls cannot stall a scenario.
func New(responses ...*Response) *Server {
	server := &Server{fallback: Text("ok"), maxCaptured: maxCapturedRequests}
	server.responses = append(server.responses, responses...)
	unstarted := httptest.NewUnstartedServer(http.HandlerFunc(server.handle))
	unstarted.Config.ConnState = server.trackConnState
	unstarted.Start()
	server.http = unstarted
	return server
}

// ConnStats is a snapshot of the connections the fixture has served.
type ConnStats struct {
	Opened int
	Closed int
	Active int
}

// ConnStats reports connection lifecycle counts. A scenario that abandons
// streams can use it to prove the connection was actually reclaimed, which is
// a sharper signal than a process-wide handle count.
func (s *Server) ConnStats() ConnStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ConnStats{Opened: s.connOpened, Closed: s.connClosed, Active: s.connActive}
}

func (s *Server) trackConnState(_ net.Conn, state http.ConnState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch state {
	case http.StateNew:
		s.connOpened++
		s.connActive++
	case http.StateClosed, http.StateHijacked:
		s.connClosed++
		s.connActive--
	}
}

// URL returns the base URL of the server.
func (s *Server) URL() string { return s.http.URL }

// Fallback replaces the response used once the script is exhausted, so a
// long-running scenario can serve the same payload for thousands of turns
// without enqueueing thousands of scripts.
func (s *Server) Fallback(response *Response) *Server {
	if response == nil {
		return s
	}
	s.mu.Lock()
	s.fallback = response
	s.mu.Unlock()
	return s
}

// Close shuts the server down.
func (s *Server) Close() { s.http.Close() }

// Enqueue appends a response to the script.
func (s *Server) Enqueue(response *Response) {
	if response == nil {
		return
	}
	s.mu.Lock()
	s.responses = append(s.responses, response)
	s.mu.Unlock()
}

// Requests returns a copy of the captured requests.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	for index := range s.requests {
		out[index] = s.requests[index]
		out[index].Header = s.requests[index].Header.Clone()
		out[index].Body = append([]byte(nil), s.requests[index].Body...)
	}
	return out
}

// RequestCount reports how many requests were captured.
func (s *Server) RequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requestCount
}

// RequestBytes reports how many request-body bytes the capture buffer holds.
// It exists so a soak test can tell a client leak apart from the harness
// remembering every request it served.
func (s *Server) RequestBytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, request := range s.requests {
		total += len(request.Body)
	}
	return total
}

// LastRequest returns the most recent captured request.
func (s *Server) LastRequest() (Request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return Request{}, false
	}
	last := s.requests[len(s.requests)-1]
	last.Header = last.Header.Clone()
	last.Body = append([]byte(nil), last.Body...)
	return last, true
}

func (s *Server) next() *Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.responses) == 0 {
		return nil
	}
	response := s.responses[0]
	s.responses = s.responses[1:]
	return response
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	request := Request{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body}
	s.mu.Lock()
	s.requests = append(s.requests, request)
	s.requestCount++
	if limit := s.maxCaptured; limit > 0 && len(s.requests) > limit {
		s.requests = append(s.requests[:0], s.requests[len(s.requests)-limit:]...)
	}
	s.mu.Unlock()

	if strings.HasSuffix(r.URL.Path, "/models") {
		writeJSON(w, http.StatusOK, map[string]any{"models": []any{}})
		return
	}

	response := s.next()
	if response == nil {
		s.mu.Lock()
		response = s.fallback
		s.mu.Unlock()
	}
	if response == nil {
		http.Error(w, "mockmodel: no response scripted", http.StatusInternalServerError)
		return
	}
	if response.Status != 0 && response.Status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.Status)
		_, _ = w.Write(response.Body)
		return
	}
	if response.HeaderDelay > 0 && !sleepContext(r.Context(), response.HeaderDelay) {
		return
	}
	for name, value := range response.Headers {
		w.Header().Set(name, value)
	}

	if !isStreamRequest(body) {
		payload := response.Body
		if len(payload) == 0 {
			payload = []byte(nonStreamBody(response.PlainText))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	flush := func() {}
	if flusher != nil {
		flush = flusher.Flush
	}
	if response.Stream != nil {
		if err := response.Stream(w, flush); err != nil {
			return
		}
		return
	}
	for index, chunk := range response.Chunks {
		if response.AbortAfter > 0 && index == response.AbortAfter {
			panic(http.ErrAbortHandler)
		}
		if response.Partial && index == response.PartialAt {
			frame := formatChunk(chunk)
			limit := response.PartialBytes
			if limit <= 0 || limit > len(frame) {
				limit = len(frame) / 2
			}
			_, _ = io.WriteString(w, frame[:limit])
			if flusher != nil {
				flusher.Flush()
			}
			panic(http.ErrAbortHandler)
		}
		if chunk.Delay > 0 && !sleepContext(r.Context(), chunk.Delay) {
			return
		}
		if chunk.Gate != nil {
			if err := chunk.Gate.Wait(r.Context()); err != nil {
				return
			}
		}
		if _, err := io.WriteString(w, formatChunk(chunk)); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// Text scripts a streaming assistant text response with small deltas.
func Text(text string) *Response {
	return TextWithChunkSize(text, 8)
}

// TextWithChunkSize is Text with an explicit delta size. Large payloads use a
// bigger chunk so the fixture does not emit millions of tiny SSE frames.
func TextWithChunkSize(text string, chunkRunes int) *Response {
	if chunkRunes <= 0 {
		chunkRunes = 8
	}
	sequence := responseSequence.Add(1)
	id := "resp-" + strconv.FormatUint(sequence, 10)
	itemID := "msg-" + strconv.FormatUint(sequence, 10)
	if len(text) > largePayloadThreshold {
		return &Response{PlainText: text, Stream: streamLargeText(id, itemID, text, chunkRunes)}
	}
	chunks := []Chunk{
		{Data: mustJSON(map[string]any{"type": "response.created", "response": map[string]any{"id": id}})},
		{Data: mustJSON(map[string]any{"type": "response.output_item.added", "item": messageItem(itemID, "")})},
	}
	for _, part := range splitRunes(text, chunkRunes) {
		chunks = append(chunks, Chunk{Data: mustJSON(map[string]any{"type": "response.output_text.delta", "delta": part})})
	}
	chunks = append(chunks,
		Chunk{Data: mustJSON(map[string]any{"type": "response.output_item.done", "item": messageItem(itemID, text)})},
		Chunk{Data: mustJSON(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id":     id,
				"output": []any{messageItem(itemID, text)},
				"usage":  map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
			},
		})},
	)
	return &Response{Chunks: chunks, PlainText: text}
}

// Error scripts a non-streaming HTTP error.
func Error(status int, body string) *Response {
	if body == "" {
		body = fmt.Sprintf(`{"error":{"message":"mock status %d"}}`, status)
	}
	return &Response{Status: status, Body: []byte(body)}
}

// AbortMidStream scripts a response that closes after the first delta.
func AbortMidStream(text string) *Response {
	response := Text(text)
	response.AbortAfter = 3
	return response
}

// PartialStream scripts a response that closes in the middle of a frame.
func PartialStream(text string) *Response {
	response := Text(text)
	response.Partial = true
	response.PartialAt = 2
	response.PartialBytes = 8
	return response
}

// GateAt installs a gate on one chunk index.
func (r *Response) GateAt(index int, gate *Gate) *Response {
	if r != nil && r.Stream != nil {
		panic("mockmodel: GateAt needs a chunked response; TextWithChunkSize streams payloads above 4 MiB")
	}
	if r != nil && index >= 0 && index < len(r.Chunks) {
		r.Chunks[index].Gate = gate
	}
	return r
}

// DelayAt installs a delay on one chunk index.
func (r *Response) DelayAt(index int, delay time.Duration) *Response {
	if r != nil && r.Stream != nil {
		panic("mockmodel: DelayAt needs a chunked response; TextWithChunkSize streams payloads above 4 MiB")
	}
	if r != nil && index >= 0 && index < len(r.Chunks) {
		r.Chunks[index].Delay = delay
	}
	return r
}

var responseSequence atomic.Uint64

func messageItem(id string, text string) map[string]any {
	return map[string]any{
		"id":   id,
		"type": "message",
		"role": "assistant",
		"content": []any{
			map[string]any{"type": "output_text", "text": text},
		},
	}
}

func splitRunes(text string, size int) []string {
	var out []string
	_ = forEachRuneChunk(text, size, func(part string) error {
		out = append(out, part)
		return nil
	})
	return out
}

// forEachRuneChunk calls fn with consecutive slices of at most size runes.
// Chunking by rune index instead of converting the whole string to []rune keeps
// the cost proportional to the payload instead of four times it.
func forEachRuneChunk(text string, size int, fn func(string) error) error {
	if text == "" {
		return nil
	}
	if size <= 0 {
		size = 1
	}
	start := 0
	count := 0
	for index := range text {
		if count == size && index > start {
			if err := fn(text[start:index]); err != nil {
				return err
			}
			start = index
			count = 0
		}
		count++
	}
	if start < len(text) {
		return fn(text[start:])
	}
	return nil
}

// largePayloadThreshold is the payload size above which TextWithChunkSize
// streams the SSE body. Pre-building the frames would keep several copies of a
// multi-megabyte item in the harness and inflate the heap the scenario is
// trying to measure on the client side.
const largePayloadThreshold = 4 << 20

func streamLargeText(id, itemID, text string, chunkRunes int) func(w io.Writer, flush func()) error {
	return func(w io.Writer, flush func()) error {
		if err := writeSSE(w, mustJSON(map[string]any{"type": "response.created", "response": map[string]any{"id": id}})); err != nil {
			return err
		}
		if err := writeSSE(w, mustJSON(map[string]any{"type": "response.output_item.added", "item": messageItem(itemID, "")})); err != nil {
			return err
		}
		flush()

		err := forEachRuneChunk(text, chunkRunes, func(part string) error {
			encoded, err := json.Marshal(part)
			if err != nil {
				return err
			}
			if _, err := io.WriteString(w, `data: {"type":"response.output_text.delta","delta":`); err != nil {
				return err
			}
			if _, err := w.Write(encoded); err != nil {
				return err
			}
			if _, err := io.WriteString(w, "}\n\n"); err != nil {
				return err
			}
			flush()
			return nil
		})
		if err != nil {
			return err
		}

		// The whole item arrives in one data line, exactly like the provider's
		// response.output_item.done frame.
		if _, err := io.WriteString(w, `data: {"type":"response.output_item.done","item":{"id":`); err != nil {
			return err
		}
		if err := writeJSONString(w, itemID); err != nil {
			return err
		}
		if _, err := io.WriteString(w, `,"type":"message","role":"assistant","content":[{"type":"output_text","text":`); err != nil {
			return err
		}
		if err := writeJSONStringChunks(w, text, chunkRunes); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "}]}}\n\n"); err != nil {
			return err
		}
		flush()

		if _, err := io.WriteString(w, `data: {"type":"response.completed","response":{"id":`); err != nil {
			return err
		}
		if err := writeJSONString(w, id); err != nil {
			return err
		}
		if _, err := io.WriteString(w, `,"output":[{"id":`); err != nil {
			return err
		}
		if err := writeJSONString(w, itemID); err != nil {
			return err
		}
		if _, err := io.WriteString(w, `,"type":"message","role":"assistant","content":[{"type":"output_text","text":`); err != nil {
			return err
		}
		if err := writeJSONStringChunks(w, text, chunkRunes); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"); err != nil {
			return err
		}
		flush()
		return nil
	}
}

func writeSSE(w io.Writer, data string) error {
	if _, err := io.WriteString(w, "data: "); err != nil {
		return err
	}
	if _, err := io.WriteString(w, data); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n\n")
	return err
}

func writeJSONString(w io.Writer, value string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = w.Write(encoded)
	return err
}

// writeJSONStringChunks writes a JSON string literal without materialising an
// escaped copy of the whole payload: each rune chunk is escaped on its own and
// the quoted fragments concatenate into one valid string.
func writeJSONStringChunks(w io.Writer, text string, chunkRunes int) error {
	if _, err := io.WriteString(w, "\""); err != nil {
		return err
	}
	err := forEachRuneChunk(text, chunkRunes, func(part string) error {
		encoded, err := json.Marshal(part)
		if err != nil {
			return err
		}
		_, err = w.Write(encoded[1 : len(encoded)-1])
		return err
	})
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "\"")
	return err
}

func formatChunk(chunk Chunk) string {
	var builder strings.Builder
	if chunk.Event != "" {
		builder.WriteString("event: ")
		builder.WriteString(chunk.Event)
		builder.WriteString("\n")
	}
	builder.WriteString("data: ")
	builder.WriteString(chunk.Data)
	builder.WriteString("\n\n")
	return builder.String()
}

func nonStreamBody(text string) string {
	return mustJSON(map[string]any{
		"id":     "resp-mock",
		"model":  "gpt-mock",
		"output": []any{messageItem("msg-mock", text)},
		"usage":  map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
	})
}

func isStreamRequest(body []byte) bool {
	var payload struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	return payload.Stream
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
