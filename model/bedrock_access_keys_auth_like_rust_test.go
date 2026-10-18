package model

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"codex_go/auth"
)

// TestBedrockAccessKeysSignSigV4LikeRust covers Rust #40481's managed AWS
// access-key path: the selected keys sign Bedrock requests without touching the
// process AWS credential chain.
func TestBedrockAccessKeysSignSigV4LikeRust(t *testing.T) {
	info := CreateAmazonBedrockProvider(&ProviderAWSAuthInfo{Region: "us-west-2"})
	provider := CreateRuntimeProvider(info, &auth.AuthDotJSON{
		AuthMode: "bedrock-access-keys",
		BedrockAccessKeys: &auth.BedrockAccessKeysAuth{
			AccessKeyID:     "AKIDEXAMPLE",
			SecretAccessKey: "secret-example",
			SessionToken:    "session-example",
			Region:          "us-west-2",
		},
	})
	headers, err := provider.APIAuth()
	if err != nil {
		t.Fatalf("APIAuth() error = %v", err)
	}
	if headers.SignRequest == nil {
		t.Fatal("access-key auth did not install SigV4 signing")
	}
	request, err := http.NewRequest(http.MethodPost, "https://bedrock-mantle.us-west-2.api.aws/openai/v1/responses", strings.NewReader(`{"input":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	request.Body = io.NopCloser(strings.NewReader(string(body)))
	signed, err := headers.SignRequest(context.Background(), request, body)
	if err != nil {
		t.Fatalf("SignRequest() error = %v", err)
	}
	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/") {
		t.Fatalf("Authorization = %q", authorization)
	}
	if request.Header.Get("X-Amz-Security-Token") != "session-example" {
		t.Fatalf("session token header = %q", request.Header.Get("X-Amz-Security-Token"))
	}
	if signed == nil || len(signed.Body) == 0 {
		t.Fatalf("signed request = %#v", signed)
	}
}
