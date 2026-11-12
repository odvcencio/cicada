// Package controls is the catalog of every sound control a score can set:
// each voice, keyboard patch, library patch, effect, mixer strip and global
// setting, with the values it accepts, where a score sets it, what it does and
// which way it moves what a listener measures. cicada params --json and
// cicada explain --json print it, and setparam looks controls up in it.
//
// The catalog keeps no list of its own. It is assembled from the tables the
// validators and engines read: the parameter registry and its host-only rows
// (internal/paramdefs), the keyboard patch table (kernel/voice/keyboard) with
// each patch's defaults, the built-in patch library (instrument) and the
// validators' fixed rules, which the tests in this package compare with the
// validators both ways.
package controls

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"m31labs.dev/cicada/internal/paramdefs"
)

// Owner kinds.
const (
	KindVoice        = "voice"         // a built-in voice: acid, drums, piano, guitar, model_<profile>
	KindPatch        = "patch"         // a modeled keyboard patch such as fm_bass
	KindLibraryPatch = "library_patch" // a built-in instrument graph that addpreset copies into a score
	KindAuthored     = "authored"      // the settings every authored instrument graph takes
	KindSampler      = "sampler"       // a sampler voice
	KindKitLane      = "kit_lane"      // one lane of a declared kit
	KindEffect       = "effect"        // an effect kind
	KindMix          = "mix"           // the mixer strip every track has
	KindBus          = "bus"           // a bus strip
	KindMaster       = "master"        // the master strip
	KindGlobal       = "global"        // a song-wide setting
)

// Places a score sets a control.
const (
	InTrack      = "track"      // the track block: track bass fm_bass { op3_level = 4 }
	InInstrument = "instrument" // an instrument declaration's param line
	InEffect     = "effect"     // an effect declaration: fx echo delay { feedback = 0.4 }
	InSampler    = "sampler"    // a sampler declaration
	InBus        = "bus"        // a bus block
	InMaster     = "master"     // the master block
	InScore      = "score"      // a top-level setting such as tempo
	InPattern    = "pattern"    // a pattern attribute such as transpose
	InScene      = "scene"      // a scene setting: scene drop { bass.cutoff = 900Hz }
	InAutomation = "automation" // an automation lane: automate bass.cutoff { ... }
	InModulation = "modulation" // a modulation route target
)

// Owner is one thing a score can name that has controls: a voice, patch,
// effect kind or mixer strip.
type Owner struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Family      string `json:"family,omitempty"`
	Meaning     string `json:"meaning"`
	ValidatedBy string `json:"validated_by"`
	ReadBy      string `json:"read_by"`
	Note        string `json:"note,omitempty"`
	// Bindings are names the declaration accepts that bind a recording or
	// asset rather than set a sound control.
	Bindings []string `json:"bindings,omitempty"`
	Controls int      `json:"controls"`
}

// Control is one setting of an owner.
type Control struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
	// Name is the control as a declaration writes it; Path is what follows
	// "<owner>." in a scene, automation or modulation path when it differs.
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
	// Aliases are other names the declaration accepts for the control.
	Aliases []string `json:"aliases,omitempty"`
	// Type is number, integer, enum, toggle or name.
	Type string `json:"type"`
	Unit string `json:"unit"`
	// Min and Max are absent when the language sets no range, as for a
	// parameter an instrument graph declares.
	Min     *float64 `json:"min"`
	Max     *float64 `json:"max"`
	Default any      `json:"default"`
	// Example is a value the declaration accepts, in source form (250ms).
	Example string `json:"example"`
	// Values are the words an enum or toggle takes, and the words a number
	// also takes (off, auto, a note division).
	Values []string `json:"values,omitempty"`
	// Excluded lists numbers inside [Min, Max] that are refused.
	Excluded []float64 `json:"excluded,omitempty"`
	// RouteMax gives a higher maximum on a route, such as a master EQ.
	RouteMax map[string]float64 `json:"route_max,omitempty"`
	// Edition is set when only one edition of the language takes the control.
	Edition int `json:"edition,omitempty"`
	// Refused marks a registry row the language does not accept yet.
	Refused bool     `json:"refused,omitempty"`
	SetIn   []string `json:"set_in"`
	// Live is true when the control can change while the song plays.
	Live       bool        `json:"live"`
	RegistryID string      `json:"registry_id,omitempty"`
	Meaning    string      `json:"meaning"`
	Note       string      `json:"note,omitempty"`
	Directions []Direction `json:"directions"`

	descriptor paramdefs.Descriptor
	// secondsPlain marks a keyboard time: the catalog shows milliseconds, and
	// a plain number in a score means seconds.
	secondsPlain bool
}

// Descriptor is the range descriptor the validators use for the control's
// source values.
func (c Control) Descriptor() paramdefs.Descriptor { return c.descriptor }

// Catalog is the whole control catalog.
type Catalog struct {
	Owners   []Owner
	Controls []Control
}

var (
	once    sync.Once
	catalog Catalog
)

// All returns the catalog. Callers must not modify it.
func All() Catalog {
	once.Do(func() { catalog = build() })
	return catalog
}

// Owner finds an owner by name.
func (c Catalog) Owner(name string) (Owner, bool) {
	for _, owner := range c.Owners {
		if owner.Name == name {
			return owner, true
		}
	}
	return Owner{}, false
}

// Control finds a control by its ID, owner.name.
func (c Catalog) Control(id string) (Control, bool) {
	for _, control := range c.Controls {
		if control.ID == id {
			return control, true
		}
	}
	return Control{}, false
}

// Of lists an owner's controls in catalog order.
func (c Catalog) Of(owner string) []Control {
	var out []Control
	for _, control := range c.Controls {
		if control.Owner == owner {
			out = append(out, control)
		}
	}
	return out
}

// Filter lists the controls whose ID is filter or starts with it, and the
// owners those controls belong to. An empty filter selects everything.
func (c Catalog) Filter(filter string) Catalog {
	if filter == "" {
		return c
	}
	var out Catalog
	owners := map[string]bool{}
	for _, control := range c.Controls {
		if control.ID == filter || strings.HasPrefix(control.ID, filter) || control.Owner == filter {
			out.Controls = append(out.Controls, control)
			owners[control.Owner] = true
		}
	}
	for _, owner := range c.Owners {
		if owners[owner.Name] {
			out.Owners = append(out.Owners, owner)
		}
	}
	return out
}

// Scopes are the owner kinds a Query's Scope takes, in catalog order.
var Scopes = []string{KindVoice, KindPatch, KindLibraryPatch, KindAuthored, KindSampler, KindKitLane, KindEffect, KindMix, KindBus, KindMaster, KindGlobal}

// sourceKinds are the owner kinds that make sound: what a Query's Voice
// names.
var sourceKinds = map[string]bool{KindVoice: true, KindPatch: true, KindLibraryPatch: true, KindAuthored: true, KindSampler: true, KindKitLane: true}

// Query selects controls. Voice names a sound source (an owner of a kind in
// sourceKinds, such as fm_bass, acid or round-bass) or a family of them (fm,
// ep, organ, analog, clav, strings, modal, graph); Scope names an owner kind
// (one of Scopes); Control is a control ID, the start of one, or an owner
// name, as Filter takes it. Empty fields select everything; the fields that
// are set must all hold.
type Query struct {
	Voice   string `json:"voice,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Control string `json:"control,omitempty"`
}

// QueryError is a Query that selects nothing, with a stable code: usage for
// a scope that is not an owner kind, no_match otherwise.
type QueryError struct {
	Code    string
	Message string
}

func (e *QueryError) Error() string { return e.Message }

// Families lists the families of the sound sources, sorted.
func (c Catalog) Families() []string {
	seen := map[string]bool{}
	for _, owner := range c.Owners {
		if owner.Family != "" && sourceKinds[owner.Kind] {
			seen[owner.Family] = true
		}
	}
	return sortedKeys(seen)
}

// Select returns the controls q selects and the owners they belong to.
func (c Catalog) Select(q Query) (Catalog, error) {
	if q.Scope != "" {
		known := false
		for _, scope := range Scopes {
			known = known || scope == q.Scope
		}
		if !known {
			return Catalog{}, &QueryError{"usage", fmt.Sprintf("scope %q is not an owner kind; use one of %s", q.Scope, strings.Join(Scopes, ", "))}
		}
	}
	if q.Voice != "" {
		matched := false
		for _, owner := range c.Owners {
			if sourceKinds[owner.Kind] && (owner.Name == q.Voice || owner.Family == q.Voice) {
				matched = true
				break
			}
		}
		if !matched {
			if owner, ok := c.Owner(q.Voice); ok {
				return Catalog{}, &QueryError{"no_match", fmt.Sprintf("%s is a %s, not a voice; select it with scope %s or control %s", q.Voice, owner.Kind, owner.Kind, q.Voice)}
			}
			return Catalog{}, &QueryError{"no_match", fmt.Sprintf("no voice or family %q; cicada params --json --index lists the voices and families", q.Voice)}
		}
	}
	selected := c.Filter(q.Control)
	kinds := map[string]Owner{}
	for _, owner := range c.Owners {
		kinds[owner.Name] = owner
	}
	var out Catalog
	keep := map[string]bool{}
	for _, control := range selected.Controls {
		owner := kinds[control.Owner]
		if q.Scope != "" && owner.Kind != q.Scope {
			continue
		}
		if q.Voice != "" && (!sourceKinds[owner.Kind] || owner.Name != q.Voice && owner.Family != q.Voice) {
			continue
		}
		out.Controls = append(out.Controls, control)
		keep[control.Owner] = true
	}
	for _, owner := range c.Owners {
		if keep[owner.Name] {
			out.Owners = append(out.Owners, owner)
		}
	}
	if len(out.Controls) == 0 {
		return Catalog{}, &QueryError{"no_match", fmt.Sprintf("no control matches %s; cicada params --json --index lists the owners", q.describe())}
	}
	return out, nil
}

func (q Query) describe() string {
	var parts []string
	if q.Voice != "" {
		parts = append(parts, "voice "+q.Voice)
	}
	if q.Scope != "" {
		parts = append(parts, "scope "+q.Scope)
	}
	if q.Control != "" {
		parts = append(parts, "control "+q.Control)
	}
	return strings.Join(parts, " and ")
}

func build() Catalog {
	var b builder
	b.mixer()
	b.voices()
	b.keyboardPatches()
	b.libraryPatches()
	b.authored()
	b.sampler()
	b.kitLanes()
	b.effects()
	b.applyDirections()
	counts := map[string]int{}
	for _, control := range b.controls {
		counts[control.Owner]++
	}
	for i := range b.owners {
		b.owners[i].Controls = counts[b.owners[i].Name]
	}
	return Catalog{Owners: b.owners, Controls: b.controls}
}

type builder struct {
	owners   []Owner
	controls []Control
}

func (b *builder) owner(o Owner) { b.owners = append(b.owners, o) }

func (b *builder) add(c Control) {
	c.ID = c.Owner + "." + c.Name
	c.Example = Example(c)
	if c.Directions == nil {
		c.Directions = []Direction{}
	}
	if c.SetIn == nil {
		c.SetIn = []string{}
	}
	b.controls = append(b.controls, c)
}

// fromDescriptor fills a control's type, unit, range, default and words from
// a registry-shaped descriptor.
func fromDescriptor(owner, name string, d paramdefs.Descriptor) Control {
	c := Control{Owner: owner, Name: name, Unit: displayUnit(d.Unit), descriptor: d}
	if d.Path != "" && d.Path != name {
		c.Path = d.Path
	}
	c.Values = append(c.Values, d.Values...)
	switch {
	case d.Curve == "enum":
		c.Type = "enum"
		c.Default = wordAt(d.Values, d.Default)
		if numericWords(d.Values) {
			c.Type, c.Default = "integer", d.Default
			c.Min, c.Max = number(d.Min), number(d.Max)
			c.Values = nil
		}
	case d.Curve == "toggle":
		c.Type = "toggle"
		if len(c.Values) == 0 {
			c.Values = []string{"off", "on"}
		}
		c.Default = wordAt(c.Values, d.Default)
	default:
		c.Type = "number"
		if d.Type == "integer" || d.Curve == "integer" {
			c.Type = "integer"
		}
		c.Min, c.Max, c.Default = number(d.Min), number(d.Max), d.Default
	}
	if d.Off {
		c.Values = append(c.Values, "off")
	}
	for _, v := range d.Isolated {
		c.Values = append(c.Values, strconv.FormatFloat(v, 'f', -1, 64))
		c.Note = strconv.FormatFloat(v, 'f', -1, 64) + " is accepted outside the range to " + d.IsolatedNote
	}
	return c
}

// setIn says where a registry row can be set besides its declaration.
func setIn(d paramdefs.Descriptor, declared ...string) []string {
	places := append([]string(nil), declared...)
	if d.Live {
		places = append(places, InScene)
	}
	if d.Live && d.Automatable && d.Curve != "toggle" && d.Curve != "enum" {
		places = append(places, InAutomation, InModulation)
	}
	return places
}

func number(v float64) *float64 { return &v }

func wordAt(words []string, index float64) any {
	if i := int(index); float64(i) == index && i >= 0 && i < len(words) {
		return words[i]
	}
	return index
}

func numericWords(words []string) bool {
	if len(words) == 0 {
		return false
	}
	for _, word := range words {
		for _, r := range word {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// displayUnit names a registry unit the way the catalog prints it.
func displayUnit(unit string) string {
	switch strings.ToLower(unit) {
	case "", "unit":
		return ""
	case "cent", "cents", "ct":
		return "cent"
	case "semitone":
		return "semitone"
	case "hz":
		return "Hz"
	case "ms":
		return "ms"
	case "db":
		return "dB"
	case "dbtp":
		return "dBTP"
	case "bpm":
		return "BPM"
	}
	return unit
}

// registryRow returns the row with the given ID.
func registryRow(id string) (paramdefs.Descriptor, bool) { return paramdefs.Lookup(id) }

// trackRowFor finds the live registry row a voice family takes for a
// control, as the compiler's path resolution does.
func trackRowFor(family, name string) (paramdefs.Descriptor, bool) {
	for _, d := range paramdefs.Registry {
		if d.Scope == "track" && d.Path == name && !strings.HasPrefix(d.ID, "mix.") && paramdefs.HasVoice(d, family) {
			return d, true
		}
	}
	return paramdefs.Descriptor{}, false
}

// Counts summarizes the catalog for a report.
type Counts struct {
	Owners     int            `json:"owners"`
	Controls   int            `json:"controls"`
	Registry   int            `json:"in_registry"`
	ByKind     map[string]int `json:"by_kind"`
	Directions DirectionCount `json:"directions"`
}

// DirectionCount counts controls by the best basis of their directions.
type DirectionCount struct {
	Stated   int `json:"stated"`
	Measured int `json:"measured"`
	Unknown  int `json:"unknown"`
}

// Count summarizes c.
func (c Catalog) Count() Counts {
	counts := Counts{Owners: len(c.Owners), Controls: len(c.Controls), ByKind: map[string]int{}}
	kinds := map[string]string{}
	for _, owner := range c.Owners {
		kinds[owner.Name] = owner.Kind
	}
	for _, control := range c.Controls {
		counts.ByKind[kinds[control.Owner]]++
		if control.RegistryID != "" {
			if _, ok := registryRow(control.RegistryID); ok {
				counts.Registry++
			}
		}
		switch DirectionBasis(control) {
		case BasisMeasured:
			counts.Directions.Measured++
		case BasisStated:
			counts.Directions.Stated++
		default:
			counts.Directions.Unknown++
		}
	}
	return counts
}

// sortedKeys returns m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
