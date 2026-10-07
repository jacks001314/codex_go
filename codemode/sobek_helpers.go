package codemode

import (
	"encoding/json"
	"fmt"

	"github.com/grafana/sobek"
)

func installSobekHelpers(runtime *sobek.Runtime, items *[]ContentItem) error {
	if err := runtime.Set("text", func(call sobek.FunctionCall) sobek.Value {
		value := sobek.Undefined()
		if len(call.Arguments) > 0 {
			value = call.Argument(0)
		}
		*items = append(*items, InputText(renderSobekText(value)))
		return sobek.Undefined()
	}); err != nil {
		return fmt.Errorf("install text helper: %w", err)
	}
	if err := runtime.Set("exit", func() {
		runtime.Interrupt(errEngineExit)
	}); err != nil {
		return fmt.Errorf("install exit helper: %w", err)
	}
	if _, err := runtime.RunString(sobekSettledHelpers); err != nil {
		return fmt.Errorf("install settled helpers: %w", err)
	}
	return nil
}

// sobekSettledHelpers ports the runtime-owned settlement helpers introduced
// upstream in codex-rs/code-mode-runtime/src/runtime/settled.js (#51126).
//
// Upstream relies on an async generator consumed with `for await`; Sobek rejects
// both constructs at parse time ("Async generators are not supported yet") and
// does not expose Symbol.asyncIterator, so `as_settled` returns a hand-rolled
// single-consumer settlement stream whose `next()`/`return()` promise protocol
// reproduces the upstream generator exactly: each input is observed once, every
// settlement is queued in settlement order while the consumer is busy, records
// carry {index, status, value|reason} (Map inputs keep their keys), and an early
// `return()` drops the queued records.
const sobekSettledHelpers = `
(() => {
  function as_settled(promises) {
    let ready = [];
    let pending = 0;
    let waiters = [];
    let closed = false;

    function settle(result) {
      pending--;
      if (closed) return;
      ready.push(result);
      if (waiters.length > 0) {
        const resolvers = waiters;
        waiters = [];
        for (const resolve of resolvers) resolve();
      }
    }

    const keyed = promises instanceof Map;
    let position = 0;
    try {
      for (const entry of promises) {
        const [index, promise] = keyed ? entry : [position++, entry];
        pending++;
        Promise.resolve(promise).then(
          (value) => settle({ index, status: "fulfilled", value }),
          (reason) => settle({ index, status: "rejected", reason }),
        );
      }
    } catch (error) {
      closed = true;
      throw error;
    }

    function next() {
      if (ready.length > 0) {
        return Promise.resolve({ value: ready.shift(), done: false });
      }
      if (closed || pending === 0) {
        closed = true;
        return Promise.resolve({ value: undefined, done: true });
      }
      return new Promise((resolve) => {
        waiters.push(() => resolve(next()));
      });
    }

    return {
      next,
      return() {
        closed = true;
        ready = [];
        waiters = [];
        return Promise.resolve({ value: undefined, done: true });
      },
    };
  }

  async function stream_settled(promises, emit) {
    if (typeof emit !== "function") {
      throw new TypeError("stream_settled requires a callback");
    }
    const iterator = as_settled(promises);
    try {
      for (;;) {
        const step = await iterator.next();
        if (step.done) break;
        await emit(step.value);
      }
    } finally {
      iterator.return();
    }
  }

  Object.assign(globalThis, { as_settled, stream_settled });
})();
`

func renderSobekText(value sobek.Value) string {
	if sobek.IsUndefined(value) {
		return "undefined"
	}
	if sobek.IsNull(value) {
		return "null"
	}
	if _, ok := value.Export().(string); ok {
		return value.String()
	}
	encoded, err := json.Marshal(value.Export())
	if err == nil {
		return string(encoded)
	}
	return value.String()
}
