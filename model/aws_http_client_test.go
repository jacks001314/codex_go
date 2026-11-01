package model

import (
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"codex_go/auth"
	"codex_go/network"
)

// Rust parity (#47408): the AWS SDK's credential and region requests run through
// the host's application client, which the model layer carries into the loaders.
func TestAWSHTTPClientRoundTripLikeRust(t *testing.T) {
	defer SetAWSHTTPClient(nil)
	if AWSHTTPClient() != nil || awsAuthLoadOptions() != nil {
		t.Fatal("an uninstalled client still produced AWS load options")
	}
	client := &http.Client{}
	SetAWSHTTPClient(client)
	if AWSHTTPClient() != client {
		t.Fatal("the installed AWS client was not returned")
	}
	if options := awsAuthLoadOptions(); options == nil || options.HTTPClient != client {
		t.Fatalf("AWS load options = %#v, want the installed client", options)
	}
	SetAWSHTTPClient(nil)
	if AWSHTTPClient() != nil || awsAuthLoadOptions() != nil {
		t.Fatal("clearing the client left AWS load options installed")
	}
}

// A restricted application policy denies signing before the request leaves the
// process, through the options the model layer builds.
func TestAWSHTTPClientEnforcesPolicyThroughModelOptionsLikeRust(t *testing.T) {
	for _, key := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_DEFAULT_PROFILE"} {
		t.Setenv(key, "")
	}
	home := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(home, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(home, "credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "false")

	controller := network.NewNetworkPolicyController()
	policy := controller.Policy()
	controller.Publish(policy.Revision(), network.RestrictedDestinationPolicy([]string{"allowed.example"}))
	SetAWSApplicationClient(network.PolicyHTTPClient(policy, network.NewHTTPClient(false, 0)), policy)
	defer SetAWSApplicationClient(nil, network.UnmanagedNetworkPolicy())

	context, err := auth.LoadAWSAuthContextWithOptions(&auth.AWSAuthConfig{Region: "us-east-1", Service: "bedrock"}, awsAuthLoadOptions())
	if err != nil {
		t.Fatalf("LoadAWSAuthContextWithOptions() error = %v, want a resolved context", err)
	}
	if _, err := context.Sign(&auth.AWSAuthRequestToSign{Method: http.MethodPost, URL: "https://denied.example/v1/responses"}); !errors.Is(err, auth.ErrAWSAuthPolicy) {
		t.Fatalf("signing a denied destination error = %v, want the application network policy denial", err)
	}
}
