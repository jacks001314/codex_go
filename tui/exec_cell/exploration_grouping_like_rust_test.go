package execcell

import (
	"strings"
	"testing"
)

func explorationRead(id string, name string, exitCode int) ExecCall {
	return ExecCall{
		CallID:  id,
		Command: []string{"bash", "-lc", "cat " + name},
		Parsed:  []ParsedCommand{{Kind: ParsedRead, Name: name}},
		Output:  &CommandOutput{ExitCode: exitCode},
		Source:  ExecSourceAgent,
	}
}

// Mirrors Rust #46487: adjacent reads collapse into one summary with unique
// names, and a failed read breaks the group so it stays separate.
func TestExecCellExploringGroupsReadsAndBreaksOnFailureLikeRust(t *testing.T) {
	cell := ExecCell{Calls: []ExecCall{
		explorationRead("a", "a.txt", 0),
		explorationRead("b", "b.txt", 0),
	}}
	display := strings.Join(cell.DisplayLines(80), "\n")
	if !strings.Contains(display, "Read a.txt, b.txt") {
		t.Fatalf("reads were not grouped: %q", display)
	}
	if strings.Count(display, "Read ") != 1 {
		t.Fatalf("grouped reads rendered more than once: %q", display)
	}

	broken := ExecCell{Calls: []ExecCall{
		explorationRead("a", "a.txt", 0),
		explorationRead("b", "b.txt", 1),
		explorationRead("c", "c.txt", 0),
	}}
	display = strings.Join(broken.DisplayLines(80), "\n")
	if strings.Contains(display, "a.txt, b.txt") || strings.Contains(display, "b.txt, c.txt") {
		t.Fatalf("a failed read stayed inside a read summary: %q", display)
	}
	if !strings.Contains(display, "Read a.txt") || !strings.Contains(display, "Read b.txt (exit 1)") || !strings.Contains(display, "Read c.txt") {
		t.Fatalf("failed read grouping = %q", display)
	}
}

// Mirrors Rust #46487: search exit 1 and compound-command outcomes carry their
// exit code on the call's last line.
func TestExecCellExploringAnnotatesExitCodesLikeRust(t *testing.T) {
	search := ExecCell{Calls: []ExecCall{{
		CallID:  "search-1",
		Command: []string{"bash", "-lc", "rg TODO"},
		Parsed:  []ParsedCommand{{Kind: ParsedSearch, Query: "TODO"}},
		Output:  &CommandOutput{ExitCode: 1},
		Source:  ExecSourceAgent,
	}}}
	display := strings.Join(search.DisplayLines(80), "\n")
	if !strings.Contains(display, "Search TODO (exit 1)") {
		t.Fatalf("search exit annotation = %q", display)
	}

	compound := ExecCell{Calls: []ExecCall{{
		CallID:  "compound-1",
		Command: []string{"bash", "-lc", "ls dir; rg TODO"},
		Parsed: []ParsedCommand{
			{Kind: ParsedListFiles, Cmd: "ls", Path: "dir"},
			{Kind: ParsedSearch, Query: "TODO"},
		},
		Output: &CommandOutput{ExitCode: 2},
		Source: ExecSourceAgent,
	}}}
	display = strings.Join(compound.DisplayLines(80), "\n")
	if !strings.Contains(display, "List dir") || !strings.Contains(display, "Search TODO (command exit 2)") {
		t.Fatalf("compound exit annotation = %q", display)
	}
	if strings.Contains(display, "List dir (command exit 2)") {
		t.Fatalf("a compound command annotated more than its last action: %q", display)
	}
}
