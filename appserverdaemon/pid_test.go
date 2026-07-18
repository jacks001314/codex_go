package appserverdaemon

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPIDBackendCommandArgsAndEnv(t *testing.T) {
	disabled := NewPIDBackend(BackendPaths{RemoteControlEnabled: false})
	if got, want := disabled.CommandArgs(), []string{"app-server", "--listen", "unix://"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("disabled CommandArgs = %#v, want %#v", got, want)
	}
	if got := disabled.CommandEnv(); !reflect.DeepEqual(got, map[string]string{RemoteControlDisabledEnvVar: "1"}) {
		t.Fatalf("disabled CommandEnv = %#v", got)
	}

	enabled := NewPIDBackend(BackendPaths{RemoteControlEnabled: true})
	if got, want := enabled.CommandArgs(), []string{"app-server", "--remote-control", "--listen", "unix://"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("enabled CommandArgs = %#v, want %#v", got, want)
	}
	if got := enabled.CommandEnv(); got != nil {
		t.Fatalf("enabled CommandEnv = %#v, want nil", got)
	}
}

func TestPIDUpdateLoopCommandArgsAndEnv(t *testing.T) {
	backend := NewPIDUpdateLoopBackend(BackendPaths{})
	if got, want := backend.CommandArgs(), []string{"app-server", "daemon", "pid-update-loop"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("update loop CommandArgs = %#v, want %#v", got, want)
	}
	if got := backend.CommandEnv(); got != nil {
		t.Fatalf("update loop CommandEnv = %#v, want nil", got)
	}
}

func TestReadStderrLogTailReturnsRecentCompleteLines(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), PIDFileName)
	logFile := StderrLogPathForPIDFile(pidFile)
	contents := strings.Repeat("x", int(StderrLogTailBytes)+4) + "\nrecent error\nusage"
	if err := os.WriteFile(logFile, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile stderr log error = %v", err)
	}

	tail, err := ReadStderrLogTail(pidFile)
	if err != nil {
		t.Fatalf("ReadStderrLogTail error = %v", err)
	}
	if tail == nil {
		t.Fatal("tail = nil")
	}
	if tail.Path != logFile {
		t.Fatalf("tail path = %q, want %q", tail.Path, logFile)
	}
	if got, want := tail.Contents, "recent error\nusage"; got != want {
		t.Fatalf("tail contents = %q, want %q", got, want)
	}
}

func TestReadStderrLogTailMissingAndEmptyReturnNil(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), PIDFileName)
	tail, err := ReadStderrLogTail(pidFile)
	if err != nil {
		t.Fatalf("ReadStderrLogTail missing error = %v", err)
	}
	if tail != nil {
		t.Fatalf("missing tail = %#v, want nil", tail)
	}

	logFile := StderrLogPathForPIDFile(pidFile)
	if err := os.WriteFile(logFile, nil, 0o600); err != nil {
		t.Fatalf("WriteFile empty stderr log error = %v", err)
	}
	tail, err = ReadStderrLogTail(pidFile)
	if err != nil {
		t.Fatalf("ReadStderrLogTail empty error = %v", err)
	}
	if tail != nil {
		t.Fatalf("empty tail = %#v, want nil", tail)
	}
}

func TestPIDLogTailAppendToContext(t *testing.T) {
	path := filepath.Join("state", "app-server.stderr.log")
	context := "app server did not become ready"
	(&PIDLogTail{
		Path:     path,
		Contents: "first\r\nsecond\n",
	}).AppendToContext(&context)

	want := "app server did not become ready\n\nManaged app-server stderr (" + path + "):\n  first\n  second"
	if context != want {
		t.Fatalf("context = %q, want %q", context, want)
	}
}
