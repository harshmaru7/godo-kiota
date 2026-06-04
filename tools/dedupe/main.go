// Command dedupe resolves duplicate top-level declarations that Kiota's Go
// generator emits when several operations share an inline schema name within
// the same package (e.g. every `/.../actions` POST body becomes
// `ActionsPostRequestBody`, and `/v2/droplets` list+create both produce a
// `DropletsResponse`). Other Kiota targets (TypeScript, C#) nest these in
// per-path namespaces so they never collide; the Go target flattens a whole
// URL level into one package, so they do.
//
// Kiota only ever references such a colliding declaration from within the very
// file that declares it (any cross-file reference would itself be ambiguous and
// would not compile), so renaming each declaration — and its references — file-
// locally is safe and preserves behavior exactly. We rewrite every occurrence
// of a colliding name in each declaring file to `<FileTag>__<Name>`, where
// FileTag is the PascalCased file stem (unique per file), guaranteeing the bare
// name disappears and no two rewrites collide.
//
// Usage: go run ./tools/dedupe <generated-root>
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
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dedupe <generated-root>")
		os.Exit(2)
	}
	root := os.Args[1]

	// Group .go files by their containing directory (== Go package).
	pkgFiles := map[string][]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir := filepath.Dir(path)
		pkgFiles[dir] = append(pkgFiles[dir], path)
		return nil
	})
	if err != nil {
		fatal(err)
	}

	totalRenames := 0
	dirs := make([]string, 0, len(pkgFiles))
	for d := range pkgFiles {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		files := pkgFiles[dir]
		// name -> set of files declaring it at top level.
		declIn := map[string]map[string]bool{}
		for _, f := range files {
			for _, name := range topLevelDecls(f) {
				if declIn[name] == nil {
					declIn[name] = map[string]bool{}
				}
				declIn[name][f] = true
			}
		}

		// Colliding names: declared in more than one file in this package.
		for name, fset := range declIn {
			if len(fset) < 2 {
				continue
			}
			for f := range fset {
				newName := fileTag(f) + "__" + name
				if renameInFile(f, name, newName) {
					totalRenames++
				}
			}
			fl := keys(fset)
			sort.Strings(fl)
			fmt.Printf("dedupe: %-32s collided across %d files in %s\n", name, len(fset), filepath.Base(dir))
		}
	}
	fmt.Printf("dedupe: rewrote %d declarations\n", totalRenames)
}

// topLevelDecls returns the names of package-level type/func(no receiver)/
// var/const declarations in a file.
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
			if decl.Recv == nil { // skip methods — they belong to a type, not the package scope
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

// fileTag converts a file stem into a unique PascalCase tag, e.g.
// droplets_item_actions_request_builder.go -> DropletsItemActionsRequestBuilder.
func fileTag(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), ".go")
	var b strings.Builder
	for _, part := range strings.Split(stem, "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}

// renameInFile rewrites whole-word occurrences of old -> new in a file.
// Whole-word (\b) boundaries ensure `Foo` does not touch `Fooable`/`NewFoo`,
// which are handled as their own colliding names. Type/func identifiers never
// appear as struct-field or string-literal text in Kiota output, so a textual
// whole-word rewrite is equivalent to an AST-scoped rename here.
func renameInFile(path, old, new string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(old) + `\b`)
	out := re.ReplaceAll(data, []byte(new))
	if string(out) == string(data) {
		return false
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		fatal(err)
	}
	return true
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "dedupe:", err)
	os.Exit(1)
}
