package edit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
)

// ReasonError is an intent refused for a reason a program can act on: Reason
// is a stable snake_case code, Field the JSON field at fault (empty when the
// whole intent is), and Uses lists what still refers to the entity when that
// is the reason. File names the other source file that holds what the edit
// would have to change, and Range the bounds a value broke. Error returns
// Message.
type ReasonError struct {
	Reason  string
	Field   string
	Message string
	Uses    []string
	File    string
	Range   *ValueRange
}

// ValueRange is a parameter's accepted range and the value given outside it.
type ValueRange struct {
	Parameter string  `json:"parameter"`
	Value     string  `json:"value"`
	Min       float64 `json:"min"`
	Max       float64 `json:"max"`
	Unit      string  `json:"unit,omitempty"`
}

func (e *ReasonError) Error() string { return e.Message }

func refuse(reason, field, format string, args ...any) error {
	return &ReasonError{Reason: reason, Field: field, Message: fmt.Sprintf(format, args...)}
}

// Effect routes, as the language allows them (notation/validate.go and
// project/validate.go): where each kind may be inserted, and which kinds must
// be. A kind with no insert route is a send return.
var (
	effectInsertRoutes = map[string][]string{
		"drive": {"track"}, "eq": {"track", "master"}, "comp": {"track", "music", "master"},
		"deesser": {"track"}, "ampcab": {"track"}, "chorus": {"track"},
		"transient": {"master"}, "width": {"master"}, "limiter": {"master"}, "convolution": {"master"},
	}
	effectIsReturn = map[string]bool{"delay": true, "reverb": true, "tape": true}
	// These kinds work only in an insert chain; validation refuses one that
	// no chain holds.
	effectNeedsInsert = map[string]bool{"comp": true, "transient": true, "width": true, "limiter": true, "convolution": true}
)

// Insert chain limits of the language.
const (
	maxTrackInserts  = 8
	maxMasterInserts = 16
)

// effectRouteNames describes the routes a kind takes, for messages.
func effectRouteNames(kind string) string {
	var names []string
	for _, route := range effectInsertRoutes[kind] {
		switch route {
		case "track":
			names = append(names, "a track")
		case "music":
			names = append(names, "bus music")
		case "master":
			names = append(names, "master")
		}
	}
	switch len(names) {
	case 0:
		return "nothing"
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// insertChain reads the effect names of an insert setting's value.
func insertChain(value string) []string {
	var names []string
	for _, part := range strings.Fields(value) {
		if part != "->" && part != "none" {
			names = append(names, part)
		}
	}
	return names
}

func paramValue(params []notation.Param, name string) string {
	for _, param := range params {
		if param.Name == name {
			return param.Value
		}
	}
	return ""
}

// checkNewEffect refuses an effect the language would not accept where the
// request puts it, naming the real reason.
func checkNewEffect(score *notation.Score, name, kind, target string) (route string, chain []string, err error) {
	if effectIsReturn[kind] {
		for _, effect := range score.Effects {
			sameReturn := effect.Kind == kind || kind != "reverb" && (effect.Kind == "delay" || effect.Kind == "tape")
			if sameReturn {
				return "", nil, refuse("effect_single_instance", "effectkind", "the score already has the %s return %s; a score has one delay or tape return and one reverb return", effect.Kind, effect.Name)
			}
		}
		if target != "" {
			return "", nil, refuse("effect_is_return", "inserton", "a %s is a send return, not an insert: add it without inserton, then send a track to it with setparam param:<track>.send.%s", kind, name)
		}
		return "", nil, nil
	}
	// Without inserton the effect is declared only. A comp or a master stage
	// must still end up in an insert chain: a later setparam of the same
	// envelope can put it there (Studio's vocal chain does), and validation
	// refuses a batch that leaves it out.
	if target == "" {
		return "", nil, nil
	}
	switch target {
	case "music", "master":
		route = target
	default:
		for _, track := range score.Tracks {
			if track.Name == target {
				route = "track"
			}
		}
	}
	if target == "sfx" {
		return "", nil, refuse("effect_route", "inserton", "bus sfx takes no inserts; a %s can be inserted on %s", kind, effectRouteNames(kind))
	}
	if route == "" {
		return "", nil, refuse("unknown_insert_target", "inserton", "inserton %q is not a track, music or master", target)
	}
	if !containsString(effectInsertRoutes[kind], route) {
		where := "master"
		switch route {
		case "track":
			where = "track " + target
		case "music":
			where = "bus music"
		}
		return "", nil, refuse("effect_route", "inserton", "a %s can be inserted on %s, not on %s", kind, effectRouteNames(kind), where)
	}
	switch route {
	case "music":
		for _, bus := range score.Buses {
			if bus.Name == "music" {
				if current := insertChain(paramValue(bus.Params, "insert")); len(current) > 0 {
					return "", nil, refuse("music_insert_taken", "inserton", "bus music already has the insert %s; it takes one compressor", current[0])
				}
			}
		}
	case "master":
		chain = insertChain(paramValue(score.Master, "insert"))
		if len(chain) >= maxMasterInserts {
			return "", nil, refuse("insert_chain_full", "inserton", "master already has %d inserts, the most it takes", maxMasterInserts)
		}
	case "track":
		for _, track := range score.Tracks {
			if track.Name == target {
				chain = insertChain(paramValue(track.Params, "insert"))
			}
		}
		if len(chain) >= maxTrackInserts {
			return "", nil, refuse("insert_chain_full", "inserton", "track %s already has %d inserts, the most it takes", target, maxTrackInserts)
		}
	}
	return route, chain, nil
}

// effectDeclaration writes fx <name> <kind> { ... } with the given settings
// as canonical literals, in the registry's order.
func effectDeclaration(name, kind string, settings map[string]json.RawMessage, master bool) (string, error) {
	fields := make([]string, 0, len(settings))
	for field := range settings {
		fields = append(fields, field)
	}
	sort.Slice(fields, func(i, j int) bool {
		a, b := descriptorOrder(kind, fields[i]), descriptorOrder(kind, fields[j])
		if a != b {
			return a < b
		}
		return fields[i] < fields[j]
	})
	var body []string
	for _, field := range fields {
		literal, err := effectSettingLiteral(kind, field, settings[field], master)
		if err != nil {
			return "", &FieldError{Field: "settings." + field, Err: err}
		}
		body = append(body, field+" = "+literal)
	}
	if len(body) == 0 {
		return "fx " + name + " " + kind + " {}", nil
	}
	// The layout cicada fmt gives a block with settings; the caller turns
	// "\n" into the file's newline.
	return "fx " + name + " " + kind + " {\n  " + strings.Join(body, "\n  ") + "\n}", nil
}

// RemoveEffect deletes an effect: its declaration, the comment lines above
// it and a comment after it on its line, and the routes that carry it (its
// place in track, music and master insert chains, and track sends to it). An
// insert setting the removal empties is removed too. It is refused with
// effect_in_use while automation, a scene setting, a modulation route, a
// MIDI mapping, a live macro, a preset that targets it, or an edition-1
// send_a or send_b level still names the effect; uses lists them. The edit
// changes only the entry file: an effect declared in another file is refused
// with declared_in_other_file, and one routed from another file with
// route_in_other_file; file names that file. Also unknown_effect and
// score_invalid.
//
// Example: {"kind":"removeeffect","name":"grit"}
type RemoveEffect struct {
	Name string `json:"name" cicada:"required"` // the effect's name
}

func (RemoveEffect) Kind() string { return "removeeffect" }

func init() {
	Register("removeeffect", func() Intent { return &RemoveEffect{} })
	Handle("removeeffect", removeEffect)
}

func removeEffect(ctx *Context, intent Intent) error {
	in := intent.(*RemoveEffect)
	if ctx.Options.MixerGuard != nil {
		if err := ctx.Options.MixerGuard(); err != nil {
			return err
		}
	}
	score, ds, err := ctx.ParseProject()
	if err != nil {
		return err
	}
	if score == nil || hasErrors(ds) {
		return refuse("score_invalid", "", "score must validate before removing an effect")
	}
	effect := FindNotationEffect(score, in.Name)
	if effect == nil {
		return refuse("unknown_effect", "name", "unknown effect %q", in.Name)
	}
	if file := effect.Position.File; file != "" && file != ctx.Options.Path {
		return &ReasonError{Reason: "declared_in_other_file", Field: "name", File: file, Message: fmt.Sprintf("effect %s is declared in %s, another file of the project; this edit changes only %s", in.Name, file, entryName(ctx.Options.Path))}
	}
	if uses := effectUses(score, effect); len(uses) > 0 {
		return &ReasonError{Reason: "effect_in_use", Field: "name", Uses: uses, Message: fmt.Sprintf("effect %s is still used by %s; remove those first", in.Name, strings.Join(uses, ", "))}
	}
	if file, uses := effectRoutesElsewhere(score, in.Name, ctx.Options.Path); len(uses) > 0 {
		return &ReasonError{Reason: "route_in_other_file", Field: "name", File: file, Uses: uses, Message: fmt.Sprintf("effect %s is routed from %s, another file of the project (%s); this edit changes only %s", in.Name, file, strings.Join(uses, ", "), entryName(ctx.Options.Path))}
	}
	updated, err := withoutEffect(ctx.Source, in.Name)
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel("Mix: effect " + in.Name + " removed")
	return nil
}

// entryName is the entry file's name for messages.
func entryName(path string) string {
	if path == "" {
		return "the score"
	}
	return path
}

// effectRoutesElsewhere lists the insert chains and sends in other source
// files than entry that route an effect, and the first such file. The
// removal edits only the entry.
func effectRoutesElsewhere(score *notation.Score, name, entry string) (string, []string) {
	var file string
	var uses []string
	add := func(position notation.Position, what string) {
		if position.File == "" || position.File == entry {
			return
		}
		if file == "" {
			file = position.File
		}
		uses = append(uses, fmt.Sprintf("%s (%s line %d)", what, position.File, position.Line))
	}
	for _, track := range score.Tracks {
		for _, param := range track.Params {
			if param.Name == "insert" && containsString(insertChain(param.Value), name) {
				add(param.Position, "track "+track.Name+" insert chain")
			}
			if param.Name == "send" && param.Target == name {
				add(param.Position, "track "+track.Name+" send")
			}
		}
	}
	for _, bus := range score.Buses {
		for _, param := range bus.Params {
			if param.Name == "insert" && containsString(insertChain(param.Value), name) {
				add(param.Position, "bus "+bus.Name+" insert chain")
			}
		}
	}
	for _, param := range score.Master {
		if param.Name == "insert" && containsString(insertChain(param.Value), name) {
			add(param.Position, "master insert chain")
		}
	}
	return file, uses
}

// effectUses names what refers to an effect beyond the insert chains and
// named sends the removal takes with it: automation, scene settings,
// modulation routes, MIDI mappings, live macros, presets that target it, and
// an edition-1 return's send_a (delay or tape) or send_b (reverb) levels.
func effectUses(score *notation.Score, effect *notation.Effect) []string {
	name := effect.Name
	prefix := name + "."
	var uses []string
	for _, preset := range score.Presets {
		if preset.Target == name {
			uses = append(uses, fmt.Sprintf("preset %s (line %d)", preset.Name, preset.Position.Line))
		}
	}
	legacy := map[string]string{"delay": "send_a", "tape": "send_a", "reverb": "send_b"}[effect.Kind]
	for _, track := range score.Tracks {
		for _, param := range track.Params {
			if legacy != "" && param.Name == legacy {
				uses = append(uses, fmt.Sprintf("track %s %s (line %d)", track.Name, legacy, param.Position.Line))
			}
		}
	}
	for _, lane := range score.Automation {
		if strings.HasPrefix(lane.Path, prefix) {
			uses = append(uses, fmt.Sprintf("automation %s (line %d)", lane.Path, lane.Position.Line))
		}
	}
	for _, scene := range score.Scenes {
		for _, setting := range scene.Settings {
			if strings.HasPrefix(setting.Path, prefix) {
				uses = append(uses, fmt.Sprintf("scene %s setting %s (line %d)", scene.Name, setting.Path, setting.Position.Line))
			}
		}
	}
	for _, route := range score.Routes {
		if strings.HasPrefix(route.Target, prefix) {
			uses = append(uses, fmt.Sprintf("modulation route %s -> %s (line %d)", route.Source, route.Target, route.Position.Line))
		}
	}
	if score.MIDI != nil {
		for _, mapping := range score.MIDI.Mappings {
			if strings.HasPrefix(mapping.Target, prefix) {
				uses = append(uses, fmt.Sprintf("MIDI mapping %s (line %d)", mapping.Target, mapping.Position.Line))
			}
		}
	}
	if score.Live != nil {
		for _, macro := range score.Live.Macros {
			if strings.HasPrefix(macro.Target, prefix) {
				uses = append(uses, fmt.Sprintf("live macro %s (line %d)", macro.Name, macro.Position.Line))
			}
		}
	}
	return uses
}

// withoutEffect removes an effect's routes and declaration from source.
func withoutEffect(source []byte, name string) ([]byte, error) {
	for {
		score, _ := notation.Parse(source)
		if score == nil {
			return nil, refuse("score_invalid", "", "score must parse before removing an effect")
		}
		root, walker, err := notation.ParseTree(source)
		if err != nil {
			return nil, err
		}
		edited := false
		for i := 0; i < root.NamedChildCount() && !edited; i++ {
			decl := root.NamedChild(i)
			kind := walker.Type(decl)
			var params []notation.Param
			switch kind {
			case "track_decl":
				params = TrackParams(score, walker.Text(walker.Field(decl, "name")))
			case "bus_decl":
				params = BusParams(score, walker.Text(walker.Field(decl, "name")))
			case "master_decl":
				params = score.Master
			default:
				continue
			}
			if chain := insertChain(paramValue(params, "insert")); containsString(chain, name) {
				rest := make([]string, 0, len(chain))
				for _, other := range chain {
					if other != name {
						rest = append(rest, other)
					}
				}
				// An emptied chain loses its setting: no insert setting and
				// "insert = none" mean the same, and a block that was already
				// expanded before the first insert gets back its bytes.
				if len(rest) == 0 {
					source = removeBlockParam(source, walker, decl, "insert")
				} else if source, _, _, _, err = editOneMixerSetting(source, walker, decl, kind, "insert", strings.Join(rest, " -> ")); err != nil {
					return nil, err
				}
				edited = true
				continue
			}
			for _, param := range params {
				if kind == "track_decl" && param.Name == "send" && param.Target == name {
					if source, _, _, _, err = removeMixerSend(source, walker, decl, name); err != nil {
						return nil, err
					}
					edited = true
					break
				}
			}
		}
		if edited {
			continue
		}
		for i := 0; i < root.NamedChildCount(); i++ {
			decl := root.NamedChild(i)
			if walker.Type(decl) == "fx_decl" && walker.Text(walker.Field(decl, "name")) == name {
				return removeTopLevelDeclaration(source, root, decl), nil
			}
		}
		return nil, refuse("declared_in_other_file", "name", "effect %s is declared in another file of the project; this edit changes only the entry", name)
	}
}

// removeBlockParam removes one setting from a declaration's block. A setting
// alone on its line takes the line; one that shares it takes the spaces
// around it, and a block left with nothing between its braces becomes "{}".
func removeBlockParam(source []byte, walker *walk.Walker, decl *gts.Node, field string) []byte {
	for i := 0; i < decl.NamedChildCount(); i++ {
		child := decl.NamedChild(i)
		param := child
		if walker.Type(child) == "mix_setting" && child.NamedChildCount() > 0 {
			param = child.NamedChild(0)
		}
		if walker.Type(param) != "param_decl" || walker.Text(walker.Field(param, "name")) != field {
			continue
		}
		start, end := int(child.StartByte()), int(child.EndByte())
		lineStart := bytes.LastIndexByte(source[:start], '\n') + 1
		lineEnd := end
		for lineEnd < len(source) && source[lineEnd] != '\n' {
			lineEnd++
		}
		if len(bytes.TrimSpace(source[lineStart:start])) == 0 && len(bytes.TrimSpace(source[end:lineEnd])) == 0 {
			if lineEnd < len(source) {
				lineEnd++
			}
			return replaceMixerSpan(source, lineStart, lineEnd, nil)
		}
		before, after := start, end
		for before > lineStart && (source[before-1] == ' ' || source[before-1] == '\t') {
			before--
		}
		for after < lineEnd && (source[after] == ' ' || source[after] == '\t') {
			after++
		}
		switch {
		case before > 0 && source[before-1] == '{' && after < len(source) && source[after] == '}':
			return replaceMixerSpan(source, before, after, nil)
		case after < lineEnd && source[after] != '\r':
			return replaceMixerSpan(source, start, after, nil)
		default:
			return replaceMixerSpan(source, before, end, nil)
		}
	}
	return source
}

// removeTopLevelDeclaration removes a declaration with the comment lines
// attached above it. A declaration alone on its lines takes its lines with
// it, and one blank line of the two around it, so no gap opens.
func removeTopLevelDeclaration(source []byte, root, decl *gts.Node) []byte {
	start, end := int(decl.StartByte()), int(decl.EndByte())
	lineStart := bytes.LastIndexByte(source[:start], '\n') + 1
	lineEnd := end
	for lineEnd < len(source) && source[lineEnd] != '\n' {
		lineEnd++
	}
	// A comment after the declaration on its line goes with it.
	rest := bytes.TrimSpace(source[end:lineEnd])
	if len(bytes.TrimSpace(source[lineStart:start])) != 0 || len(rest) != 0 && !bytes.HasPrefix(rest, []byte("//")) {
		return replaceMixerSpan(source, start, end, nil)
	}
	start = attachedCommentStart(source, start, 0)
	end = lineEnd
	if end < len(source) {
		end++
	}
	blankBefore := start >= 2 && source[start-1] == '\n' && (source[start-2] == '\n' || source[start-2] == '\r' && start >= 3 && source[start-3] == '\n')
	if blankBefore || start == 0 {
		next := end
		if next < len(source) && source[next] == '\r' {
			next++
		}
		if next < len(source) && source[next] == '\n' {
			end = next + 1
		}
	}
	return replaceMixerSpan(source, start, end, nil)
}

// effectSettingLiteral writes one effect setting as source text, checked
// against the kind's descriptor on the route the effect takes.
func effectSettingLiteral(kind, field string, raw json.RawMessage, master bool) (string, error) {
	if kind == "convolution" {
		return convolutionLiteral(field, raw)
	}
	descriptor, ok := effectDescriptor(kind, field, master)
	if !ok {
		return "", refuse("unknown_setting", "", "unknown %s setting %q; the settings are %s", kind, field, strings.Join(effectSettingNames(kind, master), ", "))
	}
	return CanonicalMixerLiteral(descriptor, raw)
}

// effectDescriptor finds the descriptor of an effect setting: the registry
// row, the host-only rows of the master stages, or the master EQ's limits.
func effectDescriptor(kind, field string, master bool) (paramdefs.Descriptor, bool) {
	if master {
		if descriptor, ok := paramdefs.DeviceParam(kind, field, true); ok {
			return descriptor, true
		}
	}
	if descriptor, ok := mixerFXDescriptor(kind, field); ok {
		return descriptor, true
	}
	return paramdefs.DeviceParam(kind, field, false)
}

// checkConvolutionAsset refuses a convolution without its impulse asset, or
// with an asset the score does not declare.
func checkConvolutionAsset(score *notation.Score, raw json.RawMessage) error {
	if len(raw) == 0 {
		return refuse("convolution_needs_asset", "settings.asset", "a convolution needs its impulse: give settings asset, the name of an asset the score declares")
	}
	name, err := jsonStringOrNumber(raw)
	if err != nil {
		return refuse("invalid_value", "settings.asset", "convolution asset must be an asset's name")
	}
	var names []string
	for _, asset := range score.Assets {
		if asset.Name == name {
			return nil
		}
		names = append(names, asset.Name)
	}
	known := "the score declares none"
	if len(names) > 0 {
		known = "the assets are " + strings.Join(names, ", ")
	}
	return refuse("unknown_asset", "settings.asset", "%q is not an asset of the score; %s", name, known)
}

// effectSettingNames lists the settings of a kind, for messages: the
// registry and host-only rows, and ampcab's table.
func effectSettingNames(kind string, master bool) []string {
	if kind == "ampcab" {
		return []string{"drive", "tone", "output", "cabinet", "bypass"}
	}
	var names []string
	for _, rows := range [][]paramdefs.Descriptor{paramdefs.Registry, paramdefs.HostOnly} {
		for _, d := range rows {
			field, ok := strings.CutPrefix(d.ID, "fx."+kind+".")
			if !ok && master {
				field, ok = strings.CutPrefix(d.ID, "fx.master."+kind+".")
			}
			if ok && !strings.Contains(field, ".") && !containsString(names, field) {
				if _, found := effectDescriptor(kind, field, master); found {
					names = append(names, field)
				}
			}
		}
	}
	return names
}

// convolutionLiteral writes a convolution setting: the impulse asset, the mix,
// or the partition size (docs/audio/mix-chain.md).
func convolutionLiteral(field string, raw json.RawMessage) (string, error) {
	value, err := jsonStringOrNumber(raw)
	if err != nil {
		return "", fmt.Errorf("convolution %s needs a value", field)
	}
	switch field {
	case "asset":
		if !ValidMixerIdentifier(value) {
			return "", fmt.Errorf("convolution asset must name a declared asset")
		}
		return value, nil
	case "mix":
		number, _, err := parseMixerUnitValue(value, "")
		if err != nil || number < 0 || number > 1 {
			return "", fmt.Errorf("convolution mix must be 0 to 1")
		}
		return formatMixerNumber(number), nil
	case "partition":
		frames, err := strconv.Atoi(strings.TrimSuffix(value, "frames"))
		if err != nil || frames < 64 || frames > 2048 || frames&(frames-1) != 0 {
			return "", fmt.Errorf("convolution partition must be a power of two from 64 to 2048 frames")
		}
		return strconv.Itoa(frames) + "frames", nil
	}
	return "", refuse("unknown_setting", "", "unknown convolution setting %q; the settings are asset, mix, partition", field)
}

// placeInChain returns the insert chain with name placed in it: at the end,
// or in front of before or behind after, an insert already in the chain.
func placeInChain(chain []string, name string, request mixerFXValue) ([]string, error) {
	anchor, field, offset := request.Before, "before", 0
	if request.After != "" {
		anchor, field, offset = request.After, "after", 1
	}
	if anchor == "" {
		return append(append([]string(nil), chain...), name), nil
	}
	if request.Before != "" && request.After != "" {
		return nil, refuse("insert_position_conflict", "after", "give before or after, not both")
	}
	if request.InsertOn == "" {
		return nil, refuse("insert_position_without_target", field, "%s places the effect in an insert chain: give inserton too", field)
	}
	for i, insert := range chain {
		if insert == anchor {
			placed := append(append(append([]string(nil), chain[:i+offset]...), name), chain[i+offset:]...)
			return placed, nil
		}
	}
	current := "none"
	if len(chain) > 0 {
		current = strings.Join(chain, " -> ")
	}
	return nil, refuse("unknown_insert_anchor", field, "%s is not in the insert chain of %s (%s)", anchor, request.InsertOn, current)
}

// checkEntryOwnsTarget refuses an insert target that another source file of
// the project declares: the edit writes only the entry.
func checkEntryOwnsTarget(score *notation.Score, root *gts.Node, walker *walk.Walker, route, target string) error {
	if route == "" {
		return nil
	}
	want, owner := "track_decl", target
	switch route {
	case "master":
		want = "master_decl"
	case "music":
		want = "bus_decl"
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if walker.Type(node) == want && (want == "master_decl" || walker.Text(walker.Field(node, "name")) == owner) {
			return nil
		}
	}
	file := ""
	switch route {
	case "master":
		for _, param := range score.Master {
			if param.Position.File != "" {
				file = param.Position.File
				break
			}
		}
		if file == "" && !score.HasMaster {
			return nil
		}
	case "music":
		for _, bus := range score.Buses {
			if bus.Name == "music" {
				file = bus.Position.File
			}
		}
		if file == "" {
			return nil
		}
	default:
		for _, track := range score.Tracks {
			if track.Name == target {
				file = track.Position.File
			}
		}
	}
	where := "another file of the project"
	if file != "" {
		where = file + ", " + where
	}
	return &ReasonError{Reason: "declared_in_other_file", Field: "inserton", File: file, Message: fmt.Sprintf("%s is declared in %s; this edit changes only the entry, so insert it there", target, where)}
}

// effectInsertRoute names the chain an effect is inserted on: master, music
// (the music bus), track, or "" when no chain holds it.
func effectInsertRoute(score *notation.Score, name string) string {
	if onMaster(score, name) {
		return "master"
	}
	if containsString(insertChain(paramValue(BusParams(score, "music"), "insert")), name) {
		return "music"
	}
	for _, track := range score.Tracks {
		if containsString(insertChain(paramValue(track.Params, "insert")), name) {
			return "track"
		}
	}
	return ""
}

// checkSidechain refuses a compressor sidechain the language does not take:
// only the comp on bus music listens to another signal, a track of the score,
// music or sfx; a comp on a track or on master uses its own input.
func checkSidechain(score *notation.Score, route, value, field string) error {
	value = strings.Trim(value, `"`)
	if route == "track" || route == "master" {
		where := "on master"
		if route == "track" {
			where = "on a track"
		}
		return refuse("sidechain_unavailable", field, "a compressor inserted %s uses its own input and takes no sidechain; only the compressor on bus music takes one", where)
	}
	if value == "music" || value == "sfx" {
		return nil
	}
	for _, track := range score.Tracks {
		if track.Name == value {
			return nil
		}
	}
	return refuse("unknown_sidechain_track", field, "sidechain %q is not a track of the score, music or sfx", value)
}

// onMaster reports whether an effect is in the master insert chain.
func onMaster(score *notation.Score, name string) bool {
	return containsString(insertChain(paramValue(score.Master, "insert")), name)
}
