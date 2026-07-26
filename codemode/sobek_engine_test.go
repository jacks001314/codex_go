package codemode

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSobekEngineJavaScriptAndTextOrdering(t *testing.T) {
	engine := NewSobekEngine()
	result, err := engine.Execute(context.Background(), EngineRequest{Source: `
		const values = [1, 2, 3].map(x => x * 2);
		text(values.reduce((a, b) => a + b, 0));
		text({ok: true, values});
		text("last");
	`})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{result.ContentItems[0].Text, result.ContentItems[1].Text, result.ContentItems[2].Text}
	want := []string{"12", `{"ok":true,"values":[2,4,6]}`, "last"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSobekEngineExit(t *testing.T) {
	result, err := NewSobekEngine().Execute(context.Background(), EngineRequest{Source: `text("before"); exit(); text("after")`})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ContentItems) != 1 || result.ContentItems[0].Text != "before" {
		t.Fatalf("result = %#v", result)
	}
}

func TestSobekEngineErrors(t *testing.T) {
	for _, source := range []string{`const =`, `throw new Error("boom")`} {
		_, err := NewSobekEngine().Execute(context.Background(), EngineRequest{Source: source})
		if err == nil || !strings.Contains(err.Error(), "javascript execution failed") {
			t.Fatalf("source %q error = %v", source, err)
		}
	}
}

func TestSobekEngineContextInterruptsInfiniteLoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err := NewSobekEngine().Execute(ctx, EngineRequest{Source: `for (;;) {}`})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
}

func TestSobekEngineDoesNotExposeHostGlobals(t *testing.T) {
	result, err := NewSobekEngine().Execute(context.Background(), EngineRequest{Source: `text([typeof process, typeof require, typeof fetch, typeof WebSocket].join(","))`})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.ContentItems[0].Text; got != "undefined,undefined,undefined,undefined" {
		t.Fatalf("globals = %q", got)
	}
}
