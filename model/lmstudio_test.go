package model

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

func TestFetchModels(t *testing.T) {
	client := NewLMStudioClient("http://lmstudio.test")
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(`{"data":[{"id":"model-a"},{"id":"model-b"}],"models":["model-a"]}`)),
			Header:     http.Header{},
			Request:    r,
		}, nil
	})}
	models, err := client.FetchModels(context.Background())
	if err != nil {
		t.Fatalf("FetchModels() error = %v", err)
	}
	if len(models) != 2 || models[0] != "model-a" || models[1] != "model-b" {
		t.Fatalf("models = %#v", models)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
