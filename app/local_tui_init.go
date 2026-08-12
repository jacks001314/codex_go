package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codex_go/appserver"
	"codex_go/doctor"
)

// initializeLocalTUIConnection performs the app-server initialize handshake for
// an in-process TUI connection before any other RPC. In-process flows still
// route through the runtime router, which enforces the same "Not initialized"
// gate as socket transports, so a synthetic connection must be initialized
// before it can issue thread/goal or other runtime requests.
func initializeLocalTUIConnection(handle func(request *appserver.Request) *appserver.Response, connectionID string) error {
	raw, err := json.Marshal(appserver.InitializeParams{
		ClientInfo: appserver.ClientInfo{
			Name:    "codex_go_tui",
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
