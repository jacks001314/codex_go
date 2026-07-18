//go:build windows

package windowssandbox

import (
	"errors"
	"os"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConvertStringSIDToSIDRoundTrip(t *testing.T) {
	const administrators = "S-1-5-32-544"
	sid, err := ConvertStringSIDToSID(administrators)
	if err != nil {
		t.Fatalf("ConvertStringSIDToSID() error = %v", err)
	}
	if sid.String != administrators {
		t.Fatalf("LocalSID.String = %q, want %q", sid.String, administrators)
	}
	if len(sid.Bytes) == 0 {
		t.Fatalf("LocalSID.Bytes is empty")
	}
	if got := StringFromSIDBytes(sid.Bytes); got != administrators {
		t.Fatalf("StringFromSIDBytes() = %q, want %q", got, administrators)
	}
}

func TestGetCurrentTokenForRestrictionOpensToken(t *testing.T) {
	token, err := GetCurrentTokenForRestriction()
	if err != nil {
		t.Fatalf("GetCurrentTokenForRestriction() error = %v", err)
	}
	if token == 0 {
		t.Fatalf("GetCurrentTokenForRestriction() returned zero token")
	}
	if err := CloseTokenHandle(token); err != nil {
		t.Fatalf("CloseTokenHandle() error = %v", err)
	}
}

func TestCloseTokenHandleZero(t *testing.T) {
	if err := CloseTokenHandle(0); err != nil {
		t.Fatalf("CloseTokenHandle(0) error = %v", err)
	}
}

func TestCreateReadonlyTokenWithCapsFromCreatesRestrictedToken(t *testing.T) {
	baseToken, err := GetCurrentTokenForRestriction()
	if err != nil {
		t.Fatalf("GetCurrentTokenForRestriction() error = %v", err)
	}
	defer CloseTokenHandle(baseToken)

	restricted, err := CreateReadonlyTokenWithCapsFrom(baseToken, []string{"S-1-5-21-1-2-3-4"})
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			t.Skipf("host rejected WRITE_RESTRICTED CreateRestrictedToken: %v", err)
		}
		t.Fatalf("CreateReadonlyTokenWithCapsFrom() error = %v", err)
	}
	if restricted == 0 {
		t.Fatalf("CreateReadonlyTokenWithCapsFrom() returned zero token")
	}
	if err := CloseTokenHandle(restricted); err != nil {
		t.Fatalf("CloseTokenHandle(restricted) error = %v", err)
	}
}

func TestCreateReadonlyTokenWithCapsFromRejectsEmptyCaps(t *testing.T) {
	baseToken, err := GetCurrentTokenForRestriction()
	if err != nil {
		t.Fatalf("GetCurrentTokenForRestriction() error = %v", err)
	}
	defer CloseTokenHandle(baseToken)

	if _, err := CreateReadonlyTokenWithCapsFrom(baseToken, nil); err == nil {
		t.Fatalf("CreateReadonlyTokenWithCapsFrom(nil) error = nil, want error")
	}
}

func TestCreateRestrictedTokenFlagMatrix(t *testing.T) {
	if os.Getenv("CODEX_WINDOWS_SANDBOX_TOKEN_MATRIX") == "" {
		t.Skip("set CODEX_WINDOWS_SANDBOX_TOKEN_MATRIX=1 to run")
	}
	baseToken, err := GetCurrentTokenForRestriction()
	if err != nil {
		t.Fatalf("GetCurrentTokenForRestriction() error = %v", err)
	}
	defer CloseTokenHandle(baseToken)

	capBuffers, capEntries, err := sidAttributesFromStrings([]string{"S-1-5-21-1-2-3-4"})
	if err != nil {
		t.Fatalf("sidAttributesFromStrings() error = %v", err)
	}
	logonSID, err := getLogonSIDBytes(windows.Token(baseToken))
	if err != nil {
		t.Fatalf("getLogonSIDBytes() error = %v", err)
	}
	everyoneSID, err := worldSIDBytes()
	if err != nil {
		t.Fatalf("worldSIDBytes() error = %v", err)
	}
	userSID, err := getUserSIDBytes(windows.Token(baseToken))
	if err != nil {
		t.Fatalf("getUserSIDBytes() error = %v", err)
	}

	flagCases := []struct {
		name  string
		flags uint32
	}{
		{"full", restrictedTokenDisableMaxPrivilege | restrictedTokenLUAToken | restrictedTokenWriteRestricted},
		{"no-write-restricted", restrictedTokenDisableMaxPrivilege | restrictedTokenLUAToken},
		{"no-lua", restrictedTokenDisableMaxPrivilege | restrictedTokenWriteRestricted},
		{"disable-max-only", restrictedTokenDisableMaxPrivilege},
		{"write-only", restrictedTokenWriteRestricted},
		{"zero", 0},
	}
	entryCases := []struct {
		name      string
		withUser  bool
		withLogon bool
		withWorld bool
	}{
		{"cap-logon-world", false, true, true},
		{"cap-user-logon-world", true, true, true},
		{"cap-only", false, false, false},
	}
	for _, entryCase := range entryCases {
		for _, flagCase := range flagCases {
			entries := append([]windows.SIDAndAttributes(nil), capEntries...)
			if entryCase.withUser {
				entries = append(entries, windows.SIDAndAttributes{Sid: sidPointerFromBytes(userSID)})
			}
			if entryCase.withLogon {
				entries = append(entries, windows.SIDAndAttributes{Sid: sidPointerFromBytes(logonSID)})
			}
			if entryCase.withWorld {
				entries = append(entries, windows.SIDAndAttributes{Sid: sidPointerFromBytes(everyoneSID)})
			}
			token, err := createRestrictedToken(windows.Token(baseToken), flagCase.flags, entries)
			if err != nil {
				t.Logf("%s/%s flags=0x%x error=%v", entryCase.name, flagCase.name, flagCase.flags, err)
				continue
			}
			t.Logf("%s/%s flags=0x%x ok", entryCase.name, flagCase.name, flagCase.flags)
			_ = token.Close()
		}
	}
	runtimeKeepAliveSIDBuffers(capBuffers, logonSID, everyoneSID)
	runtime.KeepAlive(userSID)
}
