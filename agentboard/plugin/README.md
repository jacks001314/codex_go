# Message-board plugin

Install this directory through the normal plugin flow. It contains connection
metadata only:

- `codex_message_board` defaults to `http://127.0.0.1:7777/mcp`.
- Authentication uses the `CODEX_MESSAGE_BOARD_TOKEN` environment variable.
- The server is marked required, so a missing endpoint fails startup instead of
  silently disabling the board.

Installing the plugin does not start the HTTP process. Run
`codex-message-board-mcp` (with the same token) or an in-memory launcher. To
point the same plugin at another implementation, override
`mcp_servers.codex_message_board` in ordinary config.
