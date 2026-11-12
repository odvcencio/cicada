package main

import (
	"strings"
	"testing"
)

func leb(n int) []byte {
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			out = append(out, b|0x80)
			continue
		}
		return append(out, b)
	}
}

func section(id byte, payload ...[]byte) []byte {
	var body []byte
	for _, p := range payload {
		body = append(body, p...)
	}
	return append(append([]byte{id}, leb(len(body))...), body...)
}

func name(s string) []byte { return append(leb(len(s)), s...) }

func TestParseWasmAttributesBodiesAndSegments(t *testing.T) {
	file := []byte("\x00asm\x01\x00\x00\x00")
	// One imported function shifts the defined functions' indexes by one.
	file = append(file, section(2, leb(1), name("gojs"), name("runtime.wasmExit"), []byte{0}, leb(0))...)
	body := func(n int) []byte { return append(leb(n), make([]byte, n)...) }
	file = append(file, section(10, leb(2), body(3), body(200))...)
	segment := func(address int, data string) []byte {
		out := []byte{0, 0x41}
		out = append(out, leb(address)...) // small positive values encode the same in SLEB
		out = append(out, 0x0b)
		return append(append(out, leb(len(data))...), data...)
	}
	file = append(file, section(11, leb(2), segment(16, "abc"), segment(40, "de"))...)
	names := append(leb(2), append(append(leb(1), name("net/http.(*Client).Do")...), append(leb(2), name("main.main")...)...)...)
	file = append(file, section(0, name("name"), []byte{1}, leb(len(names)), names)...)

	m, err := parseWasm(file)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, s := range m.Sections {
		total += s.Bytes
	}
	if total != len(file) {
		t.Fatalf("sections sum to %d bytes, file is %d", total, len(file))
	}
	if len(m.Functions) != 2 || m.Functions[0].Name != "net/http.(*Client).Do" || m.Functions[0].Bytes != 4 || m.Functions[1].Name != "main.main" || m.Functions[1].Bytes != 202 {
		t.Fatalf("functions = %+v", m.Functions)
	}
	if len(m.Segments) != 2 || m.Segments[1].Address != 40 || string(m.Segments[1].Data) != "de" {
		t.Fatalf("segments = %+v", m.Segments)
	}
	mem := memory{m.Segments}
	if got := string(mem.read(17, 25)[:2]); got != "bc" {
		t.Fatalf("read = %q", got)
	}
	if at, ok := mem.find([]byte("de"), 0); !ok || at != 40 {
		t.Fatalf("find = %d %v", at, ok)
	}
}

func TestPackageOfNames(t *testing.T) {
	p := newPackages([]string{"net/http", "gopkg.in/yaml.v3", "m31labs.dev/gosx/scene", "unicode", "slices", "fmt"}, "example/engine")
	for name, want := range map[string]string{
		"net/http.(*Transport).roundTrip":                 "net/http",
		"gopkg.in/yaml.v3.Unmarshal":                      "gopkg.in/yaml.v3",
		"slices.SortFunc[go.shape.[]net/http.Header]":     "slices",
		"type:.eq.m31labs.dev/gosx/scene.Material":        "m31labs.dev/gosx/scene",
		"type:*net/http.Request":                          "net/http",
		"type:map[string]*unicode.RangeTable":             "unicode",
		"go:itab.*net/http.cancelTimerBody,io.ReadCloser": "net/http",
		"main.mountShell.func1":                           "example/engine",
		"unicode..stmp_671":                               "unicode",
		"go:string.\"net/http: request canceled\"":        "",
		"wasm_pc_f_loop":                                  "",
	} {
		if got := p.of(name); got != want {
			t.Errorf("of(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestDumpdepChainsReduceToPackageBoundaries(t *testing.T) {
	deps := strings.Join([]string{
		"_ -> main.main",
		"main.main -> main.mountSounds",
		"main.mountSounds -> net/http.(*Client).Do <UsedInIface>",
		"net/http.(*Client).Do -> net/http.send",
		"net/http.send -> fmt.Sprintf",
		"main.main -> fmt.Errorf",
		"other -> fmt.Errorf",
	}, "\n")
	g := parseDumpdep(strings.NewReader(deps))
	if g.parent["fmt.Errorf"] != "main.main" {
		t.Fatalf("a symbol keeps its first parent; got %q", g.parent["fmt.Errorf"])
	}
	p := newPackages([]string{"net/http", "fmt"}, "example/engine")
	chains := g.chains(p, map[string]bool{"fmt": true}, nil)
	got := chains["fmt"]
	if len(got) != 3 || got[1].From != "main.mountSounds" || got[1].To != "net/http.(*Client).Do" || got[2].To != "fmt.Sprintf" {
		t.Fatalf("chain = %+v", got)
	}
}

func TestParseNMKeepsFileData(t *testing.T) {
	into := map[string]dataSymbol{}
	parseNM(strings.NewReader(strings.Join([]string{
		"   7a4aa       6720 d unicode..stmp_671<1>",
		"   51cdf       1222 T unicode.map.init.2",
		"   10000         64 R type:*net/http.Request",
		"   10000         72 R type:*net/http.Request",
		"   20000        512 B unicode.bss",
		"                    U fmt.Sprintf",
	}, "\n")), into)
	if len(into) != 2 || into["unicode..stmp_671"].Size != 6720 || into["type:*net/http.Request"].Size != 72 {
		t.Fatalf("symbols = %+v", into)
	}
}

func TestComma(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 16733505: "16,733,505", -43711: "-43,711"} {
		if got := comma(n); got != want {
			t.Errorf("comma(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestWasmFunctionNameMatchesTheLinker(t *testing.T) {
	for name, want := range map[string]string{
		"net/http.(*Transport).roundTrip":             "net_http.__Transport_.roundTrip",
		"m31labs.dev/gosx/scene.Material":             "m31labs.dev_gosx_scene.Material",
		"slices.SortFunc[go.shape.[]net/http.Header]": "slices.SortFunc_go.shape.__net_http.Header_",
		"type:.eq.[2]interface {}":                    "type_.eq._2_interface___",
	} {
		if got := wasmFunctionName(name); got != want {
			t.Errorf("wasmFunctionName(%q) = %q, want %q", name, got, want)
		}
	}
}
