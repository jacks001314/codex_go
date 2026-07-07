package auth

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSignAddsSigV4Headers(t *testing.T) {
	context, err := NewAWSAuthContext(&AWSAuthConfig{Region: "us-east-1", Service: "bedrock"}, &AWSAuthCredentials{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		SessionToken:    "session-token",
	})
	if err != nil {
		t.Fatalf("NewAWSAuthContext() error = %v", err)
	}
	headers := http.Header{"Content-Type": []string{"application/json"}}
	signed, err := context.SignAt(&AWSAuthRequestToSign{
		Method:  http.MethodPost,
		URL:     "https://bedrock-runtime.us-east-1.amazonaws.com/v1/responses",
		Headers: headers,
		Body:    []byte(`{"model":"x"}`),
	}, time.Unix(1700000000, 0).UTC())
	if err != nil {
		t.Fatalf("SignAt() error = %v", err)
	}
	if !strings.HasPrefix(signed.Headers.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
		t.Fatalf("Authorization = %q", signed.Headers.Get("Authorization"))
	}
	if signed.Headers.Get("X-Amz-Date") == "" || signed.Headers.Get("X-Amz-Security-Token") != "session-token" {
		t.Fatalf("headers = %+v", signed.Headers)
	}
}

func TestNewAWSAuthContextValidation(t *testing.T) {
	if _, err := NewAWSAuthContext(&AWSAuthConfig{Region: "us-east-1"}, &AWSAuthCredentials{}); !errors.Is(err, ErrAWSAuthEmptyService) {
		t.Fatalf("empty service error = %v", err)
	}
	if _, err := NewAWSAuthContext(&AWSAuthConfig{Service: "bedrock"}, &AWSAuthCredentials{}); !errors.Is(err, ErrAWSAuthMissingRegion) {
		t.Fatalf("empty region error = %v", err)
	}
	if _, err := NewAWSAuthContext(&AWSAuthConfig{Service: "bedrock", Region: "us-east-1"}, &AWSAuthCredentials{}); !errors.Is(err, ErrAWSAuthMissingCredentials) {
		t.Fatalf("empty credentials error = %v", err)
	}
}

func TestLoadAWSAuthContextUsesEnvCredentialsAndRegion(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", " env-access ")
	t.Setenv("AWS_SECRET_ACCESS_KEY", " env-secret ")
	t.Setenv("AWS_SESSION_TOKEN", " env-session ")
	t.Setenv("AWS_REGION", " us-west-2 ")
	context, err := LoadAWSAuthContext(&AWSAuthConfig{Service: "bedrock-mantle"})
	if err != nil {
		t.Fatalf("LoadAWSAuthContext returned error: %v", err)
	}
	if context.Region() != "us-west-2" || context.Service() != "bedrock-mantle" {
		t.Fatalf("context region=%q service=%q", context.Region(), context.Service())
	}
	signed, err := context.SignAt(&AWSAuthRequestToSign{
		Method: http.MethodGet,
		URL:    "https://bedrock-mantle.us-west-2.api.aws/openai/v1/models",
	}, time.Unix(1700000000, 0).UTC())
	if err != nil {
		t.Fatalf("SignAt returned error: %v", err)
	}
	if !strings.Contains(signed.Headers.Get("Authorization"), "Credential=env-access/") || signed.Headers.Get("X-Amz-Security-Token") != "env-session" {
		t.Fatalf("signed headers = %#v", signed.Headers)
	}
}

func TestLoadAWSAuthContextUsesSharedCredentialsAndConfigProfile(t *testing.T) {
	dir := t.TempDir()
	credentialsPath := filepath.Join(dir, "credentials")
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(credentialsPath, []byte(`
[default]
aws_access_key_id = default-access
aws_secret_access_key = default-secret

[codex-bedrock]
aws_access_key_id = profile-access
aws_secret_access_key = profile-secret
aws_session_token = profile-session
`), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`
[profile codex-bedrock]
region = eu-central-1
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsPath)
	t.Setenv("AWS_CONFIG_FILE", configPath)

	context, err := LoadAWSAuthContext(&AWSAuthConfig{Profile: "codex-bedrock", Service: "bedrock-mantle"})
	if err != nil {
		t.Fatalf("LoadAWSAuthContext returned error: %v", err)
	}
	if context.Region() != "eu-central-1" {
		t.Fatalf("region = %q", context.Region())
	}
	signed, err := context.SignAt(&AWSAuthRequestToSign{
		Method: http.MethodGet,
		URL:    "https://bedrock-mantle.eu-central-1.api.aws/openai/v1/models",
	}, time.Unix(1700000000, 0).UTC())
	if err != nil {
		t.Fatalf("SignAt returned error: %v", err)
	}
	if !strings.Contains(signed.Headers.Get("Authorization"), "Credential=profile-access/") || signed.Headers.Get("X-Amz-Security-Token") != "profile-session" {
		t.Fatalf("signed headers = %#v", signed.Headers)
	}
}

func TestLoadAWSAuthContextUsesCredentialProcess(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	command := quoteAWSCredentialProcessTestArg(os.Args[0]) + " -test.run=TestAWSCredentialProcessHelper -- success"
	if err := os.WriteFile(configPath, []byte(`
[profile codex-bedrock]
region = us-east-2
credential_process = `+command+`
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "missing-credentials"))
	t.Setenv("GO_WANT_AWS_CREDENTIAL_PROCESS_HELPER", "1")

	context, err := LoadAWSAuthContext(&AWSAuthConfig{Profile: "codex-bedrock", Service: "bedrock-mantle"})
	if err != nil {
		t.Fatalf("LoadAWSAuthContext returned error: %v", err)
	}
	if context.Region() != "us-east-2" {
		t.Fatalf("region = %q", context.Region())
	}
	signed, err := context.SignAt(&AWSAuthRequestToSign{
		Method: http.MethodGet,
		URL:    "https://bedrock-mantle.us-east-2.api.aws/openai/v1/models",
	}, time.Unix(1700000000, 0).UTC())
	if err != nil {
		t.Fatalf("SignAt returned error: %v", err)
	}
	if !strings.Contains(signed.Headers.Get("Authorization"), "Credential=process-access/") || signed.Headers.Get("X-Amz-Security-Token") != "process-session" {
		t.Fatalf("signed headers = %#v", signed.Headers)
	}
}

func TestAWSCredentialProcessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_AWS_CREDENTIAL_PROCESS_HELPER") != "1" {
		return
	}
	fmt.Fprint(os.Stdout, `{"Version":1,"AccessKeyId":"process-access","SecretAccessKey":"process-secret","SessionToken":"process-session","Expiration":"2099-01-01T00:00:00Z"}`)
	os.Exit(0)
}

func quoteAWSCredentialProcessTestArg(value string) string {
	if runtime.GOOS == "windows" {
		return value
	}
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}
