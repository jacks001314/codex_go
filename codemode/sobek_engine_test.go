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

func TestSobekEngineExitStopsWholeCell(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
	}{
		{name: "infinite loop after exit", source: `exit(); for (;;) {}`},
		{name: "catch and finally", source: `try { exit(); } catch { text("caught"); } finally { text("finally"); }`},
		{name: "queued microtask", source: `Promise.resolve().then(() => text("microtask")); exit();`},
		{name: "unawaited promise", source: `Promise.resolve().then(() => exit()).catch(() => text("caught")); await new Promise(() => {});`},
		{name: "async module", source: `await Promise.resolve(); try { exit(); } finally { text("finally"); }`},
		{name: "parallel promises", source: `await Promise.all([Promise.resolve().then(() => exit()), Promise.resolve().then(() => text("sibling"))]);`},
		{name: "text getter", source: `try { text({ get value() { exit(); } }); } catch { text("caught"); } finally { text("finally"); }`},
		{name: "audio getter", source: `try { audio({ get audio_url() { exit(); } }); } catch { text("caught"); } finally { text("finally"); }`},
		{name: "exception formatting", source: `throw { toString() { exit(); for (;;) {} } };`},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := NewSobekEngine().Execute(ctx, EngineRequest{Source: `text("before"); ` + test.source + ` text("after");`})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if len(result.ContentItems) != 1 || result.ContentItems[0].Text != "before" {
				t.Fatalf("items = %#v, want only pre-exit output", result.ContentItems)
			}
		})
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

func TestSobekEngineContextInterruptsOutputSerialization(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
	}{
		{name: "text getter", source: `text({ get value() { for (;;) {} } });`},
		{name: "audio getter", source: `audio({ get audio_url() { for (;;) {} } });`},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			_, err := NewSobekEngine().Execute(ctx, EngineRequest{Source: `
				text("before");
				try {
					` + test.source + `
				} catch {
					text("caught");
				} finally {
					text("finally");
				}
				text("after");
			`})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v", err)
			}
		})
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
