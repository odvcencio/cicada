package edit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
)

// ReasonError is an intent refused for a reason a program can act on: Reason
// is a stable snake_case code, Field the JSON field at fault (empty when the
// whole intent is), and Uses lists what still refers to the entity when that
// is the reason. Error returns Message.
type ReasonError struct {
	Reason  string
	Field   string
	Message string
	Uses    []string
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
	return "fx " + name + " " + kind + " { " + strings.Join(body, " ") + " }", nil
}

// RemoveEffect deletes an effect: its declaration, the comment lines above
// it, and the routes that carry it (its place in track, music and master
// insert chains, and track sends to it). It is refused while automation, a
// scene setting, a modulation route, a MIDI mapping or a live macro still
// names the effect; the refusal lists them.
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
	if uses := effectUses(score, in.Name); len(uses) > 0 {
		return &ReasonError{Reason: "effect_in_use", Field: "name", Uses: uses, Message: fmt.Sprintf("effect %s is still used by %s; remove those first", in.Name, strings.Join(uses, ", "))}
	}
	updated, err := withoutEffect(ctx.Source, in.Name)
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel("Mix: effect " + in.Name + " removed")
	return nil
}

// effectUses names what refers to an effect beyond its routes.
func effectUses(score *notation.Score, name string) []string {
	prefix := name + "."
	var uses []string
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
				literal := "none"
				if len(rest) > 0 {
					literal = strings.Join(rest, " -> ")
				}
				if source, _, _, _, err = editOneMixerSetting(source, walker, decl, kind, "insert", literal); err != nil {
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
		return nil, refuse("effect_in_other_file", "name", "effect %s is declared in another file of the project; edit that file", name)
	}
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
	if len(bytes.TrimSpace(source[lineStart:start])) != 0 || len(bytes.TrimSpace(source[end:lineEnd])) != 0 {
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
		return "", fmt.Errorf("unknown %s setting %q", kind, field)
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
	return "", fmt.Errorf("unknown convolution setting %q", field)
}

// onMaster reports whether an effect is in the master insert chain.
func onMaster(score *notation.Score, name string) bool {
	return containsString(insertChain(paramValue(score.Master, "insert")), name)
}
