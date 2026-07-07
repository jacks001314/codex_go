package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type OAuthLoginServerOptions struct {
	ServerName            string
	ServerURL             string
	ClientID              string
	ClientSecret          string
	RegistrationEndpoint  string
	ClientName            string
	AuthorizationEndpoint string
	TokenEndpoint         string
	Resource              string
	Scopes                []string
	State                 string
	Host                  string
	Port                  uint16
	Store                 *OAuthStore
	HTTPClient            *http.Client
}

type OAuthLoginServerResult struct {
	Tokens *OAuthTokenSet
	Error  error
}

type OAuthLoginServer struct {
	AuthorizationURL string
	RedirectURL      string
	CallbackURL      string
	Port             uint16

	server *http.Server
	done   chan *OAuthLoginServerResult
	once   sync.Once
}

func StartOAuthLoginServer(ctx context.Context, options *OAuthLoginServerOptions) (*OAuthLoginServer, error) {
	if options == nil {
		return nil, errors.New("MCP OAuth login server options are required")
	}
	if strings.TrimSpace(options.ServerName) == "" {
		return nil, errors.New("MCP OAuth server name is required")
	}
	listener, err := listenMCPOAuthCallback(options.Host, options.Port)
	if err != nil {
		return nil, err
	}
	port, err := listenerPort(listener)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	callbackID, err := MCPOAuthCallbackID(options.ServerURL)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	host := strings.TrimSpace(options.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	redirectURL := fmt.Sprintf("http://%s:%d/callback/%s", host, port, callbackID)
	session, err := NewOAuthLoginSessionWithClientRegistration(ctx, &OAuthLoginSessionOptions{
		ServerURL:             options.ServerURL,
		ClientID:              options.ClientID,
		ClientSecret:          options.ClientSecret,
		RegistrationEndpoint:  options.RegistrationEndpoint,
		ClientName:            options.ClientName,
		AuthorizationEndpoint: options.AuthorizationEndpoint,
		TokenEndpoint:         options.TokenEndpoint,
		RedirectURL:           redirectURL,
		Resource:              options.Resource,
		Scopes:                options.Scopes,
		State:                 options.State,
	}, options.HTTPClient)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	login := &OAuthLoginServer{
		AuthorizationURL: session.AuthorizationURL,
		RedirectURL:      redirectURL,
		CallbackURL:      redirectURL,
		Port:             port,
		done:             make(chan *OAuthLoginServerResult, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(session.CallbackPath, func(w http.ResponseWriter, r *http.Request) {
		tokens, err := session.CompleteCallback(r.Context(), r.URL.RequestURI(), NewOAuthTokenClient(options.HTTPClient), options.ServerName)
		if err == nil && options.Store != nil {
			err = options.Store.Save(tokens)
		}
		if err != nil {
			login.complete(&OAuthLoginServerResult{Error: err})
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		login.complete(&OAuthLoginServerResult{Tokens: tokens})
		_, _ = io.WriteString(w, "MCP OAuth login completed")
	})
	mux.HandleFunc("/success", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "MCP OAuth login completed")
	})
	mux.HandleFunc("/cancel", func(w http.ResponseWriter, r *http.Request) {
		err := errors.New("MCP OAuth login cancelled")
		login.complete(&OAuthLoginServerResult{Error: err})
		_, _ = io.WriteString(w, err.Error())
	})
	login.server = &http.Server{Handler: mux}
	go func() {
		if err := login.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			login.complete(&OAuthLoginServerResult{Error: err})
		}
	}()
	return login, nil
}

func (s *OAuthLoginServer) Done() <-chan *OAuthLoginServerResult {
	if s == nil {
		closed := make(chan *OAuthLoginServerResult)
		close(closed)
		return closed
	}
	return s.done
}

func (s *OAuthLoginServer) Cancel(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.complete(&OAuthLoginServerResult{Error: errors.New("MCP OAuth login cancelled")})
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), time.Second)
		defer cancel()
	}
	return s.server.Shutdown(ctx)
}

func (s *OAuthLoginServer) complete(result *OAuthLoginServerResult) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if result == nil {
			result = &OAuthLoginServerResult{}
		}
		s.done <- result
		close(s.done)
		if s.server != nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = s.server.Shutdown(ctx)
			}()
		}
	})
}

func listenMCPOAuthCallback(host string, port uint16) (net.Listener, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "127.0.0.1"
	}
	return net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
}

func listenerPort(listener net.Listener) (uint16, error) {
	if listener == nil {
		return 0, errors.New("MCP OAuth listener is nil")
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || addr == nil {
		return 0, fmt.Errorf("MCP OAuth listener address %q is not TCP", listener.Addr())
	}
	if addr.Port <= 0 || addr.Port > 65535 {
		return 0, fmt.Errorf("MCP OAuth listener port %d is invalid", addr.Port)
	}
	return uint16(addr.Port), nil
}

func oauthLoginServerCallbackURL(raw string, query url.Values) string {
	if len(query) == 0 {
		return raw
	}
	separator := "?"
	if strings.Contains(raw, "?") {
		separator = "&"
	}
	return raw + separator + query.Encode()
}
