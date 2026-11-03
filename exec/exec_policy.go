package exec

import (
	"path/filepath"
	"strings"

	"codex_go/config"
	"codex_go/execpolicy"
	"codex_go/model"
)

// execPolicyForRun mirrors Rust's session ExecPolicyManager for the lanes that
// run turns in-process (standalone `codex exec` and the embedded TUI): every
// `*.rules` file under `$CODEX_HOME/rules` is loaded, then the managed
// requirements tighten it. The app-server lane builds the same merge per turn in
// RuntimeRouter.execPolicyForThread; without it the exec lanes would ignore
// operator rules and stop deriving approval amendments from them.
func execPolicyForRun(codexHome string, cfg *config.Config) *execpolicy.Policy {
	var user *execpolicy.Policy
	if home := strings.TrimSpace(codexHome); home != "" {
		if policy, err := execpolicy.LoadPolicyDirectory(filepath.Join(home, "rules")); err == nil {
			user = policy
		}
	}
	var managed *execpolicy.RequirementsPolicy
	if cfg != nil && cfg.Requirements != nil {
		managed = cfg.Requirements.Rules
	}
	switch {
	case managed == nil:
		return user
	case user == nil:
		return managed.Policy()
	default:
		return execpolicy.NewRequirementsPolicy(user).MergeOverlay(managed).Policy()
	}
}

// execModelIgnoresAllowPrefixRules mirrors the app-server's
// modelIgnoresAllowPrefixRules: cyber-specialized models and models listed in
// auto_review.ignore_rules get one-time decisions without reusable allow
// prefixes.
func execModelIgnoresAllowPrefixRules(cfg *config.Config, modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return false
	}
	if cfg != nil && cfg.Requirements != nil && cfg.Requirements.AutoReview != nil {
		for _, ignored := range cfg.Requirements.AutoReview.IgnoreRules {
			if strings.TrimSpace(ignored) == modelID {
				return true
			}
		}
	}
	return strings.TrimSpace(execModelInfo(modelID, cfg).ModelSpecialty) == model.ModelSpecialtyCyber
}
