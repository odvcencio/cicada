// Command enginesize reports what fills Studio's UI engine WebAssembly.
//
//	cd workstation && go run ./internal/enginesize [-o build/engine-size.json] [-top 30] [-overlay file.json] [-modfile file.mod] [-wasm file]
//
// -overlay and -modfile pass through to go build and go list, for a
// differential build: -overlay replaces files of this module, -modfile can
// point a dependency at a modified copy (go refuses overlays in the module
// cache).
//
// It builds the engine with the generator's command (internal/enginebuild)
// twice: once as shipped, and once without -s so the file keeps its function
// names, with -dumpdep so the linker prints why it kept each symbol. It then
// attributes the shipped bytes to Go packages:
//
//   - code: exact, from the size of each function body and its name;
//   - data: from the sizes of the read-only and data symbols in each package
//     archive (`go tool nm`) that the linker kept;
//   - pclntab (function tables for stack traces and the garbage collector):
//     function names exactly, the rest shared out by code size (an estimate);
//   - findfunctab (the PC to function lookup): shared out by function count.
//
// It also builds two floor programs from the same toolchain and flags: an
// empty main, and a main that registers one no-op GoSX engine.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"

	"m31labs.dev/cicada/workstation/internal/enginebuild"
)

const enginePath = "m31labs.dev/cicada/workstation/engine"

// maxDataSegments is cmd/link's cap on WebAssembly data segments (wasm/asm.go).
const maxDataSegments = 100000

type fileSize struct {
	Raw    int `json:"raw"`
	Gzip   int `json:"gzip_9"`
	Brotli int `json:"brotli_11"`
}

type regionSize struct {
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
}

type packageSize struct {
	Package string `json:"package"`
	Module  string `json:"module"`
	Group   string `json:"group"`
	Total   int    `json:"total"`
	Code    int    `json:"code"`
	Funcs   int    `json:"functions"`
	Types   int    `json:"type_data"`
	Data    int    `json:"other_data"`
	Pcln    int    `json:"pclntab_estimate"`
	Lookup  int    `json:"findfunctab_estimate"`
}

type groupSize struct {
	Group    string   `json:"group"`
	Total    int      `json:"total"`
	Code     int      `json:"code"`
	Packages []string `json:"packages"`
}

type floorSize struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	fileSize
}

type report struct {
	GoVersion  string                 `json:"go_version"`
	Limit      int                    `json:"limit"`
	Headroom   int                    `json:"headroom"`
	Shipped    fileSize               `json:"shipped"`
	Sections   []wasmSection          `json:"sections"`
	Regions    []regionSize           `json:"data_regions"`
	Code       int                    `json:"code_bytes"`
	Segments   int                    `json:"data_segments"`
	Attributed int                    `json:"attributed_bytes"`
	Packages   []packageSize          `json:"packages"`
	Groups     []groupSize            `json:"groups"`
	Floors     []floorSize            `json:"floors,omitempty"`
	Chains     map[string][]chainStep `json:"chains"`
	Deps       []string               `json:"deps"`
	Notes      []string               `json:"notes,omitempty"`
}

func main() {
	output := flag.String("o", "build/engine-size.json", "JSON report path")
	top := flag.Int("top", 30, "packages and groups to print")
	overlay := flag.String("overlay", "", "go build -overlay file, for a differential build")
	modfile := flag.String("modfile", "", "go build -modfile file, for a differential build against a modified dependency")
	floors := flag.Bool("floors", true, "also build the empty and one-engine floor programs")
	keep := flag.String("wasm", "", "also write the shipped engine file here")
	flag.Parse()
	if err := run(*output, *top, *overlay, *modfile, *keep, *floors); err != nil {
		log.Fatal(err)
	}
}

func run(output string, top int, userOverlay, modfile, keep string, floors bool) error {
	work, err := os.MkdirTemp("", "cicada-engine-size-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	overlayArgs, err := writeOverlay(work, userOverlay)
	if err != nil {
		return err
	}
	if modfile != "" {
		overlayArgs = append(overlayArgs, "-modfile="+modfile)
	}

	shippedPath := filepath.Join(work, "engine.wasm")
	if out, err := combined(enginebuild.Command(shippedPath, enginebuild.ReleaseLDFlags, overlayArgs...)); err != nil {
		return fmt.Errorf("build shipped engine: %w\n%s", err, out)
	}
	shipped, err := os.ReadFile(shippedPath)
	if err != nil {
		return err
	}
	if keep != "" {
		if err := os.WriteFile(keep, shipped, 0o644); err != nil {
			return err
		}
	}
	namedPath := filepath.Join(work, "named.wasm")
	deps, err := combined(enginebuild.Command(namedPath, "-w -dumpdep", overlayArgs...))
	if err != nil {
		return fmt.Errorf("build named engine: %w\n%s", err, tail(deps, 40))
	}
	named, err := os.ReadFile(namedPath)
	if err != nil {
		return err
	}
	shippedModule, err := parseWasm(shipped)
	if err != nil {
		return fmt.Errorf("shipped engine: %w", err)
	}
	module, err := parseWasm(named)
	if err != nil {
		return fmt.Errorf("named engine: %w", err)
	}

	r := report{GoVersion: runtime.Version(), Limit: enginebuild.MaxBytes, Shipped: compressed(shipped), Sections: shippedModule.Sections}
	r.Headroom = r.Limit - r.Shipped.Raw
	if a, b := sectionBytes(shippedModule, 10, 11), sectionBytes(module, 10, 11); a != b {
		r.Notes = append(r.Notes, fmt.Sprintf("code and data differ between the shipped (%d) and named (%d) builds; attribution uses the named build", a, b))
	}

	list, err := listPackages(overlayArgs)
	if err != nil {
		return err
	}
	var paths []string
	for _, p := range list {
		paths = append(paths, p.ImportPath)
		r.Deps = append(r.Deps, p.ImportPath)
	}
	pkgs := newPackages(paths, enginePath)
	graph := parseDumpdep(bytes.NewReader(deps))
	symbols := nmArchives(list)

	sizes := map[string]*packageSize{}
	get := func(pkg string) *packageSize {
		if pkg == "" {
			pkg = "runtime"
		}
		if s := sizes[pkg]; s != nil {
			return s
		}
		s := &packageSize{Package: pkg}
		sizes[pkg] = s
		return s
	}
	// Code: every function body, by the package in its name. The name
	// section spells names with only [A-Za-z0-9_.], so each is mapped back to
	// the linker's name through -dumpdep. Bodies without a package are runtime
	// assembly and linker-made functions.
	linkerName := map[string]string{}
	for _, name := range graph.order {
		if k := wasmFunctionName(name); linkerName[k] == "" {
			linkerName[k] = name
		}
	}
	nameBytes := map[string]int{}
	functions := map[string]bool{}
	unmapped := 0
	for _, f := range module.Functions {
		name, ok := linkerName[f.Name]
		if !ok {
			name = f.Name
			unmapped++
		}
		functions[name] = true
		pkg := pkgs.of(name)
		s := get(pkg)
		s.Code += f.Bytes
		s.Funcs++
		r.Code += f.Bytes
		nameBytes[s.Package] += len(name) + 1
	}
	if unmapped > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("%d of %d function names had no -dumpdep match; they were attributed by their WebAssembly name", unmapped, len(module.Functions)))
	}
	// Data: kept symbols that occupy file bytes. Strings and other symbols
	// without a package belong to the package whose symbol first reached them.
	for _, name := range graph.order {
		sym, ok := symbols[name]
		if !ok {
			continue
		}
		pkg := pkgs.of(name)
		for s := graph.parent[name]; pkg == "" && s != "" && s != "_"; s = graph.parent[s] {
			pkg = pkgs.of(s)
		}
		if strings.HasPrefix(name, "type:") {
			get(pkg).Types += sym.Size
		} else {
			get(pkg).Data += sym.Size
		}
	}
	regions, layout, err := dataRegions(module)
	if err != nil {
		r.Notes = append(r.Notes, "data regions: "+err.Error())
	}
	r.Regions = regions
	// pclntab: function names exactly; the other tables by code share. The
	// function lookup table: by function count, as every function spans the
	// same number of buckets.
	if layout.pclntab > 0 && r.Code > 0 {
		rest := layout.pclntab - layout.funcnames
		for pkg, s := range sizes {
			s.Pcln = nameBytes[pkg] + int(int64(rest)*int64(s.Code)/int64(r.Code))
			s.Lookup = int(int64(layout.findfunctab) * int64(s.Funcs) / int64(len(module.Functions)))
		}
	}
	r.Segments = len(module.Segments)
	if r.Segments >= maxDataSegments {
		r.Notes = append(r.Notes, fmt.Sprintf("the data section has %d segments, the Go linker's cap: past it, runs of zero bytes are written out instead of skipped", r.Segments))
	}
	modules := map[string]string{}
	for _, p := range list {
		modules[p.ImportPath] = p.Module
	}
	groups := map[string]*groupSize{}
	for pkg, s := range sizes {
		s.Module = modules[pkg]
		if s.Module == "" {
			s.Module = "std"
		}
		s.Group = groupOf(pkg, s.Module)
		s.Total = s.Code + s.Types + s.Data + s.Pcln + s.Lookup
		r.Attributed += s.Total
		r.Packages = append(r.Packages, *s)
		g := groups[s.Group]
		if g == nil {
			g = &groupSize{Group: s.Group}
			groups[s.Group] = g
		}
		g.Total += s.Total
		g.Code += s.Code
		g.Packages = append(g.Packages, pkg)
	}
	sort.Slice(r.Packages, func(i, j int) bool {
		if r.Packages[i].Total != r.Packages[j].Total {
			return r.Packages[i].Total > r.Packages[j].Total
		}
		return r.Packages[i].Package < r.Packages[j].Package
	})
	for _, g := range groups {
		sort.Strings(g.Packages)
		r.Groups = append(r.Groups, *g)
	}
	sort.Slice(r.Groups, func(i, j int) bool { return r.Groups[i].Total > r.Groups[j].Total })
	want := map[string]bool{}
	for _, p := range r.Packages {
		want[p.Package] = true
	}
	r.Chains = graph.chains(pkgs, want, functions)

	if floors {
		r.Floors, err = buildFloors(work, overlayArgs)
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	printReport(os.Stdout, r, top)
	fmt.Printf("\nWrote %s\n", output)
	return nil
}

func combined(command *exec.Cmd) ([]byte, error) {
	var out bytes.Buffer
	command.Stdout, command.Stderr = &out, &out
	err := command.Run()
	return out.Bytes(), err
}

func tail(b []byte, lines int) []byte {
	parts := bytes.Split(b, []byte("\n"))
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return bytes.Join(parts, []byte("\n"))
}

func compressed(b []byte) fileSize {
	var gz bytes.Buffer
	w, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	w.Write(b)
	w.Close()
	var br bytes.Buffer
	bw := brotli.NewWriterOptions(&br, brotli.WriterOptions{Quality: 11, LGWin: 24})
	bw.Write(b)
	bw.Close()
	return fileSize{Raw: len(b), Gzip: gz.Len(), Brotli: br.Len()}
}

func sectionBytes(m *wasmModule, ids ...byte) int {
	total := 0
	for _, s := range m.Sections {
		for _, id := range ids {
			if s.ID == id && s.Name == sectionNames[id] {
				total += s.Bytes
			}
		}
	}
	return total
}

type listedPackage struct {
	ImportPath string
	Export     string
	Module     string
}

func listPackages(overlayArgs []string) ([]listedPackage, error) {
	args := append([]string{"list", "-deps", "-export", "-f", "{{.ImportPath}}\t{{.Export}}\t{{with .Module}}{{.Path}}{{end}}"}, overlayArgs...)
	command := exec.Command("go", append(args, enginebuild.Package)...)
	command.Env = enginebuild.Env()
	out, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("go list: %w\n%s", err, exit.Stderr)
		}
		return nil, err
	}
	var list []listedPackage
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			continue
		}
		list = append(list, listedPackage{ImportPath: f[0], Export: f[1], Module: f[2]})
	}
	return list, nil
}

// nmArchives reads the data symbols of every package archive in parallel.
func nmArchives(list []listedPackage) map[string]dataSymbol {
	symbols := map[string]dataSymbol{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan string)
	for range min(4, runtime.NumCPU()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for archive := range jobs {
				out, err := exec.Command("go", "tool", "nm", "-size", "-type", archive).Output()
				if err != nil {
					continue
				}
				local := map[string]dataSymbol{}
				parseNM(bytes.NewReader(out), local)
				mu.Lock()
				for name, s := range local {
					if old, ok := symbols[name]; !ok || s.Size > old.Size {
						symbols[name] = s
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, p := range list {
		if p.Export != "" {
			jobs <- p.Export
		}
	}
	close(jobs)
	wg.Wait()
	return symbols
}

// groupOf names a package's group: its module, or for the standard library
// its first path element ("std: net" holds net, net/http and net/url).
func groupOf(pkg, module string) string {
	if module != "std" {
		return module
	}
	first, _, _ := strings.Cut(pkg, "/")
	return "std: " + first
}

type moduleLayout struct {
	pclntab, funcnames, findfunctab int
}

const findfunctabName = "findfunctab (PC to function lookup)"

type span struct {
	name       string
	start, end uint64
}

// dataRegions finds the runtime's module table (runtime.firstmoduledata) in
// the data segments and uses its bounds to say what each data byte is. It
// reads only the leading fields of runtime.moduledata (go1.27, 64-bit
// pointers): the pclntab slices and the noptrdata and data bounds. Every
// other data byte is read-only data: type descriptors, strings, tables, GC
// bitmaps, typelinks and itablinks.
func dataRegions(m *wasmModule) ([]regionSize, moduleLayout, error) {
	var layout moduleLayout
	mem := memory{m.Segments}
	header, ok := mem.find([]byte{0xf1, 0xff, 0xff, 0xff, 0, 0, 1, 8}, 0)
	if !ok {
		return nil, layout, errors.New("pclntab header not found")
	}
	funcnameOffset := mem.u64(header + 32)
	var pattern [16]byte
	binary.LittleEndian.PutUint64(pattern[:8], header)
	binary.LittleEndian.PutUint64(pattern[8:], header+funcnameOffset)
	md, ok := mem.find(pattern[:], 0)
	if !ok {
		return nil, layout, errors.New("runtime module table not found")
	}
	u := func(off uint64) uint64 { return mem.u64(md + off) }
	slice := func(off, elem uint64) (uint64, uint64) { return u(off), u(off) + u(off+8)*elem }
	names0, names1 := slice(8, 1)
	cu0, cu1 := slice(32, 4)
	file0, file1 := slice(56, 1)
	pc0, pc1 := slice(80, 1)
	fn0, fn1 := slice(104, 1)
	const readOnly = "read-only data (types, strings, tables)"
	spans := []span{
		{"pclntab: header", header, names0},
		{"pclntab: function names", names0, names1},
		{"pclntab: compilation units", cu0, cu1},
		{"pclntab: file names", file0, file1},
		{"pclntab: pc-value tables", pc0, pc1},
		{"pclntab: function table", fn0, fn1},
		// runtime.findfunctab maps a PC to its function: 4 bytes per 4 KiB
		// of PCs and 1 per 256 bytes (cmd/link findfunctab). A js/wasm PC is
		// the function index shifted left 16 bits, so each function costs
		// 16 buckets, 320 bytes, before zero runs are left out of the file.
		{findfunctabName, u(152), u(152) + 4*((u(168)-u(160)+4095)/4096) + (u(168)-u(160)+255)/256},
		{"noptrdata", u(192), u(200)},
		{"data", u(208), u(216)},
	}
	for i, s := range spans {
		ordered := i == 0 || i >= len(spans)-3 || spans[i-1].end <= s.start
		if s.end < s.start || s.end-s.start > 1<<30 || !ordered || u(200) != u(208) {
			return nil, layout, fmt.Errorf("module table layout not recognised (%s %#x..%#x); update dataRegions for %s", s.name, s.start, s.end, runtime.Version())
		}
	}
	bytesIn := map[string]int{}
	for _, seg := range m.Segments {
		start, end := uint64(seg.Address), uint64(seg.Address)+uint64(len(seg.Data))
		counted := uint64(0)
		owner := readOnly
		for _, s := range spans {
			lo, hi := max(start, s.start), min(end, s.end)
			if lo < hi {
				bytesIn[s.name] += int(hi - lo)
				counted += hi - lo
				if s.start <= start && start < s.end {
					owner = s.name
				}
			}
		}
		bytesIn[readOnly] += int(end-start) - int(counted)
		bytesIn[owner] += seg.Overhead
	}
	out := []regionSize{{readOnly, bytesIn[readOnly]}}
	for _, s := range spans {
		out = append(out, regionSize{s.name, bytesIn[s.name]})
		if strings.HasPrefix(s.name, "pclntab") {
			layout.pclntab += bytesIn[s.name]
		}
	}
	layout.funcnames = bytesIn["pclntab: function names"]
	layout.findfunctab = bytesIn[findfunctabName]
	out = append(out, regionSize{"data section header", m.DataOverhead})
	return out, layout, nil
}

// The floor programs are added to the build through -overlay, so they need
// no files in the repository.
const floorEmpty = "package main\n\nfunc main() {}\n"

const floorEngine = `//go:build js && wasm

package main

import enginewasm "m31labs.dev/gosx/engine/wasm"

func main() {
	if err := enginewasm.Register("CicadaSizeFloor", func(enginewasm.Context) (enginewasm.Handle, error) {
		return enginewasm.HandleFunc(func() {}), nil
	}); err != nil {
		panic(err)
	}
	select {}
}
`

var floorPrograms = []struct{ name, dir, source, description string }{
	{"empty", "internal/enginesize/flooremptymain", floorEmpty, "func main() {}: the Go js/wasm runtime alone"},
	{"one engine", "internal/enginesize/floorgosxengine", floorEngine, "registers one no-op GoSX engine: runtime plus GoSX engine/wasm"},
}

// writeOverlay writes an overlay that adds the floor programs to the user's
// overlay, if any, and returns the go build arguments that use it.
func writeOverlay(work, userOverlay string) ([]string, error) {
	overlay := struct{ Replace map[string]string }{Replace: map[string]string{}}
	if userOverlay != "" {
		b, err := os.ReadFile(userOverlay)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &overlay); err != nil {
			return nil, fmt.Errorf("overlay %s: %w", userOverlay, err)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	for i, f := range floorPrograms {
		source := filepath.Join(work, fmt.Sprintf("floor%d.go", i))
		if err := os.WriteFile(source, []byte(f.source), 0o644); err != nil {
			return nil, err
		}
		if overlay.Replace == nil {
			overlay.Replace = map[string]string{}
		}
		overlay.Replace[filepath.Join(cwd, f.dir, "main.go")] = source
	}
	path := filepath.Join(work, "overlay.json")
	b, err := json.Marshal(overlay)
	if err != nil {
		return nil, err
	}
	return []string{"-overlay", path}, os.WriteFile(path, b, 0o644)
}

func buildFloors(work string, overlayArgs []string) ([]floorSize, error) {
	var out []floorSize
	for i, f := range floorPrograms {
		path := filepath.Join(work, fmt.Sprintf("floor%d.wasm", i))
		if b, err := combined(enginebuild.CommandFor("./"+f.dir, path, enginebuild.ReleaseLDFlags, overlayArgs...)); err != nil {
			return nil, fmt.Errorf("build floor %q: %w\n%s", f.name, err, b)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, floorSize{Name: f.name, Description: f.description, fileSize: compressed(b)})
	}
	return out, nil
}
