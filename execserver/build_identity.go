package execserver

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Rust parity: codex-rs/build-info (build_id) and exec-server-protocol
// EnvironmentInfo.executor_version/provider_id (#43513).

// BuildIdentity is the running executor's stamped commit and compiler target.
type BuildIdentity struct {
	Commit string
	Target string
}

var (
	execServerBuildIdentity BuildIdentity
	execServerVersion       = "0.0.0"
)

// SetExecServerBuildIdentity records the executor's stamped commit and target
// once at startup. Runtime environment overrides must not replace it
// (Rust #43513).
func SetExecServerBuildIdentity(commit string, target string) {
	execServerBuildIdentity = BuildIdentity{
		Commit: strings.TrimSpace(commit),
		Target: strings.TrimSpace(target),
	}
}

// SetExecServerVersion records the executor's packaged release version. The
// default is `0.0.0`, matching Rust's unknown-executor-version default.
func SetExecServerVersion(version string) {
	if trimmed := strings.TrimSpace(version); trimmed != "" {
		execServerVersion = trimmed
	}
}

// ExecServerExecutorVersion returns the executor release version reported in
// environment metadata; `0.0.0` when unknown.
func ExecServerExecutorVersion() string {
	if strings.TrimSpace(execServerVersion) == "" {
		return "0.0.0"
	}
	return execServerVersion
}

// ExecServerProviderID returns the opaque standard-build identity for the
// running executor, or "" when the build is not a standard stamped build.
func ExecServerProviderID() string {
	id, ok := BuildID(execServerBuildIdentity.Commit, execServerBuildIdentity.Target)
	if !ok {
		return ""
	}
	return id
}

// BuildID derives a standard build's opaque ID from its stamped commit and
// compiler target: SHA-256 of `git:<lowercase commit>:<target>`, formatted as
// `sha256:<hex>` (Rust #43513 build_id). It identifies a build configuration,
// not exact executable bytes, and is unavailable when the commit is not a full
// 40-digit hex string or the target is empty.
func BuildID(commit string, target string) (string, bool) {
	commit = strings.TrimSpace(commit)
	target = strings.TrimSpace(target)
	if len(commit) != 40 || target == "" {
		return "", false
	}
	for i := 0; i < len(commit); i++ {
		digit := commit[i]
		if (digit < '0' || digit > '9') && (digit < 'a' || digit > 'f') && (digit < 'A' || digit > 'F') {
			return "", false
		}
	}
	sum := sha256.Sum256([]byte("git:" + strings.ToLower(commit) + ":" + target))
	return "sha256:" + hex.EncodeToString(sum[:]), true
}
