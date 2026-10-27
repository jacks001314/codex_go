package appserver

// Google Cloud Vertex AI account setup (Rust #57394).
//
// Upstream registers `account/gcp/setup` as an experimental client request and
// implements it in
// codex-rs/app-server/src/request_processors/account_processor/gcp_setup.rs.
// The handler previews the provider write against the effective layer stack,
// verifies credentials by running the provider auth command, then commits the
// user configuration with the previewed version.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"codex_go/config"
	"codex_go/model"
	"codex_go/network"
)

// GcpSetupParams mirrors v2::GcpSetupParams.
type GcpSetupParams struct {
	ProjectID string `json:"projectId"`
	// Location selects the Vertex AI location; omission or null selects
	// `global`.
	Location *string `json:"location"`
	// CredentialFile is an absolute path to an ADC credential file. Omission or
	// null clears the active user override and uses Application Default
	// Credentials.
	CredentialFile *string `json:"credentialFile"`
}

// GcpSetupResponse mirrors v2::GcpSetupResponse (an empty object).
type GcpSetupResponse struct{}

const (
	gcpAuthReasonGcloudNotInstalled    = "gcloudNotInstalled"
	gcpAuthReasonCredentialFileInvalid = "credentialFileInvalid"
	gcpAuthReasonAuthCommandFailed     = "authCommandFailed"
)

// gcpSetupError carries Rust's structured credential failure reason in the
// JSON-RPC error data.
type gcpSetupError struct {
	reason  string
	message string
}

func (e *gcpSetupError) Error() string { return e.message }

func (e *gcpSetupError) Unwrap() error { return ErrJSONRPCInvalidRequest }

func (e *gcpSetupError) Is(target error) bool { return target == ErrJSONRPCInvalidRequest }

func (e *gcpSetupError) JSONRPCErrorData() map[string]any {
	return map[string]any{"reason": e.reason}
}

func newGCPSetupError(reason string, message string) error {
	return &gcpSetupError{reason: reason, message: message}
}

// gcpConfigWriteError mirrors Rust's ConfigManagerError::Write mapping in
// map_config_error: a config write rejection is invalid_request (-32600) with a
// config_write_error_code data field, while every other config error stays an
// internal error.
type gcpConfigWriteError struct {
	code    config.ConfigWriteErrorCode
	message string
}

func (e *gcpConfigWriteError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *gcpConfigWriteError) Unwrap() error { return ErrJSONRPCInvalidRequest }

func (e *gcpConfigWriteError) Is(target error) bool {
	return target == ErrJSONRPCInvalidRequest || target == ErrInvalidRequest
}

func (e *gcpConfigWriteError) JSONRPCErrorData() map[string]any {
	if e == nil || e.code == "" {
		return nil
	}
	return map[string]any{"config_write_error_code": string(e.code)}
}

// mapGCPConfigError mirrors gcp_setup's `map_config_error`: write errors carry
// their code and message on the wire, other config failures remain internal.
func mapGCPConfigError(err error) error {
	if err == nil {
		return nil
	}
	var writeErr *config.ConfigWriteError
	if errors.As(err, &writeErr) {
		return &gcpConfigWriteError{code: writeErr.Code, message: writeErr.Error()}
	}
	if errors.Is(err, config.ErrInvalidConfigRequest) {
		return &gcpConfigWriteError{code: config.ConfigWriteValidation, message: err.Error()}
	}
	return err
}

func (r *RuntimeRouter) ensureGCPLoginAllowed() error {
	return r.ensureProviderLoginAllowed("Google Cloud setup")
}

// handleGcpSetup mirrors AccountRequestProcessor::handle_gcp_setup.
func (r *RuntimeRouter) handleGcpSetup(request *Request) (*GcpSetupResponse, error) {
	var params GcpSetupParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := r.ensureGCPLoginAllowed(); err != nil {
		return nil, err
	}
	cwd := r.gcpSetupCWD()
	projectID := strings.TrimSpace(params.ProjectID)
	location := model.GCPDefaultLocation
	if params.Location != nil {
		location = strings.TrimSpace(*params.Location)
	}
	// Rust passes credential_file through unchanged: a configured empty value
	// is relative and must fail validation, and only an omitted value clears the
	// user override.
	settings := []config.ProviderSetting{
		{Key: "project_id", Value: &projectID},
		{Key: "location", Value: &location},
		{Key: "credential_file", Value: params.CredentialFile},
	}
	write, providerTable, err := r.requireConfig().PrepareProviderConfigForCWD(cwd, model.GCPProviderID, settings)
	if err != nil {
		return nil, mapGCPConfigError(err)
	}
	configured, err := model.ConfiguredProviderMap(map[string]any{model.GCPProviderID: providerTable})
	if err != nil {
		return nil, &gcpConfigWriteError{code: config.ConfigWriteValidation, message: err.Error()}
	}
	// The preview returns only the provider overrides, matching Rust's
	// `prepare_provider_config`; verify_credentials merges them onto the
	// built-in defaults exactly once.
	provider := configured[model.GCPProviderID]
	if strings.TrimSpace(provider.BaseURL) != "" {
		return nil, jsonRPCInvalidRequest("Google Cloud has a custom `base_url`. Clear `model_providers.gcp.base_url` before configuring a project and location.")
	}
	permit, err := r.gcpSetupNetworkPermit()
	if err != nil {
		return nil, jsonRPCInvalidRequest(err.Error())
	}
	if permit != nil {
		defer permit.Release()
	}
	// Even checking a Windows UNC credential path can contact the remote host, so
	// the credential command runs under the account's application network permit
	// (Rust NetworkPermit::run).
	verifyErr, runErr := network.RunWithNetworkPermit(context.Background(), permit, func(ctx context.Context) error {
		return verifyGCPCredentials(ctx, &provider, cwd)
	})
	if runErr != nil {
		return nil, jsonRPCInvalidRequest(runErr.Error())
	}
	if verifyErr != nil {
		return nil, verifyErr
	}
	response, err := r.requireConfig().BatchWrite(&config.ConfigBatchWriteParams{
		Edits:           write.Edits,
		ExpectedVersion: &write.ExpectedVersion,
	})
	if err != nil {
		return nil, mapGCPConfigError(err)
	}
	if response != nil && response.Status == config.WriteOKOverridden {
		message := "Configuration was overridden by a higher-precedence layer"
		if response.OverriddenMetadata != nil && strings.TrimSpace(response.OverriddenMetadata.Message) != "" {
			message = response.OverriddenMetadata.Message
		}
		return nil, &gcpConfigWriteError{code: config.ConfigWriteLayerReadonly, message: message}
	}
	r.applyConfigBatchWriteMutation(&config.ConfigBatchWriteParams{Edits: write.Edits})
	r.cancelAllAccountLoginRuntimes()
	r.requireAccount().CancelActiveLogins()
	r.invalidateModelCatalog()
	return &GcpSetupResponse{}, nil
}

// gcpSetupCWD resolves the working directory GCP credential paths and auth
// commands are relative to. Rust uses the app-server's startup cwd, never
// CODEX_HOME.
func (r *RuntimeRouter) gcpSetupCWD() string {
	if cwd := r.startupCWD(); cwd != "" {
		return cwd
	}
	return "."
}

// gcpSetupNetworkPermit acquires the account's application network permit, as
// Rust does before checking a credential path that may be a Windows UNC share.
func (r *RuntimeRouter) gcpSetupNetworkPermit() (*network.NetworkPermit, error) {
	if r == nil {
		return nil, nil
	}
	policy, _ := r.refreshApplicationNetworkPolicy()
	return policy.ForCurrentAccount().AcquireForUnsupportedSDK()
}

// verifyGCPCredentials mirrors gcp_setup::verify_credentials: validate the
// credential file shape and obtain an access token before saving.
func verifyGCPCredentials(ctx context.Context, configured *model.ProviderInfo, cwd string) error {
	if configured == nil {
		return jsonRPCInvalidRequest("missing GCP provider configuration")
	}
	if err := configured.ValidateGCPCredentialFile(); err != nil {
		return newGCPSetupError(gcpAuthReasonCredentialFileInvalid, err.Error())
	}
	defaultGcloud := configured.Auth == nil
	candidate := model.CreateGCPProvider()
	if err := model.MergeGCPProviderOverrides(&candidate, *configured); err != nil {
		return jsonRPCInvalidRequest(err.Error())
	}
	if candidate.Auth == nil {
		candidate.Auth = defaultGCPAuthInfo(cwd)
	}
	if err := candidate.Validate(); err != nil {
		return jsonRPCInvalidRequest(err.Error())
	}
	// Only the default gcloud auth falls back to the ambient ADC file. A
	// configured auth command owns its own credential discovery.
	var credentialFile *string
	if candidate.CredentialFile != nil {
		value := *candidate.CredentialFile
		credentialFile = &value
	} else if defaultGcloud {
		if value, ok := os.LookupEnv(model.GCPCredentialFileEnvVar); ok {
			credentialFile = &value
		}
	}
	if credentialFile != nil {
		path := *credentialFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		if err := validateGCPCredentialFileShape(path, defaultGcloud); err != nil {
			return newGCPSetupError(gcpAuthReasonCredentialFileInvalid, err.Error())
		}
	}
	if baseURL := strings.TrimSpace(candidate.BaseURL); baseURL != "" {
		if _, err := url.Parse(baseURL); err != nil {
			return jsonRPCInvalidRequest(err.Error())
		}
	} else if _, err := model.GCPRuntimeBaseURL(&candidate, os.Getenv(model.GCPProjectEnvVar), os.Getenv(model.GCPLocationEnvVar)); err != nil {
		return jsonRPCInvalidRequest(err.Error())
	}
	authInfo := *candidate.Auth
	// Rust's command auth manager injects only the provider's configured
	// credential_file; an ambient GOOGLE_APPLICATION_CREDENTIALS is inherited
	// from the app-server's own environment instead.
	if candidate.CredentialFile != nil {
		authInfo.Env = map[string]string{model.GCPCredentialFileEnvVar: *candidate.CredentialFile}
	}
	if _, err := model.ResolveProviderCommandAuth(ctx, &authInfo); err != nil {
		if defaultGcloud && errors.Is(err, exec.ErrNotFound) && isDirectory(cwd) {
			return newGCPSetupError(gcpAuthReasonGcloudNotInstalled, "Cannot find gcloud on the app-server host. Check its installation and PATH.")
		}
		return newGCPSetupError(gcpAuthReasonAuthCommandFailed, "The Google Cloud auth command could not obtain an access token.")
	}
	return nil
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func defaultGCPAuthInfo(cwd string) *model.ProviderAuthInfo {
	command := "gcloud"
	if runtime.GOOS == "windows" {
		command = "gcloud.cmd"
	}
	return &model.ProviderAuthInfo{
		Command:           command,
		Args:              []string{"auth", "application-default", "print-access-token"},
		TimeoutMS:         model.GCPAuthCommandTimeoutMS,
		RefreshIntervalMS: model.GCPAuthRefreshMS,
		CWD:               cwd,
	}
}

// validateGCPCredentialFileShape requires a readable regular file. Application
// Default Credentials files must also be a single JSON object with a non-empty
// string `type`; Rust streams the object past its type-specific fields and then
// calls json.end().
func validateGCPCredentialFileShape(path string, requireADC bool) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("Cannot read the Google Cloud credential file: %v", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("Cannot read the Google Cloud credential file: expected a regular file")
	}
	if !requireADC {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("Cannot read the Google Cloud credential file: %v", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var credential map[string]any
	if err := decoder.Decode(&credential); err != nil {
		return errors.New("Expected a Google Cloud credential file containing a JSON object with a nonempty string type.")
	}
	// json.end(): reject trailing JSON after the first object.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("Expected a Google Cloud credential file containing a JSON object with a nonempty string type.")
	}
	kind, ok := credential["type"].(string)
	if !ok || strings.TrimSpace(kind) == "" {
		return errors.New("Expected a Google Cloud credential file containing a JSON object with a nonempty string type.")
	}
	return nil
}

// invalidateModelCatalog forces the next model/list to reload the effective
// provider configuration (Rust model_catalog.invalidate()).
func (r *RuntimeRouter) invalidateModelCatalog() {
	if r == nil {
		return
	}
	r.authRevisionMu.Lock()
	r.modelCatalogInvalidated = true
	r.authRevisionMu.Unlock()
}
