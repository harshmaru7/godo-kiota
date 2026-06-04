// Streaming chat-completion demo against DigitalOcean Serverless Inference.
//
//	MODEL_ACCESS_KEY=... go run ./examples/inference-stream
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/harshmaru7/godo-kiota/inference"
)

func main() {
	key := os.Getenv("MODEL_ACCESS_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "set MODEL_ACCESS_KEY")
		os.Exit(1)
	}

	client, err := inference.NewInferenceClient(inference.Options{APIKey: key})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	stream, err := client.Chat().Completions().CreateStream(context.Background(), map[string]any{
		"model": "llama3.3-70b-instruct",
		"messages": []any{
			map[string]any{"role": "user", "content": "In one sentence, what is DigitalOcean?"},
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "stream:", err)
		os.Exit(1)
	}
	defer stream.Close()

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			fmt.Println()
			return
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "\nrecv:", err)
			os.Exit(1)
		}
		// chunk.choices[0].delta.content
		if choices, ok := chunk["choices"].([]any); ok && len(choices) > 0 {
			if c0, ok := choices[0].(map[string]any); ok {
				if delta, ok := c0["delta"].(map[string]any); ok {
					if content, ok := delta["content"].(string); ok {
						fmt.Print(content)
					}
				}
			}
		}
	}
}
