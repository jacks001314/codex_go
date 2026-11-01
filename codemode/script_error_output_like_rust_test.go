package codemode

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Mirrors Rust code-mode-runtime output_collection::push_error and
// code-mode-host/src/grpc/service_tests.rs
// `server_output_budgets_follow_exec_wait_and_terminate_over_grpc`: a
// server-truncated observation retains a completed failure as one logical text
// item that includes the `Script error:` label, and the label is charged to the
// observation's own output budget.

func TestServerTruncationLabelsScriptErrorsLikeRust(t *testing.T) {
	diagnostic := strings.Repeat("0123456789", 8)
	collector := newOutputCollector(OutputTruncationServer)
	if got := collector.collectError(diagnostic); got != diagnostic {
		t.Fatalf("collectError() = %q, want the unchanged diagnostic", got)
	}
	items := renderOutput(collector.intoCollectedOutput(), intPtrLocal(7))
	if len(items) != 1 || items[0].Type != "input_text" {
		t.Fatalf("rendered items = %#v, want one text item", items)
	}
	want := "Warning: truncated output (original token count: 24)\nTotal output lines: 2\n\nScript error:\n…17 tokens truncated…67890123456789"
	if items[0].Text != want {
		t.Fatalf("rendered error observation =\n%q\nwant\n%q", items[0].Text, want)
	}
}

func TestClientTruncationKeepsRawScriptErrorLikeRust(t *testing.T) {
	diagnostic := strings.Repeat("0123456789", 8)
	collector := newOutputCollector(OutputTruncationClient)
	if got := collector.collectError(diagnostic); got != diagnostic {
		t.Fatalf("client collectError() = %q", got)
	}
	if items := collector.intoCollectedOutput().Items; len(items) != 0 {
		t.Fatalf("client collector retained %#v, want the caller-owned error", items)
	}
}

func TestServerTruncationBoundsScriptErrorDiagnosticLikeRust(t *testing.T) {
	diagnostic := strings.Repeat("d", maxServerDiagnosticBytes+1024)
	collector := newOutputCollector(OutputTruncationServer)
	bounded := collector.collectError(diagnostic)
	if len(bounded) != maxServerDiagnosticBytes {
		t.Fatalf("bounded diagnostic length = %d, want %d", len(bounded), maxServerDiagnosticBytes)
	}
	if !strings.HasPrefix(bounded, "dd") || !strings.HasSuffix(bounded, "dd") || !strings.Contains(bounded, "…") {
		t.Fatalf("bounded diagnostic kept neither end: %q", bounded[:8]+"..."+bounded[len(bounded)-8:])
	}
	// The retained item still carries the label and the observable ends of the
	// error, and its gaps account for every dropped byte.
	collected := collector.intoCollectedOutput()
	if len(collected.Items) != 1 || !strings.HasPrefix(collected.Items[0].Text, scriptErrorLabel) {
		t.Fatalf("retained items = %#v, want one labeled error", collected.Items)
	}
	if collected.Items[0].Text != scriptErrorLabel+diagnostic {
		t.Fatalf("retained error is not the full labeled diagnostic")
	}
	if len(collected.Gaps) != 0 {
		t.Fatalf("an error within the retention budget must not record gaps")
	}
}

// Mirrors Rust's `terminal_error_matches_ordinary_text_across_loss_boundaries`:
// pushError must leave exactly the state an ordinary write of the labeled text
// leaves, including where it reports loss around retained items.
func TestPushErrorMatchesOrdinaryWriteLikeRust(t *testing.T) {
	label := scriptErrorLabel
	errorText := strings.Repeat("p", BoundedOutputMaxBytes-len(label)-3) +
		"XYZ" + strings.Repeat("m", 22) + "💡" + strings.Repeat("s", BoundedOutputMaxBytes-3)
	half := InputText(strings.Repeat("p", BoundedOutputMaxBytes/2))
	prefixes := [][]ContentItem{
		nil,
		repeatedTextItems(OutputMaxItems/2, ""),
		{half, InputText("lost"), InputImage("i", nil), half},
	}
	for index, prefix := range prefixes {
		terminal := newBoundedOutput()
		ordinary := newBoundedOutput()
		for _, item := range prefix {
			terminal.push(item)
			ordinary.push(item)
		}
		terminal.pushError(errorText)
		ordinary.push(InputText(label + errorText))
		actual := terminal.intoCollectedOutput()
		expected := ordinary.intoCollectedOutput()
		if len(actual.Items) != len(expected.Items) || len(actual.Gaps) != len(expected.Gaps) {
			t.Fatalf("prefix %d: pushError kept %d items/%d gaps, ordinary write %d/%d",
				index, len(actual.Items), len(actual.Gaps), len(expected.Items), len(expected.Gaps))
		}
		for i := range actual.Items {
			if actual.Items[i] != expected.Items[i] {
				t.Fatalf("prefix %d: item %d = %#v, want %#v", index, i, actual.Items[i], expected.Items[i])
			}
		}
		for i := range actual.Gaps {
			if actual.Gaps[i] != expected.Gaps[i] {
				t.Fatalf("prefix %d: gap %d = %#v, want %#v", index, i, actual.Gaps[i], expected.Gaps[i])
			}
		}
		if retained := terminal.openBytes(); retained >= 2*BoundedOutputMaxBytes {
			t.Fatalf("prefix %d: pushError retained %d bytes", index, retained)
		}
	}
}

func repeatedTextItems(count int, text string) []ContentItem {
	items := make([]ContentItem, count)
	for i := range items {
		items[i] = InputText(text)
	}
	return items
}

func (b *boundedOutput) openBytes() int {
	if b.open != nil {
		return b.open.contentBytes
	}
	return b.split.contentBytes
}

// A script that throws produces the same labeled observation through the
// session runtime the gRPC host serves.
func TestSessionRuntimeServerTruncationLabelsFailuresLikeRust(t *testing.T) {
	runtime := NewSessionRuntime()
	runtime.SetOutputTruncation(OutputTruncationServer)
	yield := uint64(60000)
	budget := 7
	started, err := runtime.Execute(context.Background(), &ExecuteRequest{
		ToolCallID:      "call-labeled-error",
		Source:          "throw '0123456789'.repeat(8);",
		YieldTimeMS:     &yield,
		MaxOutputTokens: &budget,
	}, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	waitForCellStatus(t, runtime.Cells(), started.CellID.String(), CellFailed, 5*time.Second)
	outcome, err := runtime.Wait(context.Background(), &WaitRequest{
		CellID:          started.CellID,
		YieldTimeMS:     1000,
		MaxOutputTokens: &budget,
	}, nil)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	want := "Warning: truncated output (original token count: 24)\nTotal output lines: 2\n\nScript error:\n…17 tokens truncated…67890123456789"
	if got := observationText(outcome.Response.ContentItems); got != want {
		t.Fatalf("failed observation =\n%q\nwant\n%q", got, want)
	}
	if outcome.Response.ErrorText == nil || *outcome.Response.ErrorText != strings.Repeat("0123456789", 8) {
		t.Fatalf("diagnostic = %#v, want the unchanged small error", outcome.Response.ErrorText)
	}
}

// The stdio host shares the runtime's policy, so a host-side failure also
// arrives as one labeled, budgeted item.
func TestStdioHostServerTruncationLabelsHostErrorsLikeRust(t *testing.T) {
	session := &stdioHostSession{outputTruncation: OutputTruncationServer}
	response := session.observeResponse(NewCellID("cell-1"), nil, errors.New("boom"), nil)
	if len(response.ContentItems) != 1 || response.ContentItems[0].Text != "Script error:\nboom" {
		t.Fatalf("host observation = %#v, want the labeled error", response.ContentItems)
	}
	if response.ErrorText == nil || *response.ErrorText != "boom" {
		t.Fatalf("host diagnostic = %#v, want the raw error", response.ErrorText)
	}

	client := &stdioHostSession{outputTruncation: OutputTruncationClient}
	if got := client.observeResponse(NewCellID("cell-2"), nil, errors.New("boom"), nil); len(got.ContentItems) != 0 || got.ErrorText == nil {
		t.Fatalf("client observation = %#v, want the caller-owned error", got)
	}
}

func TestServerTruncationKeepsLabelWithAnEmptyDiagnosticLikeRust(t *testing.T) {
	collector := newOutputCollector(OutputTruncationServer)
	collector.push(InputText("partial"))
	collector.collectError("")
	items := renderOutput(collector.intoCollectedOutput(), intPtrLocal(ProtocolDefaultMaxOutputTokensPerExecCall))
	joined := observationText(items)
	if joined != "partial\nScript error:\n" {
		t.Fatalf("rendered observation = %q", joined)
	}
}
