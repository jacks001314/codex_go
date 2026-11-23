package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/doctor"
)

// printAppServerStartupConfig validates configuration and emits the app-server
// version plus effective requirements without initializing environments, thread
// storage, or plugins (Rust #55300).
func printAppServerStartupConfig(opts cli.AppServerOptions, root *cli.RootOptions, stdout io.Writer) error {
	result, err := readAppServerStartupConfig(opts, root)
	if err != nil {
		body := map[string]any{
			"code":    -32600,
			"message": err.Error(),
		}
		if data := appServerStartupConfigServerErrorData(err); data != nil {
			body["data"] = data
		}
		payload := map[string]any{"error": body}
		encoded, _ := json.Marshal(payload)
		_, _ = fmt.Fprintln(stdout, string(encoded))
		return err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, string(encoded))
	return err
}

func readAppServerStartupConfig(opts cli.AppServerOptions, root *cli.RootOptions) (*appServerStartupConfig, error) {
	codexHome := strings.TrimSpace(auth.DefaultCodexHome())
	if opts.StartupSignOut {
		if auth.IsWorkloadIdentitySelected() || strings.TrimSpace(os.Getenv(auth.CodexAccessTokenEnv)) != "" {
			return nil, errors.New("configured external authentication is owned by the app-server host and cannot be changed during startup recovery")
		}
		if _, err := auth.NewStore(codexHome).Delete(); err != nil {
			return nil, err
		}
	}
	if len(opts.StartupClientInfo) > 0 {
		var client struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal(opts.StartupClientInfo, &client); err != nil || strings.TrimSpace(client.Name) == "" {
			return nil, errors.New("invalid startup client info: name is required")
		}
	}
	cfg, err := loadAppServerStartupConfigForDiscovery(codexHome, opts, root)
	if err != nil {
		if action := appServerStartupConfigServerAction(err); action != nil {
			return nil, action
		}
		return nil, err
	}
	requirements, err := appServerRequirements(codexHome, cfg)
	if err != nil {
		return nil, err
	}
	projected := config.ProjectRequirementsWithAllowedLoginMethods(requirements, cfg.AllowedLoginMethods())
	supportsIndependent := true
	return &appServerStartupConfig{
		AppServerVersion: doctor.Version(),
		ConfigRequirements: config.ConfigRequirementsReadResponse{
			SupportsIndependentSpeedModes: &supportsIndependent,
			Requirements:                  projected,
		},
	}, nil
}

// loadAppServerStartupConfigForDiscovery is the configuration entry point used
// by startup discovery; tests replace it to inject configuration-server
// failures without a network.
var loadAppServerStartupConfigForDiscovery = loadAppServerConfig

// appServerStartupActionError is the login-required startup error Rust builds
// in config_server_error: the diagnostic message names the action while data
// carries the reason and error code (#62867).
type appServerStartupActionError struct {
	message string
	data    map[string]any
}

func (e *appServerStartupActionError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

// appServerStartupConfigServerAction rewrites a login-required config-server
// failure into Rust's "Configuration server sign-in required" error.
func appServerStartupConfigServerAction(err error) error {
	data := appServerStartupConfigServerErrorData(err)
	if data == nil || data["action"] == nil {
		return nil
	}
	return &appServerStartupActionError{message: "Configuration server sign-in required", data: data}
}

// appServerStartupConfigServerErrorData mirrors Rust's config_server_error and
// config_server_login_failed (#62867): an auth failure from a required
// configuration server becomes an actionable startup error. A resolvable login
// asks the client for the configServerLogin action; every other auth failure
// reports LoginFailed.
func appServerStartupConfigServerErrorData(err error) map[string]any {
	var actionErr *appServerStartupActionError
	if errors.As(err, &actionErr) {
		return actionErr.data
	}
	var loadErr *config.CloudConfigLoadError
	if !errors.As(err, &loadErr) || loadErr.Code != config.CloudConfigLoadAuth {
		return nil
	}
	if !loadErr.RequiresLogin() {
		return map[string]any{"reason": "configServer", "errorCode": "LoginFailed"}
	}
	return map[string]any{"reason": "configServer", "errorCode": "Auth", "action": "configServerLogin"}
}

type appServerStartupConfig struct {
	AppServerVersion   string                                `json:"appServerVersion"`
	ConfigRequirements config.ConfigRequirementsReadResponse `json:"configRequirements"`
}
