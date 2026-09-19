package auth

// Loopback callback listener for the gateway authorization-code grant.
//
// Rust parity: codex-rs/login/src/gateway_auth_callback.rs. The listener owns
// only the loopback lifecycle; parsing and state validation are shared with the
// OAuth protocol layer.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const gatewayBrowserTimeout = 180 * time.Second

// gatewayCallbackListener serves exactly one `/callback` request and validates
// its state before accepting a code or propagating a provider denial.
type gatewayCallbackListener struct {
	listener    net.Listener
	server      *http.Server
	redirectURI string
	results     chan gatewayCallbackResult
	// expectedState is the OAuth state the callback must echo back.
	expectedState string
}

type gatewayCallbackResult struct {
	code string
	err  error
}

// newGatewayCallbackListener binds 127.0.0.1 on redirectPort (0 picks an
// ephemeral port) and returns the redirect URI the issuer must call back.
func newGatewayCallbackListener(redirectPort uint16, expectedState string) (*gatewayCallbackListener, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", redirectPort))
	if err != nil {
		return nil, fmt.Errorf("failed to bind provider OAuth loopback callback: %w", err)
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return nil, errors.New("invalid provider OAuth loopback address")
	}
	callback := &gatewayCallbackListener{
		listener:    listener,
		redirectURI: fmt.Sprintf("http://127.0.0.1:%d/callback", address.Port),
		results:     make(chan gatewayCallbackResult, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", callback.handleCallback)
	mux.HandleFunc("/", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte("Not found"))
	})
	callback.server = &http.Server{Handler: mux}
	go func() {
		if err := callback.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			callback.publish(gatewayCallbackResult{err: err})
		}
	}()
	callback.expectedState = expectedState
	return callback, nil
}

func (l *gatewayCallbackListener) publish(result gatewayCallbackResult) {
	select {
	case l.results <- result:
	default:
	}
}

func (l *gatewayCallbackListener) handleCallback(writer http.ResponseWriter, request *http.Request) {
	if l == nil {
		return
	}
	if request.URL == nil || request.URL.Path != "/callback" {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte("Not found"))
		return
	}
	callbackURL, err := url.Parse("http://127.0.0.1" + request.URL.RequestURI())
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte("Invalid OAuth callback"))
		return
	}
	code, callbackErr := parseCallbackParameters(callbackURL).validate(l.expectedState)
	if callbackErr != nil {
		if callbackErr.kind == callbackErrorStateMismatch {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte("OAuth callback state did not match"))
			return
		}
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte("Sign-in failed."))
		l.publish(gatewayCallbackResult{err: callbackErr.asError()})
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = writer.Write([]byte(gatewaySignInCompleteHTML))
	l.publish(gatewayCallbackResult{code: code})
}

// asError maps a validated callback failure to Rust's io::Error texts.
func (e *callbackError) asError() error {
	if e == nil {
		return nil
	}
	switch e.kind {
	case callbackErrorProvider:
		return errors.New("provider OAuth authorization was denied")
	case callbackErrorMissingCode:
		return errors.New("provider OAuth callback omitted its code")
	default:
		return errors.New("OAuth callback state did not match")
	}
}

const gatewaySignInCompleteHTML = "<!doctype html><html><body><p>Sign-in complete. You may close this window.</p><script>window.close()</script></body></html>"

// wait blocks for the callback until the browser timeout elapses or the caller
// cancels, then stops serving.
func (l *gatewayCallbackListener) wait(ctx context.Context) (string, error) {
	if l == nil {
		return "", errors.New("provider OAuth sign-in was cancelled")
	}
	timer := time.NewTimer(gatewayBrowserTimeout)
	defer timer.Stop()
	select {
	case result := <-l.results:
		l.close()
		if result.err != nil {
			return "", result.err
		}
		return result.code, nil
	case <-timer.C:
		l.close()
		return "", errors.New("timed out waiting for provider OAuth sign-in")
	case <-ctx.Done():
		l.close()
		return "", errors.New("provider OAuth sign-in was cancelled")
	}
}

func (l *gatewayCallbackListener) close() {
	if l == nil || l.server == nil {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = l.server.Shutdown(shutdownCtx)
	if l.listener != nil {
		_ = l.listener.Close()
	}
}

// redirectURL returns the loopback redirect URI registered with the issuer.
func (l *gatewayCallbackListener) redirectURL() string {
	if l == nil {
		return ""
	}
	return strings.TrimSpace(l.redirectURI)
}
