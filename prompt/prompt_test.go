package prompt

import (
	"bytes"
	"strings"
	"testing"
)

func TestResolveFromArg(t *testing.T) {
	prompt, err := Resolve("hello", strings.NewReader(""))
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if prompt != "hello" {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestResolveForcedStdin(t *testing.T) {
	prompt, err := Resolve("-", strings.NewReader("from stdin\n"))
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if prompt != "from stdin\n" {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestResolveAppendsStdinContext(t *testing.T) {
	prompt, err := Resolve("prompt", strings.NewReader("extra"))
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	want := "prompt\n\n<stdin>\nextra\n</stdin>"
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestResolveRustPromptStdinParity(t *testing.T) {
	tests := []struct {
		name      string
		arg       string
		stdin     string
		want      string
		wantError string
	}{
		{
			name:  "prompt appends piped stdin",
			arg:   "Summarize this concisely",
			stdin: "my output\n",
			want:  "Summarize this concisely\n\n<stdin>\nmy output\n</stdin>",
		},
		{
			name:  "prompt ignores empty piped stdin",
			arg:   "Summarize this concisely",
			stdin: "",
			want:  "Summarize this concisely",
		},
		{
			name:  "dash prompt reads stdin as prompt",
			arg:   "-",
			stdin: "prompt from stdin\n",
			want:  "prompt from stdin\n",
		},
		{
			name:  "missing prompt reads piped stdin",
			arg:   "",
			stdin: "prompt from stdin\n",
			want:  "prompt from stdin\n",
		},
		{
			name:      "dash prompt rejects empty stdin",
			arg:       "-",
			stdin:     "",
			wantError: "No prompt provided via stdin.",
		},
		{
			name:      "missing prompt rejects empty stdin",
			arg:       "",
			stdin:     "",
			wantError: "No prompt provided. Either specify one as an argument or pipe the prompt into stdin.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.arg, strings.NewReader(tt.stdin))
			if tt.wantError != "" {
				if err == nil || err.Error() != tt.wantError {
					t.Fatalf("Resolve error = %v, want %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("prompt = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveRustPromptDecodeParity(t *testing.T) {
	tests := []struct {
		name      string
		arg       string
		stdin     []byte
		want      string
		wantError string
	}{
		{
			name:  "strips UTF-8 BOM",
			arg:   "-",
			stdin: []byte{0xEF, 0xBB, 0xBF, 'h', 'i', '\n'},
			want:  "hi\n",
		},
		{
			name:  "decodes UTF-16LE BOM",
			arg:   "-",
			stdin: []byte{0xFF, 0xFE, 'h', 0x00, 'i', 0x00, '\n', 0x00},
			want:  "hi\n",
		},
		{
			name:  "decodes UTF-16BE BOM",
			arg:   "-",
			stdin: []byte{0xFE, 0xFF, 0x00, 'h', 0x00, 'i', 0x00, '\n'},
			want:  "hi\n",
		},
		{
			name:      "rejects UTF-32LE BOM",
			arg:       "-",
			stdin:     []byte{0xFF, 0xFE, 0x00, 0x00, 'h', 0x00, 0x00, 0x00},
			wantError: "input appears to be UTF-32LE. Convert it to UTF-8 and retry.",
		},
		{
			name:      "rejects UTF-32BE BOM",
			arg:       "-",
			stdin:     []byte{0x00, 0x00, 0xFE, 0xFF, 0x00, 0x00, 0x00, 'h'},
			wantError: "input appears to be UTF-32BE. Convert it to UTF-8 and retry.",
		},
		{
			name:      "rejects invalid UTF-8",
			arg:       "-",
			stdin:     []byte{0xC3, 0x28},
			wantError: "input is not valid UTF-8 (invalid byte at offset 0). Convert it to UTF-8 and retry (e.g., `iconv -f <ENC> -t UTF-8 prompt.txt`).",
		},
		{
			name:      "rejects invalid UTF-16LE",
			arg:       "-",
			stdin:     []byte{0xFF, 0xFE, 'h'},
			wantError: "input looked like UTF-16LE but could not be decoded. Convert it to UTF-8 and retry.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.arg, bytes.NewReader(tt.stdin))
			if tt.wantError != "" {
				if err == nil || err.Error() != tt.wantError {
					t.Fatalf("Resolve error = %v, want %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("prompt = %q, want %q", got, tt.want)
			}
		})
	}
}
