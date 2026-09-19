package cli

import "testing"

// TestNoDaemonParsesLikeRust mirrors Rust cli/src/main.rs #46088: --no-daemon is
// accepted on the interactive root and preserved through resume/fork (before or
// after the subcommand), the hidden agents spelling, and queue.
func TestNoDaemonParsesLikeRust(t *testing.T) {
	parsed, err := Parse([]string{"--no-daemon"})
	if err != nil {
		t.Fatalf("Parse(--no-daemon) error = %v", err)
	}
	if !parsed.Root.Shared.NoDaemon {
		t.Fatalf("interactive --no-daemon = %#v", parsed.Root.Shared)
	}

	for _, command := range []string{"resume", "fork"} {
		for _, args := range [][]string{
			{"--no-daemon", command, "--last"},
			{command, "--last", "--no-daemon"},
		} {
			parsed, err := Parse(args)
			if err != nil {
				t.Fatalf("Parse(%v) error = %v", args, err)
			}
			if !parsed.Root.Shared.NoDaemon && !parsed.Session.Shared.NoDaemon {
				t.Fatalf("Parse(%v) dropped --no-daemon: root=%#v session=%#v", args, parsed.Root.Shared, parsed.Session.Shared)
			}
		}
	}

	parsed, err = Parse([]string{"agents", "--no-daemon"})
	if err != nil {
		t.Fatalf("Parse(agents --no-daemon) error = %v", err)
	}
	if !parsed.Agents.NoDaemon {
		t.Fatalf("agents --no-daemon = %#v", parsed.Agents)
	}

	parsed, err = Parse([]string{"queue", "--thread", "thread-1", "--message", "hi", "--no-daemon"})
	if err != nil {
		t.Fatalf("Parse(queue --no-daemon) error = %v", err)
	}
	if !parsed.Queue.Shared.NoDaemon {
		t.Fatalf("queue --no-daemon = %#v", parsed.Queue.Shared)
	}
}
