# Inline visualization viewer CSP: Chrome A/B harness

`inline_visualization_csp_ab.mjs` is the real-browser regression check for Rust
#58436 (`codex-rs/tui/src/inline_visualization/viewer.rs`). It exists because the
bug is invisible to every cheaper kind of test: the materialized document is
well formed, the injected runtime is present in the file, and a DOM shim happily
executes it — but a `srcdoc` frame inherits the shell document's
Content-Security-Policy, so a shell policy without inline scripts makes Chrome
drop the runtime silently.

## What it does

1. Loads two viewer documents in headless Chrome (`--headless=new`) over the
   DevTools protocol.
2. Attaches to the sandboxed frame. A sandboxed `srcdoc` frame has an opaque
   origin, so Chrome runs it out of process (`Target.setAutoAttach`, flattened
   sessions); the probe evaluates `document.querySelector` inside that frame.
3. Reports one verdict per document:
   - `live_controls` — the runtime produced `.viz-carousel-controls`
   - `partial` — the runtime started (panels got `role="group"`) but no controls
   - `blocked` — the runtime never ran (the pre-fix shell CSP)

The expected A/B verdict is `{"before":"blocked","after":"live_controls"}`.

## Running it

```bash
node scripts/inline_visualization_csp_ab.mjs \
  --chrome "/path/to/chrome" \
  --before before.html --after after.html
```

- `--chrome` (or `CODEX_CHROME_PATH`) may be omitted only if you pass the flag;
  the script exits `2` when Chrome is missing so callers can skip instead of fail.
- Any Chrome/Chromium >= 111 works (`/json/new` requires `PUT`). The original
  investigation used Chrome 149 on Linux; a newer local build is fine.
- Requires Node 22+ for the built-in `fetch` and `WebSocket` (no npm install).

## Go test

`tui/inline_visualization_chrome_like_rust_test.go` generates both documents
from the real renderer (`inlineVisualizationViewerDocument`), substitutes the
pre-fix shell policy for the `before` variant, and runs this harness:

```bash
go test ./tui -run TestInlineVisualizationViewerCSPABInRealChromeLikeRust -v
```

It skips (not fails) when Chrome or Node is unavailable, so `go test ./...` stays
green on machines without a browser. The fast CSP assertions in
`tui/inline_visualization_csp_test.go` still run everywhere.
