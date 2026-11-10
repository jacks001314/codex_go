package model

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type capturedWebsocketFrame struct {
	fin     bool
	opcode  byte
	payload []byte
}

// Rust #60522 sends Responses messages larger than 1 MiB as one fragmented
// message: a text frame followed by continuation frames, ending with a final
// frame, without touching payload bytes so a multibyte character may straddle a
// frame boundary.
func TestResponsesWebsocketFragmentsLargeMessagesLikeRust(t *testing.T) {
	multibyte := "日本語テキスト"
	large := strings.Repeat("a", responsesWebsocketFragmentSize-1) + multibyte + strings.Repeat("b", responsesWebsocketFragmentSize)
	frames, received := captureWebsocketMessage(t, []byte(large))

	if received != large {
		t.Fatalf("received payload length = %d, want %d preserved byte-for-byte", len(received), len(large))
	}
	if len(frames) < 3 {
		t.Fatalf("frame count = %d, want a fragmented message", len(frames))
	}
	if frames[0].opcode != 1 || frames[0].fin {
		t.Fatalf("first frame = {fin:%v opcode:%d}, want a non-final text frame", frames[0].fin, frames[0].opcode)
	}
	if len(frames[0].payload) != responsesWebsocketFragmentSize {
		t.Fatalf("first fragment length = %d, want %d", len(frames[0].payload), responsesWebsocketFragmentSize)
	}
	for i, frame := range frames[1:] {
		if frame.opcode != 0 {
			t.Fatalf("frame %d opcode = %d, want continuation", i+1, frame.opcode)
		}
		if i < len(frames[1:])-1 && frame.fin {
			t.Fatalf("frame %d is final before the message ended", i+1)
		}
	}
	last := frames[len(frames)-1]
	if !last.fin {
		t.Fatal("final frame is not marked final")
	}

	// A payload at the boundary still travels as one frame.
	boundary := strings.Repeat("c", responsesWebsocketFragmentSize)
	frames, received = captureWebsocketMessage(t, []byte(boundary))
	if received != boundary {
		t.Fatal("boundary payload was not preserved")
	}
	if len(frames) != 1 || frames[0].opcode != 1 || !frames[0].fin {
		t.Fatalf("boundary frames = %#v, want one final text frame", frames)
	}
}

func captureWebsocketMessage(t *testing.T, payload []byte) ([]capturedWebsocketFrame, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	type result struct {
		frames []capturedWebsocketFrame
		data   []byte
		err    error
	}
	results := make(chan result, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			results <- result{err: acceptErr}
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		reader := bufio.NewReader(conn)
		if handshakeErr := serveWebsocketHandshake(reader, conn); handshakeErr != nil {
			results <- result{err: handshakeErr}
			return
		}
		var frames []capturedWebsocketFrame
		var data []byte
		for {
			frame, frameErr := readWebsocketFrame(reader)
			if frameErr != nil {
				results <- result{frames: frames, data: data, err: frameErr}
				return
			}
			frames = append(frames, frame)
			data = append(data, frame.payload...)
			if frame.fin {
				results <- result{frames: frames, data: data}
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+listener.Addr().String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := writeResponsesWebsocketMessage(ctx, conn, websocket.MessageText, payload); err != nil {
		t.Fatalf("writeResponsesWebsocketMessage: %v", err)
	}
	select {
	case got := <-results:
		if got.err != nil && got.data == nil {
			t.Fatalf("capture frames: %v", got.err)
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return got.frames, string(got.data)
	case <-ctx.Done():
		_ = conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("timed out capturing websocket frames")
	}
	return nil, ""
}

func serveWebsocketHandshake(reader *bufio.Reader, conn net.Conn) error {
	requestLine, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.TrimSpace(requestLine), "GET ") {
		return fmt.Errorf("unexpected request line %q", strings.TrimSpace(requestLine))
	}
	key := ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Sec-WebSocket-Key") {
			key = strings.TrimSpace(value)
		}
	}
	if key == "" {
		return fmt.Errorf("missing Sec-WebSocket-Key")
	}
	digest := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	accept := base64.StdEncoding.EncodeToString(digest[:])
	_, err = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+accept+"\r\n\r\n")
	return err
}

func readWebsocketFrame(reader *bufio.Reader) (capturedWebsocketFrame, error) {
	var header [2]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return capturedWebsocketFrame{}, err
	}
	frame := capturedWebsocketFrame{fin: header[0]&0x80 != 0, opcode: header[0] & 0x0f}
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return capturedWebsocketFrame{}, err
		}
		length = uint64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return capturedWebsocketFrame{}, err
		}
		length = binary.BigEndian.Uint64(extended[:])
	}
	var mask [4]byte
	if header[1]&0x80 != 0 {
		if _, err := io.ReadFull(reader, mask[:]); err != nil {
			return capturedWebsocketFrame{}, err
		}
	}
	if length > 0 {
		frame.payload = make([]byte, length)
		if _, err := io.ReadFull(reader, frame.payload); err != nil {
			return capturedWebsocketFrame{}, err
		}
		for i := range frame.payload {
			frame.payload[i] ^= mask[i%4]
		}
	}
	return frame, nil
}
