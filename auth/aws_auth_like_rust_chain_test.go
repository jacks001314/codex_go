package auth

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Rust parity (aws-auth/src/lib.rs): the SDK errors are reported verbatim.
func TestAWSAuthSDKErrorMessageLikeRust(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{ErrAWSAuthMissingRegion, "AWS SDK config did not resolve a region"},
		{ErrAWSAuthMissingCredentialsProvider, "AWS SDK config did not resolve a credentials provider"},
		{ErrAWSAuthCredentials, "failed to load AWS credentials"},
	} {
		if got := tc.err.Error(); got != tc.want {
			t.Fatalf("error = %q, want %q", got, tc.want)
		}
	}
}

// clearAWSCredentialEnvironmentLikeRust isolates a test from the host's AWS
// configuration so the SDK chain starts from an empty machine.
func clearAWSCredentialEnvironmentLikeRust(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"AWS_ACCESS_KEY_ID",
		"AWS_ACCESS_KEY",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SECRET_KEY",
		"AWS_SESSION_TOKEN",
		"AWS_PROFILE",
		"AWS_DEFAULT_PROFILE",
		"AWS_REGION",
		"AWS_DEFAULT_REGION",
		"AWS_EC2_METADATA_DISABLED",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI",
		"AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_ROLE_ARN",
	} {
		t.Setenv(key, "")
	}
	home := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(home, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(home, "credentials"))
	os.WriteFile(filepath.Join(home, "config"), nil, 0o600)
	os.WriteFile(filepath.Join(home, "credentials"), nil, 0o600)
}

// Rust parity (AwsAuthContext::load): the region is resolved before any
// credential lookup, and no credentials are fetched while loading. A machine
// without credentials therefore loads successfully and only signing reports
// "failed to load AWS credentials: the credential provider was not enabled",
// matching aws-config's disabled provider instead of an IMDS timeout.
func TestLoadAWSAuthContextDefersCredentialsLikeRust(t *testing.T) {
	clearAWSCredentialEnvironmentLikeRust(t)

	context, err := LoadAWSAuthContext(&AWSAuthConfig{Region: "us-east-1", Service: "bedrock"})
	if err != nil {
		t.Fatalf("LoadAWSAuthContext() error = %v, want a resolved context", err)
	}
	_, err = context.SignAt(&AWSAuthRequestToSign{
		Method: http.MethodPost,
		URL:    "https://bedrock.us-east-1.amazonaws.com/v1/responses",
	}, time.Unix(1700000000, 0).UTC())
	if !errors.Is(err, ErrAWSAuthCredentials) {
		t.Fatalf("signing error = %v, want ErrAWSAuthCredentials", err)
	}
	if got := err.Error(); got != "failed to load AWS credentials: the credential provider was not enabled" {
		t.Fatalf("signing error = %q", got)
	}
}

// Rust parity (AwsAuthContext::load): the missing region wins over the missing
// credential provider because resolved_region runs after load_sdk_config, which
// never reports missing credentials.
func TestLoadAWSAuthContextNeedsRegionBeforeCredentialsLikeRust(t *testing.T) {
	clearAWSCredentialEnvironmentLikeRust(t)

	_, err := LoadAWSAuthContext(&AWSAuthConfig{Service: "bedrock"})
	if !errors.Is(err, ErrAWSAuthMissingRegion) {
		t.Fatalf("LoadAWSAuthContext() error = %v, want ErrAWSAuthMissingRegion", err)
	}
}

// Rust parity (exec-server-cli/src/exec_server_auth.rs): an explicit profile
// tolerates an unknown profile name - the environment still resolves the region
// and signing reports the disabled credential provider.
func TestLoadAWSAuthContextAcceptsUnknownProfileLikeRust(t *testing.T) {
	clearAWSCredentialEnvironmentLikeRust(t)
	t.Setenv("AWS_REGION", "us-east-1")

	context, err := LoadAWSAuthContext(&AWSAuthConfig{Profile: "nope", Service: "bedrock"})
	if err != nil {
		t.Fatalf("LoadAWSAuthContext() error = %v, want a resolved context", err)
	}
	if context.Region() != "us-east-1" {
		t.Fatalf("region = %q, want the environment region", context.Region())
	}
	_, err = context.Sign(&AWSAuthRequestToSign{
		Method: http.MethodPost,
		URL:    "https://bedrock.us-east-1.amazonaws.com/v1/responses",
	})
	if !errors.Is(err, ErrAWSAuthCredentials) || !strings.Contains(err.Error(), "the credential provider was not enabled") {
		t.Fatalf("signing error = %v, want the disabled credential provider", err)
	}
}

// Rust parity (ProfileFileCredentialsProvider): an explicit profile takes
// priority over environment keys, which are ignored entirely.
func TestLoadAWSAuthContextProfileBeatsEnvironmentKeysLikeRust(t *testing.T) {
	clearAWSCredentialEnvironmentLikeRust(t)
	home := t.TempDir()
	credentialsPath := filepath.Join(home, "credentials")
	configPath := filepath.Join(home, "config")
	if err := os.WriteFile(credentialsPath, []byte("[managed]\naws_access_key_id = profile-key\naws_secret_access_key = profile-secret\n"), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("[profile managed]\nregion = us-west-2\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsPath)
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_ACCESS_KEY_ID", "env-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
	t.Setenv("AWS_REGION", "us-east-1")

	context, err := LoadAWSAuthContext(&AWSAuthConfig{Profile: "managed", Service: "bedrock"})
	if err != nil {
		t.Fatalf("LoadAWSAuthContext() error = %v", err)
	}
	// Rust resolves the region through load_sdk_config, where AWS_REGION wins
	// over the profile's region = ... entry (verified against codex 0.166).
	if context.Region() != "us-east-1" {
		t.Fatalf("region = %q, want the environment region", context.Region())
	}
	signed, err := context.SignAt(&AWSAuthRequestToSign{
		Method: http.MethodPost,
		URL:    "https://bedrock.us-east-1.amazonaws.com/v1/responses",
	}, time.Unix(1700000000, 0).UTC())
	if err != nil {
		t.Fatalf("SignAt() error = %v", err)
	}
	if got := signed.Headers.Get("Authorization"); !strings.Contains(got, "Credential=profile-key/") {
		t.Fatalf("Authorization = %q, want the profile key", got)
	}
}
