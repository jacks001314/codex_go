package codemode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	codemodev1 "codex_go/codemode/grpc"
	"codex_go/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func TestGrpcProxy503AdmissionLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name, body, status string
		failures           int32
		wantAttempts       int32
		wantCode           codes.Code
	}{
		{"plain exhausted", "no healthy upstream", "", 100, 5, codes.Unavailable},
		{"html recovery", "<html>no healthy upstream</html>", "", 1, 2, codes.OK},
		{"explicit internal", "no healthy upstream", "13", 100, 1, codes.Internal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := grpc.NewServer()
			codemodev1.RegisterCodeModeHostServer(host, &grpcSessionHostServer{})
			defer host.Stop()
			var attempts atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if attempts.Add(1) <= tc.failures {
					w.Header().Set("Content-Type", "text/plain")
					if tc.status != "" {
						w.Header().Set("Grpc-Status", tc.status)
					}
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(tc.body))
					return
				}
				host.ServeHTTP(w, r)
			}))
			server.EnableHTTP2 = true
			server.StartTLS()
			defer server.Close()
			provider := NewGrpcCodeModeSessionProvider(server.URL, server.Client())
			defer provider.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, err := provider.OpenSession(ctx, &recordingGrpcDelegate{}, tool.CodeModeSessionOptions{})
			if attempts.Load() != tc.wantAttempts {
				t.Fatalf("attempts=%d want=%d error=%v", attempts.Load(), tc.wantAttempts, err)
			}
			if tc.wantCode == codes.OK {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Code != tc.wantCode {
				t.Fatalf("error=%#v want=%v", err, tc.wantCode)
			}
		})
	}
}
