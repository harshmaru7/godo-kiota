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
                                             │    1. redocly bundle    → openapi.yaml
                                             │    2. tools/specprep    (hoist inline request bodies in the spec)
                                             │    3. kiota generate -l go → ./client (pristine, no post-gen edits)
                                             │    4. tools/collisionfix (hoist colliding response schemas, regen until clean)
                                             │    5. tools/inferencegen → ./inference (OpenAI-compatible + SSE)
                                             │    6. go mod tidy && go build
                                             └─ open PR "regen: go sdk (kiota) @ …"
```

Run it locally against a checkout of the spec repo:

```sh
SPEC_REPO_DIR=/path/to/openapi-sdk make generate
```

## Layout

| Path                 | What                                                                |
|----------------------|--------------------------------------------------------------------|
| `client/`            | Kiota-generated control-plane client (`DigitalOceanClient`). Pristine — never hand-patched. |
| `inference/`         | OpenAI-compatible Serverless Inference client with **SSE** support. |
| `tools/specprep/`    | Pre-Kiota spec pass: hoists inline request bodies into named components (kills request-body collisions deterministically). |
| `tools/collisionfix/`| Post-Kiota, collision-driven: hoists only the response schemas that actually collide, then re-generates. |
| `tools/inferencegen/`| Spec-driven generator for the `inference` package (Go port of dots' `postgen-inference.mjs`). |
| `godo.go`            | `NewClientWithToken` convenience constructor.                       |
| `examples/`          | Runnable examples (list regions, streaming chat).                  |
| `tests/`             | SSE + inference behavior tests (kept out of generated dirs).       |

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

## How collisions are handled (at the root, not patched)

Kiota's Go target flattens a whole URL level (e.g. everything under `/v2`) into a
single Go package, then auto-names any **anonymous** inline schema after its path
+ HTTP method. So every `/.../actions` POST body becomes `ActionsPostRequestBody`
and `/v2/droplets` list+create both yield `DropletsResponse` — and they collide.
Other Kiota targets nest these in per-path namespaces, so they never collide.

Rather than patch the generated Go, we remove the anonymity **in the spec** so
Kiota emits clean, unique names on its own:

- **`tools/specprep`** (before Kiota) hoists every inline request body into
  `components/schemas`, keyed by `operationId` (unique by spec). Deterministic;
  produces names like `Droplet_actions_post_request`, consistent with the rest of
  the generated models.
- **`tools/collisionfix`** (after Kiota) is collision-driven: it inspects the
  generated Go, finds any genuine collision, maps it back to its OpenAPI path via
  the `urlTemplate` Kiota embeds, hoists only those response schemas, and signals
  a re-generate. Non-colliding responses keep Kiota's tidy default names.

The result: the `client/` tree is **pristine Kiota output** with zero post-gen
edits, and the pipeline is robust to future spec changes.
