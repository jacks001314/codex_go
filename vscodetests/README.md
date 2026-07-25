# VS Code Protocol Smoke Tests

This harness simulates the OpenAI VS Code extension's app-server JSONL client.
It runs the same handshake against the extension-bundled Rust CLI and a Go CLI,
then compares request responses and notification lifecycle without opening a UI
or calling a model.

```sh
npm --prefix vscodetests run test:smoke -- \
  --rust /path/to/openai.chatgpt-*/bin/linux-x86_64/codex \
  --go /path/to/codex-go
```

After the offline suite passes, run the opt-in live turn smoke with the same
binaries. It copies only `auth.json` into separate temporary homes and runs
Rust before Go. If the Rust baseline cannot complete, Go is skipped.

```sh
npm --prefix vscodetests run test:live-turn -- \
  --rust /path/to/openai.chatgpt-*/bin/linux-x86_64/codex \
  --go /path/to/codex-go \
  --auth /path/to/auth.json
```

The Linux command protocol suite is offline and covers buffered success,
buffered nonzero exit, and streamed stdout/stderr output:

```sh
npm --prefix vscodetests run test:command-exec -- \
  --rust /path/to/openai.chatgpt-*/bin/linux-x86_64/codex \
  --go /path/to/codex-go
```

Artifacts contain only protocol metadata. Temporary `CODEX_HOME` directories
and auth/config files are never copied by this suite.
