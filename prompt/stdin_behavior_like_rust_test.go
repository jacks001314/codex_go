package prompt

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// terminalReader reports itself as a terminal, matching what codexmain passes
// when the process stdin is a TTY.
type terminalReader struct {
	*strings.Reader
}

func (terminalReader) IsTerminal() bool { return true }

func TestReadStdinPromptBehaviorsLikeRust(t *testing.T) {
	tests := []struct {
		name       string
		behavior   StdinPromptBehavior
		terminal   bool
		input      string
		wantText   string
		wantOK     bool
		wantStderr string
		wantError  string
		direct     bool
	}{
		{
			name:       "required with terminal stdin",
			behavior:   RequiredIfPiped,
			terminal:   true,
			wantError:  "No prompt provided. Either specify one as an argument or pipe the prompt into stdin.",
			direct:     true,
			wantStderr: "",
		},
		{
			name:       "required with empty pipe",
			behavior:   RequiredIfPiped,
			input:      "  \n",
			wantStderr: "Reading prompt from stdin...\n",
			wantError:  "No prompt provided via stdin.",
			direct:     true,
		},
		{
			name:       "required with piped prompt",
			behavior:   RequiredIfPiped,
			input:      "hello\n",
			wantText:   "hello\n",
			wantOK:     true,
			wantStderr: "Reading prompt from stdin...\n",
		},
		{
			name:      "forced with empty pipe",
			behavior:  Forced,
			wantError: "No prompt provided via stdin.",
			direct:    true,
		},
		{
			name:     "forced with piped prompt",
			behavior: Forced,
			input:    "hello\n",
			wantText: "hello\n",
			wantOK:   true,
		},
		{
			name:     "optional append with terminal stdin",
			behavior: OptionalAppend,
			terminal: true,
			wantOK:   false,
		},
		{
			name:       "optional append with blank pipe",
			behavior:   OptionalAppend,
			input:      "\n",
			wantStderr: "Reading additional input from stdin...\n",
		},
		{
			name:       "optional append with piped context",
			behavior:   OptionalAppend,
			input:      "context\n",
			wantText:   "context\n",
			wantOK:     true,
			wantStderr: "Reading additional input from stdin...\n",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var reader io.Reader = strings.NewReader(testCase.input)
			if testCase.terminal {
				reader = terminalReader{strings.NewReader(testCase.input)}
			}
			var stderr bytes.Buffer
			text, ok, err := ReadStdinPrompt(reader, &stderr, testCase.behavior)
			if stderr.String() != testCase.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), testCase.wantStderr)
			}
			if text != testCase.wantText || ok != testCase.wantOK {
				t.Fatalf("text = %q ok = %v, want %q %v", text, ok, testCase.wantText, testCase.wantOK)
			}
			if testCase.wantError == "" {
				if err != nil {
					t.Fatalf("error = %v, want none", err)
				}
				return
			}
			if err == nil || err.Error() != testCase.wantError {
				t.Fatalf("error = %v, want %q", err, testCase.wantError)
			}
			// Rust prints these lines with eprintln! before exiting, so they
			// must not pick up anyhow's "Error: " prefix.
			var direct *DirectExitError
			if errors.As(err, &direct) != testCase.direct {
				t.Fatalf("direct exit message = %v, want %v", !testCase.direct, testCase.direct)
			}
		})
	}
}

func TestResolveExecPromptLikeRust(t *testing.T) {
	var stderr bytes.Buffer
	text, err := ResolveExecPrompt("hi", terminalReader{strings.NewReader("ignored")}, &stderr)
	if err != nil || text != "hi" || stderr.Len() != 0 {
		t.Fatalf("positional prompt = %q err = %v stderr = %q", text, err, stderr.String())
	}

	stderr.Reset()
	text, err = ResolveExecPrompt("-", strings.NewReader("from stdin\n"), &stderr)
	if err != nil || text != "from stdin\n" || stderr.Len() != 0 {
		t.Fatalf("forced prompt = %q err = %v stderr = %q", text, err, stderr.String())
	}

	stderr.Reset()
	text, err = ResolveExecPrompt("", strings.NewReader(""), &stderr)
	if err == nil || text != "" {
		t.Fatalf("empty stdin prompt = %q err = %v", text, err)
	}
	if stderr.String() != "Reading prompt from stdin...\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestResolveRootPromptAppendsStdinContextLikeRust(t *testing.T) {
	var stderr bytes.Buffer
	text, err := ResolveRootPrompt("hi", strings.NewReader("context\n"), &stderr)
	if err != nil {
		t.Fatalf("ResolveRootPrompt error = %v", err)
	}
	want := "hi\n\n<stdin>\ncontext\n</stdin>"
	if text != want {
		t.Fatalf("prompt = %q, want %q", text, want)
	}
	if stderr.String() != "Reading additional input from stdin...\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}

	// A terminal stdin is never read for a positional prompt.
	stderr.Reset()
	text, err = ResolveRootPrompt("hi", terminalReader{strings.NewReader("ignored")}, &stderr)
	if err != nil || text != "hi" || stderr.Len() != 0 {
		t.Fatalf("terminal prompt = %q err = %v stderr = %q", text, err, stderr.String())
	}

	// No positional prompt still reads the pipe as the prompt itself.
	stderr.Reset()
	text, err = ResolveRootPrompt("", strings.NewReader("piped\n"), &stderr)
	if err != nil || text != "piped\n" {
		t.Fatalf("piped prompt = %q err = %v", text, err)
	}
}
