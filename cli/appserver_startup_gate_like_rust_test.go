package cli

import (
	"strings"
	"testing"
)

// This table is generated from the 0.166.0 `codex.exe app-server` output: each
// entry pins one argument combination and the exact stderr text Rust prints for
// it. The startup gate mirrors clap's conflicts and requires pass plus the
// unexpected-value diagnostic a `--flag=value` spelling triggers on a flag that
// takes no value.
var appServerStartupGateDiagnostics = []struct {
	args []string
	want string
}{
	{args: []string{"--print-startup-config", "--stdio"}, want: "error: the argument '--print-startup-config' cannot be used with '--stdio'\n\nUsage: codex app-server --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--stdio", "--print-startup-config"}, want: "error: the argument '--stdio' cannot be used with '--print-startup-config'\n\nUsage: codex app-server --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--listen", "stdio://"}, want: "error: the argument '--print-startup-config' cannot be used with '--listen <URL>'\n\nUsage: codex app-server --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--listen", "stdio://", "--print-startup-config"}, want: "error: the argument '--listen <URL>' cannot be used with '--print-startup-config'\n\nUsage: codex app-server --listen <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--listen", "off"}, want: "error: the argument '--print-startup-config' cannot be used with '--listen <URL>'\n\nUsage: codex app-server --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--listen", "off", "--print-startup-config"}, want: "error: the argument '--listen <URL>' cannot be used with '--print-startup-config'\n\nUsage: codex app-server --listen <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--remote-control"}, want: "error: the argument '--print-startup-config' cannot be used with '--remote-control'\n\nUsage: codex app-server --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--remote-control", "--print-startup-config"}, want: "error: the argument '--remote-control' cannot be used with '--print-startup-config'\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--ws-auth", "capability-token"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-sign-out --ws-auth <MODE>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--ws-auth", "capability-token"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-client-info <STARTUP_CLIENT_INFO> --ws-auth <MODE>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--ws-token-file", "C:/tmp/tok"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-sign-out --ws-token-file <PATH>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--ws-token-file", "C:/tmp/tok"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-client-info <STARTUP_CLIENT_INFO> --ws-token-file <PATH>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--analytics-default-enabled"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-sign-out --analytics-default-enabled\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--analytics-default-enabled"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-client-info <STARTUP_CLIENT_INFO> --analytics-default-enabled\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--cloud-skills-snapshot", "C:/tmp/snap.json"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-sign-out --cloud-skills-snapshot <PATH>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--cloud-skills-snapshot", "C:/tmp/snap.json"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-client-info <STARTUP_CLIENT_INFO> --cloud-skills-snapshot <PATH>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--code-mode-host", "http://127.0.0.1:1"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-sign-out --code-mode-host <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--code-mode-host", "http://127.0.0.1:1"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-client-info <STARTUP_CLIENT_INFO> --code-mode-host <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "-c", "model=\"o3\""}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-sign-out --config <key=value>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "-c", "model=\"o3\""}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-client-info <STARTUP_CLIENT_INFO> --config <key=value>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--strict-config"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-sign-out --strict-config\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--strict-config"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-client-info <STARTUP_CLIENT_INFO> --strict-config\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--enable", "foo"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-sign-out --enable <FEATURE>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--enable", "foo"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-client-info <STARTUP_CLIENT_INFO> --enable <FEATURE>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--disable", "foo"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-sign-out --disable <FEATURE>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--disable", "foo"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-client-info <STARTUP_CLIENT_INFO> --disable <FEATURE>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-sign-out\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-client-info <STARTUP_CLIENT_INFO>\n\nFor more information, try '--help'."},
	{args: []string{"--stdio", "--listen", "stdio://"}, want: "error: the argument '--stdio' cannot be used with '--listen <URL>'\n\nUsage: codex app-server --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--listen", "stdio://", "--stdio"}, want: "error: the argument '--listen <URL>' cannot be used with '--stdio'\n\nUsage: codex app-server --listen <URL>\n\nFor more information, try '--help'."},
	{args: []string{"--stdio", "--listen", "stdio://", "--print-startup-config"}, want: "error: the argument '--stdio' cannot be used with:\n  --listen <URL>\n  --print-startup-config\n\nUsage: codex app-server --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config=x"}, want: "error: unexpected value 'x' for '--print-startup-config' found; no more were expected\n\nUsage: codex app-server --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config=x", "--stdio"}, want: "error: unexpected value 'x' for '--print-startup-config' found; no more were expected\n\nUsage: codex app-server --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--stdio", "--print-startup-config=x"}, want: "error: unexpected value 'x' for '--print-startup-config' found; no more were expected\n\nUsage: codex app-server --stdio --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config=true"}, want: "error: unexpected value 'true' for '--print-startup-config' found; no more were expected\n\nUsage: codex app-server --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config=--stdio"}, want: "error: unexpected value '--stdio' for '--print-startup-config' found; no more were expected\n\nUsage: codex app-server --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--stdio", "--bogus"}, want: "error: unexpected argument '--bogus' found\n\nUsage: codex app-server --print-startup-config --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "generate-json-schema"}, want: "error: the following required arguments were not provided:\n  --out <DIR>\n\nUsage: codex app-server generate-json-schema --out <DIR>\n\nFor more information, try '--help'."},
	{args: []string{"generate-json-schema", "--out", "C:\\Users\\huoga\\AppData\\Local\\Temp\\cxr42cmp\\gen", "--print-startup-config"}, want: "error: unexpected argument '--print-startup-config' found\n\nUsage: codex app-server generate-json-schema --out <DIR>\n\nFor more information, try '--help'."},
	{args: []string{"--stdio", "--listen", "stdio://", "generate-json-schema", "--out", "C:\\Users\\huoga\\AppData\\Local\\Temp\\cxr42cmp\\gen"}, want: "error: the argument '--stdio' cannot be used with '--listen <URL>'\n\nUsage: codex app-server --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--print-startup-config", "--stdio"}, want: "error: the argument '--print-startup-config' cannot be used with '--stdio'\n\nUsage: codex app-server --startup-sign-out --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--stdio", "--print-startup-config"}, want: "error: the argument '--stdio' cannot be used with '--print-startup-config'\n\nUsage: codex app-server --startup-sign-out --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}"}, want: "error: the argument '--startup-client-info <STARTUP_CLIENT_INFO>' cannot be used multiple times\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--stdio"}, want: "error: the argument '--startup-client-info <STARTUP_CLIENT_INFO>' cannot be used multiple times\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--startup-sign-out", "--stdio"}, want: "error: the argument '--print-startup-config' cannot be used with '--stdio'\n\nUsage: codex app-server --print-startup-config --startup-sign-out\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--stdio"}, want: "error: the argument '--print-startup-config' cannot be used with '--stdio'\n\nUsage: codex app-server --print-startup-config --startup-client-info <STARTUP_CLIENT_INFO>\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--stdio", "--remote-control"}, want: "error: the argument '--print-startup-config' cannot be used with:\n  --stdio\n  --remote-control\n\nUsage: codex app-server --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--remote-control", "--stdio"}, want: "error: the argument '--print-startup-config' cannot be used with:\n  --remote-control\n  --stdio\n\nUsage: codex app-server --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--listen", "stdio://", "--stdio", "--remote-control"}, want: "error: the argument '--print-startup-config' cannot be used with:\n  --listen <URL>\n  --stdio\n  --remote-control\n\nUsage: codex app-server --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--stdio", "--listen", "stdio://"}, want: "error: the argument '--stdio' cannot be used with '--listen <URL>'\n\nUsage: codex app-server --print-startup-config --startup-sign-out --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--stdio", "--listen", "stdio://"}, want: "error: the argument '--stdio' cannot be used with '--listen <URL>'\n\nUsage: codex app-server --print-startup-config --startup-client-info <STARTUP_CLIENT_INFO> --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--strict-config", "--stdio"}, want: "error: the argument '--print-startup-config' cannot be used with '--stdio'\n\nUsage: codex app-server --print-startup-config --strict-config\n\nFor more information, try '--help'."},
	{args: []string{"--strict-config", "--stdio", "--print-startup-config"}, want: "error: the argument '--stdio' cannot be used with '--print-startup-config'\n\nUsage: codex app-server --strict-config --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "-c", "x=1", "--stdio"}, want: "error: the argument '--print-startup-config' cannot be used with '--stdio'\n\nUsage: codex app-server --print-startup-config --config <key=value>\n\nFor more information, try '--help'."},
	{args: []string{"--ws-auth", "capability-token", "--print-startup-config", "--stdio"}, want: "error: the argument '--print-startup-config' cannot be used with '--stdio'\n\nUsage: codex app-server --ws-auth <MODE> --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--analytics-default-enabled", "--stdio"}, want: "error: the argument '--print-startup-config' cannot be used with '--stdio'\n\nUsage: codex app-server --print-startup-config --analytics-default-enabled\n\nFor more information, try '--help'."},
	{args: []string{"--remote-control", "--print-startup-config"}, want: "error: the argument '--remote-control' cannot be used with '--print-startup-config'\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--remote-control", "--stdio", "--print-startup-config"}, want: "error: the argument '--remote-control' cannot be used with '--print-startup-config'\n\nUsage: codex app-server --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--remote-control", "--bogus"}, want: "error: unexpected argument '--bogus' found\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--remote-contro"}, want: "error: unexpected argument '--remote-contro' found\n\n  tip: a similar argument exists: '--remote-control'\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--stdio", "--bogus"}, want: "error: unexpected argument '--bogus' found\n\nUsage: codex app-server --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--stdio", "--stdio"}, want: "error: the argument '--stdio' cannot be used multiple times\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--stdio", "--stdio"}, want: "error: the argument '--stdio' cannot be used multiple times\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--startup-sign-out"}, want: "error: the argument '--startup-sign-out' cannot be used multiple times\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--analytics-default-enabled", "--analytics-default-enabled"}, want: "error: the argument '--analytics-default-enabled' cannot be used multiple times\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--strict-config", "--strict-config"}, want: "error: the argument '--strict-config' cannot be used multiple times\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--remote-control", "--remote-control"}, want: "error: the argument '--remote-control' cannot be used multiple times\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}"}, want: "error: the argument '--startup-client-info <STARTUP_CLIENT_INFO>' cannot be used multiple times\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--code-mode-host", "http://a", "--code-mode-host", "http://b"}, want: "error: the argument '--code-mode-host <URL>' cannot be used multiple times\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--cloud-skills-snapshot", "a", "--cloud-skills-snapshot", "b"}, want: "error: the argument '--cloud-skills-snapshot <PATH>' cannot be used multiple times\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--ws-issuer", "a", "--ws-issuer", "b"}, want: "error: the argument '--ws-issuer <ISSUER>' cannot be used multiple times\n\nUsage: codex app-server [OPTIONS] [COMMAND]\n\nFor more information, try '--help'."},
	{args: []string{"--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--remote-control=x"}, want: "error: unexpected value 'x' for '--remote-control' found; no more were expected\n\nUsage: codex app-server --remote-control\n\nFor more information, try '--help'."},
	{args: []string{"--strict-config=x"}, want: "error: unexpected value 'x' for '--strict-config' found; no more were expected\n\nUsage: codex app-server --strict-config\n\nFor more information, try '--help'."},
	{args: []string{"--analytics-default-enabled=x"}, want: "error: unexpected value 'x' for '--analytics-default-enabled' found; no more were expected\n\nUsage: codex app-server --analytics-default-enabled\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out=x"}, want: "error: unexpected value 'x' for '--startup-sign-out' found; no more were expected\n\nUsage: codex app-server --startup-sign-out\n\nFor more information, try '--help'."},
	{args: []string{"--help=x"}, want: "error: unexpected value 'x' for '--help' found; no more were expected\n\nUsage: codex app-server --help\n\nFor more information, try '--help'."},
	{args: []string{"--stdio=x", "--print-startup-config"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --stdio <--startup-sign-out|--print-startup-config|--startup-client-info <STARTUP_CLIENT_INFO>>\n\nFor more information, try '--help'."},
	{args: []string{"--strict-config", "--print-startup-config=x"}, want: "error: unexpected value 'x' for '--print-startup-config' found; no more were expected\n\nUsage: codex app-server --strict-config --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"-c", "a=b", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--ws-auth", "capability-token", "--strict-config", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --strict-config --stdio <--ws-auth <MODE>|--ws-token-file <PATH>|--ws-token-sha256 <HEX>|--ws-shared-secret-file <PATH>|--ws-issuer <ISSUER>|--ws-audience <AUDIENCE>|--ws-max-clock-skew-seconds <SECONDS>>\n\nFor more information, try '--help'."},
	{args: []string{"--strict-config", "--ws-auth", "capability-token", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --strict-config --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--strict-config", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --strict-config --stdio <--startup-sign-out|--print-startup-config|--startup-client-info <STARTUP_CLIENT_INFO>>\n\nFor more information, try '--help'."},
	{args: []string{"--strict-config", "--startup-sign-out", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --strict-config --stdio <--startup-sign-out|--print-startup-config|--startup-client-info <STARTUP_CLIENT_INFO>>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --stdio <--startup-sign-out|--print-startup-config|--startup-client-info <STARTUP_CLIENT_INFO>>\n\nFor more information, try '--help'."},
	{args: []string{"--ws-auth", "capability-token", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--ws-auth", "capability-token", "--informational"}, want: "error: unexpected argument '--informational' found\n\nUsage: codex app-server --ws-auth <MODE>\n\nFor more information, try '--help'."},
	{args: []string{"--strict-config", "--bogus"}, want: "error: unexpected argument '--bogus' found\n\nUsage: codex app-server --strict-config\n\nFor more information, try '--help'."},
	{args: []string{"--ws-auth", "capability-token", "--bogus"}, want: "error: unexpected argument '--bogus' found\n\nUsage: codex app-server --ws-auth <MODE>\n\nFor more information, try '--help'."},
	{args: []string{"--remote-control", "--print-startup-config", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --stdio <--startup-sign-out|--print-startup-config|--startup-client-info <STARTUP_CLIENT_INFO>>\n\nFor more information, try '--help'."},
	{args: []string{"--cloud-skills-snapshot", "x.json", "--strict-config", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --cloud-skills-snapshot <PATH> --strict-config --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--code-mode-host", "http://127.0.0.1:1", "--strict-config", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --strict-config --stdio <--code-mode-host <URL>>\n\nFor more information, try '--help'."},
	{args: []string{"--listen", "stdio://", "--strict-config", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --listen <URL> --strict-config --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--ws-auth", "capability-token", "--strict-config", "--startup-sign-out", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --strict-config --stdio <--ws-auth <MODE>|--ws-token-file <PATH>|--ws-token-sha256 <HEX>|--ws-shared-secret-file <PATH>|--ws-issuer <ISSUER>|--ws-audience <AUDIENCE>|--ws-max-clock-skew-seconds <SECONDS>> <--startup-sign-out|--print-startup-config|--startup-client-info <STARTUP_CLIENT_INFO>>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--strict-config", "--ws-auth", "capability-token", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --strict-config --stdio <--startup-sign-out|--print-startup-config|--startup-client-info <STARTUP_CLIENT_INFO>>\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--strict-config", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --strict-config --stdio <--startup-sign-out|--print-startup-config|--startup-client-info <STARTUP_CLIENT_INFO>>\n\nFor more information, try '--help'."},
	{args: []string{"--strict-config", "--analytics-default-enabled", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --strict-config --analytics-default-enabled --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--ws-token-sha256", "ab", "--strict-config", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --strict-config --stdio <--ws-auth <MODE>|--ws-token-file <PATH>|--ws-token-sha256 <HEX>|--ws-shared-secret-file <PATH>|--ws-issuer <ISSUER>|--ws-audience <AUDIENCE>|--ws-max-clock-skew-seconds <SECONDS>>\n\nFor more information, try '--help'."},
	{args: []string{"--ws-max-clock-skew-seconds", "5", "--strict-config", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --strict-config --stdio <--ws-auth <MODE>|--ws-token-file <PATH>|--ws-token-sha256 <HEX>|--ws-shared-secret-file <PATH>|--ws-issuer <ISSUER>|--ws-audience <AUDIENCE>|--ws-max-clock-skew-seconds <SECONDS>>\n\nFor more information, try '--help'."},
	{args: []string{"--ws-issuer", "a", "--strict-config", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --strict-config --stdio <--ws-auth <MODE>|--ws-token-file <PATH>|--ws-token-sha256 <HEX>|--ws-shared-secret-file <PATH>|--ws-issuer <ISSUER>|--ws-audience <AUDIENCE>|--ws-max-clock-skew-seconds <SECONDS>>\n\nFor more information, try '--help'."},
	{args: []string{"-c", "a=b", "--strict-config", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --config <key=value> --strict-config --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--enable", "foo", "--strict-config", "--stdio=x"}, want: "error: unexpected value 'x' for '--stdio' found; no more were expected\n\nUsage: codex app-server --enable <FEATURE> --strict-config --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--cloud-skills-snapshot", "x.json", "--strict-config", "--remote-control=x"}, want: "error: unexpected value 'x' for '--remote-control' found; no more were expected\n\nUsage: codex app-server --cloud-skills-snapshot <PATH> --strict-config --remote-control\n\nFor more information, try '--help'."},
	{args: []string{"--remote-control=x", "--strict-config", "--stdio"}, want: "error: unexpected value 'x' for '--remote-control' found; no more were expected\n\nUsage: codex app-server --remote-control\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--listen", "bogus:"}, want: "error: invalid value 'bogus:' for '--listen <URL>': unsupported --listen URL `bogus:`; expected `stdio://`, `unix://`, `unix://PATH`, `ws://IP:PORT`, or `off`\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "--listen", "bogus:"}, want: "error: invalid value 'bogus:' for '--listen <URL>': unsupported --listen URL `bogus:`; expected `stdio://`, `unix://`, `unix://PATH`, `ws://IP:PORT`, or `off`\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--ws-auth", "bogus"}, want: "error: invalid value 'bogus' for '--ws-auth <MODE>'\n  [possible values: capability-token, signed-bearer-token]\n\nFor more information, try '--help'."},
	{args: []string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--startup-sign-out"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-client-info <STARTUP_CLIENT_INFO> --startup-sign-out\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--listen=off"}, want: "error: the argument '--print-startup-config' cannot be used with '--listen <URL>'\n\nUsage: codex app-server --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--print-startup-config", "--listen", "ws://127.0.0.1:1"}, want: "error: the argument '--print-startup-config' cannot be used with '--listen <URL>'\n\nUsage: codex app-server --print-startup-config\n\nFor more information, try '--help'."},
	{args: []string{"--stdio", "--listen", "stdio://", "proxy"}, want: "error: the argument '--stdio' cannot be used with '--listen <URL>'\n\nUsage: codex app-server --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--stdio", "--listen", "stdio://", "generate-ts", "--out", "C:\\Users\\huoga\\AppData\\Local\\Temp\\cxr42cmp\\gen"}, want: "error: the argument '--stdio' cannot be used with '--listen <URL>'\n\nUsage: codex app-server --stdio\n\nFor more information, try '--help'."},
	{args: []string{"--startup-sign-out", "proxy"}, want: "error: the following required arguments were not provided:\n  --print-startup-config\n\nUsage: codex app-server --print-startup-config --startup-sign-out\n\nFor more information, try '--help'."},
	{args: []string{"--remote-control", "--stdio", "--print-startup-config", "proxy"}, want: "error: the argument '--remote-control' cannot be used with '--print-startup-config'\n\nUsage: codex app-server --stdio\n\nFor more information, try '--help'."},
	{args: []string{"daemon", "--bogus"}, want: "error: unexpected argument '--bogus' found\n\nUsage: codex app-server daemon [OPTIONS] <COMMAND>\n\nFor more information, try '--help'."},
}

// appServerStartupGateAcceptedCombinations lists the argument combinations the
// gate must let through: Rust parses them and serves, so a clean parse is the
// only accepted outcome.
var appServerStartupGateAcceptedCombinations = [][]string{
	[]string{"--startup-sign-out", "--stdio"},
	[]string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--stdio"},
	[]string{"--startup-sign-out", "--listen", "stdio://"},
	[]string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--listen", "stdio://"},
	[]string{"--startup-sign-out", "--remote-control"},
	[]string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--remote-control"},
	[]string{"--print-startup-config", "--ws-auth", "capability-token"},
	[]string{"--ws-auth", "capability-token", "--print-startup-config"},
	[]string{"--print-startup-config", "--ws-token-file", "C:/tmp/tok"},
	[]string{"--ws-token-file", "C:/tmp/tok", "--print-startup-config"},
	[]string{"--print-startup-config", "--analytics-default-enabled"},
	[]string{"--analytics-default-enabled", "--print-startup-config"},
	[]string{"--print-startup-config", "--cloud-skills-snapshot", "C:/tmp/snap.json"},
	[]string{"--cloud-skills-snapshot", "C:/tmp/snap.json", "--print-startup-config"},
	[]string{"--print-startup-config", "--code-mode-host", "http://127.0.0.1:1"},
	[]string{"--code-mode-host", "http://127.0.0.1:1", "--print-startup-config"},
	[]string{"--print-startup-config", "-c", "model=\"o3\""},
	[]string{"-c", "model=\"o3\"", "--print-startup-config"},
	[]string{"--print-startup-config", "--strict-config"},
	[]string{"--strict-config", "--print-startup-config"},
	[]string{"--print-startup-config", "--help"},
	[]string{"--startup-sign-out", "--help"},
	[]string{"--startup-client-info", "{\"name\":\"x\",\"version\":\"1\"}", "--remote-control", "--stdio"},
	[]string{"--print-startup-config", "-c", "model=\"o3\"", "-c", "x=1"},
	[]string{"--stdio", "--remote-control"},
	[]string{"--print-startup-config", "--ws-auth", "capability-token", "--ws-token-file", "C:/tmp/tok"},
	nil,
	[]string{"--print-startup-config", "daemon", "--help"},
}

// appServerStartupGateGroupHelpCombinations lists the combinations where clap
// never runs its gates: the subcommand declares arg_required_else_help, so a
// bare invocation prints that page to stderr and exits 2.
var appServerStartupGateGroupHelpCombinations = [][]string{
	[]string{"--print-startup-config", "daemon"},
	[]string{"--strict-config", "daemon"},
	[]string{"daemon"},
}

func TestAppServerStartupGateDiagnosticsLikeRust(t *testing.T) {
	for _, test := range appServerStartupGateDiagnostics {
		argv := append([]string{"app-server"}, test.args...)
		_, err := Parse(argv)
		usageErr, ok := err.(*UsageError)
		if !ok {
			t.Fatalf("Parse(%v) error = %v, want a clap diagnostic", argv, err)
		}
		if got := strings.TrimRight(clapErrorText(usageErr.Message, usageErr.Usage), "\n"); got != test.want {
			t.Fatalf("Parse(%v) diagnostic =\n%s\nwant:\n%s", argv, got, test.want)
		}
	}
}

func TestAppServerStartupGateAcceptsRustCombinations(t *testing.T) {
	for _, args := range appServerStartupGateAcceptedCombinations {
		argv := append([]string{"app-server"}, args...)
		if _, err := Parse(argv); err != nil {
			t.Fatalf("Parse(%v) error = %v, want a clean parse", argv, err)
		}
	}
}

func TestAppServerStartupGateGroupHelpLikeRust(t *testing.T) {
	for _, args := range appServerStartupGateGroupHelpCombinations {
		argv := append([]string{"app-server"}, args...)
		parsed, err := Parse(argv)
		if err != nil {
			t.Fatalf("Parse(%v) error = %v, want the daemon help page", argv, err)
		}
		if parsed.GroupHelp == nil || !parsed.GroupHelp.Required {
			t.Fatalf("Parse(%v) group help = %#v, want the required page", argv, parsed.GroupHelp)
		}
	}
}

func TestAppServerPrintStartupConfigSkipsTransportValidationLikeRust(t *testing.T) {
	// Rust reports the startup configuration before it validates the WebSocket
	// transport flags, so an incomplete `--ws-auth` pair is not an error there.
	parsed, err := Parse([]string{"app-server", "--print-startup-config", "--ws-auth", "capability-token"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if !parsed.AppServer.PrintStartupConfig {
		t.Fatalf("PrintStartupConfig = false")
	}
	if _, err := Parse([]string{"app-server", "--ws-auth", "capability-token"}); err == nil ||
		!strings.Contains(err.Error(), "required when `--ws-auth capability-token` is set") {
		t.Fatalf("transport validation error = %v", err)
	}
}
