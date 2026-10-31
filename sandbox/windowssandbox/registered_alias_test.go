package windowssandbox

import (
	"strings"
	"testing"
)

const aliasRunnerPath = `C:\Program Files\WindowsApps\OpenAI.Codex_1.0.0.0_arm64__publisher\codex-core-command-runner.exe`

func aliasRecord() *InstallationRecord {
	record := installationFixture()
	record.Runtime = readyRuntimeFixture()
	for i := range record.Runtime.Accounts {
		record.Runtime.Accounts[i].AliasPath = stringPointer(aliasRunnerPath)
	}
	return record
}

func TestSandboxRuntimeAccountForUsernameMatchesManagedAccounts(t *testing.T) {
	caseInsensitive, err := SandboxRuntimeAccountForUsername(strings.ToUpper(OfflineUsername))
	if err != nil || caseInsensitive != SandboxRuntimeAccountOffline {
		t.Fatalf("offline lookup = %q, %v", caseInsensitive, err)
	}
	online, err := SandboxRuntimeAccountForUsername(OnlineUsername)
	if err != nil || online != SandboxRuntimeAccountOnline {
		t.Fatalf("online lookup = %q, %v", online, err)
	}
	if _, err := SandboxRuntimeAccountForUsername("Administrator"); err == nil {
		t.Fatal("a non-managed account must not map to a runtime account")
	}
}

func TestAuthorizeRunnerReceiptRefusesForeignOwners(t *testing.T) {
	owner := "S-1-5-21-1-2-3-1000"
	home := `C:\Users\owner\.codex`
	cases := []struct {
		name    string
		record  *InstallationRecord
		owner   string
		home    string
		pkg     string
		wantSub string
	}{
		{"missing receipt", nil, owner, home, readyPackageFullName, "has not completed"},
		{"other owner", aliasRecord(), "S-1-5-21-9-9-9-1000", home, readyPackageFullName, "another owner"},
		{"other home", aliasRecord(), owner, `C:\Users\other\.codex`, readyPackageFullName, "another Codex home"},
		{"retiring", func() *InstallationRecord {
			record := aliasRecord()
			generation := "one-cleanup-generation"
			record.Runtime.Retiring = &generation
			return record
		}(), owner, home, readyPackageFullName, "being removed"},
		{"stale package", aliasRecord(), owner, home, "OpenAI.Codex_2.0.0.0_arm64__publisher", "not complete for the installed app"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := AuthorizeRunnerReceipt(tc.record, tc.owner, tc.home, tc.pkg)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("AuthorizeRunnerReceipt() error = %v, want %q", err, tc.wantSub)
			}
		})
	}
	if err := AuthorizeRunnerReceipt(aliasRecord(), owner, home, readyPackageFullName); err != nil {
		t.Fatalf("authorized receipt refused: %v", err)
	}
}

func TestRunnerAliasForAccountValidatesServiceReceipt(t *testing.T) {
	registration := func() *RuntimeRegistration { return aliasRecord().Runtime }
	offlineSID := "S-1-5-21-1-2-3-1001"
	cases := []struct {
		name    string
		mutate  func(*RuntimeRegistration)
		account SandboxRuntimeAccount
		sid     string
		wantSub string
	}{
		{"unknown account", func(*RuntimeRegistration) {}, SandboxRuntimeAccount("bogus"), offlineSID, "not ready"},
		{"changed account", func(*RuntimeRegistration) {}, SandboxRuntimeAccountOffline, "S-1-5-21-1-2-3-9999", "account changed"},
		{"missing alias", func(r *RuntimeRegistration) { r.Accounts[0].AliasPath = nil }, SandboxRuntimeAccountOffline, offlineSID, "alias is not ready"},
		{"relative alias", func(r *RuntimeRegistration) { r.Accounts[0].AliasPath = stringPointer(`codex-core-command-runner.exe`) }, SandboxRuntimeAccountOffline, offlineSID, "invalid alias"},
		{"wrong alias name", func(r *RuntimeRegistration) { r.Accounts[0].AliasPath = stringPointer(`C:\aliases\codex.exe`) }, SandboxRuntimeAccountOffline, offlineSID, "invalid alias"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := registration()
			tc.mutate(runtime)
			_, err := RunnerAliasForAccount(runtime, tc.account, tc.sid)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("RunnerAliasForAccount() error = %v, want %q", err, tc.wantSub)
			}
		})
	}
	alias, err := RunnerAliasForAccount(registration(), SandboxRuntimeAccountOffline, offlineSID)
	if err != nil || alias != aliasRunnerPath {
		t.Fatalf("RunnerAliasForAccount() = %q, %v", alias, err)
	}
}
