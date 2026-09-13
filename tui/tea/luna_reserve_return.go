package tea

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Rust parity: codex-rs/tui/src/chatwidget/luna_reserve_return.rs. The
// account-bound return model is persisted per task before automatically
// entering Reserve, so a reconnect or resume can restore the original model
// without changing global model defaults.

const (
	reserveReturnDirectoryName = "tui-luna-reserve"
	reserveReturnMaxBytes      = 4096
)

type reserveReturnFile struct {
	AccountID string  `json:"account_id"`
	Model     string  `json:"model"`
	Effort    *string `json:"effort"`
}

func reserveReturnPath(codexHome string, threadID string) string {
	return filepath.Join(strings.TrimSpace(codexHome), reserveReturnDirectoryName, strings.TrimSpace(threadID)+".json")
}

// loadReserveReturn reads the persisted return target. A missing, oversized, or
// corrupt cache yields nil.
func loadReserveReturn(codexHome string, threadID string) *ReserveReturn {
	if strings.TrimSpace(codexHome) == "" || strings.TrimSpace(threadID) == "" {
		return nil
	}
	file, err := os.Open(reserveReturnPath(codexHome, threadID))
	if err != nil {
		return nil
	}
	defer file.Close()
	limited := make([]byte, reserveReturnMaxBytes)
	read, err := file.Read(limited)
	if err != nil && read == 0 {
		return nil
	}
	var stored reserveReturnFile
	if json.Unmarshal(limited[:read], &stored) != nil {
		return nil
	}
	if strings.TrimSpace(stored.AccountID) == "" || strings.TrimSpace(stored.Model) == "" {
		return nil
	}
	return &ReserveReturn{
		AccountID: stored.AccountID,
		Model:     stored.Model,
		Effort:    strings.TrimSpace(stringPtrOrEmpty(stored.Effort)),
	}
}

// saveReserveReturn atomically persists the return target.
func saveReserveReturn(codexHome string, threadID string, value *ReserveReturn) error {
	if strings.TrimSpace(codexHome) == "" || strings.TrimSpace(threadID) == "" || value == nil {
		return nil
	}
	path := reserveReturnPath(codexHome, threadID)
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	var effort *string
	if strings.TrimSpace(value.Effort) != "" {
		text := value.Effort
		effort = &text
	}
	data, err := json.Marshal(reserveReturnFile{AccountID: value.AccountID, Model: value.Model, Effort: effort})
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, "reserve-return-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

// clearReserveReturn removes a persisted target; a missing cache needs no cleanup.
func clearReserveReturn(codexHome string, threadID string) {
	if strings.TrimSpace(codexHome) == "" || strings.TrimSpace(threadID) == "" {
		return
	}
	_ = os.Remove(reserveReturnPath(codexHome, threadID))
}

func stringPtrOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
