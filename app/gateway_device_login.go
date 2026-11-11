package app

// Gateway device-code sign-in before the TUI starts.
//
// Rust parity: codex-rs/tui/src/device_login.rs and
// onboarding_screen::run_gateway_device_login (#60662). A headless TUI session
// prefers the device flow when the terminal is over SSH, when no browser can be
// launched, and always checks the provider a resumed thread will use before it
// requests models.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/model"
	codextui "codex_go/tui"
)

const (
	gatewayDeviceLoginTimeout    = 15 * time.Minute
	gatewayDeviceLoginCancelled  = "Startup cancelled."
	gatewayDeviceLoginConnection = "Lost the connection while signing in to your gateway. Restart Codex to try again."
)

var (
	gatewayLoginGOOS       = runtime.GOOS
	gatewayLoginLookupEnv  = os.LookupEnv
	gatewayLoginLookupPath = exec.LookPath
	gatewayLoginRunCommand = func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		return cmd.Run()
	}
	gatewayTerminalOverSSH   = codextui.TerminalAppOverSSH
	gatewayProviderIsBedrock = interactiveProviderIsAmazonBedrock
)

// preferGatewayDeviceLogin mirrors device_login::prefer_device_login: SSH and
// headless Linux sessions cannot reach a browser, and neither can a host with
// no registered browser handler.
func preferGatewayDeviceLogin() bool {
	if gatewayTerminalOverSSH() {
		return true
	}
	if gatewayLoginGOOS == "linux" {
		display := strings.TrimSpace(gatewayEnv("DISPLAY"))
		wayland := strings.TrimSpace(gatewayEnv("WAYLAND_DISPLAY"))
		if display == "" && wayland == "" {
			return true
		}
	}
	return !gatewayBrowserHandlerAvailable()
}

func gatewayEnv(name string) string {
	value, _ := gatewayLoginLookupEnv(name)
	return value
}

// gatewayBrowserHandlerAvailable mirrors webbrowser::Browser::is_available.
func gatewayBrowserHandlerAvailable() bool {
	switch gatewayLoginGOOS {
	case "windows":
		if _, err := gatewayLoginLookupPath("rundll32"); err != nil {
			return false
		}
		return gatewayLoginRunCommand("reg", "query", `HKCR\http\shell\open\command`) == nil
	case "darwin":
		_, err := gatewayLoginLookupPath("open")
		return err == nil
	default:
		for _, name := range []string{"xdg-open", "gio", "sensible-browser", "x-www-browser"} {
			if _, err := gatewayLoginLookupPath(name); err == nil {
				return true
			}
		}
		return false
	}
}

// interactiveProviderIsAmazonBedrock reports whether this launch uses Bedrock,
// whose gateway sign-in is owned by the AWS credential chain (Rust
// run_ratatui_app skips device login for Bedrock).
func interactiveProviderIsAmazonBedrock(root *cli.RootOptions) bool {
	providerID := strings.ToLower(strings.TrimSpace(interactiveConfiguredModelProvider(root)))
	if providerID == "" {
		return false
	}
	return providerID == strings.ToLower(model.AmazonBedrockProviderID) ||
		providerID == strings.ToLower(model.AmazonBedrockRuntimeProviderID)
}

// needsGatewayDeviceLogin mirrors device_login::needs_device_login.
func needsGatewayDeviceLogin(state *appserver.GatewayOAuthReadResponse, preferDevice bool) bool {
	if state == nil || !preferDevice || !state.Required || !state.SupportsDeviceAuth || state.Status == nil {
		return false
	}
	switch *state.Status {
	case appserver.GatewayOAuthStatusNotReady, appserver.GatewayOAuthStatusFailed:
		return true
	default:
		return false
	}
}

// gatewayDeviceLoginBackend is the app-server surface the startup login uses.
type gatewayDeviceLoginBackend interface {
	readGatewayOAuth(ctx context.Context, provider string) (*appserver.GatewayOAuthReadResponse, error)
	probeLegacyGatewayOAuth(ctx context.Context) (bool, error)
	loginGatewayDevice(ctx context.Context, provider string) (*appserver.GatewayOAuthLoginResponse, error)
	waitGatewayOAuth(ctx context.Context, loginID string) error
	cancelGatewayOAuth(ctx context.Context) error
	close()
}

type gatewayDeviceLoginRPCError struct {
	code    int
	message string
}

func (e *gatewayDeviceLoginRPCError) Error() string { return e.message }

func gatewayOAuthMethodNotFound(err error) bool {
	code, message := 0, ""
	var remoteErr *remoteRPCError
	var localErr *gatewayDeviceLoginRPCError
	switch {
	case errors.As(err, &remoteErr) && remoteErr != nil:
		code, message = remoteErr.Code, remoteErr.Message
	case errors.As(err, &localErr) && localErr != nil:
		code, message = localErr.code, localErr.message
	default:
		return false
	}
	if code == -32601 {
		return true
	}
	// Older servers deserialize the new method as an unknown request variant.
	return code == -32600 && (strings.Contains(message, "unknown variant") || strings.Contains(message, "unknown method"))
}

// maybeRunGatewayDeviceLogin runs the startup device login when this session
// owns the workspace and the selected provider requires a gateway grant.
func maybeRunGatewayDeviceLogin(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, stdout io.Writer) error {
	if interactiveRemoteEndpointIsExplicitRemote(root) {
		return nil
	}
	if endpoint != nil && !interactiveRemoteEndpointIsLocal(endpoint) {
		return nil
	}
	if gatewayProviderIsBedrock(root) {
		return nil
	}
	provider := interactiveLocalModelProviderClientOverride(root)
	if endpoint != nil {
		provider = interactiveRemoteModelProviderClientOverride(root)
	}
	backend, err := newGatewayDeviceLoginBackend(ctx, root, endpoint)
	if err != nil {
		return err
	}
	defer backend.close()
	loginCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	return runGatewayDeviceLogin(loginCtx, backend, provider, preferGatewayDeviceLogin(), stdout)
}

func newGatewayDeviceLoginBackend(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint) (gatewayDeviceLoginBackend, error) {
	if endpoint == nil {
		return newEmbeddedGatewayDeviceLoginBackend(root)
	}
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	return &remoteGatewayDeviceLoginBackend{client: client}, nil
}

// runGatewayDeviceLogin performs the read/login/wait sequence and reports
// whether startup may continue. Errors from servers that know the RPCs are
// preserved; only an older server without the RPCs skips the prompt.
func runGatewayDeviceLogin(ctx context.Context, backend gatewayDeviceLoginBackend, provider string, preferDevice bool, stdout io.Writer) error {
	state, err := backend.readGatewayOAuth(ctx, provider)
	if err != nil {
		if gatewayOAuthMethodNotFound(err) {
			return nil
		}
		if legacy, probeErr := backend.probeLegacyGatewayOAuth(ctx); probeErr == nil && legacy {
			return nil
		}
		return err
	}
	if strings.TrimSpace(provider) != "" && !strings.EqualFold(provider, state.ProviderID) {
		return nil
	}
	if !needsGatewayDeviceLogin(state, preferDevice) {
		return nil
	}
	login, err := backend.loginGatewayDevice(ctx, provider)
	if err != nil {
		return err
	}
	if login == nil || login.Prompt == nil {
		return nil
	}
	renderGatewayDeviceCodePrompt(stdout, login.Prompt)
	waitCtx, cancel := context.WithTimeout(ctx, gatewayDeviceLoginTimeout)
	err = backend.waitGatewayOAuth(waitCtx, login.Prompt.LoginID)
	cancel()
	if err == nil {
		return nil
	}
	cancelCtx, stop := context.WithTimeout(context.Background(), time.Second)
	_ = backend.cancelGatewayOAuth(cancelCtx)
	stop()
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return errors.New(gatewayDeviceLoginCancelled)
	}
	return err
}

// renderGatewayDeviceCodePrompt mirrors onboarding::render_gateway_device_prompt.
func renderGatewayDeviceCodePrompt(writer io.Writer, prompt *appserver.GatewayOAuthPrompt) {
	if writer == nil || prompt == nil {
		return
	}
	url := strings.TrimSpace(prompt.VerificationURL)
	code := strings.TrimSpace(prompt.UserCode)
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "  Sign in to your gateway")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "  1. Open this link in a browser")
	fmt.Fprintln(writer)
	fmt.Fprintf(writer, "    \x1b[36;4m%s\x1b[0m\n", url)
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "  2. Enter this one-time code when prompted")
	fmt.Fprintln(writer)
	fmt.Fprintf(writer, "    \x1b[36;1m%s\x1b[0m\n", code)
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "  Continue only if you started this login in Codex. If a website or another person gave you this code, cancel.")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "  Press ctrl+c to cancel")
	fmt.Fprintln(writer)
}

// remoteGatewayDeviceLoginBackend talks to a local daemon or remote app-server.
type remoteGatewayDeviceLoginBackend struct {
	client *remoteAppServerTUIClient
}

func (b *remoteGatewayDeviceLoginBackend) readGatewayOAuth(ctx context.Context, provider string) (*appserver.GatewayOAuthReadResponse, error) {
	params := appserver.GatewayOAuthReadParams{}
	if value := strings.TrimSpace(provider); value != "" {
		params.ModelProvider = &value
	}
	var response appserver.GatewayOAuthReadResponse
	if err := remoteSessionRequest(ctx, b.client, appserver.MethodGatewayOAuthRead, params, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// probeLegacyGatewayOAuth asks without a provider. Servers that predate device
// login answer with providerId and no supportsDeviceAuth field.
func (b *remoteGatewayDeviceLoginBackend) probeLegacyGatewayOAuth(ctx context.Context) (bool, error) {
	var raw json.RawMessage
	if err := remoteSessionRequest(ctx, b.client, appserver.MethodGatewayOAuthRead, nil, &raw); err != nil {
		return false, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false, err
	}
	_, hasProvider := fields["providerId"]
	_, hasDevice := fields["supportsDeviceAuth"]
	return hasProvider && !hasDevice, nil
}

func (b *remoteGatewayDeviceLoginBackend) loginGatewayDevice(ctx context.Context, provider string) (*appserver.GatewayOAuthLoginResponse, error) {
	params := appserver.GatewayOAuthLoginParams{DeviceAuth: true}
	if value := strings.TrimSpace(provider); value != "" {
		params.ModelProvider = &value
	}
	var response appserver.GatewayOAuthLoginResponse
	if err := remoteSessionRequest(ctx, b.client, appserver.MethodGatewayOAuthLogin, params, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (b *remoteGatewayDeviceLoginBackend) waitGatewayOAuth(ctx context.Context, loginID string) error {
	loginID = strings.TrimSpace(loginID)
	for {
		message, err := b.client.readRemoteMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New(gatewayDeviceLoginConnection)
		}
		if strings.TrimSpace(message.Method) == "" {
			continue
		}
		if len(message.ID) > 0 {
			// The sign-in screen cannot answer operations, so decline them
			// instead of leaving the server waiting (Rust rejects them).
			var id appserver.RequestID
			if err := json.Unmarshal(message.ID, &id); err == nil {
				_ = b.client.writeJSON(ctx, appserver.ErrorResponse(id, -32000, "Codex cannot handle this request during gateway sign-in", nil))
			}
			continue
		}
		if appserver.NotificationMethod(message.Method) != appserver.NotificationGatewayOAuthChanged {
			continue
		}
		var changed appserver.GatewayOAuthChangedNotification
		if err := json.Unmarshal(message.Params, &changed); err != nil {
			continue
		}
		if changed.LoginID == nil || *changed.LoginID != loginID {
			continue
		}
		switch changed.Status {
		case appserver.GatewayOAuthStatusSucceeded:
			return nil
		case appserver.GatewayOAuthStatusFailed, appserver.GatewayOAuthStatusNotReady:
			if changed.Error != nil && strings.TrimSpace(*changed.Error) != "" {
				return errors.New(*changed.Error)
			}
			return errors.New("Gateway sign-in failed")
		}
	}
}

func (b *remoteGatewayDeviceLoginBackend) cancelGatewayOAuth(ctx context.Context) error {
	var response appserver.GatewayOAuthCancelResponse
	return remoteSessionRequest(ctx, b.client, appserver.MethodGatewayOAuthCancel, nil, &response)
}

func (b *remoteGatewayDeviceLoginBackend) close() {
	if b != nil && b.client != nil {
		b.client.close()
	}
}

// embeddedGatewayDeviceLoginBackend drives the in-process app server.
type embeddedGatewayDeviceLoginBackend struct {
	router       *appserver.RuntimeRouter
	connectionID string
	nextID       int64
	changes      chan *appserver.GatewayOAuthChangedNotification
}

func newEmbeddedGatewayDeviceLoginBackend(root *cli.RootOptions) (*embeddedGatewayDeviceLoginBackend, error) {
	router := appserver.NewRuntimeRouter(appserver.RuntimeServices{
		Config:     interactiveConfigService(root),
		DefaultCWD: interactiveSessionPickerCWD(root),
	})
	backend := &embeddedGatewayDeviceLoginBackend{
		router:       router,
		connectionID: "tui-gateway-device-login",
		changes:      make(chan *appserver.GatewayOAuthChangedNotification, 8),
	}
	router.SetNotificationSink(&gatewayDeviceLoginSink{changes: backend.changes})
	params, err := json.Marshal(appserver.InitializeParams{ClientInfo: appserver.ClientInfo{Name: "codex_tui", Version: "0.0.0"}})
	if err != nil {
		router.Close()
		return nil, err
	}
	response := router.Handle(&appserver.Request{
		ID:           appserver.StringID("tui-gateway-device-login-init"),
		Method:       appserver.MethodInitialize,
		Params:       params,
		ConnectionID: backend.connectionID,
	})
	if response.Error != nil {
		router.Close()
		return nil, &gatewayDeviceLoginRPCError{code: response.Error.Code, message: response.Error.Message}
	}
	return backend, nil
}

type gatewayDeviceLoginSink struct {
	changes chan *appserver.GatewayOAuthChangedNotification
}

func (s *gatewayDeviceLoginSink) Notify(notification *appserver.Notification) {
	s.record(notification)
}

func (s *gatewayDeviceLoginSink) NotifyToConnection(_ string, notification *appserver.Notification) {
	s.record(notification)
}

func (s *gatewayDeviceLoginSink) record(notification *appserver.Notification) {
	if s == nil || notification == nil || notification.Method != appserver.NotificationGatewayOAuthChanged {
		return
	}
	changed, ok := notification.Params.(*appserver.GatewayOAuthChangedNotification)
	if !ok || changed == nil {
		return
	}
	select {
	case s.changes <- changed:
	default:
	}
}

func (b *embeddedGatewayDeviceLoginBackend) request(method appserver.Method, params any) (any, error) {
	b.nextID++
	var raw json.RawMessage
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = encoded
	}
	response := b.router.Handle(&appserver.Request{
		ID:           appserver.IntID(b.nextID),
		Method:       method,
		Params:       raw,
		ConnectionID: b.connectionID,
	})
	if response.Error != nil {
		return nil, &gatewayDeviceLoginRPCError{code: response.Error.Code, message: response.Error.Message}
	}
	return response.Result, nil
}

func (b *embeddedGatewayDeviceLoginBackend) readGatewayOAuth(_ context.Context, provider string) (*appserver.GatewayOAuthReadResponse, error) {
	params := appserver.GatewayOAuthReadParams{}
	if value := strings.TrimSpace(provider); value != "" {
		params.ModelProvider = &value
	}
	result, err := b.request(appserver.MethodGatewayOAuthRead, params)
	if err != nil {
		return nil, err
	}
	response, ok := result.(*appserver.GatewayOAuthReadResponse)
	if !ok || response == nil {
		return nil, errors.New("account/gatewayOAuth/read returned no response")
	}
	return response, nil
}

func (b *embeddedGatewayDeviceLoginBackend) probeLegacyGatewayOAuth(context.Context) (bool, error) {
	result, err := b.request(appserver.MethodGatewayOAuthRead, nil)
	if err != nil {
		return false, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return false, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return false, err
	}
	_, hasProvider := fields["providerId"]
	_, hasDevice := fields["supportsDeviceAuth"]
	return hasProvider && !hasDevice, nil
}

func (b *embeddedGatewayDeviceLoginBackend) loginGatewayDevice(_ context.Context, provider string) (*appserver.GatewayOAuthLoginResponse, error) {
	params := appserver.GatewayOAuthLoginParams{DeviceAuth: true}
	if value := strings.TrimSpace(provider); value != "" {
		params.ModelProvider = &value
	}
	result, err := b.request(appserver.MethodGatewayOAuthLogin, params)
	if err != nil {
		return nil, err
	}
	response, ok := result.(*appserver.GatewayOAuthLoginResponse)
	if !ok || response == nil {
		return nil, errors.New("account/gatewayOAuth/login returned no response")
	}
	return response, nil
}

func (b *embeddedGatewayDeviceLoginBackend) waitGatewayOAuth(ctx context.Context, loginID string) error {
	loginID = strings.TrimSpace(loginID)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case changed := <-b.changes:
			if changed == nil || changed.LoginID == nil || *changed.LoginID != loginID {
				continue
			}
			switch changed.Status {
			case appserver.GatewayOAuthStatusSucceeded:
				return nil
			case appserver.GatewayOAuthStatusFailed, appserver.GatewayOAuthStatusNotReady:
				if changed.Error != nil && strings.TrimSpace(*changed.Error) != "" {
					return errors.New(*changed.Error)
				}
				return errors.New("Gateway sign-in failed")
			}
		}
	}
}

func (b *embeddedGatewayDeviceLoginBackend) cancelGatewayOAuth(context.Context) error {
	_, err := b.request(appserver.MethodGatewayOAuthCancel, nil)
	return err
}

func (b *embeddedGatewayDeviceLoginBackend) close() {
	if b != nil && b.router != nil {
		b.router.Close()
	}
}
