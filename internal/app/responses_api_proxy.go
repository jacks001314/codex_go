package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"codex_go/internal/cli"
)

const responsesAPIProxyAuthHeaderBufferSize = 1024

var responsesAPIProxyExit = os.Exit

type responsesAPIProxyServer struct {
	opts        *cli.ResponsesAPIProxyOptions
	authHeader  string
	upstreamURL *url.URL
	httpClient  *http.Client
	dumper      *responsesAPIProxyDumper
	stderr      io.Writer
	server      *http.Server
	nextDumpSeq atomic.Uint64
}

type responsesAPIProxyServerInfo struct {
	Port int `json:"port"`
	PID  int `json:"pid"`
}

type responsesAPIProxyDump struct {
	Method  string                    `json:"method,omitempty"`
	URL     string                    `json:"url,omitempty"`
	Status  int                       `json:"status,omitempty"`
	Headers []responsesAPIProxyHeader `json:"headers"`
	Body    any                       `json:"body"`
}

type responsesAPIProxyHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type responsesAPIProxyDumper struct {
	dir     string
	nextSeq *atomic.Uint64
}

type responsesAPIProxyExchangeDump struct {
	responsePath string
}

func runResponsesAPIProxy(ctx context.Context, opts *cli.ResponsesAPIProxyOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	server, err := newResponsesAPIProxyServer(opts, stdin)
	if err != nil {
		return err
	}
	return server.Run(ctx, stdout, stderr)
}

func newResponsesAPIProxyServer(opts *cli.ResponsesAPIProxyOptions, stdin io.Reader) (*responsesAPIProxyServer, error) {
	if opts == nil {
		opts = &cli.ResponsesAPIProxyOptions{}
	}
	authHeader, err := readResponsesAPIProxyAuthHeader(stdin)
	if err != nil {
		return nil, err
	}
	upstreamValue := firstNonEmptyLocal(opts.UpstreamURL, "https://api.openai.com/v1/responses")
	upstreamURL, err := url.Parse(upstreamValue)
	if err != nil {
		return nil, fmt.Errorf("parsing --upstream-url: %w", err)
	}
	if upstreamURL.Scheme == "" {
		return nil, errors.New("parsing --upstream-url: relative URL without a base")
	}
	if upstreamURL.Host == "" {
		return nil, errors.New("upstream URL must include a host")
	}
	proxy := &responsesAPIProxyServer{
		opts:        opts,
		authHeader:  authHeader,
		upstreamURL: upstreamURL,
		httpClient:  &http.Client{Timeout: 0},
	}
	if strings.TrimSpace(opts.DumpDir) != "" {
		if err := os.MkdirAll(opts.DumpDir, 0o755); err != nil {
			return nil, err
		}
		proxy.dumper = &responsesAPIProxyDumper{dir: opts.DumpDir, nextSeq: &proxy.nextDumpSeq}
	}
	return proxy, nil
}

func (s *responsesAPIProxyServer) Run(ctx context.Context, stdout, stderr io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.stderr = stderr
	port := 0
	if s.opts != nil && s.opts.Port != nil {
		port = int(*s.opts.Port)
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	tcpAddr, _ := listener.Addr().(*net.TCPAddr)
	info := &responsesAPIProxyServerInfo{PID: os.Getpid()}
	if tcpAddr != nil {
		info.Port = tcpAddr.Port
	}
	if s.opts != nil && strings.TrimSpace(s.opts.ServerInfo) != "" {
		data, err := json.Marshal(info)
		if err != nil {
			_ = listener.Close()
			return err
		}
		if err := writeFileCreatingParent(s.opts.ServerInfo, append(data, '\n')); err != nil {
			_ = listener.Close()
			return err
		}
	}
	fmt.Fprintf(stderr, "responses-api-proxy listening on 127.0.0.1:%d\n", info.Port)

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	s.server = &http.Server{Handler: mux}
	done := make(chan error, 1)
	go func() {
		err := s.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.server.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (s *responsesAPIProxyServer) handle(w http.ResponseWriter, r *http.Request) {
	if s.opts != nil && s.opts.HTTPShutdown && r.Method == http.MethodGet && r.URL.Path == "/shutdown" {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		go func() {
			responsesAPIProxyExit(0)
			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = s.server.Shutdown(shutdownCtx)
		}()
		return
	}
	if r.Method != http.MethodPost || r.URL.RequestURI() != "/v1/responses" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if err := s.forward(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
	}
}

func (s *responsesAPIProxyServer) forward(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	var exchange *responsesAPIProxyExchangeDump
	if s.dumper != nil {
		var dumpErr error
		exchange, dumpErr = s.dumper.DumpRequest(r, body)
		if dumpErr != nil {
			s.logf("responses-api-proxy failed to dump request: %v\n", dumpErr)
		}
	}
	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.upstreamURL.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	copyForwardHeaders(upstreamReq.Header, r.Header)
	upstreamReq.Header.Set("Authorization", s.authHeader)
	upstreamReq.Host = s.upstreamURL.Host
	response, err := s.httpClient.Do(upstreamReq)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	copyResponseHeaders(w.Header(), response.Header)
	if response.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(response.ContentLength, 10))
	}
	w.WriteHeader(response.StatusCode)
	writer := io.Writer(w)
	if flusher, ok := w.(http.Flusher); ok {
		writer = &flushWriter{writer: w, flusher: flusher}
	}
	var responseBody bytes.Buffer
	dumpWritten := false
	if exchange != nil {
		defer func() {
			if dumpWritten {
				return
			}
			if dumpErr := exchange.DumpResponse(response, responseBody.Bytes()); dumpErr != nil {
				s.logf("responses-api-proxy failed to write %s: %v\n", exchange.responsePath, dumpErr)
			}
		}()
	}
	if exchange != nil {
		writer = io.MultiWriter(writer, &responseBody)
	}
	_, err = io.Copy(writer, response.Body)
	if exchange != nil {
		dumpWritten = true
		if dumpErr := exchange.DumpResponse(response, responseBody.Bytes()); dumpErr != nil {
			s.logf("responses-api-proxy failed to write %s: %v\n", exchange.responsePath, dumpErr)
		}
	}
	return err
}

func (s *responsesAPIProxyServer) logf(format string, args ...any) {
	if s != nil && s.stderr != nil {
		fmt.Fprintf(s.stderr, format, args...)
	}
}

type flushWriter struct {
	writer  io.Writer
	flusher http.Flusher
}

func (w *flushWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	w.flusher.Flush()
	return n, err
}

func readResponsesAPIProxyAuthHeader(stdin io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(stdin, responsesAPIProxyAuthHeaderBufferSize+1))
	if err != nil {
		return "", err
	}
	sawNewline := false
	if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
		data = data[:idx+1]
		sawNewline = true
	}
	capacity := responsesAPIProxyAuthHeaderBufferSize - len("Bearer ")
	if len(data) >= capacity && !sawNewline {
		return "", fmt.Errorf("API key is too large to fit in the %d-byte buffer", responsesAPIProxyAuthHeaderBufferSize)
	}
	for len(data) > 0 && (data[len(data)-1] == '\n' || data[len(data)-1] == '\r') {
		data = data[:len(data)-1]
	}
	if len(data) == 0 {
		return "", errors.New("API key must be provided via stdin (e.g. printenv OPENAI_API_KEY | codex responses-api-proxy)")
	}
	for _, b := range data {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
			return "", errors.New("API key may only contain ASCII letters, numbers, '-' or '_'")
		}
	}
	return "Bearer " + string(data), nil
}

func copyForwardHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		lower := strings.ToLower(key)
		if lower == "authorization" || lower == "host" {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyResponseHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		if responsesAPIProxyManagedResponseHeader(strings.ToLower(key)) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func responsesAPIProxyManagedResponseHeader(lower string) bool {
	switch lower {
	case "content-length", "transfer-encoding", "connection", "trailer", "upgrade":
		return true
	default:
		return false
	}
}

func (d *responsesAPIProxyDumper) DumpRequest(r *http.Request, body []byte) (*responsesAPIProxyExchangeDump, error) {
	if d == nil {
		return nil, nil
	}
	seq := d.nextSeq.Add(1)
	prefix := fmt.Sprintf("%06d-%d", seq, time.Now().UnixMilli())
	requestPath := filepath.Join(d.dir, prefix+"-request.json")
	responsePath := filepath.Join(d.dir, prefix+"-response.json")
	dump := &responsesAPIProxyDump{
		Method:  r.Method,
		URL:     r.URL.RequestURI(),
		Headers: dumpHeaders(r.Header),
		Body:    dumpBody(body),
	}
	if err := writeJSONDump(requestPath, dump); err != nil {
		return nil, err
	}
	return &responsesAPIProxyExchangeDump{responsePath: responsePath}, nil
}

func (d *responsesAPIProxyExchangeDump) DumpResponse(response *http.Response, body []byte) error {
	if d == nil {
		return nil
	}
	dump := &responsesAPIProxyDump{
		Status:  response.StatusCode,
		Headers: dumpHeaders(response.Header),
		Body:    dumpBody(body),
	}
	return writeJSONDump(d.responsePath, dump)
}

func dumpHeaders(headers http.Header) []responsesAPIProxyHeader {
	out := make([]responsesAPIProxyHeader, 0, len(headers))
	for key, values := range headers {
		for _, value := range values {
			if shouldRedactResponsesAPIProxyHeader(key) {
				value = "[REDACTED]"
			}
			out = append(out, responsesAPIProxyHeader{Name: key, Value: value})
		}
	}
	return out
}

func shouldRedactResponsesAPIProxyHeader(name string) bool {
	lower := strings.ToLower(name)
	return lower == "authorization" || strings.Contains(lower, "cookie")
}

func dumpBody(body []byte) any {
	var value any
	if err := json.Unmarshal(body, &value); err == nil {
		return value
	}
	return string(body)
}

func writeJSONDump(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}
