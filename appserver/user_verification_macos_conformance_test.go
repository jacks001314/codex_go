//go:build darwin

package appserver

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// DarwinUserVerificationErrorCase is one (domain, code) pair from Rust's
// platform_macos/error.rs classification table.
type darwinUserVerificationErrorCase struct {
	domain  string
	code    int64
	kind    string
	reason  string
	message string
}

// darwinUserVerificationErrorTable enumerates every pair platform_macos/error.rs
// classifies plus the wrong-domain combinations that must fall through to
// providerError: 17 classified pairs + 10 cross-domain pairs = 27 cases.
func darwinUserVerificationErrorTable() []darwinUserVerificationErrorCase {
	const (
		localAuth = "com.apple.LocalAuthentication"
		osStatus  = "NSOSStatusErrorDomain"
	)
	userCancelled := darwinUserVerificationErrorCase{
		kind: "cancelled", reason: string(UserVerificationCancellationUserCancelled), message: "authentication was cancelled",
	}
	interrupted := darwinUserVerificationErrorCase{
		kind: "cancelled", reason: string(UserVerificationCancellationInterrupted), message: "authentication was interrupted",
	}
	authFailed := darwinUserVerificationErrorCase{
		kind: "failed", reason: string(UserVerificationFailureAuthenticationFailed), message: "biometric authentication failed",
	}
	biometrics := darwinUserVerificationErrorCase{
		kind: "unavailable", reason: string(UserVerificationUnavailableBiometricsUnavailable), message: "biometric authentication is not available right now",
	}
	authUINotPermitted := darwinUserVerificationErrorCase{
		kind: "unavailable", reason: string(UserVerificationUnavailableProviderUnavailable), message: "authentication UI is not permitted in this process",
	}
	entitlements := darwinUserVerificationErrorCase{
		kind: "unavailable", reason: string(UserVerificationUnavailableProviderUnavailable), message: "this binary is missing the required keychain entitlements",
	}
	keychain := darwinUserVerificationErrorCase{
		kind: "unavailable", reason: string(UserVerificationUnavailableProviderUnavailable), message: "the platform keychain is not available right now",
	}
	unknown := darwinUserVerificationErrorCase{
		kind: "failed", reason: string(UserVerificationFailureProviderError), message: "the platform could not complete user verification",
	}
	with := func(template darwinUserVerificationErrorCase, domain string, code int64) darwinUserVerificationErrorCase {
		template.domain = domain
		template.code = code
		return template
	}
	return []darwinUserVerificationErrorCase{
		// 17 classified pairs.
		with(userCancelled, osStatus, -128),
		with(userCancelled, localAuth, -2),
		with(userCancelled, localAuth, -3),
		with(interrupted, localAuth, -4),
		with(interrupted, localAuth, -9),
		with(authFailed, osStatus, -25293),
		with(authFailed, localAuth, -1),
		with(biometrics, localAuth, -5),
		with(biometrics, localAuth, -6),
		with(biometrics, localAuth, -7),
		with(biometrics, localAuth, -8),
		with(biometrics, localAuth, -12),
		with(biometrics, localAuth, -13),
		with(authUINotPermitted, localAuth, -1004),
		with(entitlements, osStatus, -34018),
		with(keychain, osStatus, -25291),
		with(keychain, osStatus, -25308),
		// 10 wrong-domain pairs: the same codes under the other domain must not
		// impersonate cancellation or unavailability.
		with(unknown, localAuth, -128),
		with(unknown, osStatus, -2),
		with(unknown, osStatus, -3),
		with(unknown, osStatus, -4),
		with(unknown, osStatus, -9),
		with(unknown, osStatus, -1),
		with(unknown, osStatus, -1005),
		with(unknown, osStatus, -5),
		with(unknown, localAuth, -25293),
		with(unknown, localAuth, -34018),
	}
}

// TestDarwinUserVerificationErrorClassificationCoversRustTable walks the whole
// platform_macos/error.rs table and checks both the native category and the
// public RPC error the app-server adapter emits for it.
func TestDarwinUserVerificationErrorClassificationCoversRustTable(t *testing.T) {
	table := darwinUserVerificationErrorTable()
	if len(table) != 27 {
		t.Fatalf("error table has %d cases, want 27", len(table))
	}
	adapterMessage := map[string]string{
		"cancelled":   userVerificationCancelledMessage,
		"unavailable": userVerificationLocalUnavailableMessage,
		"failed":      userVerificationFailedMessage,
	}
	seen := map[string]bool{}
	for _, tc := range table {
		if seen[tc.domain+":"+strconv.FormatInt(tc.code, 10)] {
			t.Fatalf("duplicate case %s:%d", tc.domain, tc.code)
		}
		seen[tc.domain+":"+strconv.FormatInt(tc.code, 10)] = true

		native := classifyDarwinUserVerificationError(tc.domain, tc.code)
		if native.kind != tc.kind || native.reason != tc.reason || native.message != tc.message {
			t.Errorf("classify(%s, %d) = %s/%s/%q, want %s/%s/%q",
				tc.domain, tc.code, native.kind, native.reason, native.message, tc.kind, tc.reason, tc.message)
			continue
		}
		// The adapter must hide OS diagnostics behind its fixed messages and
		// report every non-validation failure as a JSON-RPC -32603.
		err, ok := native.rpcError().(*userVerificationError)
		if !ok {
			t.Fatalf("rpcError(%s, %d) = %T, want *userVerificationError", tc.domain, tc.code, native.rpcError())
		}
		if err.data.Type != tc.kind || err.data.Reason != tc.reason {
			t.Errorf("rpc data(%s, %d) = %+v, want %s/%s", tc.domain, tc.code, err.data, tc.kind, tc.reason)
		}
		if err.message != adapterMessage[tc.kind] {
			t.Errorf("rpc message(%s, %d) = %q, want %q", tc.domain, tc.code, err.message, adapterMessage[tc.kind])
		}
		if err.invalid {
			t.Errorf("rpc error(%s, %d) must not be an invalid-params error", tc.domain, tc.code)
		}
		if code := runtimeErrorCode(err); code != -32603 {
			t.Errorf("JSON-RPC code(%s, %d) = %d, want -32603", tc.domain, tc.code, code)
		}
	}
}

// TestDarwinUserVerificationLockTimeoutKeepsHolderLockFile checks the
// contention path Rust's lifecycle_lock covers: a timed-out waiter reports
// timeout (never panics) and leaves the holder's lock file and lock in place.
func TestDarwinUserVerificationLockTimeoutKeepsHolderLockFile(t *testing.T) {
	previousRoot := darwinUserVerificationLockRootOverride
	previousWait := darwinUserVerificationLockWaitTime
	previousPoll := darwinUserVerificationLockPollTime
	darwinUserVerificationLockRootOverride = ""
	t.Cleanup(func() {
		darwinUserVerificationLockRootOverride = previousRoot
		darwinUserVerificationLockWaitTime = previousWait
		darwinUserVerificationLockPollTime = previousPoll
	})
	directory := t.TempDir()
	darwinUserVerificationLockRootOverride = directory

	namespace := darwinUserVerificationLabel("account-user-contention")
	holder := &darwinUserVerificationProvider{backend: &fakeDarwinUVBackend{}, namespace: namespace}
	releaseHolder, lockErr := holder.lock()
	if lockErr != nil {
		t.Fatalf("holder lock = %+v", lockErr)
	}

	waiter := &darwinUserVerificationProvider{backend: &fakeDarwinUVBackend{}, namespace: namespace}
	darwinUserVerificationLockWaitTime = 120 * time.Millisecond
	darwinUserVerificationLockPollTime = 10 * time.Millisecond
	releaseWaiter, timeoutErr := waiter.lock()
	if releaseWaiter != nil {
		t.Fatal("the waiter must not acquire a held lock")
	}
	if timeoutErr == nil || timeoutErr.kind != "failed" ||
		timeoutErr.reason != string(UserVerificationFailureTimeout) ||
		timeoutErr.message != darwinUserVerificationLockTimeoutMessage {
		t.Fatalf("waiter error = %+v, want failed/timeout", timeoutErr)
	}

	// The waiter must leave the holder's file and flock alone.
	lockPath := filepath.Join(directory, namespace+".lock")
	info, statErr := os.Stat(lockPath)
	if statErr != nil {
		t.Fatalf("lock file disappeared after a timed-out waiter: %v", statErr)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("lock file mode = %o, want 0600", info.Mode().Perm())
	}
	probe, openErr := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if openErr != nil {
		t.Fatalf("open lock file: %v", openErr)
	}
	defer probe.Close()
	if err := unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
		_ = unix.Flock(int(probe.Fd()), unix.LOCK_UN)
		t.Fatal("the timed-out waiter released somebody else's lock")
	}

	releaseHolder()
	releaseAcquired, afterErr := waiter.lock()
	if afterErr != nil {
		t.Fatalf("lock after the holder released = %+v", afterErr)
	}
	releaseAcquired()
}
