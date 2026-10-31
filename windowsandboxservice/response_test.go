package windowsandboxservice

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestErrorMessageJoinsWrappedCausesInOrder(t *testing.T) {
	err := fmt.Errorf("requested sandbox settings violate administrator-controlled machine policy: %w",
		fmt.Errorf("enforce managed provisioning requirements: %w", errors.New("managed policy does not permit HTTP proxy port 8080")))
	want := "requested sandbox settings violate administrator-controlled machine policy: enforce managed provisioning requirements: managed policy does not permit HTTP proxy port 8080"
	if got := ErrorMessage(err); got != want {
		t.Fatalf("ErrorMessage() = %q, want %q", got, want)
	}
}

func TestErrorMessageReplacesControlCharacters(t *testing.T) {
	got := ErrorMessage(errors.New("bad\x00value\r\nnext"))
	if strings.ContainsAny(got, "\x00\r\n") {
		t.Fatalf("ErrorMessage() kept a control character: %q", got)
	}
	if got != "bad value  next" {
		t.Fatalf("ErrorMessage() = %q, want %q", got, "bad value  next")
	}
}

func TestErrorMessageIsBoundedAndKeepsUTF8Boundaries(t *testing.T) {
	got := ErrorMessage(errors.New(strings.Repeat("é", MaxResponseMessageBytes)))
	if len(got) > MaxResponseMessageBytes {
		t.Fatalf("ErrorMessage() length = %d, want <= %d", len(got), MaxResponseMessageBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("ErrorMessage() split a UTF-8 sequence: %q", got)
	}
	if len(got) == 0 {
		t.Fatal("ErrorMessage() dropped the whole message")
	}
}

func TestErrorMessageOfNilIsEmpty(t *testing.T) {
	if got := ErrorMessage(nil); got != "" {
		t.Fatalf("ErrorMessage(nil) = %q, want empty", got)
	}
}
