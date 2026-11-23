package app

// Dedicated startup OAuth pipe (Rust #62867
// app-server/src/startup_config/oauth.rs). The pipe serves only
// account/gatewayOAuth/login and account/gatewayOAuth/cancel before the app
// server initializes, so a client can obtain a configuration-server grant and
// then rerun --print-startup-config.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"codex_go/appserver"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/keyring"
)

const (
	startupOAuthNotConfiguredMessage = "Configuration server OAuth is not configured"
	startupOAuthInitMessage          = "Could not initialize configuration server sign-in"
	startupOAuthCancelledMessage     = "Configuration server sign-in was cancelled"
	startupOAuthFailedMessage        = "Configuration server sign-in failed"
	startupOAuthAlreadyRunning       = "Sign-in already in progress"
	startupOAuthTargetRequired       = "Startup login requires the configServer target"
	startupOAuthBrowserOnly          = "Startup supports browser authentication only"
	startupOAuthInvalidParamsMessage = "Invalid startup OAuth parameters"
	startupOAuthUnknownMethod        = "Unknown startup OAuth method"
)

var (
	errStartupOAuthNotConfigured = errors.New(startupOAuthNotConfiguredMessage)
	errStartupOAuthInitFailed    = errors.New(startupOAuthInitMessage)
)

// startupConfigServerLogin starts the browser sign-in for the locally required
// configuration server and reports the authorization URL through onURL. It
// returns once the grant is stored, the attempt fails, or ctx is cancelled.
// Tests replace it to exercise the pipe without a browser or an OS keyring.
var startupConfigServerLogin = func(ctx context.Context, codexHome string, loaded *config.Config, onURL func(string)) error {
	if loaded == nil || loaded.Requirements == nil || loaded.Requirements.ConfigServer == nil {
		return errStartupOAuthNotConfigured
	}
	policy := publishApplicationNetworkPolicy(loaded)
	client := configServerHTTPClient(loaded, nil)
	resolved, err := resolveConfigServerOAuth(ctx, loaded.Requirements.ConfigServer, client, &policy)
	if err != nil {
		return fmt.Errorf("%w: %v", errStartupOAuthInitFailed, err)
	}
	if resolved == nil || resolved.OAuth == nil {
		return errStartupOAuthNotConfigured
	}
	manager := newConfigServerAuthManager(codexHome, resolved, client, keyring.New())
	if manager == nil {
		return errStartupOAuthNotConfigured
	}
	attempt, err := manager.LoginIfRequired()
	if err != nil {
		return err
	}
	if attempt == nil {
		return nil
	}
	defer func() { _ = attempt.Close() }()
	return attempt.LoginWithBrowser(ctx, onURL, ConfigServerLoginFailureMessage)
}

type startupOAuthMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type startupOAuthErrorBody struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

type startupOAuthReply struct {
	ID     json.RawMessage        `json:"id"`
	Result any                    `json:"result,omitempty"`
	Error  *startupOAuthErrorBody `json:"error,omitempty"`
}

type startupOAuthNotification struct {
	Method string                                           `json:"method"`
	Params appserver.StartupGatewayOAuthChangedNotification `json:"params"`
}

type startupOAuthAttempt struct {
	id     json.RawMessage
	cancel context.CancelFunc
	done   chan error
}

func startupOAuthLoginFailed(message string) *startupOAuthErrorBody {
	return &startupOAuthErrorBody{
		Code:    -32600,
		Message: message,
		Data:    map[string]any{"reason": "configServer", "errorCode": "LoginFailed"},
	}
}

func startupOAuthInvalidParams(message string) *startupOAuthErrorBody {
	return &startupOAuthErrorBody{Code: -32602, Message: message}
}

func startupOAuthInvalidRequest(message string) *startupOAuthErrorBody {
	return &startupOAuthErrorBody{Code: -32600, Message: message}
}

func startupOAuthMethodNotFound(message string) *startupOAuthErrorBody {
	return &startupOAuthErrorBody{Code: -32601, Message: message}
}

func writeStartupOAuthJSON(stdout io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", encoded)
	return err
}

func writeStartupOAuthReply(stdout io.Writer, id json.RawMessage, result any, failure *startupOAuthErrorBody) error {
	return writeStartupOAuthJSON(stdout, &startupOAuthReply{ID: id, Result: result, Error: failure})
}

func startupOAuthResult(err error) (any, *startupOAuthErrorBody) {
	if err == nil {
		return appserver.GatewayOAuthLoginResponse{}, nil
	}
	return nil, startupOAuthLoginFailed(startupOAuthFailedMessage)
}

// serveAppServerStartupOAuth reads one JSON request per line from stdin and
// answers on stdout, without an initialize handshake (Rust #62867).
func serveAppServerStartupOAuth(ctx context.Context, opts cli.AppServerOptions, root *cli.RootOptions, stdin io.Reader, stdout io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	codexHome := strings.TrimSpace(auth.DefaultCodexHome())
	loaded, loadErr := config.LoadEffectiveWithOptions(codexHome, appServerStartupLoadOptions(opts, root))

	lines := make(chan string)
	readErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdin)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				readErr <- ctx.Err()
				return
			}
		}
		readErr <- scanner.Err()
		close(lines)
	}()

	var active *startupOAuthAttempt
	finish := func(err error) error {
		if active == nil {
			return nil
		}
		result, failure := startupOAuthResult(err)
		writeErr := writeStartupOAuthReply(stdout, active.id, result, failure)
		active.cancel()
		active = nil
		return writeErr
	}

	for {
		if active != nil {
			select {
			case err := <-active.done:
				if writeErr := finish(err); writeErr != nil {
					return writeErr
				}
				continue
			default:
			}
		}
		var done <-chan error
		if active != nil {
			done = active.done
		}
		select {
		case <-ctx.Done():
			if active != nil {
				active.cancel()
				<-active.done
			}
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				if active != nil {
					active.cancel()
					<-active.done
				}
				return <-readErr
			}
			var message startupOAuthMessage
			if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &message); err != nil {
				return fmt.Errorf("invalid startup OAuth request: %w", err)
			}
			switch appserver.Method(message.Method) {
			case appserver.MethodGatewayOAuthLogin:
				var params appserver.GatewayOAuthLoginParams
				raw := strings.TrimSpace(string(message.Params))
				if raw != "" && raw != "null" {
					if err := json.Unmarshal(message.Params, &params); err != nil {
						return writeStartupOAuthReply(stdout, message.ID, nil, startupOAuthInvalidParams(startupOAuthInvalidParamsMessage))
					}
				}
				if params.Target == nil || params.Target.Kind != appserver.GatewayOAuthTargetConfigServer || params.ModelProvider != nil {
					return writeStartupOAuthReply(stdout, message.ID, nil, startupOAuthInvalidParams(startupOAuthTargetRequired))
				}
				if params.DeviceAuth {
					return writeStartupOAuthReply(stdout, message.ID, nil, startupOAuthInvalidParams(startupOAuthBrowserOnly))
				}
				if active != nil {
					return writeStartupOAuthReply(stdout, message.ID, nil, startupOAuthInvalidRequest(startupOAuthAlreadyRunning))
				}
				if loadErr != nil {
					return writeStartupOAuthReply(stdout, message.ID, nil, startupOAuthLoginFailed(startupOAuthInitMessage))
				}
				attempt, handoff, err := startStartupOAuthAttempt(ctx, message.ID, codexHome, loaded, stdout)
				if err != nil {
					return err
				}
				if attempt != nil && handoff {
					active = attempt
				}
			case appserver.MethodGatewayOAuthCancel:
				if active != nil {
					completed := false
					select {
					case err := <-active.done:
						if writeErr := finish(err); writeErr != nil {
							return writeErr
						}
						completed = true
					default:
					}
					if !completed {
						active.cancel()
						<-active.done
						if writeErr := writeStartupOAuthReply(stdout, active.id, nil, startupOAuthLoginFailed(startupOAuthCancelledMessage)); writeErr != nil {
							return writeErr
						}
						active = nil
					}
				}
				return writeStartupOAuthReply(stdout, message.ID, appserver.GatewayOAuthCancelResponse{}, nil)
			default:
				return writeStartupOAuthReply(stdout, message.ID, nil, startupOAuthMethodNotFound(startupOAuthUnknownMethod))
			}
		case err := <-done:
			if writeErr := finish(err); writeErr != nil {
				return writeErr
			}
		}
	}
}

// startStartupOAuthAttempt begins the browser sign-in and reports whether the
// authorization URL handoff was written, which keeps the login request pending.
func startStartupOAuthAttempt(ctx context.Context, id json.RawMessage, codexHome string, loaded *config.Config, stdout io.Writer) (*startupOAuthAttempt, bool, error) {
	loginCtx, cancel := context.WithCancel(ctx)
	attempt := &startupOAuthAttempt{id: id, cancel: cancel, done: make(chan error, 1)}
	ready := make(chan string, 1)
	go func() {
		attempt.done <- startupConfigServerLogin(loginCtx, codexHome, loaded, func(url string) {
			select {
			case ready <- url:
			default:
			}
		})
	}()
	select {
	case url := <-ready:
		handoff := appserver.StartupGatewayOAuthChangedNotification{
			Target:  appserver.GatewayOAuthTarget{Kind: appserver.GatewayOAuthTargetConfigServer},
			Status:  appserver.GatewayOAuthStatusStarted,
			AuthURL: url,
		}
		if err := writeStartupOAuthJSON(stdout, &startupOAuthNotification{Method: string(appserver.NotificationGatewayOAuthChanged), Params: handoff}); err != nil {
			cancel()
			<-attempt.done
			return nil, false, err
		}
		return attempt, true, nil
	case err := <-attempt.done:
		cancel()
		select {
		case url := <-ready:
			handoff := appserver.StartupGatewayOAuthChangedNotification{
				Target:  appserver.GatewayOAuthTarget{Kind: appserver.GatewayOAuthTargetConfigServer},
				Status:  appserver.GatewayOAuthStatusStarted,
				AuthURL: url,
			}
			if writeErr := writeStartupOAuthJSON(stdout, &startupOAuthNotification{Method: string(appserver.NotificationGatewayOAuthChanged), Params: handoff}); writeErr != nil {
				return nil, false, writeErr
			}
		default:
		}
		result, failure := startupOAuthResult(err)
		if writeErr := writeStartupOAuthReply(stdout, id, result, failure); writeErr != nil {
			return nil, false, writeErr
		}
		return nil, false, nil
	}
}
