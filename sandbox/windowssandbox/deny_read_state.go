package windowssandbox

import (
	"fmt"
	"os"
	"path/filepath"

	json "github.com/goccy/go-json"
)

const denyReadACLStateFile = "deny_read_acl_state.json"

type persistentDenyReadACLState struct {
	Principals map[string][]string `json:"principals"`
}

func denyReadACLStatePath(codexHome string) string {
	return filepath.Join(SandboxDir(codexHome), denyReadACLStateFile)
}

func loadDenyReadACLState(path string) (*persistentDenyReadACLState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &persistentDenyReadACLState{Principals: map[string][]string{}}, nil
		}
		return nil, fmt.Errorf("read deny-read ACL state %s: %w", path, err)
	}
	var state persistentDenyReadACLState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse deny-read ACL state %s: %w", path, err)
	}
	if state.Principals == nil {
		state.Principals = map[string][]string{}
	}
	return &state, nil
}

func storeDenyReadACLState(path string, state *persistentDenyReadACLState) error {
	if state == nil {
		return ErrInvalidRequest
	}
	if state.Principals == nil {
		state.Principals = map[string][]string{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize deny-read ACL state: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write deny-read ACL state %s: %w", path, err)
	}
	return nil
}
