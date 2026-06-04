// Command specprep preprocesses the bundled OpenAPI document so that Microsoft
// Kiota's Go generator emits clean, collision-free code.
//
// Kiota's Go target flattens an entire URL level (e.g. everything under /v2)
// into a single Go package, then auto-names any *anonymous* request/response
// schema after its path segment + HTTP method. When two operations share that
// shape — every `/.../actions` POST body, or `/v2/droplets` list vs create —
// the auto-names collide and the package will not compile. Other Kiota targets
// nest these in per-path namespaces, so they never collide.
//
// The fix is to remove the anonymity: we hoist inline schemas into
// `components/schemas` under deterministic, unique keys and replace them with a
// `$ref`. Kiota then names the Go type after the schema key, which we control.
//
// specprep performs the deterministic half: it hoists EVERY inline
// application/json request body, keyed by operationId (which OpenAPI guarantees
// unique). This yields clean names like DropletActionsPostRequest and removes
// all request-body collisions before Kiota ever runs. Response-side collisions,
// which are rare and live inside shared components/responses, are handled
// separately by tools/collisionfix (collision-driven, so non-colliding
// responses keep Kiota's tidy default names).
//
// Usage: go run ./tools/specprep <bundled-spec.yaml>
package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: specprep <bundled-spec.yaml>")
		os.Exit(2)
	}
	path := os.Args[1]

	raw, err := os.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		fatal(err)
	}

	schemas := ensureSchemas(root)
	paths, _ := root["paths"].(map[string]any)

	hoisted := 0
	// Deterministic iteration for stable output.
	for _, p := range sortedKeys(paths) {
		item, ok := paths[p].(map[string]any)
		if !ok {
			continue
		}
		for _, verb := range []string{"get", "post", "put", "patch", "delete"} {
			op, ok := item[verb].(map[string]any)
			if !ok {
				continue
			}
			rb, ok := op["requestBody"].(map[string]any)
			if !ok {
				continue
			}
			content, ok := rb["content"].(map[string]any)
			if !ok {
				continue
			}
			mt, ok := content["application/json"].(map[string]any)
			if !ok {
				continue
			}
			schema, ok := mt["schema"].(map[string]any)
			if !ok {
				continue
			}
			if _, isRef := schema["$ref"]; isRef {
				continue // already named
			}
			if !nameable(schema) {
				continue // primitive/array body — Kiota does not create a colliding named type
			}
			opID, _ := op["operationId"].(string)
			base := snake(opID)
			if base == "" {
				base = snake(strings.Trim(p, "/")) + "_" + verb
			}
			key := uniqueKey(schemas, base+"_request")
			schemas[key] = schema
			mt["schema"] = map[string]any{"$ref": "#/components/schemas/" + key}
			hoisted++
		}
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("specprep: hoisted %d inline request bodies into components/schemas\n", hoisted)
}

// nameable reports whether Kiota would mint a named type for this schema (and
// thus risk a collision): objects and composed schemas. Primitives and bare
// arrays are passed through inline and never collide.
func nameable(s map[string]any) bool {
	for _, k := range []string{"properties", "allOf", "oneOf", "anyOf"} {
		if _, ok := s[k]; ok {
			return true
		}
	}
	if t, _ := s["type"].(string); t == "object" {
		return true
	}
	return false
}

func ensureSchemas(root map[string]any) map[string]any {
	comps, ok := root["components"].(map[string]any)
	if !ok {
		comps = map[string]any{}
		root["components"] = comps
	}
	schemas, ok := comps["schemas"].(map[string]any)
	if !ok {
		schemas = map[string]any{}
		comps["schemas"] = schemas
	}
	return schemas
}

func uniqueKey(schemas map[string]any, want string) string {
	if _, exists := schemas[want]; !exists {
		return want
	}
	for i := 2; ; i++ {
		k := fmt.Sprintf("%s_%d", want, i)
		if _, exists := schemas[k]; !exists {
			return k
		}
	}
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)
var camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// snake converts an operationId (e.g. "dropletActions_post" / "volumeActions_post_byId")
// to lower_snake_case so the resulting schema key PascalCases cleanly in Kiota.
func snake(s string) string {
	s = camelBoundary.ReplaceAllString(s, "${1}_${2}")
	s = nonAlnum.ReplaceAllString(s, "_")
	return strings.ToLower(strings.Trim(s, "_"))
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "specprep:", err)
	os.Exit(1)
}
