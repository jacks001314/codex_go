package execserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"
	"time"
)

// Rust #54135 (codex-rs/exec-server/src/connection.rs
// `direct_websocket_connection`): the direct transport must not apply the
// client's 8 KiB inbound request limit, so an fs/writeFile larger than that
// still reaches the executor. WebSocket message and frame limits stay in place.
//
// Go's direct client already raises the connection read limit to the transport
// limit (execserver/client.go, `conn.SetReadLimit(16 * 1024 * 1024)`) and has no
// separate inbound request cap, so this guards that behavior end to end.
func TestDirectWebSocketWritesAboveClientRequestLimitLikeRust(t *testing.T) {
	client, exec := startFileWriteStreamExecutor(t, false)
	content := bytes.Repeat([]byte("0123456789abcdef"), 32768) // 512 KiB, above the harness 256 KiB frame cap
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := client.FSWriteFile(ctx, &FSWriteFileParams{
		Path:       "file:///tmp/large.bin",
		DataBase64: base64.StdEncoding.EncodeToString(content),
	}); err != nil {
		t.Fatalf("FSWriteFile() error = %v: a direct WebSocket must accept %d bytes", err, len(content))
	}

	_, _, _, writeFiles, _ := exec.snapshot()
	if len(writeFiles) != 1 {
		t.Fatalf("executor saw %d fs/writeFile requests, want 1", len(writeFiles))
	}
	decoded, err := base64.StdEncoding.DecodeString(writeFiles[0].DataBase64)
	if err != nil {
		t.Fatalf("fs/writeFile payload is not base64: %v", err)
	}
	if !bytes.Equal(decoded, content) {
		t.Fatalf("executor received %d bytes, want %d", len(decoded), len(content))
	}
}
