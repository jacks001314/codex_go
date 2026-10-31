//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"codex_go/config"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

// unparseableConfigError loads a Codex home whose config.toml cannot be parsed
// and returns it under the label the policy loader attaches, mirroring Rust
// ipc_tests::config_parse_errors_are_distinguished_from_policy_and_io_failures.
func unparseableConfigError(t *testing.T) error {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("["), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	_, err := config.LoadEffectiveWithOptions(home, &config.EffectiveOptions{IncludeManagedConfig: true})
	if err == nil {
		t.Fatal("LoadEffectiveWithOptions() accepted an unparseable config.toml")
	}
	return fmt.Errorf("load managed configuration: %w", err)
}

func TestPolicyRejectionDiagnosticNamesTheFailingStage(t *testing.T) {
	status := 503
	for _, testCase := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "bootstrap io failure",
			err:  fmt.Errorf("load bootstrap configuration: %w", syscall.Errno(5)),
			want: "stage=bootstrap io=PermissionDenied win32=Some(5)",
		},
		{
			name: "cloud bundle failure",
			err: fmt.Errorf("load managed configuration: %w",
				config.NewCloudConfigLoadError(config.CloudConfigLoadRequestFailed, &status, "the bundle request was refused")),
			want: "stage=managed cloud=RequestFailed http=Some(503)",
		},
		{
			name: "unlabelled failure",
			err:  errors.New("managed policy does not permit the elevated Windows sandbox"),
			want: "stage=unknown cause=unavailable",
		},
		{
			name: "runtime failure",
			err:  fmt.Errorf("start managed configuration runtime: %w", syscall.Errno(87)),
			want: "stage=runtime io=InvalidInput win32=Some(87)",
		},
		{
			name: "policy failure",
			err: fmt.Errorf("enforce managed provisioning requirements: %w",
				errors.New("managed policy does not permit HTTP proxy port 8080")),
			want: "stage=enforce cause=unavailable",
		},
		{
			name: "authentication initialisation failure",
			err:  fmt.Errorf("initialize cloud configuration authentication: %w", syscall.Errno(232)),
			want: "stage=initialize_auth io=BrokenPipe win32=Some(232)",
		},
		{
			name: "authentication resolution failure",
			err:  fmt.Errorf("resolve cloud configuration authentication: %w", syscall.Errno(2)),
			want: "stage=resolve_auth io=NotFound win32=Some(2)",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			want := "Codex sandbox provisioning was rejected by administrator policy: " + testCase.want
			if got := policyRejectionDiagnostic(testCase.err); got != want {
				t.Fatalf("policyRejectionDiagnostic() = %q, want %q", got, want)
			}
		})
	}
}

func TestPolicyRejectionDiagnosticKeepsOnlyTypedCodes(t *testing.T) {
	secret := "Bearer synthetic-secret"
	// A parser wrapper can carry whole configuration lines, so the event text
	// must come from the service's own labels instead.
	err := fmt.Errorf("load managed configuration: %w",
		errors.New(secret+"\ntoml: basic strings cannot have new lines"))
	if got := policyRejectionDiagnostic(err); strings.Contains(got, secret) {
		t.Fatalf("policyRejectionDiagnostic() leaked the parser text: %q", got)
	}
}

func TestConfigParseErrorsBecomeUnavailable(t *testing.T) {
	parseErr := unparseableConfigError(t)
	if !isConfigParseError(parseErr) {
		t.Fatalf("isConfigParseError(%v) = false, want true", parseErr)
	}
	want := "Codex sandbox provisioning was rejected by administrator policy: stage=managed io=InvalidData win32=None"
	if got := policyRejectionDiagnostic(parseErr); got != want {
		t.Fatalf("policyRejectionDiagnostic() = %q, want %q", got, want)
	}
	for _, err := range []error{
		errors.New("managed policy does not permit the elevated Windows sandbox"),
		syscall.Errno(5),
		fmt.Errorf("load managed configuration: %w",
			config.NewCloudConfigLoadError(config.CloudConfigLoadAuth, nil, "no credentials")),
	} {
		if isConfigParseError(err) {
			t.Fatalf("isConfigParseError(%v) = true, want false", err)
		}
	}
}

func TestPolicyViolationResponseLogsOneSanitizedEvent(t *testing.T) {
	previousLog := logPolicyRejection
	logged := []string(nil)
	logPolicyRejection = func(message string) { logged = append(logged, message) }
	defer func() { logPolicyRejection = previousLog }()

	policyErr := fmt.Errorf("enforce managed provisioning requirements: %w",
		errors.New("managed policy does not permit HTTP proxy port 8080"))
	response := policyViolationResponse(policyErr)
	want := "requested sandbox settings violate administrator-controlled machine policy: enforce managed provisioning requirements: managed policy does not permit HTTP proxy port 8080"
	if response.Kind != provisionservice.ResponseError || response.Message != want {
		t.Fatalf("policyViolationResponse() = %+v, want an Error with %q", response, want)
	}
	if len(logged) != 1 {
		t.Fatalf("policy rejection logged %d events, want 1", len(logged))
	}
	wantEvent := "Codex sandbox provisioning was rejected by administrator policy: stage=enforce cause=unavailable"
	if logged[0] != wantEvent {
		t.Fatalf("rejection event = %q, want %q", logged[0], wantEvent)
	}

	logged = nil
	parseResponse := policyViolationResponse(unparseableConfigError(t))
	if parseResponse.Kind != provisionservice.ResponseUnavailable {
		t.Fatalf("policyViolationResponse(parse error) = %+v, want Unavailable", parseResponse)
	}
	if len(logged) != 0 {
		t.Fatalf("a parse failure was recorded as a policy rejection: %q", logged)
	}
}
