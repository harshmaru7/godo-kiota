# godo-kiota — Kiota-generated DigitalOcean Go SDK.
#
# Generation pipeline (mirrors digitalocean/dots, but for Go):
#   1. bundle the multi-file OpenAPI spec into a single document (redocly)
#   2. kiota generate -l go               -> ./client
#   3. dedupe colliding Kiota type names  (Go flattens namespaces; see tools/dedupe)
#   4. inferencegen                        -> ./inference (OpenAI-compatible + SSE)
#   5. go mod tidy && go build ./...
#
# Local use:
#   SPEC_REPO_DIR=/path/to/openapi-sdk make generate

KIOTA_CLASS      ?= DigitalOceanClient
MODULE           ?= github.com/harshmaru7/godo-kiota
CLIENT_NS        ?= $(MODULE)/client
BUNDLED          ?= openapi.yaml

# Where the (multi-file) OpenAPI spec lives and the entry document within it.
SPEC_REPO_DIR    ?= .spec
SPEC_ENTRY       ?= specification/DigitalOcean-public.v2.yaml

REDOCLY_VERSION  ?= 1.25.13

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}'

.PHONY: clean
clean: ## Remove generated code
	@echo "==> cleaning generated code"
	rm -rf client inference $(BUNDLED)

.PHONY: bundle
bundle: ## Bundle the multi-file spec into a single document
	@echo "==> bundling $(SPEC_REPO_DIR)/$(SPEC_ENTRY) -> $(BUNDLED)"
	npx -y @redocly/cli@$(REDOCLY_VERSION) bundle "$(SPEC_REPO_DIR)/$(SPEC_ENTRY)" --output "$(BUNDLED)" --ext yaml
	@echo "    bundle size: $$(wc -c < $(BUNDLED)) bytes"

.PHONY: generate-client
generate-client: ## Run Kiota to generate the control-plane client
	@echo "==> kiota generate -l go"
	kiota generate -l go -d "$(BUNDLED)" -c $(KIOTA_CLASS) -n $(CLIENT_NS) -o ./client --clean-output

.PHONY: dedupe
dedupe: ## Resolve Kiota's duplicate Go type declarations
	@echo "==> dedupe colliding type declarations"
	go run ./tools/dedupe ./client

.PHONY: generate-inference
generate-inference: ## Generate the OpenAI-compatible inference client (with SSE)
	@echo "==> inferencegen -> ./inference"
	go run ./tools/inferencegen "$(BUNDLED)" ./inference

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: build
build: ## Build everything
	go build ./...

.PHONY: generate
generate: clean bundle generate-client dedupe generate-inference tidy build ## Full regeneration pipeline
	@echo "==> generation complete"

.PHONY: test
test: ## Run tests
	go test ./...
