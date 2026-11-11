package controls_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/controls"
)

// docTable reads the rows of the first table after a heading: the first cell
// (a control name in backticks) and the remaining cells.
func docTable(t *testing.T, path, heading string) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	rows := map[string][]string{}
	in := false
	for _, line := range lines {
		if strings.HasPrefix(line, "#") {
			if in && len(rows) > 0 {
				break
			}
			in = strings.TrimSpace(strings.TrimLeft(line, "#")) == heading
			continue
		}
		if !in || !strings.HasPrefix(line, "| `") {
			if in && len(rows) > 0 && !strings.HasPrefix(line, "|") {
				break
			}
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows[strings.Trim(cells[0], "`")] = cells[1:]
	}
	if len(rows) == 0 {
		t.Fatalf("%s: no table under %q", path, heading)
	}
	return rows
}

var docRange = regexp.MustCompile(`^(-?[0-9.]+)\s*[–-]\s*(-?[0-9.]+)$`)

func docNumber(t *testing.T, text string) float64 {
	t.Helper()
	n, err := strconv.ParseFloat(strings.ReplaceAll(text, "−", "-"), 64)
	if err != nil {
		t.Fatalf("unreadable number %q", text)
	}
	return n
}

// The keyboard reference (docs/manual/keys.md) lists each family's controls
// with a range and unit. Every row must agree with the catalog that cicada
// params and cicada explain print, and every control must have a row (FM's
// op1_* rows stand for every operator, and the routes are described below
// the tables).
func TestKeysReferenceAgreesWithTheCatalog(t *testing.T) {
	catalog := controls.All()
	for heading, patch := range map[string]string{
		"Tine and Reed EP controls": "tine_ep", "Clav controls": "clav", "Tonewheel Organ controls": "tonewheel_organ",
		"FM keys controls": "fm_ep", "Analog poly keys controls": "poly_keys", "String machine controls": "string_machine",
	} {
		rows := docTable(t, "../../docs/manual/keys.md", heading)
		for _, c := range catalog.Of(patch) {
			if c.Name == "octave" || strings.HasPrefix(c.Name, "route") || patch == "fm_ep" && strings.HasPrefix(c.Name, "op") && !strings.HasPrefix(c.Name, "op1_") {
				continue
			}
			row, ok := rows[c.Name]
			if !ok {
				t.Errorf("docs/manual/keys.md %q has no row for %s", heading, c.ID)
				continue
			}
			rangeText, unit := row[0], row[1]
			scale := 1.0
			switch unit {
			case "s":
				scale = 1000
				if c.Unit != "ms" {
					t.Errorf("%s: the reference says seconds, the catalog %q", c.ID, c.Unit)
				}
			case "hz", "db", "cents":
				if want := map[string]string{"hz": "Hz", "db": "dB", "cents": "cent"}[unit]; c.Unit != want {
					t.Errorf("%s: the reference says %s, the catalog %q", c.ID, unit, c.Unit)
				}
			case "normalized", "integer":
				if c.Unit != "" {
					t.Errorf("%s: the reference says %s, the catalog %q", c.ID, unit, c.Unit)
				}
			default:
				t.Errorf("%s: unknown unit %q in the reference", c.ID, unit)
			}
			if rangeText == "2 or 4" {
				if *c.Min != 2 || *c.Max != 4 || len(c.Excluded) != 1 || c.Excluded[0] != 3 {
					t.Errorf("%s: the reference says 2 or 4, the catalog %v..%v without %v", c.ID, *c.Min, *c.Max, c.Excluded)
				}
				continue
			}
			match := docRange.FindStringSubmatch(rangeText)
			if match == nil {
				t.Errorf("%s: unreadable range %q", c.ID, rangeText)
				continue
			}
			lo, hi := docNumber(t, match[1])*scale, docNumber(t, match[2])*scale
			min, max := 0.0, float64(len(c.Values)-1)
			if c.Min != nil {
				min, max = *c.Min, *c.Max
			}
			if !close(lo, min) || !close(hi, max) {
				t.Errorf("%s: the reference says %s %s, the catalog %v..%v %s", c.ID, rangeText, unit, min, max, c.Unit)
			}
		}
		for name := range rows {
			if _, ok := catalog.Control(patch + "." + name); !ok {
				t.Errorf("docs/manual/keys.md %q lists %s, which %s does not have", heading, name, patch)
			}
		}
	}
}

func close(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= 1e-6*(1+b*b)
}

// The de-esser and tape tables of docs/audio/mix-chain.md agree with the
// catalog's ranges and defaults.
func TestEffectReferenceAgreesWithTheCatalog(t *testing.T) {
	catalog := controls.All()
	for heading, owner := range map[string]string{"Track inserts": "deesser", "Tape echo": "tape"} {
		rows := docTable(t, "../../docs/audio/mix-chain.md", heading)
		for name, row := range rows {
			c, ok := catalog.Control(owner + "." + name)
			if !ok {
				t.Errorf("docs/audio/mix-chain.md %q lists %s, which %s does not have", heading, name, owner)
				continue
			}
			if name == "time" {
				if row[2] != c.Example {
					t.Errorf("%s: the reference default %q, the catalog %q", c.ID, row[2], c.Example)
				}
				continue
			}
			match := docRange.FindStringSubmatch(strings.ReplaceAll(row[1], "−", "-"))
			if match == nil {
				t.Errorf("%s: unreadable range %q", c.ID, row[1])
				continue
			}
			if !close(docNumber(t, match[1]), *c.Min) || !close(docNumber(t, match[2]), *c.Max) || !close(docNumber(t, row[2]), c.Default.(float64)) {
				t.Errorf("%s: the reference says %s default %s, the catalog %v..%v default %v", c.ID, row[1], row[2], *c.Min, *c.Max, c.Default)
			}
		}
		if len(rows) != len(catalog.Of(owner)) {
			t.Errorf("docs/audio/mix-chain.md %q lists %d %s controls, the catalog %d", heading, len(rows), owner, len(catalog.Of(owner)))
		}
	}
}
