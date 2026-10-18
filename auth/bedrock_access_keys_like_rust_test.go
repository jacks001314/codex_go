package auth

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestBedrockAccessKeysSnapshotAndLoginLikeRust covers Rust #40481's managed
// auth snapshot, account/reporting mode, login params, and redacted debug.
func TestBedrockAccessKeysSnapshotAndLoginLikeRust(t *testing.T) {
	snapshot := FromBedrockAccessKeys(" AKID ", " SECRET ", " TOKEN ", " us-west-2 ")
	if snapshot.Mode() != "bedrock-access-keys" {
		t.Fatalf("mode = %q", snapshot.Mode())
	}
	keys, ok := snapshot.BedrockAccessKeys.(*BedrockAccessKeysAuth)
	if !ok || keys.AccessKeyID != "AKID" || keys.SecretAccessKey != "SECRET" ||
		keys.SessionToken != "TOKEN" || keys.Region != "us-west-2" {
		t.Fatalf("snapshot = %#v", snapshot.BedrockAccessKeys)
	}
	account := AccountFromAuth(&snapshot)
	if account == nil || account.Type != AccountAmazonBedrock || !account.UsesCodexManagedCredentials {
		t.Fatalf("account = %#v", account)
	}

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded AuthDotJSON
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Mode() != "bedrock-access-keys" {
		t.Fatalf("round-trip mode = %q", decoded.Mode())
	}

	params := &LoginAccountParams{
		Type:            "amazonBedrockAccessKeys",
		AccessKeyID:     "AKID",
		SecretAccessKey: "SECRET",
		SessionToken:    "TOKEN",
		Region:          "us-west-2",
	}
	paramJSON, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"amazonBedrockAccessKeys"`, `"accessKeyId":"AKID"`, `"secretAccessKey":"SECRET"`, `"region":"us-west-2"`} {
		if !strings.Contains(string(paramJSON), want) {
			t.Fatalf("login params = %s, missing %s", paramJSON, want)
		}
	}
	if err := params.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	manager := NewAccountManager()
	response, err := manager.Login(params)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	account = manager.GetAccount(nil).Account
	if response.Type != AccountAmazonBedrock || account == nil ||
		account.Type != AccountAmazonBedrock || !account.UsesCodexManagedCredentials {
		t.Fatalf("login response/account = %#v / %#v", response, account)
	}

	formatted := fmt.Sprintf("%+v %#v", keys, keys)
	if strings.Contains(formatted, "AKID") || strings.Contains(formatted, "SECRET") || strings.Contains(formatted, "TOKEN") {
		t.Fatalf("derived formatting leaked access-key secrets: %s", formatted)
	}
}
