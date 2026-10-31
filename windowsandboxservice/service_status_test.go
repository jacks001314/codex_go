package windowsandboxservice

import (
	"strings"
	"testing"
)

func TestParseRunModeMatchesTheRustServiceArguments(t *testing.T) {
	cases := []struct {
		name      string
		arguments []string
		wantErr   string
	}{
		{"no argument", nil, ""},
		{"explicit service", []string{"--service"}, ""},
		{"foreground is debug-only", []string{"--foreground"}, "unrecognized service argument \"--foreground\""},
		{"unknown", []string{"--broker"}, "unrecognized service argument \"--broker\""},
		{"two arguments", []string{"--service", "--service"}, "expected at most one service argument"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mode, err := ParseRunMode(testCase.arguments)
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("ParseRunMode(%v) error = %v", testCase.arguments, err)
				}
				if mode != RunModeService {
					t.Fatalf("ParseRunMode(%v) = %v, want service", testCase.arguments, mode)
				}
				return
			}
			if err == nil || err.Error() != testCase.wantErr {
				t.Fatalf("ParseRunMode(%v) error = %v, want %q", testCase.arguments, err, testCase.wantErr)
			}
		})
	}
}

func TestReportedServiceStatusAcceptsControlsOnlyWhileRunning(t *testing.T) {
	running := NewReportedServiceStatus(ServiceRunning, NoError)
	if running.ControlsAccepted != ServiceAcceptStop|ServiceAcceptShutdown|ServiceAcceptSessionChange {
		t.Fatalf("running controls = %d", running.ControlsAccepted)
	}
	if running.CheckPoint != 0 || running.WaitHint != 0 {
		t.Fatalf("running status must not look pending: %+v", running)
	}
	if running.ServiceSpecificExitCode != 0 {
		t.Fatalf("running specific exit code = %d, want 0", running.ServiceSpecificExitCode)
	}
	for _, state := range []uint32{ServiceStartPending, ServiceStopPending} {
		pending := NewReportedServiceStatus(state, NoError)
		if pending.ControlsAccepted != 0 {
			t.Fatalf("state %d accepts controls: %d", state, pending.ControlsAccepted)
		}
		if pending.CheckPoint != 1 || pending.WaitHint != 10_000 {
			t.Fatalf("state %d pending status = %+v", state, pending)
		}
	}
}

func TestReportedServiceStatusFlagsASpecificExitCode(t *testing.T) {
	stopped := NewReportedServiceStatus(ServiceStopped, ErrorServiceSpecificError)
	if stopped.Win32ExitCode != ErrorServiceSpecificError || stopped.ServiceSpecificExitCode != 1 {
		t.Fatalf("fatal status = %+v", stopped)
	}
	clean := NewReportedServiceStatus(ServiceStopped, NoError)
	if clean.Win32ExitCode != 0 || clean.ServiceSpecificExitCode != 0 {
		t.Fatalf("clean status = %+v", clean)
	}
}

func TestEventMessageReplacesControlCharacters(t *testing.T) {
	got := EventMessage("fatal\x00error\r\nnext")
	if got != "fatal error  next" {
		t.Fatalf("EventMessage() = %q", got)
	}
}

func TestEventMessageIsBoundedInUTF16Units(t *testing.T) {
	ascii := EventMessage(strings.Repeat("a", MaxEventMessageUnits*2))
	if len(ascii) != MaxEventMessageUnits {
		t.Fatalf("EventMessage() length = %d, want %d", len(ascii), MaxEventMessageUnits)
	}
	emoji := EventMessage(strings.Repeat("\U0001F600", MaxEventMessageUnits))
	if want := MaxEventMessageUnits / 2; len([]rune(emoji)) != want {
		t.Fatalf("EventMessage() kept %d surrogate pairs, want %d", len([]rune(emoji)), want)
	}
}
