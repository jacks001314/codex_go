package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codex_go/appserver"
	"codex_go/doctor"
)

// tuiClientName is the app-server client identity of the interactive TUI. Rust
// registers the in-process TUI connection as "codex-tui"
// (codex-rs/tui/src/lib.rs and app_server_connection.rs), and the app-server
// derives the session originator from that name.
const tuiClientName = "codex-tui"

// initializeLocalTUIConnection performs the app-server initialize handshake for
// an in-process TUI connection before any other RPC. In-process flows still
// route through the runtime router, which enforces the same "Not initialized"
// gate as socket transports, so a synthetic connection must be initialized
// before it can issue thread/goal or other runtime requests.
func initializeLocalTUIConnection(handle func(request *appserver.Request) *appserver.Response, connectionID string) error {
	raw, err := json.Marshal(appserver.InitializeParams{
		ClientInfo: appserver.ClientInfo{
			// Rust's in-process TUI connects as "codex-tui"
			// (codex-rs/tui/src/lib.rs: client_name: "codex-tui"). The
			// app-server derives the request originator from clientInfo.name,
			// so the TUI's model traffic must carry "codex-tui" exactly as
			// upstream does - "codex_go_tui" made every TUI request identify
			// itself as a foreign client (and never matched the first-party
			// originator checks).
			Name:    "codex-tui",
			Version: doctor.Version(),
		},
		Capabilities: &appserver.InitializeCapabilities{
			ExperimentalAPI:                true,
			MCPServerOpenAIFormElicitation: true,
		},
	})
	if err != nil {
		return fmt.Errorf("initialize failed in TUI: %w", err)
	}
	response := handle(&appserver.Request{
		JSONRPC:      "2.0",
		ID:           appserver.IntID(1),
		Method:       appserver.MethodInitialize,
		Params:       raw,
		ConnectionID: connectionID,
	})
	if response == nil {
		return errors.New("initialize failed in TUI: no response")
	}
	if response.Error != nil {
		return fmt.Errorf("initialize failed in TUI: %s", strings.TrimSpace(response.Error.Message))
	}
	return nil
}
