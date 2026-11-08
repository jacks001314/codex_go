package exec

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/cli"
	"codex_go/config"
)

// ignoreUserConfigRejection is a load-time diagnostic that only a user config
// can produce, which makes it a reliable probe for "did the loader read the
// user file".
const ignoreUserConfigRejection = `approval_policy = "untrusted" is no longer supported`

// Acceptance for `codex exec --ignore-user-config`: the flag has to reach the
// configuration loader, not just the CLI parser and the worktree conflict check
// (Rust LoaderOverrides::ignore_user_config). The control run proves the probe
// really fires when the user config is loaded.
func TestExecIgnoreUserConfigReachesConfigLoader(t *testing.T) {
	home := t.TempDir()
	userConfig := "approval_policy = \"untrusted\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(userConfig), 0o644); err != nil {
		t.Fatalf("WriteFile(user config) error = %v", err)
	}
	runner := NewRunner(home)
	run := func(ignoreUserConfig bool) error {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := runner.RunContext(ctx,
			&Request{Exec: cli.ExecOptions{IgnoreUserConfig: ignoreUserConfig}},
			strings.NewReader(""),
			io.Discard,
			io.Discard,
		)
		return err
	}
	err := run(true)
	// Whatever happens next is irrelevant: the only thing under test is that the
	// user config was not read. The log keeps the downstream failure visible.
	t.Logf("--ignore-user-config run error (expected to be unrelated): %v", err)
	if err != nil && strings.Contains(err.Error(), ignoreUserConfigRejection) {
		t.Fatalf("--ignore-user-config still loaded CODEX_HOME/config.toml: %v", err)
	}
	if err := run(false); err == nil || !strings.Contains(err.Error(), ignoreUserConfigRejection) {
		t.Fatalf("control run error = %v, want it to report %q", err, ignoreUserConfigRejection)
	}
}
