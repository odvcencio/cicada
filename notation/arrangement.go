package notation

import (
	"fmt"
	gts "github.com/odvcencio/gotreesitter"
	"m31labs.dev/cicada/kernel/seq"
	"math/big"
	"strconv"
	"strings"
)

type Arrangement struct {
	Placements []Placement
	Markers    []Marker
	Position   Position
}
type Placement struct {
	Name, Track, Content string
	AtTick, LengthTicks  int64
	Params               []Param
	Position             Position
}
type Marker struct {
	Name     string
	AtTick   int64
	Params   []Param
	Position Position
}

// MusicalTicks accepts exact 4/4 positions, bars, ticks, or note divisions.
// Values outside the fixed tick resolution fail instead of being rounded.
func MusicalTicks(value string, position bool) (int64, error) {
	if strings.HasPrefix(value, "@") && position {
		parts := strings.Split(value[1:], ".")
		if len(parts) != 3 {
			return 0, fmt.Errorf("expected @bar.beat.step")
		}
		var n [3]int64
		for i, part := range parts {
			v, err := strconv.ParseInt(part, 10, 64)
			if err != nil || v < 1 {
				return 0, fmt.Errorf("invalid musical position %q", value)
			}
			n[i] = v
		}
		if n[0] > (1<<53-1)/seq.TicksPerBar || n[1] > 4 || n[2] > 4 {
			return 0, fmt.Errorf("position outside 4/4 grid: %q", value)
		}
		return (n[0]-1)*seq.TicksPerBar + (n[1]-1)*seq.PPQ + (n[2]-1)*seq.TicksPerStep, nil
	}
	multiplier := int64(1)
	number := value
	switch {
	case strings.HasSuffix(value, "ticks"):
		number = strings.TrimSuffix(value, "ticks")
	case strings.HasSuffix(value, "bars"):
		number = strings.TrimSuffix(value, "bars")
		multiplier = seq.TicksPerBar
	case strings.HasSuffix(value, "bar"):
		number = strings.TrimSuffix(value, "bar")
		multiplier = seq.TicksPerBar
	case strings.Contains(value, "/") && !position:
		multiplier = seq.TicksPerBar
	default:
		return 0, fmt.Errorf("expected exact ticks, bars, or note division; actual %q", value)
	}
	r, ok := new(big.Rat).SetString(number)
	if !ok {
		return 0, fmt.Errorf("invalid musical time %q", value)
	}
	r.Mul(r, big.NewRat(multiplier, 1))
	if !r.IsInt() || !r.Num().IsInt64() || r.Sign() < 0 || r.Num().Int64() > 1<<53-1 || !position && r.Sign() == 0 {
		return 0, fmt.Errorf("expected exact nonnegative musical ticks; actual %q", value)
	}
	return r.Num().Int64(), nil
}

func parseArrangement(w *loweringWalker, n *gts.Node) *Arrangement {
	a := &Arrangement{Position: w.position(n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		params := audioParams(w, c)
		var at, length int64
		for _, p := range params {
			if p.Name == "at" {
				at, _ = MusicalTicks(p.Value, true)
			}
			if p.Name == "length" {
				length, _ = MusicalTicks(p.Value, false)
			}
		}
		switch w.Type(c) {
		case "place_decl":
			a.Placements = append(a.Placements, Placement{Name: w.Text(w.Field(c, "name")), Track: w.Text(w.Field(c, "track")), Content: w.Text(w.Field(c, "content")), AtTick: at, LengthTicks: length, Params: params, Position: w.position(c)})
		case "marker_decl":
			a.Markers = append(a.Markers, Marker{Name: w.Text(w.Field(c, "name")), AtTick: at, Params: params, Position: w.position(c)})
		}
	}
	return a
}

func ValidateArrangement(s *Score) []Diagnostic {
	if s.Arrange == nil {
		return nil
	}
	var ds []Diagnostic
	add := func(code, message string, p Position) {
		ds = append(ds, Diagnostic{Code: code, Severity: "error", Message: message, Position: p})
	}
	a := s.Arrange
	if s.Version != 2 {
		add("CICADA-VERSION", "arrange requires edition 2", a.Position)
	}
	if s.SongPosition.Line != 0 || len(s.Song) != 0 {
		add("CICADA-ARRANGEMENT", "song and arrange are mutually exclusive", a.Position)
	}
	if len(a.Placements) == 0 {
		add("CICADA-ARRANGEMENT", "arrange needs at least one placement", a.Position)
	}
	names := map[string]bool{}
	fields := func(name string, params []Param, at, length int64, placement bool, p Position) {
		if len(name) == 0 || len(name) > 64 || names[name] {
			add("CICADA-DUPLICATE", "invalid or duplicate arrangement name "+name, p)
		}
		names[name] = true
		seen := map[string]bool{}
		for _, f := range params {
			if seen[f.Name] {
				add("CICADA-DUPLICATE", "duplicate "+f.Name, f.Position)
			}
			seen[f.Name] = true
			if f.Name != "at" && (!placement || f.Name != "length") {
				add("CICADA-ARRANGEMENT", "unknown arrangement field "+f.Name, f.Position)
				continue
			}
			v, err := MusicalTicks(f.Value, f.Name == "at")
			if err != nil {
				add("CICADA-ARRANGEMENT", err.Error(), f.ValuePosition)
			} else if f.Name == "at" && v != at || f.Name == "length" && v != length {
				add("CICADA-ARRANGEMENT", "source and typed musical time differ", f.ValuePosition)
			}
		}
		if !seen["at"] || placement && !seen["length"] {
			add("CICADA-ARRANGEMENT", "expected at and placement length fields", p)
		}
	}
	for _, p := range a.Placements {
		fields(p.Name, p.Params, p.AtTick, p.LengthTicks, true, p.Position)
		if p.AtTick < 0 || p.LengthTicks <= 0 || p.AtTick > 1<<53-1-p.LengthTicks {
			add("CICADA-ARRANGEMENT", "placement time is out of range", p.Position)
		}
		var track *Track
		for i := range s.Tracks {
			if s.Tracks[i].Name == p.Track {
				track = &s.Tracks[i]
				break
			}
		}
		if track == nil {
			add("CICADA-REFERENCE", "unknown placement track "+p.Track, p.Position)
			continue
		}
		if track.Kind == "audio" {
			if !scoreHasClip(s, p.Content) {
				add("CICADA-REFERENCE", "unknown placement clip "+p.Content, p.Position)
			}
			continue
		}
		var pattern *Pattern
		for i := range s.Patterns {
			if s.Patterns[i].Name == p.Content {
				pattern = &s.Patterns[i]
				break
			}
		}
		if pattern == nil {
			add("CICADA-REFERENCE", "unknown placement pattern "+p.Content, p.Position)
			continue
		}
		isDrum := track.Kind == "drums"
		for _, kit := range s.Kits {
			isDrum = isDrum || kit.Name == track.Kind
		}
		if isDrum != (pattern.Kind == "drums") || track.Kind != "acid" && !isDrum && pattern.Kind == "acid" {
			add("CICADA-ARRANGEMENT", "placement pattern kind differs from track", p.Position)
		}
		if p.AtTick%seq.TicksPerStep != 0 {
			add("CICADA-ARRANGEMENT", "step pattern placement must start on a step", p.Position)
		}
		for _, prior := range a.Placements {
			if prior.Name != p.Name && prior.Track == p.Track && prior.AtTick < p.AtTick+p.LengthTicks && p.AtTick < prior.AtTick+prior.LengthTicks {
				add("CICADA-UNSUPPORTED", "overlapping step patterns on track "+p.Track, p.Position)
				break
			}
		}
	}
	for _, m := range a.Markers {
		fields(m.Name, m.Params, m.AtTick, 0, false, m.Position)
	}
	return ds
}

// SetPlacementField changes one authored time literal and retains every other
// byte, including comments and whitespace. Validation precedes publication.
func SetPlacementField(source []byte, name, field, value string) ([]byte, error) {
	if field != "at" && field != "length" {
		return nil, fmt.Errorf("unknown placement field %q", field)
	}
	if _, err := MusicalTicks(value, field == "at"); err != nil {
		return nil, err
	}
	doc, err := ParseDocument(source)
	if err != nil {
		return nil, err
	}
	w := doc.Walker
	for i := 0; i < doc.Root.NamedChildCount(); i++ {
		a := doc.Root.NamedChild(i)
		if w.Type(a) != "arrange_decl" {
			continue
		}
		for j := 0; j < a.NamedChildCount(); j++ {
			p := a.NamedChild(j)
			if w.Type(p) != "place_decl" || w.Text(w.Field(p, "name")) != name {
				continue
			}
			for k := 0; k < p.NamedChildCount(); k++ {
				f := p.NamedChild(k)
				if w.Type(f) != "param_decl" || w.Text(w.Field(f, "name")) != field {
					continue
				}
				v := w.Field(f, "value")
				start, end := int(v.StartByte()), int(v.EndByte())
				result := make([]byte, 0, len(source)+len(value)-(end-start))
				result = append(result, source[:start]...)
				result = append(result, value...)
				result = append(result, source[end:]...)
				_, ds := Parse(result)
				for _, d := range ds {
					if d.Severity == "error" {
						return nil, fmt.Errorf("%s: %s", d.Code, d.Message)
					}
				}
				return result, nil
			}
		}
	}
	return nil, fmt.Errorf("placement %q has no %s field", name, field)
}
