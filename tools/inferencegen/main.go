// Command inferencegen reads the bundled DigitalOcean OpenAPI document and
// emits an OpenAI-compatible, SSE-capable inference client into the `inference`
// package. It is the Go counterpart of dots' scripts/postgen-inference.mjs.
//
// The inference surface is entirely spec-derived — when a new Serverless
// Inference path lands in the spec it is picked up on the next generation:
//
//   - paths come from `x-inference-base-url`, the "Serverless Inference" tag,
//     or a server URL containing "inference";
//   - method names come from HTTP verbs + path shape (POST→Create, GET→List,
//     GET-with-id→Retrieve, trailing action segment→that segment, …);
//   - group nesting comes from the literal path segments;
//   - streaming is detected from a `stream` property on the request body;
//   - responses.create output_text aggregation is detected from an `output`
//     array on the response body.
//
// Unlike the Kiota-generated control-plane client, this client uses net/http
// directly so responses keep their native snake_case field names and SSE
// streaming works without Kiota's typed-model machinery.
//
// Usage: go run ./tools/inferencegen <bundled-spec.yaml> <output-dir>
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const fallbackInferenceHost = "https://inference.do-ai.run"

type doc struct {
	Paths map[string]map[string]any
	root  map[string]any
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: inferencegen <spec.yaml> <output-dir>")
		os.Exit(2)
	}
	specPath, outDir := os.Args[1], os.Args[2]

	raw, err := os.ReadFile(specPath)
	if err != nil {
		fatal(err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		fatal(fmt.Errorf("parse spec: %w", err))
	}
	d := &doc{root: root, Paths: map[string]map[string]any{}}
	if p, ok := root["paths"].(map[string]any); ok {
		for k, v := range p {
			if m, ok := v.(map[string]any); ok {
				d.Paths[k] = m
			}
		}
	}

	endpoints, paths, baseURL := collect(d)
	if len(endpoints) == 0 {
		fmt.Fprintln(os.Stderr, "inferencegen: no inference paths found")
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatal(err)
	}
	runtime := renderRuntime(filepath.Base(specPath), paths, baseURL)
	if err := os.WriteFile(filepath.Join(outDir, "client.go"), []byte(runtime), 0o644); err != nil {
		fatal(err)
	}
	groups := renderGroups(endpoints)
	if err := os.WriteFile(filepath.Join(outDir, "groups.go"), []byte(groups), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("inferencegen: emitted %d endpoints across %d paths -> %s\n", len(endpoints), len(paths), outDir)
}

/* ─────────────────────── spec helpers ─────────────────────── */

func (d *doc) resolveRef(ref string) any {
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	var cur any = d.root
	for _, p := range strings.Split(ref[2:], "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

func (d *doc) resolve(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	if ref, ok := m["$ref"].(string); ok {
		if r, ok := d.resolveRef(ref).(map[string]any); ok {
			return r
		}
		return nil
	}
	return m
}

func isInferenceOp(d *doc, op map[string]any) bool {
	if op == nil {
		return false
	}
	if x, ok := op["x-inference-base-url"].(string); ok && strings.TrimSpace(x) != "" && !strings.Contains(x, "{") {
		return true
	}
	if tags, ok := op["tags"].([]any); ok {
		for _, t := range tags {
			if s, ok := t.(string); ok && strings.EqualFold(s, "serverless inference") {
				return true
			}
		}
	}
	if servers, ok := op["servers"].([]any); ok {
		for _, s := range servers {
			if sm, ok := s.(map[string]any); ok {
				if u, ok := sm["url"].(string); ok && strings.Contains(u, "inference") && !strings.Contains(u, "{") {
					return true
				}
			}
		}
	}
	return false
}

var httpVerbs = []string{"get", "post", "put", "patch", "delete"}

type endpoint struct {
	Path             string
	Method           string   // lower-case http verb
	Prefix           []string // camelCase literal segments → group nesting
	Params           []string // path-param names (snake_case)
	Suffix           []string // trailing action segments
	MethodName       string   // Go-exported method name
	HasRequestBody   bool
	SupportsStream   bool
	HasOutputArray   bool
}

func collect(d *doc) (eps []endpoint, paths []string, baseURL string) {
	pathSet := map[string]bool{}
	baseURLs := map[string]bool{}

	for pkey, item := range d.Paths {
		for _, verb := range httpVerbs {
			op, _ := item[verb].(map[string]any)
			if !isInferenceOp(d, op) {
				continue
			}
			pathSet[pkey] = true
			if x, ok := op["x-inference-base-url"].(string); ok && strings.TrimSpace(x) != "" {
				baseURLs[strings.TrimRight(strings.TrimSpace(x), "/")] = true
			}
			if servers, ok := op["servers"].([]any); ok {
				for _, s := range servers {
					if sm, ok := s.(map[string]any); ok {
						if u, ok := sm["url"].(string); ok && u != "" && !strings.Contains(u, "{") {
							baseURLs[strings.TrimRight(strings.TrimSpace(u), "/")] = true
						}
					}
				}
			}
		}
	}

	for p := range pathSet {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	baseURL = fallbackInferenceHost
	if !baseURLs[fallbackInferenceHost] {
		var sorted []string
		for b := range baseURLs {
			sorted = append(sorted, b)
		}
		sort.Strings(sorted)
		if len(sorted) > 0 {
			baseURL = sorted[0]
		}
	}

	for _, p := range paths {
		item := d.Paths[p]
		prefix, params, suffix := classifyPath(p)
		for _, verb := range httpVerbs {
			op, _ := item[verb].(map[string]any)
			if !isInferenceOp(d, op) {
				continue
			}
			reqSchema := d.requestSchema(op)
			resSchema := d.responseSchema(op)
			eps = append(eps, endpoint{
				Path:           p,
				Method:         verb,
				Prefix:         camelAll(prefix),
				Params:         params,
				Suffix:         suffix,
				MethodName:     deriveMethodName(verb, params, suffix),
				HasRequestBody: reqSchema != nil,
				SupportsStream: hasProp(reqSchema, "stream"),
				HasOutputArray: propType(resSchema, "output") == "array",
			})
		}
	}
	return eps, paths, baseURL
}

func (d *doc) requestSchema(op map[string]any) map[string]any {
	rb := d.resolve(op["requestBody"])
	if rb == nil {
		return nil
	}
	return d.contentSchema(rb)
}

func (d *doc) responseSchema(op map[string]any) map[string]any {
	resps, _ := op["responses"].(map[string]any)
	for _, code := range []string{"200", "201", "202"} {
		r := d.resolve(resps[code])
		if r == nil {
			continue
		}
		if s := d.contentSchema(r); s != nil {
			return s
		}
	}
	return nil
}

func (d *doc) contentSchema(holder map[string]any) map[string]any {
	content, _ := holder["content"].(map[string]any)
	app, _ := content["application/json"].(map[string]any)
	return d.resolve(app["schema"])
}

func hasProp(schema map[string]any, name string) bool {
	if schema == nil {
		return false
	}
	props, _ := schema["properties"].(map[string]any)
	_, ok := props[name]
	return ok
}

func propType(schema map[string]any, name string) string {
	if schema == nil {
		return ""
	}
	props, _ := schema["properties"].(map[string]any)
	p, _ := props[name].(map[string]any)
	t, _ := p["type"].(string)
	return t
}

/* ─────────────── path classification & naming ─────────────── */

var paramRe = regexp.MustCompile(`^\{(.+)\}$`)

func classifyPath(p string) (prefix, params, suffix []string) {
	trimmed := strings.TrimPrefix(p, "/v1/")
	seenParam := false
	for _, s := range strings.Split(trimmed, "/") {
		if m := paramRe.FindStringSubmatch(s); m != nil {
			params = append(params, m[1])
			seenParam = true
		} else if seenParam {
			suffix = append(suffix, s)
		} else {
			prefix = append(prefix, s)
		}
	}
	return prefix, params, suffix
}

// deriveMethodName mirrors OpenAI/Stripe-style conventions, exported for Go.
func deriveMethodName(verb string, params, suffix []string) string {
	if len(suffix) > 0 {
		return pascal(suffix[len(suffix)-1])
	}
	if len(params) > 0 {
		switch verb {
		case "get":
			return "Retrieve"
		case "delete":
			return "Delete"
		case "put", "patch":
			return "Update"
		case "post":
			return "Create"
		}
	}
	switch verb {
	case "get":
		return "List"
	case "post":
		return "Create"
	case "put", "patch":
		return "Update"
	case "delete":
		return "Delete"
	}
	return "Call"
}

// pathExpr builds a Go expression that reconstructs the path with `{param}`
// segments replaced by url.PathEscape(camelParam).
func pathExpr(p string) string {
	segs := strings.Split(p, "/")[1:]
	var tokens []string
	pending := ""
	flush := func() {
		if pending != "" {
			tokens = append(tokens, fmt.Sprintf("%q", pending))
			pending = ""
		}
	}
	for _, s := range segs {
		if m := paramRe.FindStringSubmatch(s); m != nil {
			pending += "/"
			flush()
			tokens = append(tokens, "url.PathEscape("+camel(m[1])+")")
		} else {
			pending += "/" + s
		}
	}
	flush()
	if len(tokens) == 1 {
		return tokens[0]
	}
	return strings.Join(tokens, " + ")
}

func camel(s string) string {
	re := regexp.MustCompile(`[-_]([a-zA-Z])`)
	return re.ReplaceAllStringFunc(s, func(m string) string {
		return strings.ToUpper(m[1:])
	})
}

func pascal(s string) string {
	c := camel(s)
	if c == "" {
		return c
	}
	return strings.ToUpper(c[:1]) + c[1:]
}

func camelAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = camel(s)
	}
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "inferencegen:", err)
	os.Exit(1)
}
