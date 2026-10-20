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
		payload := map[string]any{
			"error": map[string]any{
				"code":    -32600,
				"message": err.Error(),
			},
		}
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
	cfg, err := loadAppServerConfig(codexHome, opts, root)
	if err != nil {
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

type appServerStartupConfig struct {
	AppServerVersion   string                                `json:"appServerVersion"`
	ConfigRequirements config.ConfigRequirementsReadResponse `json:"configRequirements"`
}
