package windowssandbox

import (
	"errors"
	"strings"
	"testing"
)

func TestSandboxAccountLogonFailureClassificationLikeRust(t *testing.T) {
	for _, code := range []uint32{1326, 1330, 1907} {
		err := sandboxAccountLogonFailure(code, errors.New("windows"))
		var mismatch *SandboxAccountCredentialMismatch
		if !errors.As(err, &mismatch) {
			t.Fatalf("code %d = %v, want credential mismatch", code, err)
		}
		if !strings.Contains(err.Error(), "rejected") {
			t.Fatalf("code %d message = %q", code, err)
		}
	}
	for _, code := range []uint32{5, 1056, 1069} {
		err := sandboxAccountLogonFailure(code, errors.New("windows"))
		var mismatch *SandboxAccountCredentialMismatch
		if errors.As(err, &mismatch) {
			t.Fatalf("code %d must stay a policy error: %v", code, err)
		}
	}
}

func TestCredentialsNeedRepairOnlyAfterOwnershipValidationLikeRust(t *testing.T) {
	accounts := []SandboxCredentials{
		{Username: OfflineUsername, Password: "offline", Domain: "."},
		{Username: OnlineUsername, Password: "online", Domain: "."},
	}
	type logonOutcome int
	const (
		healthy logonOutcome = iota
		passwordRejected
		policyFailure
	)
	cases := []struct {
		name             string
		outcomes         []logonOutcome
		ownershipOK      bool
		wantRepair       bool
		wantError        string
		wantOwnershipRun bool
	}{
		{
			name:     "healthy accounts reuse setup",
			outcomes: []logonOutcome{healthy, healthy},
		},
		{
			name:             "offline password rejected",
			outcomes:         []logonOutcome{passwordRejected, healthy},
			ownershipOK:      true,
			wantRepair:       true,
			wantOwnershipRun: true,
		},
		{
			name:             "online password rejected",
			outcomes:         []logonOutcome{healthy, passwordRejected},
			ownershipOK:      true,
			wantRepair:       true,
			wantOwnershipRun: true,
		},
		{
			name:        "policy failure wins",
			outcomes:    []logonOutcome{passwordRejected, policyFailure},
			ownershipOK: true,
			wantError:   "policy",
		},
		{
			name:             "replaced account blocks rotation",
			outcomes:         []logonOutcome{passwordRejected, healthy},
			ownershipOK:      false,
			wantError:        "replacement",
			wantOwnershipRun: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			outcomes := append([]logonOutcome(nil), testCase.outcomes...)
			ownershipRun := false
			repair, err := credentialsNeedRepair(
				accounts,
				func(SandboxCredentials) error {
					outcome := outcomes[0]
					outcomes = outcomes[1:]
					switch outcome {
					case passwordRejected:
						return &SandboxAccountCredentialMismatch{Underlying: errors.New("password")}
					case policyFailure:
						return errors.New("policy")
					default:
						return nil
					}
				},
				func() error {
					ownershipRun = true
					if !testCase.ownershipOK {
						return errors.New("replacement")
					}
					return nil
				},
			)
			if repair != testCase.wantRepair {
				t.Fatalf("repair = %v, want %v", repair, testCase.wantRepair)
			}
			if (testCase.wantError == "") != (err == nil) ||
				(testCase.wantError != "" && !strings.Contains(err.Error(), testCase.wantError)) {
				t.Fatalf("error = %v, want %q", err, testCase.wantError)
			}
			if ownershipRun != testCase.wantOwnershipRun {
				t.Fatalf("ownership checked = %v, want %v", ownershipRun, testCase.wantOwnershipRun)
			}
		})
	}
}

func TestSandboxAccountOwnershipRequiresRecordedMatchingSIDLikeRust(t *testing.T) {
	records := []SandboxUserRecord{
		{Username: OfflineUsername, SID: "S-1-5-21-offline"},
		{Username: OnlineUsername, SID: "S-1-5-21-online"},
	}
	resolve := func(username string) (string, error) {
		return map[string]string{
			OfflineUsername: "S-1-5-21-offline",
			OnlineUsername:  "S-1-5-21-online",
		}[username], nil
	}
	if err := validateSandboxAccountOwnership(records, resolve); err != nil {
		t.Fatalf("matching ownership error = %v", err)
	}
	missing := []SandboxUserRecord{{Username: OfflineUsername}}
	if err := validateSandboxAccountOwnership(missing, resolve); err == nil || !strings.Contains(err.Error(), "no recorded SID") {
		t.Fatalf("missing SID error = %v", err)
	}
	replaced := append([]SandboxUserRecord(nil), records...)
	replaced[1].SID = "S-1-5-21-old-online"
	if err := validateSandboxAccountOwnership(replaced, resolve); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("replaced account error = %v", err)
	}
}
