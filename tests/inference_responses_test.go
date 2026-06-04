package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestInferenceResponsesOutputText verifies the responses.Create convenience:
// it concatenates output[].content[].text parts of type "output_text" into an
// output_text field, mirroring the OpenAI Responses API.
func TestInferenceResponsesOutputText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Method != http.MethodPost {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "resp_1",
			"output": [
				{"type": "reasoning", "content": [{"type": "reasoning_text", "text": "ignore me"}]},
				{"type": "message", "content": [
					{"type": "output_text", "text": "Hello, "},
					{"type": "output_text", "text": "world!"}
				]}
			]
		}`))
	}))
	defer srv.Close()

	out, err := newClient(t, srv.URL).Responses().Create(context.Background(), map[string]any{
		"model": "openai-gpt-oss-20b",
		"input": "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := out["output_text"].(string); got != "Hello, world!" {
		t.Fatalf("output_text = %q, want %q", got, "Hello, world!")
	}
}

// TestInferenceErrorBody surfaces non-2xx responses as an error carrying the
// status and body.
func TestInferenceErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	_, err := newClient(t, srv.URL).Models().List(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("error %q missing status/body", err.Error())
	}
}
