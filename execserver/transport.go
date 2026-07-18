package execserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const DefaultListenURL = "ws://127.0.0.1:0"

type ListenKind string

const (
	ListenKindWebSocket ListenKind = "websocket"
	ListenKindStdio     ListenKind = "stdio"
)

type ListenTransport struct {
	Kind    ListenKind
	Address string
}

func ParseListenURL(listenURL string) (ListenTransport, error) {
	if listenURL == "stdio" || listenURL == "stdio://" {
		return ListenTransport{Kind: ListenKindStdio}, nil
	}
	if strings.HasPrefix(listenURL, "ws://") {
		address := strings.TrimPrefix(listenURL, "ws://")
		host, portText, err := net.SplitHostPort(address)
		if err != nil {
			return ListenTransport{}, invalidWebSocketListenURLError(listenURL)
		}
		ip := net.ParseIP(host)
		port, err := strconv.Atoi(portText)
		if ip == nil || err != nil || port < 0 || port > 65535 {
			return ListenTransport{}, invalidWebSocketListenURLError(listenURL)
		}
		return ListenTransport{
			Kind:    ListenKindWebSocket,
			Address: net.JoinHostPort(ip.String(), strconv.Itoa(port)),
		}, nil
	}
	return ListenTransport{}, fmt.Errorf("unsupported --listen URL `%s`; expected `ws://IP:PORT` or `stdio`", listenURL)
}

func invalidWebSocketListenURLError(listenURL string) error {
	return fmt.Errorf("invalid websocket --listen URL `%s`; expected `ws://IP:PORT`", listenURL)
}

func (s *Server) ServeTransport(ctx context.Context, listenURL string, stdin io.Reader, stdout io.Writer) error {
	transport, err := ParseListenURL(listenURL)
	if err != nil {
		return err
	}
	switch transport.Kind {
	case ListenKindStdio:
		return s.Serve(ctx, stdin, stdout)
	case ListenKindWebSocket:
		return s.ServeWebSocket(ctx, transport.Address, stdout)
	default:
		return fmt.Errorf("unsupported exec-server listen transport %q", transport.Kind)
	}
}

func (s *Server) ServeWebSocket(ctx context.Context, address string, stdout io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	defer s.shutdownSessions()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	if stdout != nil {
		if _, err := fmt.Fprintf(stdout, "ws://%s\n", listener.Addr().String()); err != nil {
			_ = listener.Close()
			return err
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			return
		}
		conn.SetReadLimit(16 * 1024 * 1024)
		_ = s.serveWebSocketConnection(r.Context(), conn)
	})
	httpServer := &http.Server{Handler: mux}
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) serveWebSocketConnection(ctx context.Context, conn *websocket.Conn) error {
	defer conn.CloseNow()
	baseCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var requests sync.WaitGroup
	var writeMu sync.Mutex
	errCh := make(chan error, 1)
	reportError := func(err error) {
		if err == nil {
			return
		}
		select {
		case errCh <- err:
		default:
		}
		cancel()
	}
	writeJSON := func(value any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		writeCtx, writeCancel := context.WithTimeout(baseCtx, 30*time.Second)
		defer writeCancel()
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.Write(writeCtx, websocket.MessageText, encoded)
	}
	notify := processNotifier(func(method string, params any) {
		if err := writeJSON(map[string]any{"jsonrpc": "2.0", "method": method, "params": params}); err != nil {
			reportError(err)
		}
	})
	connectionCtx := withConnectionProtocolState(withHTTPBodyStreamRegistry(context.WithValue(baseCtx, processNotifierContextKey{}, notify)))
	defer s.detachConnection(connectionCtx)
	for {
		messageType, data, err := conn.Read(connectionCtx)
		if err != nil {
			cancel()
			s.detachConnection(connectionCtx)
			requests.Wait()
			select {
			case requestErr := <-errCh:
				return requestErr
			default:
			}
			if ctx.Err() != nil || websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
				return nil
			}
			return err
		}
		if messageType != websocket.MessageText && messageType != websocket.MessageBinary {
			continue
		}
		line := bytes.TrimSpace(data)
		if len(line) == 0 {
			continue
		}
		requestData := append([]byte(nil), line...)
		if clientMessageClosesConnection(connectionCtx, requestData) {
			cancel()
			requests.Wait()
			return nil
		}
		hasID, idErr := lineHasTopLevelID(requestData)
		if idErr != nil {
			malformed := errorResponse(
				RequestID{value: -1},
				-32600,
				"failed to parse websocket JSON-RPC message from exec-server websocket: "+idErr.Error(),
			)
			if err := writeJSON(malformed); err != nil {
				reportError(err)
			}
			continue
		}
		if !hasID {
			out, ok := s.handleLineWithLabel(connectionCtx, requestData, "exec-server websocket")
			if ok {
				if err := writeJSON(out); err != nil {
					reportError(err)
				}
			}
			continue
		}
		requests.Add(1)
		go func() {
			defer requests.Done()
			requestCtx, afterResponse := withAfterResponseActions(connectionCtx)
			out, ok := s.handleLineWithLabel(requestCtx, requestData, "exec-server websocket")
			if !ok {
				return
			}
			if err := writeJSON(out); err != nil {
				afterResponse.run()
				reportError(err)
				return
			}
			afterResponse.run()
		}()
	}
}

type processNotifierContextKey struct{}

func processNotifierFromContext(ctx context.Context) processNotifier {
	if ctx == nil {
		return nil
	}
	notify, _ := ctx.Value(processNotifierContextKey{}).(processNotifier)
	return notify
}
