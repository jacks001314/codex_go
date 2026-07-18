package execpolicy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
)

const defaultPolicyFile = "default.rules"

func DefaultPolicyPath(codexHome string) string {
	return filepath.Join(codexHome, "rules", defaultPolicyFile)
}

func AppendNetworkRule(policyPath string, host string, protocol string, decision Decision, justification string) error {
	host, err := normalizeNetworkRuleHost(host)
	if err != nil {
		return err
	}
	protocol, err = parseNetworkRuleProtocol(protocol)
	if err != nil {
		return err
	}
	decisionValue := ""
	switch decision {
	case DecisionAllow:
		decisionValue = "allow"
	case DecisionPrompt:
		decisionValue = "prompt"
	case DecisionForbidden:
		decisionValue = "deny"
	default:
		return fmt.Errorf("unsupported decision %q", decision)
	}
	if justification != "" && strings.TrimSpace(justification) == "" {
		return fmt.Errorf("justification cannot be empty")
	}

	fields := []struct {
		name  string
		value string
	}{
		{name: "host", value: host},
		{name: "protocol", value: protocol},
		{name: "decision", value: decisionValue},
	}
	if justification != "" {
		fields = append(fields, struct {
			name  string
			value string
		}{name: "justification", value: justification})
	}
	args := make([]string, 0, len(fields))
	for _, field := range fields {
		encoded, marshalErr := json.Marshal(field.value)
		if marshalErr != nil {
			return fmt.Errorf("serialize network rule field %s: %w", field.name, marshalErr)
		}
		args = append(args, field.name+"="+string(encoded))
	}
	return appendRuleLine(policyPath, "network_rule("+strings.Join(args, ", ")+")")
}

func appendRuleLine(policyPath string, line string) error {
	policyPath = filepath.Clean(policyPath)
	dir := filepath.Dir(policyPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create policy directory %s: %w", dir, err)
	}

	// Use a sibling lock file so Windows callers can still read and append the
	// policy file while holding the cross-process lock.
	lock := flock.New(policyPath + ".lock")
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("failed to lock policy file %s: %w", policyPath, err)
	}
	defer func() { _ = lock.Unlock() }()

	contents, err := os.ReadFile(policyPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read policy file %s: %w", policyPath, err)
	}
	for _, existing := range strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n") {
		if existing == line {
			return nil
		}
	}

	file, err := os.OpenFile(policyPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open policy file %s: %w", policyPath, err)
	}
	defer file.Close()
	if len(contents) > 0 && contents[len(contents)-1] != '\n' {
		if _, err := file.WriteString("\n"); err != nil {
			return fmt.Errorf("failed to write policy file %s: %w", policyPath, err)
		}
	}
	if _, err := file.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("failed to write policy file %s: %w", policyPath, err)
	}
	return nil
}
