package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

func osGoalFS(root string) appServerGoalFS {
	return appServerGoalFS{
		createDirectoryAll: func(path string) error { return os.MkdirAll(path, 0o755) },
		writeFile:          func(path string, data []byte) error { return os.WriteFile(path, data, 0o600) },
		readFile:           func(path string) ([]byte, error) { return os.ReadFile(path) },
		remove:             func(path string, recursive bool) error { return os.RemoveAll(path) },
	}
}

func TestMaterializeGoalDraftOversizedWritesGoalFile(t *testing.T) {
	root := t.TempDir()
	objective := strings.Repeat("x", codextui.MaxGoalObjectiveRune+1)
	reference, err := materializeGoalDraft(osGoalFS(root), root, codextui.GoalDraft{Objective: objective})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	path, ok := codextui.GoalObjectiveFilePath(reference, root)
	if !ok {
		t.Fatalf("reference = %q", reference)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read goal file: %v", err)
	}
	if string(data) != objective {
		t.Fatalf("goal file content mismatch")
	}
}

func TestMaterializeGoalDraftPasteWritesPasteFile(t *testing.T) {
	root := t.TempDir()
	placeholder := "[Pasted Content 5 chars]"
	draft := codextui.GoalDraft{
		Objective:     "Use " + placeholder,
		PendingPastes: [][2]string{{placeholder, "hello"}},
	}
	objective, err := materializeGoalDraft(osGoalFS(root), root, draft)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	path, ok := strings.CutPrefix(objective, "Use pasted text file: ")
	if !ok {
		t.Fatalf("objective = %q", objective)
	}
	path, ok = strings.CutSuffix(path, ". Read this file before continuing.")
	if !ok {
		t.Fatalf("objective = %q", objective)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "hello" {
		t.Fatalf("paste file = %q err=%v", data, err)
	}
}

func TestMaterializeGoalDraftImageWithPlaceholder(t *testing.T) {
	root := t.TempDir()
	imagePath := filepath.Join(root, "local-image.png")
	if err := os.WriteFile(imagePath, []byte("png bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	draft := codextui.GoalDraft{
		Objective:   "Describe [Image #1]",
		LocalImages: []codextui.GoalLocalImage{{Placeholder: "[Image #1]", Path: imagePath}},
	}
	objective, err := materializeGoalDraft(osGoalFS(root), root, draft)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	path, ok := strings.CutPrefix(objective, "Describe image file: ")
	if !ok {
		t.Fatalf("objective = %q", objective)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "png bytes" {
		t.Fatalf("image file = %q err=%v", data, err)
	}
}

func TestMaterializeGoalDraftImageWithoutPlaceholderAppendsSection(t *testing.T) {
	root := t.TempDir()
	imagePath := filepath.Join(root, "photo.jpeg")
	if err := os.WriteFile(imagePath, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	draft := codextui.GoalDraft{
		Objective:   "plain objective",
		LocalImages: []codextui.GoalLocalImage{{Placeholder: "", Path: imagePath}},
	}
	objective, err := materializeGoalDraft(osGoalFS(root), root, draft)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if !strings.Contains(objective, "Referenced image files:\n- [Image #1]: ") {
		t.Fatalf("objective = %q", objective)
	}
}

func TestMaterializeGoalDraftRemoteURLs(t *testing.T) {
	root := t.TempDir()
	draft := codextui.GoalDraft{
		Objective:       "use this image",
		RemoteImageURLs: []string{"https://example.com/a.png"},
	}
	objective, err := materializeGoalDraft(osGoalFS(root), root, draft)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if !strings.Contains(objective, "Referenced image URLs:\n- [Image #1]: https://example.com/a.png") {
		t.Fatalf("objective = %q", objective)
	}
}

func TestMaterializeGoalDraftEmptyAndWhitespacePaste(t *testing.T) {
	root := t.TempDir()
	if _, err := materializeGoalDraft(osGoalFS(root), root, codextui.GoalDraft{Objective: "   "}); err == nil {
		t.Fatalf("empty objective accepted")
	}
	placeholder := "[Pasted Content 3 chars]"
	if _, err := materializeGoalDraft(osGoalFS(root), root, codextui.GoalDraft{
		Objective:     placeholder,
		PendingPastes: [][2]string{{placeholder, " \n\t"}},
	}); err == nil {
		t.Fatalf("whitespace-only paste accepted")
	}
}

func TestMaterializeGoalDraftStalePlaceholderSkipsFile(t *testing.T) {
	root := t.TempDir()
	attachmentsDir := filepath.Join(root, codextui.GoalAttachmentDir)
	draft := codextui.GoalDraft{
		Objective:     "small goal",
		PendingPastes: [][2]string{{"[Pasted Content 5 chars]", "hello"}},
	}
	objective, err := materializeGoalDraft(osGoalFS(root), root, draft)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if objective != "small goal" {
		t.Fatalf("objective = %q", objective)
	}
	if _, err := os.Stat(attachmentsDir); !os.IsNotExist(err) {
		t.Fatalf("stale paste should not create attachments dir")
	}
}
