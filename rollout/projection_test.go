package rollout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPaginatedRecorderWritesAndResumesOrdinalsLikeRust(t *testing.T) {
	home := t.TempDir()
	recorder, err := NewRecorder(&CreateParams{CodexHome: home, ThreadID: "thread-1", HistoryMode: "paginated"})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStarted("turn-1", fixedProjectionTime()); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	lines, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].Ordinal == nil || *lines[0].Ordinal != 0 || lines[1].Ordinal == nil || *lines[1].Ordinal != 1 {
		t.Fatalf("initial ordinals = %#v, %#v", lines[0].Ordinal, lines[1].Ordinal)
	}
	resumed, err := Resume(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := resumed.AppendTurnComplete("turn-1", fixedProjectionTime(), 0); err != nil {
		t.Fatal(err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatal(err)
	}
	lines, _, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[2].Ordinal == nil || *lines[2].Ordinal != 2 {
		t.Fatalf("resumed ordinal = %#v", lines[2].Ordinal)
	}
}

func TestReadProjectionStepsDefersRejectedRetryLikeRust(t *testing.T) {
	path := writeProjectionFixture(t, "{not json}\n"+projectionLineJSON(0, "turn-0"))
	steps, nextOffset, err := ReadProjectionSteps(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Kind != ProjectionLine || steps[0].Ordinal != 0 {
		t.Fatalf("steps = %#v", steps)
	}
	info, _ := os.Stat(path)
	if nextOffset != uint64(info.Size()) {
		t.Fatalf("next offset = %d, want %d", nextOffset, info.Size())
	}
}

func TestReadProjectionStepsLeavesUnprojectableTailPendingLikeRust(t *testing.T) {
	path := writeProjectionFixture(t, `{"ordinal":0,"type":"future_item","payload":{}}`+"\n")
	steps, nextOffset, err := ReadProjectionSteps(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 0 || nextOffset != 0 {
		t.Fatalf("steps/offset = %#v/%d", steps, nextOffset)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString(projectionLineJSON(0, "retry") + `{"ordinal":1,"type":"future_item","payload":{}}` + "\n" + projectionLineJSON(2, "turn-2"))
	_ = file.Close()
	steps, nextOffset, err = ReadProjectionSteps(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 || steps[0].Ordinal != 0 || steps[1].Kind != ProjectionSkippedOrdinalRange || steps[1].Ordinal != 1 || steps[1].EndOrdinalExclusive != 2 || steps[2].Ordinal != 2 {
		t.Fatalf("steps = %#v", steps)
	}
	info, _ := os.Stat(path)
	if nextOffset != uint64(info.Size()) {
		t.Fatalf("next offset = %d, want %d", nextOffset, info.Size())
	}
}

func TestReadProjectionStepsRejectsUnexplainedOrdinalGapLikeRust(t *testing.T) {
	path := writeProjectionFixture(t, "{not json}\n"+projectionLineJSON(3, "turn-3"))
	_, nextOffset, err := ReadProjectionSteps(path, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "cannot cover that gap") || nextOffset != 0 {
		t.Fatalf("error/offset = %v/%d", err, nextOffset)
	}
}

func TestReadProjectionStepsAdvancesOnlyBlankPrefixLikeRust(t *testing.T) {
	path := writeProjectionFixture(t, "\n \t\r\n{not json}\n")
	steps, nextOffset, err := ReadProjectionSteps(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 0 || nextOffset != 5 {
		t.Fatalf("steps/offset = %#v/%d", steps, nextOffset)
	}
}

func projectionLineJSON(ordinal uint64, turnID string) string {
	return `{"timestamp":"2025-01-01T00:00:00Z","ordinal":` + uintString(ordinal) + `,"type":"event_msg","payload":{"type":"turn_started","turn_id":"` + turnID + `"}}` + "\n"
}

func uintString(value uint64) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 20)
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	for left, right := 0, len(digits)-1; left < right; left, right = left+1, right-1 {
		digits[left], digits[right] = digits[right], digits[left]
	}
	return string(digits)
}

func writeProjectionFixture(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixedProjectionTime() time.Time {
	return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
}
