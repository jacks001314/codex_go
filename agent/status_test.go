package agent

import "testing"

func TestStatusFromEvent(t *testing.T) {
	cases := []struct {
		name  string
		event Event
		want  Status
	}{
		{name: "started", event: Event{Type: "turn_started"}, want: Status{Kind: StatusRunning}},
		{name: "complete", event: Event{Type: "turn_complete", LastAgentMessage: "done"}, want: Status{Kind: StatusCompleted, Message: "done"}},
		{name: "interrupted", event: Event{Type: "turn_aborted", Reason: "interrupted"}, want: Status{Kind: StatusInterrupted}},
		{name: "budget", event: Event{Type: "turn_aborted", Reason: "budget_limited"}, want: Status{Kind: StatusInterrupted}},
		{name: "abort error", event: Event{Type: "turn_aborted", Reason: "failed"}, want: Status{Kind: StatusErrored, Message: "failed"}},
		{name: "error", event: Event{Type: "error", Message: "boom"}, want: Status{Kind: StatusErrored, Message: "boom"}},
		{name: "shutdown", event: Event{Type: "shutdown_complete"}, want: Status{Kind: StatusShutdown}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := StatusFromEvent(&tc.event)
			if !ok {
				t.Fatalf("StatusFromEvent() ok = false, want true")
			}
			if *got != tc.want {
				t.Fatalf("StatusFromEvent() = %#v, want %#v", *got, tc.want)
			}
		})
	}
}

func TestStatusFromEventIgnoresUnknownEvents(t *testing.T) {
	if got, ok := StatusFromEvent(&Event{Type: "noop"}); ok || got != nil {
		t.Fatalf("StatusFromEvent(unknown) = %#v/%v, want nil/false", got, ok)
	}
}

func TestStatusIsFinal(t *testing.T) {
	final := []Status{{Kind: StatusCompleted}, {Kind: StatusErrored}, {Kind: StatusShutdown}}
	for _, status := range final {
		if !status.IsFinal() {
			t.Fatalf("%s IsFinal() = false, want true", status.Kind)
		}
	}
	notFinal := []Status{{Kind: StatusPendingInit}, {Kind: StatusRunning}, {Kind: StatusInterrupted}}
	for _, status := range notFinal {
		if status.IsFinal() {
			t.Fatalf("%s IsFinal() = true, want false", status.Kind)
		}
	}
}
