# macOS filesystem helper spawn audit

Rust upstream: `595cc91e8c Avoid fork when spawning macOS filesystem helpers (#46661)`.

## Verdict

Not applicable. The upstream change replaces Rust PTY child fork/exec with a
macOS spawn helper path. Go's exec-server filesystem helper starts through
`os/exec` directly and has no fork-in-parent PTY `Child` wrapper, so there is no
equivalent fork path to remove.
