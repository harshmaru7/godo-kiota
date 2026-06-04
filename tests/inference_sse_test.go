package tests

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/harshmaru7/godo-kiota/inference"
)

func newClient(t *testing.T, url string) *inference.InferenceClient {
	t.Helper()
	c, err := inference.NewInferenceClient(inference.Options{APIKey: "test-key", BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestInferenceSSEStreaming drives the SSE client against a mock endpoint and
// asserts chunks decode in order and the stream terminates on [DONE].
func TestInferenceSSEStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("auth header = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, tok := range []string{"Hello", " from", " DO"} {
			w.Write([]byte(`data: {"choices":[{"delta":{"content":"` + tok + `"}}]}` + "\n\n"))
			fl.Flush()
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	stream, err := newClient(t, srv.URL).Chat().Completions().CreateStream(context.Background(), map[string]any{"model": "m"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	if got := drain(t, stream); got != "Hello from DO" {
		t.Fatalf("got %q", got)
	}
}

// TestInferenceSSEMultiLineAndComments verifies SSE-spec behavior: multiple
// data: lines in one event are joined with "\n", comment/keep-alive lines (":")
// and unknown fields (event:/id:) are ignored, and a final event without a
// trailing blank line is still flushed.
func TestInferenceSSEMultiLineAndComments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// keep-alive comment, an event with a type, and a JSON payload split
		// across two data: lines (must be re-joined before parsing).
		io.WriteString(w, ": keep-alive\n\n")
		io.WriteString(w, "event: chunk\n")
		io.WriteString(w, "id: 1\n")
		io.WriteString(w, `data: {"choices":[{"delta":`+"\n")
		io.WriteString(w, `data: {"content":"hi"}}]}`+"\n\n")
		// final event, NO trailing blank line:
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"!"}}]}`+"\n")
	}))
	defer srv.Close()

	stream, err := newClient(t, srv.URL).Chat().Completions().CreateStream(context.Background(), map[string]any{"model": "m"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	if got := drain(t, stream); got != "hi!" {
		t.Fatalf("got %q, want %q", got, "hi!")
	}
}

// TestInferenceSSEEarlyCloseNoLeak ensures closing the stream mid-flight does
// not leak the reader goroutine, even when it is parked sending to a full
// channel (server keeps streaming, consumer reads one then closes).
func TestInferenceSSEEarlyCloseNoLeak(t *testing.T) {
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for {
			select {
			case <-stop:
				return
			case <-r.Context().Done():
				return
			default:
				w.Write([]byte(`data: {"choices":[{"delta":{"content":"x"}}]}` + "\n\n"))
				fl.Flush()
				time.Sleep(time.Millisecond)
			}
		}
	}))
	defer srv.Close()
	defer close(stop)

	before := runtime.NumGoroutine()
	for i := 0; i < 25; i++ {
		stream, err := newClient(t, srv.URL).Chat().Completions().CreateStream(context.Background(), map[string]any{"model": "m"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Recv(); err != nil { // read one chunk
			t.Fatal(err)
		}
		stream.Close() // close mid-stream
	}

	// Allow goroutines to wind down.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+5 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}

// TestInferenceContextCancel ensures a cancelled context unblocks a pending Recv.
func TestInferenceContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done() // hold the connection open until cancelled
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := newClient(t, srv.URL).Chat().Completions().CreateStream(ctx, map[string]any{"model": "m"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	done := make(chan error, 1)
	go func() { _, err := stream.Recv(); done <- err }()
	cancel()
	select {
	case <-done: // Recv returned (EOF or error) — did not hang
	case <-time.After(3 * time.Second):
		t.Fatal("Recv did not return after context cancel")
	}
}

// TestInferenceBaseURLNormalization checks a trailing /v1 is stripped so
// generated paths (which already include /v1/...) are not doubled.
func TestInferenceBaseURLNormalization(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	if _, err := newClient(t, srv.URL+"/v1").Models().List(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("got path %q, want /v1/models", gotPath)
	}
}

func drain(t *testing.T, stream *inference.SSEStream) string {
	t.Helper()
	var sb strings.Builder
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			return sb.String()
		}
		if err != nil {
			t.Fatal(err)
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		delta, _ := choices[0].(map[string]any)["delta"].(map[string]any)
		if c, ok := delta["content"].(string); ok {
			sb.WriteString(c)
		}
	}
}
