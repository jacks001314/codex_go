package prompt

import (
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
