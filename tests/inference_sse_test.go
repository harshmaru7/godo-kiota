package tests

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/harshmaru7/godo-kiota/inference"
)

// TestInferenceSSEStreaming drives the generated SSE client against a mock
// Serverless Inference endpoint and asserts that chunks are decoded in order
// and the stream terminates on [DONE].
func TestInferenceSSEStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("missing/incorrect auth header: %q", got)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for _, tok := range []string{"Hello", " from", " DO"} {
			w.Write([]byte(`data: {"choices":[{"delta":{"content":"` + tok + `"}}]}` + "\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	client, err := inference.NewInferenceClient(inference.Options{APIKey: "test-key", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	stream, err := client.Chat().Completions().CreateStream(context.Background(), map[string]any{
		"model":    "llama3.3-70b-instruct",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var got string
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		choices := chunk["choices"].([]any)
		delta := choices[0].(map[string]any)["delta"].(map[string]any)
		got += delta["content"].(string)
	}

	if want := "Hello from DO"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestInferenceBaseURLNormalization checks that a trailing /v1 is stripped so
// generated paths (which already include /v1/...) are not doubled.
func TestInferenceBaseURLNormalization(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	client, err := inference.NewInferenceClient(inference.Options{APIKey: "k", BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Models().List(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("got path %q, want /v1/models", gotPath)
	}
}
