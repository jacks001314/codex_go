package windowssandbox

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	json "github.com/goccy/go-json"
)

const denyReadACLStateFile = "deny_read_acl_state.json"

// denyReadACLStateRetryDeadline bounds how long a malformed or contended state
// file is retried before recovery (Rust #50940). Tests shrink it through the
// variables so recovery does not slow the suite down.
var (
	denyReadACLStateRetryDeadline = 2 * time.Second
	denyReadACLStateRetryInterval = 25 * time.Millisecond
)

type persistentDenyReadACLState struct {
	Principals map[string][]string `json:"principals"`
}

func denyReadACLStatePath(codexHome string) string {
	return filepath.Join(SandboxDir(codexHome), denyReadACLStateFile)
}

func emptyDenyReadACLState() *persistentDenyReadACLState {
	return &persistentDenyReadACLState{Principals: map[string][]string{}}
}

// loadDenyReadACLState reads the deny-read ACL bookkeeping. Malformed content is
// retried briefly (a legacy writer may be mid-update), and once the retry
// deadline passes the state is rebuilt as empty so reconciliation can re-apply
// the current denies; unknown historical restrictions are never revoked because
// their paths are no longer known (Rust #50940).
func loadDenyReadACLState(path string) (*persistentDenyReadACLState, error) {
	deadline := time.Now().Add(denyReadACLStateRetryDeadline)
	for {
		// Once the deadline passes the read excludes live writers, so recovery
		// cannot rebuild bookkeeping while a legacy writer still holds it.
		stable := !time.Now().Before(deadline)
		data, err := readDenyReadACLStateBytes(path, stable)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return emptyDenyReadACLState(), nil
			}
			return nil, fmt.Errorf("read deny-read ACL state %s: %w", path, err)
		}
		state, err := parseDenyReadACLState(data, path)
		if err == nil {
			return state, nil
		}
		if stable {
			return emptyDenyReadACLState(), nil
		}
		time.Sleep(denyReadACLStateRetryInterval)
	}
}

// readDenyReadACLStateBytes opens and validates the state leaf before reading.
func readDenyReadACLStateBytes(path string, stable bool) ([]byte, error) {
	file, err := openDenyReadACLStateForRead(path, stable)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := validateDenyReadACLStateFile(file); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func parseDenyReadACLState(data []byte, path string) (*persistentDenyReadACLState, error) {
	var state persistentDenyReadACLState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse deny-read ACL state %s: %w", path, err)
	}
	if state.Principals == nil {
		state.Principals = map[string][]string{}
	}
	return &state, nil
}

// storeDenyReadACLState writes the bookkeeping in place, validating the opened
// leaf first so a reparse point or multiply-linked file is never truncated or
// overwritten (Rust #50940).
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
	deadline := time.Now().Add(denyReadACLStateRetryDeadline)
	var file *os.File
	for {
		file, err = openDenyReadACLStateForWrite(path)
		if err == nil {
			break
		}
		if isDenyReadStateSharingViolation(err) && time.Now().Before(deadline) {
			time.Sleep(denyReadACLStateRetryInterval)
			continue
		}
		return fmt.Errorf("write deny-read ACL state %s: %w", path, err)
	}
	defer file.Close()
	if err := validateDenyReadACLStateFile(file); err != nil {
		return fmt.Errorf("write deny-read ACL state %s: %w", path, err)
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write deny-read ACL state %s: %w", path, err)
	}
	if err := file.Truncate(int64(len(data))); err != nil {
		return fmt.Errorf("write deny-read ACL state %s: %w", path, err)
	}
	return nil
}
