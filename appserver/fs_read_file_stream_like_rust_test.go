package appserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rust #62599: the streamed stdio frame must be byte-for-byte identical to the
// eagerly encoded response, including JSON-RPC conversion of the request ID and
// the base64 payload.
func TestWriteStreamedFileReadResponseMatchesEagerEncodingLikeRust(t *testing.T) {
	large := bytes.Repeat([]byte("codex-go streamed file contents\n"), 4096)
	for _, testCase := range []struct {
		name    string
		payload []byte
	}{
		{name: "empty", payload: nil},
		{name: "one byte padding", payload: []byte{0xff}},
		{name: "two byte padding", payload: []byte{0x00, 0xff}},
		{name: "larger than buffer", payload: large},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := OK(IntID(7), &ReadFileResponse{Data: testCase.payload})
			eager, err := json.Marshal(response)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			var streamed bytes.Buffer
			if err := writeStreamedFileReadResponse(&streamed, response, testCase.payload); err != nil {
				t.Fatalf("writeStreamedFileReadResponse() error = %v", err)
			}
			if want := string(eager) + "\n"; streamed.String() != want {
				t.Fatalf("streamed frame = %q, want %q", streamed.String(), want)
			}
			var decoded struct {
				ID     int `json:"id"`
				Result struct {
					DataBase64 string `json:"dataBase64"`
				} `json:"result"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(streamed.Bytes()), &decoded); err != nil {
				t.Fatalf("streamed frame is not valid JSON: %v", err)
			}
			if decoded.ID != 7 || decoded.Result.DataBase64 != (&ReadFileResponse{Data: testCase.payload}).Base64() {
				t.Fatalf("decoded frame = %#v", decoded)
			}
		})
	}
}

// A service read keeps the file as bytes; every non-streaming transport still
// receives the same dataBase64 field through the response's JSON form.
func TestReadFileResponseKeepsBytesUntilEncodedLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.bin")
	payload := []byte{0x00, 0xff, 0x10, 0x7f}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	response, err := NewFSService().ReadFile(&ReadFileParams{Path: path})
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(response.Data, payload) || response.DataBase64 != "" {
		t.Fatalf("ReadFile() encoded eagerly: %#v", response)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(encoded) != `{"dataBase64":"AP8Qfw=="}` {
		t.Fatalf("encoded = %s", encoded)
	}
	var roundTrip ReadFileResponse
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if roundTrip.Base64() != "AP8Qfw==" {
		t.Fatalf("round trip = %#v", roundTrip)
	}
}

// The stdio connection writes an fs/readFile response through the streaming
// path and still produces the exact JSON-RPC frame a client expects.
func TestStdioConnectionStreamsFSReadFileLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "streamed.txt")
	payload := bytes.Repeat([]byte{0x00, 0xff, 0x7f}, 40*1024)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{FS: NewFSService()})
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"fs-stream-test","version":"1"}}}` + "\n"
	request := fmt.Sprintf(`{"jsonrpc":"2.0","id":9,"method":"fs/readFile","params":{"path":%q}}`, path)
	var stdout bytes.Buffer
	if err := serveJSONLineConnection(router, strings.NewReader(initialize+request+"\n"), &stdout); err != nil {
		t.Fatalf("serveJSONLineConnection() error = %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n"))
	line := lines[len(lines)-1]
	var decoded struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			DataBase64 string `json:"dataBase64"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &decoded); err != nil {
		t.Fatalf("streamed frame is not valid JSON: %v: %s", err, line)
	}
	if decoded.ID != 9 || decoded.Result.DataBase64 != (&ReadFileResponse{Data: payload}).Base64() {
		t.Fatalf("decoded frame = %#v from %.200s", decoded, line)
	}
}

// Only a service result that still holds raw bytes takes the streaming path;
// an in-process response built with an eager string keeps the buffered writer.
func TestStreamableFileReadResponseSelectsRawBytesLikeRust(t *testing.T) {
	if _, _, ok := streamableFileReadResponse(OK(IntID(1), &ReadFileResponse{DataBase64: "aGVsbG8="})); ok {
		t.Fatal("eagerly encoded response was selected for streaming")
	}
	if _, _, ok := streamableFileReadResponse(OK(IntID(1), &ReadFileResponse{Data: []byte("hello")})); !ok {
		t.Fatal("raw-bytes response was not selected for streaming")
	}
	request := &Request{ID: IntID(1), Method: MethodFSReadFile}
	if _, _, ok := streamableFileReadResponse(ErrorResponse(request.ID, -32600, "not initialized", nil)); ok {
		t.Fatal("error response was selected for streaming")
	}
}
