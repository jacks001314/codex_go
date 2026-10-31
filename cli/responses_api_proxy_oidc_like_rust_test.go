package cli

import (
	"strings"
	"testing"
)

// Rust responses-api-proxy declares `--github-oidc` with requires_all on the
// workload identity settings and `requires = "github_oidc"` on each of them
// (#55320). These tests pin the parsed values and clap's diagnostics.
func TestParseResponsesAPIProxyGithubOIDCLikeRust(t *testing.T) {
	parsed, err := Parse([]string{
		"responses-api-proxy",
		"--github-oidc",
		"--oidc-audience", "aud-1",
		"--identity-provider-id=idp-1",
		"--service-account-id", "sa-1",
		"--github-oidc-request-url=https://example.test/oidc",
	})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	proxy := parsed.ResponsesAPIProxy
	if !proxy.GithubOIDC {
		t.Fatalf("GithubOIDC = false")
	}
	if proxy.OIDCAudience != "aud-1" || proxy.IdentityProviderID != "idp-1" ||
		proxy.ServiceAccountID != "sa-1" || proxy.GithubOIDCRequestURL != "https://example.test/oidc" {
		t.Fatalf("workload identity settings = %#v", proxy)
	}
	if proxy.UpstreamURL != "https://api.openai.com/v1/responses" {
		t.Fatalf("UpstreamURL = %q, want the default OpenAI endpoint", proxy.UpstreamURL)
	}
}

func TestParseResponsesAPIProxyGithubOIDCRequiresEverySettingLikeRust(t *testing.T) {
	_, err := Parse([]string{"responses-api-proxy", "--github-oidc"})
	usageErr, ok := err.(*UsageError)
	if !ok {
		t.Fatalf("Parse error = %v, want a clap diagnostic", err)
	}
	want := "error: the following required arguments were not provided:\n" +
		"  --oidc-audience <AUDIENCE>\n" +
		"  --identity-provider-id <ID>\n" +
		"  --service-account-id <ID>\n" +
		"  --github-oidc-request-url <URL>\n\n" +
		"Usage: codex responses-api-proxy --github-oidc <--oidc-audience <AUDIENCE>|--identity-provider-id <ID>|--service-account-id <ID>|--github-oidc-request-url <URL>>\n\n" +
		"For more information, try '--help'."
	if got := strings.TrimRight(clapErrorText(usageErr.Message, usageErr.Usage), "\n"); got != want {
		t.Fatalf("diagnostic =\n%s\nwant:\n%s", got, want)
	}
}

func TestParseResponsesAPIProxyOIDCSettingRequiresGithubOIDCLikeRust(t *testing.T) {
	_, err := Parse([]string{"responses-api-proxy", "--oidc-audience", "aud-1"})
	usageErr, ok := err.(*UsageError)
	if !ok {
		t.Fatalf("Parse error = %v, want a clap diagnostic", err)
	}
	want := "error: the following required arguments were not provided:\n  --github-oidc\n\n" +
		"Usage: codex responses-api-proxy --oidc-audience <AUDIENCE> --github-oidc\n\n" +
		"For more information, try '--help'."
	if got := strings.TrimRight(clapErrorText(usageErr.Message, usageErr.Usage), "\n"); got != want {
		t.Fatalf("diagnostic =\n%s\nwant:\n%s", got, want)
	}
}

func TestParseResponsesAPIProxyAcceptsEmptyWorkloadIdentityValuesLikeRust(t *testing.T) {
	// clap satisfies a requires relation on presence, so an empty value parses;
	// the proxy rejects it when it builds the workload identity auth.
	if _, err := Parse([]string{
		"responses-api-proxy",
		"--github-oidc",
		"--oidc-audience=",
		"--identity-provider-id=",
		"--service-account-id=",
		"--github-oidc-request-url=",
	}); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}
