package turn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// TestImageGenerationCarriesRequestIDOnFailureLikeRust pins Rust #47327: the
// nested ImageGen request id survives HTTP, decode, and no-image failures and is
// delivered only to the in-process analytics observer.
func TestImageGenerationCarriesRequestIDOnFailureLikeRust(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		header     string
		wantID     string
		wantErrSub string
	}{
		{name: "http error", status: http.StatusInternalServerError, body: "image backend failed", header: "req-imagegen-failed-123", wantID: "req-imagegen-failed-123", wantErrSub: "image backend failed"},
		{name: "decode error", status: http.StatusOK, body: "not json", header: "req-imagegen-decode", wantID: "req-imagegen-decode", wantErrSub: "failed to decode image response"},
		{name: "no image data", status: http.StatusOK, body: `{"data":[]}`, header: "req-imagegen-empty", wantID: "req-imagegen-empty", wantErrSub: "no image data"},
		{name: "no header", status: http.StatusInternalServerError, body: "boom", wantErrSub: "boom"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.header != "" {
					w.Header().Set(imagegenRequestIDHeader, test.header)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			t.Cleanup(server.Close)

			var mu sync.Mutex
			var observed []string
			handler := NewImageGenerationHandler(&ImageGenerationOptions{
				SessionID:  "thread-imagegen",
				Provider:   model.APIProvider{BaseURL: server.URL + "/api/codex"},
				HTTPClient: server.Client(),
				RequestIDObserver: func(threadID string, callID string, requestID string) {
					mu.Lock()
					observed = append(observed, threadID+"/"+callID+"/"+requestID)
					mu.Unlock()
				},
			})
			output, err := handler.Execute(context.Background(), &tool.Invocation{
				CallID:   "call-image",
				ToolName: tool.NamespacedName(ImageGenerationNamespace, ImageGenerationToolName),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"prompt":"paint"}`},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if output == nil || output.Success || !strings.Contains(output.Error, test.wantErrSub) {
				t.Fatalf("output = %#v, want failure containing %q", output, test.wantErrSub)
			}
			for key := range output.Data {
				if strings.Contains(strings.ToLower(key), "imagegen") {
					t.Fatalf("request id leaked into item data: %#v", output.Data)
				}
			}
			mu.Lock()
			got := append([]string(nil), observed...)
			mu.Unlock()
			if test.wantID == "" {
				if len(got) != 0 {
					t.Fatalf("observer calls = %#v, want none", got)
				}
				return
			}
			want := "thread-imagegen/call-image/" + test.wantID
			if len(got) != 1 || got[0] != want {
				t.Fatalf("observer calls = %#v, want %q", got, want)
			}
		})
	}
}
