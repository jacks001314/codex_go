package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func helperGenerationHeaders(generation int) http.Header {
	headers := http.Header{}
	headers.Set("X-Helper-Generation", strconv.Itoa(generation))
	return headers
}

func newHelperRefreshTestTransport(serverURL string, run func(context.Context, string, string) (http.Header, error)) *mcpHTTPHeadersHelperTransport {
	transport, err := newMCPHTTPHeadersHelperTransport(nil, serverURL, "helper", "")
	if err != nil {
		panic(err)
	}
	transport.run = run
	return transport
}

// Mirrors Rust #41400: a same-origin POST rejected with 401/403 reruns the
// helper once and retries only when the effective headers changed.
func TestMCPHTTPHeadersHelperRefreshesRejectedPostLikeRust(t *testing.T) {
	var requests atomic.Int32
	var helperCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := requests.Add(1)
		if call == 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("X-Helper-Generation"); got != "2" {
			t.Errorf("retry generation = %q, want 2", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport := newHelperRefreshTestTransport(server.URL, func(context.Context, string, string) (http.Header, error) {
		return helperGenerationHeaders(int(helperCalls.Add(1))), nil
	})
	transport.base = server.Client().Transport
	client := &http.Client{Transport: transport}
	response, err := client.Post(server.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("server requests = %d, want 2", got)
	}
	if got := helperCalls.Load(); got != 2 {
		t.Fatalf("helper calls = %d, want 2", got)
	}
}

func TestMCPHTTPHeadersHelperDoesNotRetryUnchangedOrFailedRefreshLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(int) (http.Header, error)
	}{
		{
			name: "unchanged",
			run: func(int) (http.Header, error) {
				return helperGenerationHeaders(1), nil
			},
		},
		{
			name: "failed",
			run: func(call int) (http.Header, error) {
				if call > 1 {
					return nil, errors.New("helper failed")
				}
				return helperGenerationHeaders(call), nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			var helperCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			}))
			defer server.Close()
			transport := newHelperRefreshTestTransport(server.URL, func(context.Context, string, string) (http.Header, error) {
				return tc.run(int(helperCalls.Add(1)))
			})
			transport.base = server.Client().Transport
			client := &http.Client{Transport: transport}
			response, err := client.Post(server.URL, "application/json", nil)
			if err != nil {
				t.Fatalf("POST error = %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", response.StatusCode)
			}
			if got := requests.Load(); got != 1 {
				t.Fatalf("server requests = %d, want no retry", got)
			}
			if got := helperCalls.Load(); got != 2 {
				t.Fatalf("helper calls = %d, want initial plus one refresh", got)
			}
		})
	}
}

func TestMCPHTTPHeadersHelperPreservesExplicitAuthorizationLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer explicit" {
			t.Errorf("Authorization = %q, want the explicit credential", got)
		}
		if got := r.Header.Get("X-Helper-Generation"); got != "1" {
			t.Errorf("helper header = %q, want 1", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	transport := newHelperRefreshTestTransport(server.URL, func(context.Context, string, string) (http.Header, error) {
		headers := helperGenerationHeaders(1)
		headers.Set("Authorization", "Bearer helper")
		return headers, nil
	})
	transport.base = server.Client().Transport
	client := &http.Client{Transport: transport}
	request, _ := http.NewRequest(http.MethodPost, server.URL, nil)
	request.Header.Set("Authorization", "Bearer explicit")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("POST error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestMCPHTTPHeadersHelperSharesConcurrentRefreshesLikeRust(t *testing.T) {
	var requests atomic.Int32
	rejected := make(chan struct{}, 2)
	allowRefresh := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := requests.Add(1)
		if call <= 2 {
			rejected <- struct{}{}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var helperCalls atomic.Int32
	transport := newHelperRefreshTestTransport(server.URL, func(context.Context, string, string) (http.Header, error) {
		call := int(helperCalls.Add(1))
		if call == 2 {
			<-allowRefresh
		}
		return helperGenerationHeaders(call), nil
	})
	transport.base = server.Client().Transport
	client := &http.Client{Transport: transport}
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := client.Post(server.URL, "application/json", nil)
			if err != nil {
				t.Errorf("POST error = %v", err)
				return
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200", response.StatusCode)
			}
		}()
	}
	for range 2 {
		select {
		case <-rejected:
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent requests were not rejected")
		}
	}
	close(allowRefresh)
	wait.Wait()
	if got := helperCalls.Load(); got != 2 {
		t.Fatalf("helper calls = %d, want initial plus one shared refresh", got)
	}
}
