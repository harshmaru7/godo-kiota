// Command collisionfix resolves the rare response-side type collisions that
// remain after tools/specprep, by hoisting the offending schemas in the spec
// and signaling that Kiota should be re-run.
//
// Where specprep deterministically hoists every request body, response
// collisions are rare (they only occur when two operations on one resource —
// e.g. GET vs POST /v2/droplets — both have anonymous response schemas that
// Kiota collapses to the same name). Hoisting *all* responses would needlessly
// rename the hundreds that don't collide, so collisionfix is collision-driven:
// it inspects the generated Go, finds the genuine collisions, maps each back to
// its OpenAPI path via the urlTemplate Kiota embeds in the request builder, and
// hoists only those operations' response schemas into components/schemas.
//
// Exit codes: 0 = no collisions (clean), 2 = spec changed (re-run Kiota),
// 1 = error.
//
// Usage: go run ./tools/collisionfix <bundled-spec.yaml> <generated-client-dir>
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: collisionfix <bundled-spec.yaml> <generated-client-dir>")
		os.Exit(1)
	}
	specPath, clientDir := os.Args[1], os.Args[2]

	// 1. Find colliding top-level declarations, grouped by package dir.
	collisions := findCollisions(clientDir) // name -> []file
	if len(collisions) == 0 {
		fmt.Println("collisionfix: no collisions")
		os.Exit(0)
	}

	// 2. Load spec.
	raw, err := os.ReadFile(specPath)
	if err != nil {
		fatal(err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		fatal(err)
	}
	schemas := ensureSchemas(root)
	specPaths, _ := root["paths"].(map[string]any)

	// 3. Resolve each collision to OpenAPI path(s) and hoist the offending schemas.
	hoisted := 0
	for _, name := range sortedKeys(collisions) {
		files := collisions[name]
		paths := pathsFromFiles(files, clientDir)
		if len(paths) == 0 {
			fmt.Fprintf(os.Stderr, "collisionfix: WARNING could not map %q (files: %v) to a path\n", name, base(files))
			continue
		}
		isRequest := strings.Contains(name, "RequestBody")
		for _, p := range paths {
			item, ok := specPaths[p].(map[string]any)
			if !ok {
				continue
			}
			for _, verb := range []string{"get", "post", "put", "patch", "delete"} {
				op, ok := item[verb].(map[string]any)
				if !ok {
					continue
				}
				if isRequest {
					hoisted += hoistRequest(schemas, op, p, verb)
				} else {
					hoisted += hoistResponses(root, schemas, op)
				}
			}
		}
		fmt.Printf("collisionfix: %-40s -> hoisted schemas for %v\n", name, paths)
	}

	if hoisted == 0 {
		fmt.Fprintln(os.Stderr, "collisionfix: collisions found but nothing hoisted (already $ref?) — cannot resolve")
		os.Exit(1)
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(specPath, out, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("collisionfix: hoisted %d schema(s); re-run Kiota\n", hoisted)
	os.Exit(2)
}

/* ───────────────────── collision detection (Go AST) ───────────────────── */

func findCollisions(root string) map[string][]string {
	pkgFiles := map[string][]string{}
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir := filepath.Dir(path)
		pkgFiles[dir] = append(pkgFiles[dir], path)
		return nil
	})

	out := map[string][]string{}
	for _, files := range pkgFiles {
		declIn := map[string][]string{}
		for _, f := range files {
			for _, n := range topLevelDecls(f) {
				declIn[n] = append(declIn[n], f)
			}
		}
		for n, fs := range declIn {
			if len(uniq(fs)) > 1 {
				out[n] = uniq(fs)
			}
		}
	}
	return out
}

func topLevelDecls(path string) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		fatal(fmt.Errorf("parse %s: %w", path, err))
	}
	var names []string
	for _, d := range file.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			if decl.Recv == nil {
				names = append(names, decl.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					names = append(names, s.Name.Name)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						names = append(names, n.Name)
					}
				}
			}
		}
	}
	return names
}

/* ───────────────────── file -> OpenAPI path mapping ───────────────────── */

var urlRe = regexp.MustCompile(`\{\+baseurl\}([^"?]*)`)

// pathsFromFiles extracts OpenAPI paths from the urlTemplate literals Kiota
// embeds. Model files (e.g. *_response.go) carry no template, so we also pull
// templates from their sibling request builder in the same package.
func pathsFromFiles(files []string, clientDir string) []string {
	set := map[string]bool{}
	scan := func(f string) {
		data, err := os.ReadFile(f)
		if err != nil {
			return
		}
		for _, m := range urlRe.FindAllStringSubmatch(string(data), -1) {
			p := strings.TrimRight(m[1], "{")
			p = strings.TrimRight(p, "/")
			if p != "" {
				set[p] = true
			}
		}
	}
	for _, f := range files {
		scan(f)
		// sibling request builder, e.g. droplets_response.go -> droplets_request_builder.go
		stem := strings.TrimSuffix(filepath.Base(f), ".go")
		stem = strings.TrimSuffix(stem, "_response")
		sib := filepath.Join(filepath.Dir(f), stem+"_request_builder.go")
		if sib != f {
			scan(sib)
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

/* ───────────────────────────── hoisting ───────────────────────────── */

func hoistRequest(schemas map[string]any, op map[string]any, path, verb string) int {
	rb, ok := op["requestBody"].(map[string]any)
	if !ok {
		return 0
	}
	schema, holder := jsonSchema(rb)
	if schema == nil || isRef(schema) || !nameable(schema) {
		return 0
	}
	base := snake(strOr(op["operationId"], strings.Trim(path, "/")+"_"+verb))
	key := uniqueKey(schemas, base+"_request")
	schemas[key] = schema
	holder["schema"] = ref(key)
	return 1
}

func hoistResponses(root, schemas, op any) int {
	opm, _ := op.(map[string]any)
	resps, _ := opm["responses"].(map[string]any)
	rootm, _ := root.(map[string]any)
	schemasm, _ := schemas.(map[string]any)
	count := 0
	for _, code := range []string{"200", "201", "202", "203"} {
		resp, ok := resps[code].(map[string]any)
		if !ok {
			continue
		}
		if r, ok := resp["$ref"].(string); ok {
			// Shared response component: hoist its schema, keyed by the component name.
			compKey := lastSeg(r)
			comp := resolveRef(rootm, r)
			if comp == nil {
				continue
			}
			schema, holder := jsonSchema(comp)
			if schema == nil || isRef(schema) || !nameable(schema) {
				continue
			}
			// Suffix "_response" so the hoisted type reads clearly and never
			// clashes with a same-named components/schemas entry (e.g. the spec
			// has both a `droplet_create` schema and `droplet_create` response).
			key := uniqueKey(schemasm, compKey+"_response")
			schemasm[key] = schema
			holder["schema"] = ref(key)
			count++
			continue
		}
		// Operation-local inline response.
		schema, holder := jsonSchema(resp)
		if schema == nil || isRef(schema) || !nameable(schema) {
			continue
		}
		base := snake(strOr(opm["operationId"], "op")) + "_" + code
		key := uniqueKey(schemasm, base+"_response")
		schemasm[key] = schema
		holder["schema"] = ref(key)
		count++
	}
	return count
}

// jsonSchema returns the application/json schema map of a requestBody/response/
// response-component holder, plus the media-type map that holds it (for in-place
// replacement).
func jsonSchema(holder map[string]any) (schema, mediaType map[string]any) {
	content, ok := holder["content"].(map[string]any)
	if !ok {
		return nil, nil
	}
	mt, ok := content["application/json"].(map[string]any)
	if !ok {
		return nil, nil
	}
	s, _ := mt["schema"].(map[string]any)
	return s, mt
}

/* ───────────────────────────── helpers ───────────────────────────── */

func nameable(s map[string]any) bool {
	for _, k := range []string{"properties", "allOf", "oneOf", "anyOf"} {
		if _, ok := s[k]; ok {
			return true
		}
	}
	t, _ := s["type"].(string)
	return t == "object"
}

func isRef(s map[string]any) bool { _, ok := s["$ref"]; return ok }

func ref(key string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + key} }

func resolveRef(root map[string]any, r string) map[string]any {
	if !strings.HasPrefix(r, "#/") {
		return nil
	}
	var cur any = root
	for _, p := range strings.Split(r[2:], "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	m, _ := cur.(map[string]any)
	return m
}

func lastSeg(r string) string {
	parts := strings.Split(r, "/")
	return parts[len(parts)-1]
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

func snake(s string) string {
	s = camelBoundary.ReplaceAllString(s, "${1}_${2}")
	s = nonAlnum.ReplaceAllString(s, "_")
	return strings.ToLower(strings.Trim(s, "_"))
}

func strOr(v any, fallback string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func base(files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = filepath.Base(f)
	}
	return out
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "collisionfix:", err)
	os.Exit(1)
}
