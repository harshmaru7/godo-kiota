# godo-kiota — Kiota-generated DigitalOcean Go SDK.
#
# Generation pipeline (mirrors digitalocean/dots, but for Go) — see scripts/generate.sh:
#   1. redocly bundle    multi-file spec -> openapi.yaml
#   2. specprep          hoist inline request bodies (spec-only)   -> clean, unique names
#   3. kiota generate    -> ./client                                (pristine, no post-gen edits)
#   4. collisionfix      hoist colliding response schemas, regen until clean
#   5. inferencegen      -> ./inference                             (OpenAI-compatible + SSE)
#   6. go mod tidy && go build
#
# Local use:
#   SPEC_REPO_DIR=/path/to/openapi-sdk make generate

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: generate
generate: ## Full regeneration pipeline (bundle -> specprep -> kiota -> collisionfix -> inference -> build)
	@bash scripts/generate.sh

.PHONY: clean
clean: ## Remove generated code and intermediates
	rm -rf client inference openapi.yaml .tools

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: build
build: ## Build everything
	go build ./...

.PHONY: test
test: ## Run tests
	go test ./...
