# fakeappserver

Deterministic stand-in for the Codex app server, used by the Linux
real-terminal tests. It speaks JSON-RPC over a websocket on a unix socket, so a
real `codex` TUI process can be driven through the resume, locked-view and fork
paths without network access, credentials or a real backend.

Library: `codex_go/internal/fakeappserver` (`fakeappserver.New`).
CLI: this directory (`go run ./scripts/fakeappserver`).

## Usage

```sh
go run ./scripts/fakeappserver -socket /tmp/fs.sock -log /tmp/fs.log -forkdelay 2s
# in another terminal
./codex --remote unix:///tmp/fs.sock
```

The CLI prints the socket path and the active switches on stdout, then serves
until SIGINT/SIGTERM. Every request and response is appended to the log with a
`HH:MM:SS.mmm` stamp, which is what the terminal tests use to prove *when* a
`thread/fork` request was sent.

## Flags

| flag | default | meaning |
| --- | --- | --- |
| `-socket` | `/tmp/fakeappserver.sock` | unix socket path |
| `-log` | `/tmp/fakeappserver.log` | request/response log |
| `-forkdelay` | `0` | sleep before answering `thread/fork` (e.g. `2s`), so the TUI's pending state is observable |
| `-forkfail` | `false` | answer `thread/fork` with JSON-RPC error `fork failed: simulated backend error` |
| `-forkclose` | `false` | drop the connection instead of answering `thread/fork` (simulates the app server disappearing) |
| `-readonly` | `true` | answer `thread/resume` with `thread … already has an active writer`, which puts the TUI in the locked (external writer) view |
| `-threadid` | `01910000-0000-7000-8000-000000000001` | the thread served by `thread/list` |
| `-forkid` | `01910000-0000-7000-8000-000000000002` | the thread returned by `thread/fork` |

## Scenarios

**Locked-view fork (Rust #47185).** Start with the defaults, run
`codex --remote unix:///tmp/fs.sock`, then `/resume` → Enter → Enter to attach to
the active-writer thread. Press `f`. With `-forkdelay 2s` the notice card is
replaced by `Forking conversation…` immediately and `Fork created. You can
continue here.` arrives about two seconds later. `-forkfail` keeps the locked
card and prints the backend error.

**`/fork` and the checkout picker.** Same server without the locked view: type
`/fork` and press Enter. The footer shows `Forking conversation…` and the
session switches to the forked thread when the answer arrives.

**Disconnect while forking.** Start with `-forkdelay 2s -forkclose`, press `f`,
and the in-flight request fails instead of hanging.

## What it answers

`initialize`, `account/read`, `account/rateLimits/read`, `account/usage/read`,
`thread/list`, `thread/read`, `thread/resume`, `thread/fork`,
`thread/turns/list`, `skills/list`, `apps/list`, `experimentalFeature/list`,
`plugin/list`, `mcpServer/list`. Anything else gets an empty result, which
keeps startup quiet.

`thread/fork` counts its requests (`Server.ForkCount`) so a test can prove that
a double trigger produced exactly one request.

## Limits

It is not a product server: no persistence, no turn streaming, no auth. It is
only as faithful as the TUI surface under test needs it to be — extend
`resultFor` when a new path needs another method.
