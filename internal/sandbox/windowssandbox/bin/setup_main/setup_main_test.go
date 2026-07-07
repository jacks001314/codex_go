package setup_main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestRunReportsMissingPayloadOrExplicitUnsupported(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(nil, nil, &stderr)
	if code == 0 {
		t.Fatalf("Run() code = 0, want failure")
	}
	want := "bin.setup_main.win.run"
	if runtime.GOOS == "windows" {
		want = "helper_request_args_failed: expected payload argument"
	}
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}
