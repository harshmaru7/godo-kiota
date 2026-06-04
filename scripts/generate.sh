#!/usr/bin/env bash
# Full Kiota Go SDK generation pipeline.
#
#   1. redocly bundle            multi-file spec -> single openapi.yaml
#   2. specprep                  hoist inline request bodies      (spec-only, deterministic)
#   3. kiota generate -l go      -> ./client                      (pristine, no post-gen edits)
#   4. collisionfix loop         hoist any colliding response schemas, regenerate until clean
#   5. inferencegen              -> ./inference                   (OpenAI-compatible + SSE)
#   6. go mod tidy && go build
#
# The collision fixes live entirely in the spec (steps 2 & 4); the generated Go
# is never hand-patched.
set -euo pipefail

SPEC_REPO_DIR="${SPEC_REPO_DIR:-.spec}"
SPEC_ENTRY="${SPEC_ENTRY:-specification/DigitalOcean-public.v2.yaml}"
BUNDLED="${BUNDLED:-openapi.yaml}"
MODULE="${MODULE:-github.com/harshmaru7/godo-kiota}"
KIOTA_CLASS="${KIOTA_CLASS:-DigitalOceanClient}"
REDOCLY_VERSION="${REDOCLY_VERSION:-1.25.13}"
MAX_ITERS="${MAX_ITERS:-6}"

gen() {
  kiota generate -l go -d "$BUNDLED" -c "$KIOTA_CLASS" -n "$MODULE/client" -o ./client --clean-output
}

echo "==> [1/6] bundling $SPEC_REPO_DIR/$SPEC_ENTRY"
npx -y "@redocly/cli@${REDOCLY_VERSION}" bundle "$SPEC_REPO_DIR/$SPEC_ENTRY" --output "$BUNDLED" --ext yaml
echo "    bundle size: $(wc -c < "$BUNDLED") bytes"

echo "==> [2/6] specprep (hoist inline request bodies)"
go run ./tools/specprep "$BUNDLED"

echo "==> [3/6] kiota generate -l go"
mkdir -p .tools
go build -o .tools/collisionfix ./tools/collisionfix
gen

echo "==> [4/6] resolving response collisions"
for ((i = 1; i <= MAX_ITERS; i++)); do
  set +e
  .tools/collisionfix "$BUNDLED" ./client
  rc=$?
  set -e
  case "$rc" in
    0) echo "    no collisions remain"; break ;;
    2) echo "    re-generating after hoist (iteration $i)"; gen ;;
    *) echo "    collisionfix failed (rc=$rc)" >&2; exit 1 ;;
  esac
  if [[ "$i" -eq "$MAX_ITERS" ]]; then
    echo "    collisionfix did not converge after $MAX_ITERS iterations" >&2
    exit 1
  fi
done

echo "==> [5/6] inferencegen (OpenAI-compatible inference client + SSE)"
go run ./tools/inferencegen "$BUNDLED" ./inference

echo "==> [6/6] go mod tidy && go build"
go mod tidy
go build ./...

echo "==> generation complete"
