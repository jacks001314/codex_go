package execserver

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestReadChatGPTTokenFromReaderLikeRust(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer reader.Close()
	defer writer.Close()
	go func() {
		_, _ = io.WriteString(writer, "e30.e30.c2ln\nlifetime\n")
		_ = writer.Close()
	}()
	token, remaining, err := ReadChatGPTTokenFromReader(reader)
	if err != nil {
		t.Fatalf("ReadChatGPTTokenFromReader(pipe) error = %v", err)
	}
	if token != "e30.e30.c2ln" {
		t.Fatalf("token = %q", token)
	}
	rest, err := io.ReadAll(remaining)
	if err != nil {
		t.Fatalf("ReadAll(remaining) error = %v", err)
	}
	if string(rest) != "lifetime\n" {
		t.Fatalf("remaining stdin = %q, want the parent lifetime bytes", rest)
	}
}

func TestReadChatGPTTokenRequiresNewlineAndBound(t *testing.T) {
	if _, _, err := ReadChatGPTTokenFromReader(strings.NewReader("e30.e30.c2ln")); !errors.Is(err, ErrChatGPTTokenNotTerminated) {
		t.Fatalf("missing newline error = %v, want ErrChatGPTTokenNotTerminated", err)
	}
	if _, _, err := ReadChatGPTTokenFromReader(strings.NewReader("\n")); !errors.Is(err, ErrInvalidChatGPTToken) {
		t.Fatalf("empty token error = %v, want ErrInvalidChatGPTToken", err)
	}
	large := bytes.Repeat([]byte("a"), ChatGPTTokenMaxBytes+1)
	if _, _, err := ReadChatGPTTokenFromReader(bytes.NewReader(append(large, '\n'))); !errors.Is(err, ErrChatGPTTokenNotTerminated) {
		t.Fatalf("oversized token error = %v, want ErrChatGPTTokenNotTerminated", err)
	}
}
