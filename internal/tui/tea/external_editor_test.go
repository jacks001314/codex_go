package tea

import (
	"errors"
	"os"
	"reflect"
	"runtime"
	"testing"
)

func TestResolveExternalEditorCommandPrefersVisual(t *testing.T) {
	withEditorEnv(t, "vis --wait", "ed")

	got, err := resolveExternalEditorCommand()
	if err != nil {
		t.Fatalf("resolveExternalEditorCommand() error = %v", err)
	}
	if want := []string{"vis", "--wait"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveExternalEditorCommand() = %#v, want %#v", got, want)
	}
}

func TestResolveExternalEditorCommandErrorsWhenUnset(t *testing.T) {
	withEditorEnv(t, "", "")
	os.Unsetenv("VISUAL")
	os.Unsetenv("EDITOR")

	_, err := resolveExternalEditorCommand()
	if !errors.Is(err, errExternalEditorMissing) {
		t.Fatalf("resolveExternalEditorCommand() error = %v, want missing editor", err)
	}
}

func TestResolveExternalEditorCommandEmptyVisualDoesNotFallback(t *testing.T) {
	withEditorEnv(t, "", "ed")

	_, err := resolveExternalEditorCommand()
	if !errors.Is(err, errExternalEditorEmpty) {
		t.Fatalf("resolveExternalEditorCommand() error = %v, want empty editor", err)
	}
}

func TestSplitEditorCommandLineQuotedPath(t *testing.T) {
	got, err := splitEditorCommandLine(`"editor app" --wait`)
	if err != nil {
		t.Fatalf("splitEditorCommandLine() error = %v", err)
	}
	want := []string{"editor app", "--wait"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitEditorCommandLine() = %#v, want %#v", got, want)
	}
}

func TestSplitEditorCommandLineRejectsUnclosedQuote(t *testing.T) {
	got, err := splitEditorCommandLine(`"vim`)
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatalf("splitEditorCommandLine() error = %v, want nil on Windows", err)
		}
		if want := []string{"vim"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("splitEditorCommandLine() = %#v, want %#v", got, want)
		}
		return
	}
	if !errors.Is(err, errExternalEditorParse) {
		t.Fatalf("splitEditorCommandLine() error = %v, want parse error", err)
	}
}

func TestSplitEditorCommandLinePreservesQuotedEmptyArgsLikeRust(t *testing.T) {
	got, err := splitEditorCommandLine(`vim "" " "`)
	if err != nil {
		t.Fatalf("splitEditorCommandLine() error = %v", err)
	}
	want := []string{"vim", "", " "}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitEditorCommandLine() = %#v, want %#v", got, want)
	}
}

func TestSplitEditorCommandLineSingleQuoteMatchesRustPlatform(t *testing.T) {
	got, err := splitEditorCommandLine(`vim 'two words'`)
	if err != nil {
		t.Fatalf("splitEditorCommandLine() error = %v", err)
	}
	want := []string{"vim", "two words"}
	if runtime.GOOS == "windows" {
		want = []string{"vim", "'two", "words'"}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitEditorCommandLine() = %#v, want %#v", got, want)
	}
}

func TestExternalEditorCommandRunReturnsUpdatedContent(t *testing.T) {
	t.Setenv("GO_WANT_EXTERNAL_EDITOR_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	command := newExternalEditorCommand("seed", []string{
		exe,
		"-test.run=^TestExternalEditorHelperProcess$",
		"--",
	})

	if err := command.Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := command.EditedText(); got != "edited\n" {
		t.Fatalf("EditedText() = %q, want edited newline", got)
	}
}

func TestExternalEditorHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_EXTERNAL_EDITOR_HELPER") != "1" {
		return
	}
	if len(os.Args) == 0 {
		t.Fatal("missing helper args")
	}
	path := os.Args[len(os.Args)-1]
	if err := os.WriteFile(path, []byte("edited\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func withEditorEnv(t *testing.T, visual string, editor string) {
	t.Helper()
	oldVisual, hadVisual := os.LookupEnv("VISUAL")
	oldEditor, hadEditor := os.LookupEnv("EDITOR")
	t.Cleanup(func() {
		restoreEnvValue("VISUAL", oldVisual, hadVisual)
		restoreEnvValue("EDITOR", oldEditor, hadEditor)
	})
	os.Setenv("VISUAL", visual)
	os.Setenv("EDITOR", editor)
}

func restoreEnvValue(key string, value string, ok bool) {
	if ok {
		os.Setenv(key, value)
		return
	}
	os.Unsetenv(key)
}
