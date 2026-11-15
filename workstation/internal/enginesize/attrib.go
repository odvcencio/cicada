package main

import (
	"bufio"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// packages maps the linker's package names to import paths. The engine's own
// package is "main" in symbol names.
type packages struct {
	known map[string]bool
	main  string
}

func newPackages(importPaths []string, mainPath string) packages {
	known := map[string]bool{"main": true}
	for _, p := range importPaths {
		known[p] = true
	}
	return packages{known: known, main: mainPath}
}

// of returns the import path that owns a function or data symbol, or "" when
// the name carries no package (linker-made and assembly symbols).
func (p packages) of(name string) string {
	for _, prefix := range []string{"type:.eq.", "type:.hash.", "go:itab.", "go:func.", "type:.namedata.", "type:.importpath.", "type:"} {
		if strings.HasPrefix(name, prefix) {
			return p.inType(name[len(prefix):])
		}
	}
	if strings.HasPrefix(name, "go:") || strings.ContainsAny(name, "\" ") {
		// Strings and other content-addressed data: the caller attributes
		// them to the package that first reached them.
		return ""
	}
	if i := strings.IndexByte(name, '['); i >= 0 {
		name = name[:i]
	}
	found := ""
	for i := 0; i < len(name); i++ {
		if name[i] == '.' && p.known[name[:i]] {
			found = name[:i]
		}
	}
	if found == "" {
		// A package not in the list (for example one the linker adds).
		slash := strings.LastIndexByte(name, '/')
		if dot := strings.IndexByte(name[slash+1:], '.'); dot > 0 {
			found = name[:slash+1+dot]
		}
	}
	if found == "main" {
		return p.main
	}
	return found
}

var qualified = regexp.MustCompile(`[A-Za-z0-9_\-./]+\.[A-Za-z_][A-Za-z0-9_]*`)

// inType returns the package of the first package-qualified name inside a
// type string such as "*net/http.Request" or "map[string]*unicode.RangeTable".
func (p packages) inType(typ string) string {
	for _, m := range qualified.FindAllString(typ, -1) {
		if pkg := p.of(m); pkg != "" && (p.known[pkg] || pkg == p.main) {
			return pkg
		}
	}
	return ""
}

// depGraph is the linker's reachability tree from -ldflags=-dumpdep: every
// symbol the linker kept, in the order it was marked, with the symbol that
// first reached it.
type depGraph struct {
	order  []string
	parent map[string]string
}

func parseDumpdep(r io.Reader) depGraph {
	g := depGraph{parent: map[string]string{}}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		from, to, ok := strings.Cut(scanner.Text(), " -> ")
		if !ok {
			continue
		}
		from, to = stripDepFlags(from), stripDepFlags(to)
		if _, seen := g.parent[to]; seen {
			continue
		}
		g.parent[to] = from
		g.order = append(g.order, to)
	}
	return g
}

func stripDepFlags(name string) string {
	for _, flag := range []string{" <UsedInIface><ReflectMethod>", " <UsedInIface>", " <ReflectMethod>"} {
		name = strings.TrimSuffix(name, flag)
	}
	return name
}

var nonNameCharacter = regexp.MustCompile(`[^\w.]`)

// wasmFunctionName is how the Go linker spells a function in the WebAssembly
// name section: every character other than [A-Za-z0-9_.] becomes "_".
func wasmFunctionName(name string) string {
	return nonNameCharacter.ReplaceAllString(name, "_")
}

// chainStep is one package boundary on the path from the program's roots to a
// package: the symbol that reached into the next package.
type chainStep struct {
	Package string `json:"package"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// chains returns, for each wanted package, the linker's path from a root to
// the first function of that package it kept (or, for a package with no
// functions, its first symbol), reduced to package boundaries.
func (g depGraph) chains(p packages, want, functions map[string]bool) map[string][]chainStep {
	out := map[string][]chainStep{}
	first := map[string]string{}
	for _, sym := range g.order {
		pkg := p.of(sym)
		if !want[pkg] {
			continue
		}
		if _, seen := first[pkg]; !seen || functions[sym] && !functions[first[pkg]] {
			first[pkg] = sym
		}
	}
	for pkg, sym := range first {
		var path []string
		seen := map[string]bool{}
		for s := sym; s != "_" && s != "" && !seen[s]; s = g.parent[s] {
			seen[s] = true
			path = append(path, s)
		}
		// path runs from the package's symbol back to a root; reverse it.
		for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
			path[i], path[j] = path[j], path[i]
		}
		var steps []chainStep
		last := ""
		for i, s := range path {
			sp := p.of(s)
			if sp == "" || sp == last {
				continue
			}
			from := ""
			if i > 0 {
				from = path[i-1]
			}
			steps = append(steps, chainStep{Package: sp, From: from, To: s})
			last = sp
		}
		out[pkg] = steps
	}
	return out
}

// dataSymbol is one read-only or data symbol from a package archive.
type dataSymbol struct {
	Size int
	Kind byte // R, r, D or d as go tool nm prints
}

var nmLine = regexp.MustCompile(`^\s*[0-9a-f]*\s+(\d+)\s+([A-Za-z])\s+(.+)$`)
var staticSuffix = regexp.MustCompile(`<\d+>$`)

// parseNM reads `go tool nm -size -type` output and keeps symbols that occupy
// file bytes: read-only data and initialized data. Text is measured from the
// WebAssembly code section instead, and BSS is not stored in the file.
func parseNM(r io.Reader, into map[string]dataSymbol) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		m := nmLine.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		kind := m[2][0]
		if kind != 'R' && kind != 'r' && kind != 'D' && kind != 'd' {
			continue
		}
		size, err := strconv.Atoi(m[1])
		if err != nil || size == 0 {
			continue
		}
		name := staticSuffix.ReplaceAllString(m[3], "")
		if old, ok := into[name]; !ok || size > old.Size {
			into[name] = dataSymbol{Size: size, Kind: kind}
		}
	}
}
