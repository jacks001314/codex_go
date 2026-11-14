package auth

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSPIFFEOpenAIMarkersSelectWorkloadIdentity(t *testing.T) {
	for _, env := range []workloadIdentityProcessEnv{
		{hasIdentityTokenSource: true}, {hasSPIFFEAudience: true}, {hasSPIFFEID: true},
	} {
		if !env.hasMarker() {
			t.Fatal("OpenAI marker did not select workload identity")
		}
		cfg, err := resolveWorkloadIdentityConfig(defaultWorkloadChatGPTBaseURL, env)
		if cfg != nil || err == nil {
			t.Fatal("partial OpenAI marker configuration must fail closed")
		}
	}
	if (workloadIdentityProcessEnv{}).hasMarker() {
		t.Fatal("unconfigured environment selected workload identity")
	}
}

func TestWorkloadIdentitySourceConflictsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		audience, id bool
		want         string
	}{
		{"implicit file audience", "", true, false, "SPIFFE settings require"},
		{"explicit file id", "file", false, true, "SPIFFE settings require"},
		{"unknown source", "other", false, false, "must be file or spiffe"},
		{"case sensitive source", "SPIFFE", false, false, "must be file or spiffe"},
		{"spiffe plus file", "spiffe", false, false, "cannot also set"},
		{"explicit file", "file", false, false, ""},
		{"default file", "", false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := workloadIdentityProcessEnv{hasFederationRule: true, federationRuleID: "test-rule", hasIdentityToken: true, identityTokenFile: filepath.Join(t.TempDir(), "synthetic-assertion"), identityTokenSource: tc.source, hasIdentityTokenSource: tc.source != "", hasSPIFFEAudience: tc.audience, hasSPIFFEID: tc.id}
			cfg, err := resolveWorkloadIdentityConfig(defaultWorkloadChatGPTBaseURL, env)
			if tc.want == "" {
				if err != nil || cfg == nil {
					t.Fatalf("file config: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want %q", err, tc.want)
			}
		})
	}
}

func TestSPIFFERequiredSettingsAndPlatformBoundary(t *testing.T) {
	env := workloadIdentityProcessEnv{hasFederationRule: true, federationRuleID: "test-rule", hasIdentityTokenSource: true, identityTokenSource: "spiffe"}
	for _, tc := range []struct{ endpoint, audience, want string }{
		{"", "", SPIFFEEndpointSocketEnv},
		{"unix:///test/agent.sock", "", OpenAISPIFFEAudienceEnv},
	} {
		env.spiffeEndpoint, env.spiffeAudience = tc.endpoint, tc.audience
		env.hasSPIFFEEndpoint, env.hasSPIFFEAudience = tc.endpoint != "", tc.audience != ""
		_, err := resolveWorkloadIdentityConfig(defaultWorkloadChatGPTBaseURL, env)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("error=%v want=%s", err, tc.want)
		}
	}
	if spiffePlatformSupported {
		return
	}
	env.spiffeEndpoint, env.spiffeAudience = "unix:///test/agent.sock", "test-audience"
	env.hasSPIFFEEndpoint, env.hasSPIFFEAudience = true, true
	_, err := resolveWorkloadIdentityConfig(defaultWorkloadChatGPTBaseURL, env)
	if err == nil || !strings.Contains(err.Error(), "requires a Unix platform") {
		t.Fatalf("platform error=%v", err)
	}
}

func TestWorkloadIdentitySourcePreservesOptionalStringSemantics(t *testing.T) {
	for _, source := range []string{"", " file ", " spiffe ", "FILE"} {
		env := workloadIdentityProcessEnv{hasFederationRule: true, federationRuleID: "rule", hasIdentityToken: true, identityTokenFile: filepath.Join(t.TempDir(), "synthetic"), hasIdentityTokenSource: true, identityTokenSource: source}
		_, err := resolveWorkloadIdentityConfig(defaultWorkloadChatGPTBaseURL, env)
		if err == nil || !strings.Contains(err.Error(), "must be file or spiffe") {
			t.Fatalf("source %q error=%v", source, err)
		}
	}
	t.Setenv(OpenAIIdentityTokenSourceEnv, " file ")
	t.Setenv(OpenAISPIFFEIDEnv, " spiffe://example.org/test ")
	env := workloadReadProcessEnv()
	if env.identityTokenSource != " file " || env.spiffeID != " spiffe://example.org/test " {
		t.Fatal("optional source/ID unexpectedly trimmed")
	}
}

func TestSPIFFEEnvironmentUnicodeErrors(t *testing.T) {
	for _, variable := range []string{SPIFFEEndpointSocketEnv, OpenAISPIFFEAudienceEnv, OpenAISPIFFEIDEnv, OpenAIIdentityTokenSourceEnv} {
		for _, value := range []string{"", " \t\n", "synthetic-\xff"} {
			if (variable == OpenAISPIFFEIDEnv || variable == OpenAIIdentityTokenSourceEnv) && value != "synthetic-\xff" {
				continue
			}
			t.Run(variable+"/"+value, func(t *testing.T) {
				env := workloadIdentityProcessEnv{hasFederationRule: true, federationRuleID: "rule", hasIdentityTokenSource: true, identityTokenSource: "spiffe", hasSPIFFEEndpoint: true, spiffeEndpoint: "unix:///test/agent.sock", hasSPIFFEAudience: true, spiffeAudience: "codex"}
				switch variable {
				case SPIFFEEndpointSocketEnv:
					env.spiffeEndpoint = value
				case OpenAISPIFFEAudienceEnv:
					env.spiffeAudience = value
				case OpenAISPIFFEIDEnv:
					env.hasSPIFFEID = true
					env.spiffeID = value
				case OpenAIIdentityTokenSourceEnv:
					env.identityTokenSource = value
				}
				_, err := resolveWorkloadIdentityConfig(defaultWorkloadChatGPTBaseURL, env)
				want := "workload identity variable " + variable + " is invalid"
				if err == nil || err.Error() != want {
					t.Fatalf("error=%v want=%q", err, want)
				}
			})
		}
	}
}

func TestSPIFFERequiredUnicodeTrimsAndDistinguishesMissing(t *testing.T) {
	for _, variable := range []string{SPIFFEEndpointSocketEnv, OpenAISPIFFEAudienceEnv} {
		value, err := workloadRequiredUnicode(" \u2003synthetic\u2003 ", true, variable)
		if err != nil || value != "synthetic" {
			t.Fatalf("trim: value=%q error=%v", value, err)
		}
		_, err = workloadRequiredUnicode("", false, variable)
		if err == nil || err.Error() != "workload identity requires "+variable {
			t.Fatalf("missing: %v", err)
		}
	}
	t.Setenv(SPIFFEEndpointSocketEnv, " \u2003 ")
	t.Setenv(OpenAISPIFFEAudienceEnv, "")
	env := workloadReadProcessEnv()
	if !env.hasSPIFFEEndpoint || !env.hasSPIFFEAudience || env.spiffeEndpoint != " \u2003 " {
		t.Fatal("lost raw value or presence")
	}
}
