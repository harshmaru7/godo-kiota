# godo-kiota

POC: a **Microsoft Kiota–generated Go SDK** for the DigitalOcean API, auto-regenerated
whenever the spec changes — the same architecture and flow as
[`digitalocean/dots`](https://github.com/digitalocean/dots) (the Kiota-generated
TypeScript SDK), but for Go.

## How regeneration works

```
harshmaru7/openapi-sdk (spec)          this repo (godo-kiota)
  │  PR merged to main                    │
  │  .github/workflows/dispatch.yml       │
  └── repository_dispatch ──────────────► .github/workflows/regen.yml
        (event: spec-updated, payload.sha)   │
                                             ├─ checkout openapi-sdk @ sha
                                             ├─ make generate
                                             │    1. redocly bundle  → openapi.yaml
                                             │    2. kiota generate -l go → ./client
                                             │    3. tools/dedupe       (fix Go type collisions)
                                             │    4. tools/inferencegen → ./inference (SSE)
                                             │    5. go mod tidy && go build
                                             └─ open PR "regen: go sdk (kiota) @ …"
```

Run it locally against a checkout of the spec repo:

```sh
SPEC_REPO_DIR=/path/to/openapi-sdk make generate
```

## Layout

| Path                 | What                                                                |
|----------------------|--------------------------------------------------------------------|
| `client/`            | Kiota-generated control-plane client (`DigitalOceanClient`).       |
| `inference/`         | OpenAI-compatible Serverless Inference client with **SSE** support. |
| `tools/dedupe/`      | Post-gen fix for Kiota's flattened-namespace type collisions in Go. |
| `tools/inferencegen/`| Spec-driven generator for the `inference` package (Go port of dots' `postgen-inference.mjs`). |
| `godo.go`            | `NewClientWithToken` convenience constructor.                       |
| `examples/`          | Runnable examples (list regions, streaming chat).                  |

## Usage

### Control plane (Kiota)

```go
client, _ := godokiota.NewClientWithToken(os.Getenv("DIGITALOCEAN_TOKEN"))
resp, _ := client.V2().Regions().Get(context.Background(), nil)
for _, r := range resp.GetRegions() {
    fmt.Println(*r.GetSlug())
}
```

### Inference (OpenAI-compatible, with streaming)

```go
ic, _ := inference.NewInferenceClient(inference.Options{APIKey: os.Getenv("MODEL_ACCESS_KEY")})

// Non-streaming
out, _ := ic.Chat().Completions().Create(ctx, map[string]any{
    "model":    "llama3.3-70b-instruct",
    "messages": []any{map[string]any{"role": "user", "content": "Hello!"}},
})

// Streaming (SSE)
stream, _ := ic.Chat().Completions().CreateStream(ctx, map[string]any{ /* ... */ })
for {
    chunk, err := stream.Recv()
    if err == io.EOF { break }
    // chunk["choices"][0]["delta"]["content"]
}
```

## Why a post-gen `dedupe` step?

Kiota's Go target flattens a whole URL level (e.g. everything under `/v2`) into a
single Go package, so inline schemas that share a name across paths — every
`/.../actions` POST body becomes `ActionsPostRequestBody`, `/v2/droplets`
list+create both yield `DropletsResponse` — collide. Other Kiota targets nest
these in per-path namespaces, so they never collide. `tools/dedupe` renames each
colliding declaration file-locally (Kiota only ever references them from their
own file), which is safe and preserves behavior. See `tools/dedupe/main.go`.
