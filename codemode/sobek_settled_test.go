package codemode

import (
	"context"
	"strings"
	"testing"
)

// Mirrors upstream codex-rs/code-mode-runtime/src/runtime/settled.js (#51126):
// as_settled / stream_settled are installed by installSobekHelpers and must be
// reachable from a script executed by the real Sobek engine.
func TestSobekEngineSettledHelpers(t *testing.T) {
	result, err := NewSobekEngine().Execute(context.Background(), EngineRequest{Source: `
		const seen = [];
		await stream_settled(
			[Promise.resolve("a"), Promise.reject(new Error("nope")), 42],
			async (record) => {
				seen.push("start" + record.index);
				await Promise.resolve();
				seen.push("end" + record.index);
				text(JSON.stringify([record.index, record.status, record.value ?? record.reason.message]));
			},
		);
		text("order:" + seen.join(","));

		const stream = as_settled(new Map([["x", Promise.resolve(1)], ["y", Promise.reject(new Error("bad"))]]));
		const keyed = [];
		for (;;) {
			const step = await stream.next();
			if (step.done) break;
			const record = step.value;
			keyed.push(record.index + ":" + record.status + ":" + (record.status === "fulfilled" ? record.value : record.reason.message));
		}
		text("keyed:" + keyed.join(","));

		const early = as_settled([Promise.resolve("one"), Promise.resolve("two")]);
		const first = await early.next();
		early.return();
		const after = await early.next();
		text("early:" + first.value.value + ":" + after.done);

		let caught = "none";
		try {
			await stream_settled([Promise.resolve(1)], () => { throw new Error("boom"); });
		} catch (error) {
			caught = error.message;
		}
		text("caught:" + caught);
	`})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(result.ContentItems))
	for _, item := range result.ContentItems {
		got = append(got, item.Text)
	}
	want := []string{
		`[0,"fulfilled","a"]`,
		`[1,"rejected","nope"]`,
		`[2,"fulfilled",42]`,
		"order:start0,end0,start1,end1,start2,end2",
		"keyed:x:fulfilled:1,y:rejected:bad",
		"early:one:true",
		"caught:boom",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("settled helpers output:\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSobekEngineSettledHelpersRequireCallback(t *testing.T) {
	_, err := NewSobekEngine().Execute(context.Background(), EngineRequest{Source: `await stream_settled([Promise.resolve(1)])`})
	if err == nil || !strings.Contains(err.Error(), "stream_settled requires a callback") {
		t.Fatalf("error = %v", err)
	}
}
