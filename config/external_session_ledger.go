package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const externalSessionImportLedgerFile = "external_agent_session_imports.json"

var externalSessionLedgerMu sync.Mutex

type externalSessionImportLedger struct {
	Records []externalSessionImportRecord `json:"records"`
}

type externalSessionImportRecord struct {
	SourcePath       string   `json:"source_path"`
	ContentSHA256    string   `json:"content_sha256"`
	ImportedThreadID string   `json:"imported_thread_id"`
	ImportedAt       int64    `json:"imported_at"`
	SourceModifiedAt *int64   `json:"source_modified_at,omitempty"`
	ConnectorNames   []string `json:"connector_names"`
	Title            *string  `json:"title,omitempty"`
}

// ExternalSessionImportCompletion identifies a persisted external session that
// should be recorded in the import ledger.
type ExternalSessionImportCompletion struct {
	SourcePath       string
	ImportedThreadID string
	ConnectorNames   []string
	Title            *string
}

// ExternalSessionImportMapping describes the ledger target for one canonical
// external transcript. Ambiguous is true when legacy records map the same
// source to more than one imported thread.
type ExternalSessionImportMapping struct {
	Found               bool
	Ambiguous           bool
	SourceContentSHA256 string
	ImportedThreadID    string
}

// FindExternalSessionImport resolves the unique imported thread previously
// associated with sourcePath. Multiple records fail closed as ambiguous.
func FindExternalSessionImport(codexHome string, sourcePath string) (ExternalSessionImportMapping, error) {
	externalSessionLedgerMu.Lock()
	defer externalSessionLedgerMu.Unlock()
	canonical, _, _, err := externalSessionSourceState(sourcePath)
	if err != nil {
		return ExternalSessionImportMapping{}, err
	}
	ledger, err := loadExternalSessionImportLedger(codexHome)
	if err != nil {
		return ExternalSessionImportMapping{}, err
	}
	var matches []externalSessionImportRecord
	for _, record := range ledger.Records {
		if externalSamePath(record.SourcePath, canonical) {
			matches = append(matches, record)
		}
	}
	switch len(matches) {
	case 0:
		return ExternalSessionImportMapping{}, nil
	case 1:
		return ExternalSessionImportMapping{
			Found:               true,
			SourceContentSHA256: matches[0].ContentSHA256,
			ImportedThreadID:    matches[0].ImportedThreadID,
		}, nil
	default:
		return ExternalSessionImportMapping{Found: true, Ambiguous: true}, nil
	}
}

// ExternalSessionContentSHA256 returns the canonical source path and current
// content hash used by conditional import checkpoints.
func ExternalSessionContentSHA256(sourcePath string) (string, string, error) {
	canonical, hash, _, err := externalSessionSourceState(sourcePath)
	return canonical, hash, err
}

// CheckpointExternalSessionImport advances one unique source mapping only when
// both the ledger and the source file still match the state used for append.
func CheckpointExternalSessionImport(codexHome string, sourcePath string, importedThreadID string, expectedHash string, newHash string) (bool, error) {
	if strings.TrimSpace(expectedHash) == "" || expectedHash == newHash {
		return false, nil
	}
	externalSessionLedgerMu.Lock()
	defer externalSessionLedgerMu.Unlock()

	canonical, currentHash, modifiedAt, err := externalSessionSourceState(sourcePath)
	if err != nil {
		return false, err
	}
	if currentHash != newHash {
		return false, nil
	}
	_, verifiedHash, verifiedModifiedAt, err := externalSessionSourceState(canonical)
	if err != nil {
		return false, err
	}
	if verifiedHash != newHash || !equalExternalSessionModifiedAt(modifiedAt, verifiedModifiedAt) {
		return false, nil
	}

	ledger, err := loadExternalSessionImportLedger(codexHome)
	if err != nil {
		return false, err
	}
	matchingIndex := -1
	for index := range ledger.Records {
		if !externalSamePath(ledger.Records[index].SourcePath, canonical) {
			continue
		}
		if matchingIndex >= 0 {
			return false, nil
		}
		matchingIndex = index
	}
	if matchingIndex < 0 {
		return false, nil
	}
	record := &ledger.Records[matchingIndex]
	if record.ImportedThreadID != importedThreadID || record.ContentSHA256 != expectedHash {
		return false, nil
	}
	record.ContentSHA256 = newHash
	record.ImportedAt = time.Now().Unix()
	record.SourceModifiedAt = modifiedAt
	if err := saveExternalSessionImportLedger(codexHome, ledger); err != nil {
		return false, err
	}
	return true, nil
}

func externalSessionImportIsCurrent(codexHome string, sourcePath string) bool {
	if codexHome == "" || sourcePath == "" {
		return false
	}
	externalSessionLedgerMu.Lock()
	defer externalSessionLedgerMu.Unlock()
	ledger, err := loadExternalSessionImportLedger(codexHome)
	if err != nil || len(ledger.Records) == 0 {
		return false
	}
	canonical, hash, _, err := externalSessionSourceState(sourcePath)
	if err != nil {
		return false
	}
	for _, record := range ledger.Records {
		if externalSamePath(record.SourcePath, canonical) && record.ContentSHA256 == hash {
			return true
		}
	}
	return false
}

// RecordExternalSessionImport records a completed external transcript import.
func RecordExternalSessionImport(codexHome string, sourcePath string, importedThreadID string) error {
	return RecordExternalSessionImports(codexHome, []ExternalSessionImportCompletion{{
		SourcePath:       sourcePath,
		ImportedThreadID: importedThreadID,
	}})
}

// RecordExternalSessionImports records completed transcript imports in one
// ledger update so an error cannot leave only part of a batch recorded.
func RecordExternalSessionImports(codexHome string, imports []ExternalSessionImportCompletion) error {
	if len(imports) == 0 {
		return nil
	}
	detectedConnectorNames := externalSessionConnectorNames(imports)
	externalSessionLedgerMu.Lock()
	defer externalSessionLedgerMu.Unlock()
	ledger, err := loadExternalSessionImportLedger(codexHome)
	if err != nil {
		return err
	}
	for _, completed := range imports {
		canonical, hash, modifiedAt, stateErr := externalSessionSourceState(completed.SourcePath)
		if stateErr != nil {
			return stateErr
		}
		connectorNames := append([]string(nil), completed.ConnectorNames...)
		if len(connectorNames) == 0 {
			sessionID := strings.TrimSpace(strings.TrimSuffix(filepath.Base(completed.SourcePath), filepath.Ext(completed.SourcePath)))
			connectorNames = append(connectorNames, detectedConnectorNames[sessionID]...)
		}
		record := externalSessionImportRecord{
			SourcePath:       canonical,
			ContentSHA256:    hash,
			ImportedThreadID: completed.ImportedThreadID,
			ImportedAt:       time.Now().Unix(),
			SourceModifiedAt: modifiedAt,
			ConnectorNames:   connectorNames,
			Title:            cloneStringPtr(completed.Title),
		}
		for index := len(ledger.Records) - 1; index >= 0; index-- {
			existing := ledger.Records[index]
			if externalSamePath(existing.SourcePath, canonical) && existing.ContentSHA256 == hash {
				ledger.Records = append(ledger.Records[:index], ledger.Records[index+1:]...)
				break
			}
		}
		ledger.Records = append(ledger.Records, record)
	}
	return saveExternalSessionImportLedger(codexHome, ledger)
}

func saveExternalSessionImportLedger(codexHome string, ledger externalSessionImportLedger) error {
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(codexHome, externalSessionImportLedgerFile)
	temporary, err := os.CreateTemp(codexHome, externalSessionImportLedgerFile+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
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

func equalExternalSessionModifiedAt(left *int64, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func loadExternalSessionImportLedger(codexHome string) (externalSessionImportLedger, error) {
	data, err := os.ReadFile(filepath.Join(codexHome, externalSessionImportLedgerFile))
	if errors.Is(err, os.ErrNotExist) {
		return externalSessionImportLedger{Records: []externalSessionImportRecord{}}, nil
	}
	if err != nil {
		return externalSessionImportLedger{}, err
	}
	var ledger externalSessionImportLedger
	if err := json.Unmarshal(data, &ledger); err != nil {
		return externalSessionImportLedger{}, err
	}
	if ledger.Records == nil {
		ledger.Records = []externalSessionImportRecord{}
	}
	return ledger, nil
}

func externalSessionSourceState(sourcePath string) (string, string, *int64, error) {
	canonical, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		return "", "", nil, err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", "", nil, err
	}
	data, err := os.ReadFile(canonical)
	if err != nil {
		return "", "", nil, err
	}
	hash := sha256.Sum256(data)
	var modifiedAt *int64
	if info, statErr := os.Stat(canonical); statErr == nil {
		value := info.ModTime().UnixNano()
		modifiedAt = &value
	}
	return filepath.Clean(canonical), hex.EncodeToString(hash[:]), modifiedAt, nil
}
