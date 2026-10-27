package turn

import (
	"errors"
	"strings"
	"testing"
)

func stringPointer(value string) *string { return &value }

func TestAdditionalContextEntryValidateLikeRust(t *testing.T) {
	for _, test := range []struct {
		name  string
		entry AdditionalContextEntry
		want  string
	}{
		{name: "unnamed", entry: AdditionalContextEntry{Value: "value", Kind: AdditionalContextUntrusted}},
		{name: "named untrusted", entry: AdditionalContextEntry{Value: "value", Kind: AdditionalContextUntrusted, Name: stringPointer("browser")}},
		{name: "minimum length", entry: AdditionalContextEntry{Value: "value", Kind: AdditionalContextUntrusted, Name: stringPointer("a")}},
		{name: "digits and underscore", entry: AdditionalContextEntry{Value: "value", Kind: AdditionalContextUntrusted, Name: stringPointer("browser_2")}},
		{name: "maximum length", entry: AdditionalContextEntry{Value: "value", Kind: AdditionalContextUntrusted, Name: stringPointer(strings.Repeat("a", 64))}},
		{name: "application without name", entry: AdditionalContextEntry{Value: "value", Kind: AdditionalContextApplication}},
		{
			name:  "empty name",
			entry: AdditionalContextEntry{Value: "value", Kind: AdditionalContextUntrusted, Name: stringPointer("")},
			want:  "additional context name must match [a-z][a-z0-9_]{0,63}",
		},
		{
			name:  "dotted name",
			entry: AdditionalContextEntry{Value: "value", Kind: AdditionalContextUntrusted, Name: stringPointer("agent.browser")},
			want:  "additional context name must match [a-z][a-z0-9_]{0,63}",
		},
		{
			name:  "leading digit",
			entry: AdditionalContextEntry{Value: "value", Kind: AdditionalContextUntrusted, Name: stringPointer("1browser")},
			want:  "additional context name must match [a-z][a-z0-9_]{0,63}",
		},
		{
			name:  "uppercase",
			entry: AdditionalContextEntry{Value: "value", Kind: AdditionalContextUntrusted, Name: stringPointer("Browser")},
			want:  "additional context name must match [a-z][a-z0-9_]{0,63}",
		},
		{
			name:  "non ascii",
			entry: AdditionalContextEntry{Value: "value", Kind: AdditionalContextUntrusted, Name: stringPointer("é")},
			want:  "additional context name must match [a-z][a-z0-9_]{0,63}",
		},
		{
			name:  "too long",
			entry: AdditionalContextEntry{Value: "value", Kind: AdditionalContextUntrusted, Name: stringPointer(strings.Repeat("a", 65))},
			want:  "additional context name must match [a-z][a-z0-9_]{0,63}",
		},
		{
			name:  "application name",
			entry: AdditionalContextEntry{Value: "value", Kind: AdditionalContextApplication, Name: stringPointer("browser")},
			want:  "additional context name is only supported for untrusted context",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.entry.Validate()
			if test.want == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			var invalid *AdditionalContextValidationError
			if !errors.As(err, &invalid) || invalid.Message != test.want {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
			if !errors.Is(err, ErrInvalidTurnRequest) {
				t.Fatalf("Validate() error = %v, want ErrInvalidTurnRequest", err)
			}
		})
	}
}

func TestValidateAdditionalContextVisitsKeysInSortedOrderLikeRust(t *testing.T) {
	err := ValidateAdditionalContext(map[string]AdditionalContextEntry{
		"z_source": {Value: "value", Kind: AdditionalContextApplication, Name: stringPointer("browser")},
		"a_source": {Value: "value", Kind: AdditionalContextUntrusted, Name: stringPointer("agent.browser")},
	})
	var invalid *AdditionalContextValidationError
	if !errors.As(err, &invalid) || invalid.Message != "additional context name must match [a-z][a-z0-9_]{0,63}" {
		t.Fatalf("ValidateAdditionalContext() error = %v, want the a_source format error first", err)
	}
}

func TestTurnStartAndSteerRejectInvalidAdditionalContextNamesBeforeAdmission(t *testing.T) {
	service := NewTurnService()
	invalid := map[string]AdditionalContextEntry{
		"source": {Value: "rejected context", Kind: AdditionalContextUntrusted, Name: stringPointer("agent.browser")},
	}
	if _, err := service.Start(&TurnStartParams{ThreadID: "thread-1", Prompt: "rejected input", AdditionalContext: invalid}); !errors.Is(err, ErrInvalidTurnRequest) {
		t.Fatalf("Start() error = %v, want ErrInvalidTurnRequest", err)
	}
	if _, ok := service.active["thread-1"]; ok {
		t.Fatal("turn/start admitted a turn with an invalid context name")
	}

	started, err := service.Start(&TurnStartParams{ThreadID: "thread-1", Prompt: "valid input"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := service.Steer(&TurnSteerParams{ThreadID: "thread-1", ExpectedTurnID: started.Turn.ID, Prompt: "steer", AdditionalContext: invalid}); !errors.Is(err, ErrInvalidTurnRequest) {
		t.Fatalf("Steer() error = %v, want ErrInvalidTurnRequest", err)
	}
	if got := service.active["thread-1"].ID; got != started.Turn.ID {
		t.Fatalf("active turn = %q, want %q", got, started.Turn.ID)
	}

	validName := "browser_2"
	if _, err := service.Start(&TurnStartParams{
		ThreadID: "thread-2",
		Prompt:   "valid named context",
		AdditionalContext: map[string]AdditionalContextEntry{
			"source": {Value: "value", Kind: AdditionalContextUntrusted, Name: &validName},
		},
	}); err != nil {
		t.Fatalf("Start() with a valid name error = %v", err)
	}
}
