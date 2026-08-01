package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestExternalSessionImportCheckpointPreservesMetadataAndGuardsOldState(t *testing.T) {
	codexHome := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(sourcePath, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	title := "Imported title"
	if err := RecordExternalSessionImports(codexHome, []ExternalSessionImportCompletion{
		{SourcePath: sourcePath, ImportedThreadID: "thread-1", ConnectorNames: []string{"Figma"}, Title: &title},
	}); err != nil {
		t.Fatal(err)
	}
	mapping, err := FindExternalSessionImport(codexHome, sourcePath)
	if err != nil || !mapping.Found || mapping.Ambiguous {
		t.Fatalf("initial mapping = %#v err=%v", mapping, err)
	}
	oldHash := mapping.SourceContentSHA256
	if err := os.WriteFile(sourcePath, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, newHash, err := ExternalSessionContentSHA256(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if checkpointed, err := CheckpointExternalSessionImport(codexHome, sourcePath, "wrong-thread", oldHash, newHash); err != nil || checkpointed {
		t.Fatalf("wrong-thread checkpoint = %v err=%v", checkpointed, err)
	}
	if checkpointed, err := CheckpointExternalSessionImport(codexHome, sourcePath, "thread-1", "wrong-hash", newHash); err != nil || checkpointed {
		t.Fatalf("wrong-hash checkpoint = %v err=%v", checkpointed, err)
	}
	if checkpointed, err := CheckpointExternalSessionImport(codexHome, sourcePath, "thread-1", oldHash, newHash); err != nil || !checkpointed {
		t.Fatalf("valid checkpoint = %v err=%v", checkpointed, err)
	}
	ledger, err := loadExternalSessionImportLedger(codexHome)
	if err != nil || len(ledger.Records) != 1 {
		t.Fatalf("ledger = %#v err=%v", ledger, err)
	}
	record := ledger.Records[0]
	if record.ContentSHA256 != newHash || record.ImportedThreadID != "thread-1" || record.Title == nil || *record.Title != title || !reflect.DeepEqual(record.ConnectorNames, []string{"Figma"}) {
		t.Fatalf("checkpointed record = %#v", record)
	}
}

func TestExternalSessionImportCheckpointRejectsAmbiguousAndChangedSource(t *testing.T) {
	codexHome := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(sourcePath, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordExternalSessionImport(codexHome, sourcePath, "thread-1"); err != nil {
		t.Fatal(err)
	}
	mapping, err := FindExternalSessionImport(codexHome, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	oldHash := mapping.SourceContentSHA256
	if err := os.WriteFile(sourcePath, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, secondHash, err := ExternalSessionContentSHA256(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("third"), 0o600); err != nil {
		t.Fatal(err)
	}
	if checkpointed, err := CheckpointExternalSessionImport(codexHome, sourcePath, "thread-1", oldHash, secondHash); err != nil || checkpointed {
		t.Fatalf("changed-source checkpoint = %v err=%v", checkpointed, err)
	}

	ledger, err := loadExternalSessionImportLedger(codexHome)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := ledger.Records[0]
	duplicate.ImportedThreadID = "thread-2"
	ledger.Records = append(ledger.Records, duplicate)
	if err := saveExternalSessionImportLedger(codexHome, ledger); err != nil {
		t.Fatal(err)
	}
	ambiguous, err := FindExternalSessionImport(codexHome, sourcePath)
	if err != nil || !ambiguous.Found || !ambiguous.Ambiguous {
		t.Fatalf("ambiguous mapping = %#v err=%v", ambiguous, err)
	}
	_, thirdHash, err := ExternalSessionContentSHA256(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if checkpointed, err := CheckpointExternalSessionImport(codexHome, sourcePath, "thread-1", oldHash, thirdHash); err != nil || checkpointed {
		t.Fatalf("ambiguous checkpoint = %v err=%v", checkpointed, err)
	}
}
