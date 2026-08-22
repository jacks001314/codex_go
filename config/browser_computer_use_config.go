package config

import "fmt"

// AllowDenyRequirement mirrors Rust's AllowDenyRequirement / AllowDenyRequirementToml
// allowance enumerations (allow | deny) used by browser/computer-use config and
// requirements (#40018, #39995).
type AllowDenyRequirement string

const (
	AllowDenyRequirementAllow AllowDenyRequirement = "allow"
	AllowDenyRequirementDeny  AllowDenyRequirement = "deny"
)

func (r AllowDenyRequirement) valid() bool {
	switch r {
	case AllowDenyRequirementAllow, AllowDenyRequirementDeny:
		return true
	default:
		return false
	}
}

// BrowserUseAccessApprovalLifetime mirrors Rust's
// BrowserUseAccessApprovalLifetime (turn | thread).
type BrowserUseAccessApprovalLifetime string

const (
	BrowserUseAccessApprovalLifetimeTurn   BrowserUseAccessApprovalLifetime = "turn"
	BrowserUseAccessApprovalLifetimeThread BrowserUseAccessApprovalLifetime = "thread"
)

func (l BrowserUseAccessApprovalLifetime) valid() bool {
	switch l {
	case BrowserUseAccessApprovalLifetimeTurn, BrowserUseAccessApprovalLifetimeThread:
		return true
	default:
		return false
	}
}

// validateBrowserUseConfigValues mirrors Rust BrowserUseConfigToml's
// deny_unknown_fields contract (#40018): only the documented keys are accepted
// and per-field values must be well-typed.
func validateBrowserUseConfigValues(values map[string]any) error {
	if values == nil {
		return nil
	}
	for key, raw := range values {
		switch key {
		case "allow_history_access":
			if _, ok := raw.(bool); !ok {
				return fmt.Errorf("browser_use.allow_history_access must be a boolean")
			}
		case "default_origin_policy":
			table, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("browser_use.%s must be a table", key)
			}
			if err := validateBrowserUseOriginPolicyValues(table); err != nil {
				return err
			}
		case "origins":
			table, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("browser_use.origins must be a table")
			}
			for name, value := range table {
				policy, ok := value.(map[string]any)
				if !ok {
					return fmt.Errorf("browser_use.origins.%s must be a table", name)
				}
				if err := validateBrowserUseOriginPolicyValues(policy); err != nil {
					return fmt.Errorf("browser_use.origins.%s: %w", name, err)
				}
			}
		default:
			return fmt.Errorf("unknown field %q in browser_use", key)
		}
	}
	return nil
}

func validateBrowserUseOriginPolicyValues(values map[string]any) error {
	for key, raw := range values {
		switch key {
		case "access", "downloads", "uploads", "full_cdp_access", "auto_review":
			if err := validateAllowDenyValue(raw); err != nil {
				return fmt.Errorf("browser_use.%s: %w", key, err)
			}
		case "persistent_approval":
			if _, ok := raw.(bool); !ok {
				return fmt.Errorf("browser_use.persistent_approval must be a boolean")
			}
		case "access_approval_lifetime":
			value, ok := raw.(string)
			if !ok || !BrowserUseAccessApprovalLifetime(value).valid() {
				return fmt.Errorf("browser_use.access_approval_lifetime must be turn or thread")
			}
		default:
			return fmt.Errorf("unknown field %q in browser_use origin policy", key)
		}
	}
	return nil
}

// validateComputerUseConfigValues mirrors Rust ComputerUseConfigToml's
// deny_unknown_fields contract (#40018).
func validateComputerUseConfigValues(values map[string]any) error {
	if values == nil {
		return nil
	}
	for key, raw := range values {
		switch key {
		case "default_app_access":
			if err := validateAllowDenyValue(raw); err != nil {
				return fmt.Errorf("computer_use.default_app_access: %w", err)
			}
		case "macos":
			table, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("computer_use.macos must be a table")
			}
			for k, v := range table {
				if k != "bundle_ids" {
					return fmt.Errorf("unknown field %q in computer_use.macos", k)
				}
				if err := validateAllowDenyMap(v); err != nil {
					return fmt.Errorf("computer_use.macos.bundle_ids: %w", err)
				}
			}
		case "windows":
			table, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("computer_use.windows must be a table")
			}
			for k, v := range table {
				switch k {
				case "aumids":
					if err := validateAllowDenyMap(v); err != nil {
						return fmt.Errorf("computer_use.windows.aumids: %w", err)
					}
				case "exes":
					items, ok := v.([]any)
					if !ok {
						return fmt.Errorf("computer_use.windows.exes must be an array")
					}
					for i, item := range items {
						if err := validateComputerUseWindowsExe(item); err != nil {
							return fmt.Errorf("computer_use.windows.exes[%d]: %w", i, err)
						}
					}
				default:
					return fmt.Errorf("unknown field %q in computer_use.windows", k)
				}
			}
		default:
			return fmt.Errorf("unknown field %q in computer_use", key)
		}
	}
	return nil
}

func validateComputerUseWindowsExe(raw any) error {
	table, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("must be a table")
	}
	for key, value := range table {
		switch key {
		case "publisher_name":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("publisher_name must be a string")
			}
		case "product_name":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("product_name must be a string")
			}
		case "binary_name":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("binary_name must be a string")
			}
		case "access":
			if err := validateAllowDenyValue(value); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown field %q in computer_use.windows exe", key)
		}
	}
	return nil
}

func validateAllowDenyMap(raw any) error {
	table, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("must be a table")
	}
	for _, value := range table {
		if err := validateAllowDenyValue(value); err != nil {
			return err
		}
	}
	return nil
}

func validateAllowDenyValue(raw any) error {
	value, ok := raw.(string)
	if !ok || !AllowDenyRequirement(value).valid() {
		return fmt.Errorf("must be allow or deny")
	}
	return nil
}
