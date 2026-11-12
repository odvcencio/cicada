package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

func printReport(w io.Writer, r report, top int) {
	raw := r.Shipped.Raw
	pct := func(n int) string { return fmt.Sprintf("%5.1f%%", 100*float64(n)/float64(raw)) }
	fmt.Fprintf(w, "UI engine WebAssembly (%s)\n", r.GoVersion)
	fmt.Fprintf(w, "  shipped %s bytes; gzip -9 %s; Brotli q11 %s\n", comma(raw), comma(r.Shipped.Gzip), comma(r.Shipped.Brotli))
	fmt.Fprintf(w, "  limit %s; headroom %s\n", comma(r.Limit), comma(r.Headroom))

	fmt.Fprintf(w, "\nSections of the shipped file\n")
	for _, s := range r.Sections {
		fmt.Fprintf(w, "  %12s %s  %s\n", comma(s.Bytes), pct(s.Bytes), s.Name)
	}
	if len(r.Regions) > 0 {
		fmt.Fprintf(w, "\nData section by runtime table (%s segments)\n", comma(r.Segments))
		for _, s := range r.Regions {
			fmt.Fprintf(w, "  %12s %s  %s\n", comma(s.Bytes), pct(s.Bytes), s.Name)
		}
	}

	fmt.Fprintf(w, "\nTop %d packages (total = code + type data + other data + pclntab and findfunctab estimates)\n", top)
	fmt.Fprintf(w, "  %12s %6s %11s %7s %10s %10s %10s %10s  %s\n", "total", "share", "code", "funcs", "types", "data", "pclntab", "lookup", "package")
	for i, p := range r.Packages {
		if i == top {
			break
		}
		fmt.Fprintf(w, "  %12s %s %11s %7s %10s %10s %10s %10s  %s\n", comma(p.Total), pct(p.Total), comma(p.Code), comma(p.Funcs), comma(p.Types), comma(p.Data), comma(p.Pcln), comma(p.Lookup), p.Package)
	}

	fmt.Fprintf(w, "\nTop %d groups (module, or the standard library's first path element)\n", min(top, len(r.Groups)))
	for i, g := range r.Groups {
		if i == top {
			break
		}
		fmt.Fprintf(w, "  %12s %s  %-48s %d packages\n", comma(g.Total), pct(g.Total), g.Group, len(g.Packages))
	}

	fmt.Fprintf(w, "\nWhy the top %d packages are linked (the linker's first path, one line per package boundary)\n", min(top, 15))
	for i, p := range r.Packages {
		if i == min(top, 15) {
			break
		}
		fmt.Fprintf(w, "  %s\n", p.Package)
		steps := r.Chains[p.Package]
		for _, s := range steps {
			if s.From == "" {
				fmt.Fprintf(w, "      root %s\n", s.To)
				continue
			}
			fmt.Fprintf(w, "      %s -> %s\n", short(s.From), short(s.To))
		}
	}

	if len(r.Floors) > 0 {
		fmt.Fprintf(w, "\nFloors (same toolchain and flags)\n")
		for _, f := range r.Floors {
			fmt.Fprintf(w, "  %12s raw  %10s gzip  %10s Brotli  %s: %s\n", comma(f.Raw), comma(f.Gzip), comma(f.Brotli), f.Name, f.Description)
		}
	}
	fmt.Fprintf(w, "\nAttributed to packages: %s of %s bytes (%s); code %s (%s)\n", comma(r.Attributed), comma(raw), strings.TrimSpace(pct(r.Attributed)), comma(r.Code), strings.TrimSpace(pct(r.Code)))
	for _, n := range r.Notes {
		fmt.Fprintf(w, "Note: %s\n", n)
	}
}

func short(sym string) string {
	if len(sym) > 110 {
		return sym[:107] + "..."
	}
	return sym
}

func comma(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
