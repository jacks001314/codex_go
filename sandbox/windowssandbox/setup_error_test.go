package windowssandbox

import "testing"

func TestSetupErrorReportRoundTrip(t *testing.T) {
	home := t.TempDir()
	report := &SetupErrorReport{Code: SetupErrorHelperUnknownError, Message: "boom"}
	if err := WriteSetupErrorReport(home, report); err != nil {
		t.Fatalf("WriteSetupErrorReport() error = %v", err)
	}
	got, err := ReadSetupErrorReport(home)
	if err != nil {
		t.Fatalf("ReadSetupErrorReport() error = %v", err)
	}
	if got == nil || got.Code != report.Code || got.Message != report.Message {
		t.Fatalf("report = %#v", got)
	}
	if err := ClearSetupErrorReport(home); err != nil {
		t.Fatalf("ClearSetupErrorReport() error = %v", err)
	}
	got, err = ReadSetupErrorReport(home)
	if err != nil || got != nil {
		t.Fatalf("after clear report = %#v err = %v", got, err)
	}
}

func TestSanitizeTagValueRedactsUsernameSegments(t *testing.T) {
	msg := `failed to write C:\Users\Alice\file.txt; fallback D:\Profiles\Bob\x`
	got := RedactUsernameSegmentsFromSetupMessage(msg, []string{"Alice", "Bob"})
	want := `failed to write C:\Users\<user>\file.txt; fallback D:\Profiles\<user>\x`
	if got != want {
		t.Fatalf("redacted = %q, want %q", got, want)
	}
}
