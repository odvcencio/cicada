package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/command"
	"m31labs.dev/gosx/controller"
	"m31labs.dev/gosx/engine"
	"m31labs.dev/gosx/server"
	"m31labs.dev/gosx/session"
)

// Shell regions start at these sizes, in CSS pixels, until a stored layout loads.
const (
	shellBrowserWidth = 232
	shellCodeWidth    = 360
	shellDockHeight   = 240
)

var (
	shellTabs   = []string{"arrange", "session", "mixer"}
	shellDocks  = []string{"clip", "device", "automation"}
	shellPhones = []string{"center", "browser", "detail", "code"}
)

func oneOf(value string, allowed []string) string {
	for _, item := range allowed {
		if value == item {
			return value
		}
	}
	return allowed[0]
}

// shellLink changes one shell selection and keeps everything else in the
// query: the inspected file, pattern, step and the other regions' selections.
func shellLink(current url.Values, key, value string) string {
	query := url.Values{}
	for name, values := range current {
		query[name] = append([]string(nil), values...)
	}
	query.Set("tab", oneOf(query.Get("tab"), shellTabs))
	query.Set("dock", oneOf(query.Get("dock"), shellDocks))
	query.Set("region", oneOf(query.Get("region"), shellPhones))
	query.Set(key, value)
	// Defaults stay implicit, except for the key being chosen.
	for name, fallback := range map[string]string{"tab": shellTabs[0], "dock": shellDocks[0], "region": shellPhones[0]} {
		if name != key && query.Get(name) == fallback {
			query.Del(name)
		}
	}
	return "?" + query.Encode()
}

func (s *studioApp) shell(ctx *server.Context) gosx.Node {
	body, _, _ := s.shellContent(ctx)
	return body
}

// shellPage is one read of the workspace for the shell and everything its
// update regions render from. The page renders every region; an edit response
// and a region refresh render only the regions the browser asks for.
type shellPage struct {
	s              *studioApp
	ctx            *server.Context
	view, codeView workspace
	props          ui.ShellProps
	csrf, returnTo string
	query          url.Values
	tab, dock      string
	transport      func() transport
}

// loadShell reads the workspace once for the shell. The Code lens always shows
// the score, so the loader selects its file: the Code lens gets the selected
// file and any restored draft; every other panel gets the unmodified snapshot,
// so its source matches the saved project. When ok is false the node explains
// why the app is unavailable.
func (s *studioApp) loadShell(ctx *server.Context) (*shellPage, gosx.Node, bool) {
	var view workspace
	codeView, props, unavailable, ok := s.loadWorkspace(ctx, "code", &view)
	if !ok {
		return nil, unavailable, false
	}
	query := ctx.Request.URL.Query()
	page := &shellPage{s: s, ctx: ctx, view: view, codeView: codeView, props: props, csrf: session.Token(ctx.Request), returnTo: ctx.Request.URL.RequestURI(), query: query,
		tab: oneOf(query.Get("tab"), shellTabs), dock: oneOf(query.Get("dock"), shellDocks)}
	page.transport = sync.OnceValue(func() transport {
		var state transport
		_ = s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/transport", nil, &state)
		return state
	})
	return page, gosx.Fragment(), true
}

func (p *shellPage) form(id, name string, children ...gosx.Node) gosx.Node {
	return ui.ActionForm(ui.ActionFormProps{ID: id, Action: "/__actions/" + name, CSRF: p.csrf, Revision: p.view.Revision, ReturnTo: p.returnTo}, children...)
}

func (p *shellPage) panel(name string) gosx.Node {
	v := p.view
	if name == "code" {
		v = p.codeView
	}
	return shellLinks(p.ctx.Request.URL, p.s.shellPanel(p.ctx, v, p.csrf, name))
}

// regionContent renders one update region's content. The bar and step readout
// is not a region: it is a live binding, and a server snapshot would move it
// backwards during playback.
func (p *shellPage) regionContent(id string) (gosx.Node, bool) {
	button := func(value, label, glyph string) gosx.Node {
		return ui.ActionButton(ui.ActionButtonProps{Name: "action", Value: value, Label: label, Text: glyph, Class: "transport-glyph"})
	}
	file := func(name, glyph string) gosx.Node {
		return gosx.Fragment(ui.HiddenField(ui.HiddenFieldProps{Name: "file", Value: p.view.File}), ui.HiddenField(ui.HiddenFieldProps{Name: "fileRevision", Value: p.view.FileRevision}), ui.ActionButton(ui.ActionButtonProps{Label: name, Text: glyph, Class: "transport-glyph"}))
	}
	switch id {
	case "project":
		return ui.ScoreTitle(ui.ScoreTitleProps{Title: p.props.Title, Filename: p.props.Filename}), true
	case "transport":
		return p.form("transport-form", "transport", button("home", "Return to start", "▏◀"), button("play", "Play", "▶"), button("pause", "Pause", "Ⅱ"), button("stop", "Stop", "■")), true
	case "tempo":
		return ui.Tempo(ui.TempoProps{Tempo: p.props.Tempo, Key: p.props.Key}), true
	case "actions":
		return gosx.Fragment(
			p.form("undo-form", "undo", hidden("source_revision", p.view.SourceRevision), file("Undo", "↶")),
			p.form("redo-form", "redo", hidden("source_revision", p.view.SourceRevision), file("Redo", "↷")),
		), true
	case "status":
		return gosx.Fragment(
			ui.StatusTrigger(ui.StatusTriggerProps{Target: "#history-dialog", Controls: "history-dialog", Label: "History", Text: savedStatus(p.view)}),
			gosx.El("span", gosx.Attrs(gosx.Attr("class", "shell-status__spacer"))),
			ui.StatusTrigger(ui.StatusTriggerProps{Target: "#audio-dialog", Controls: "audio-dialog", Label: "Audio", Text: audioStatus(p.transport())}),
		), true
	case "dock-header":
		clip, track := dockSelection(p.view, p.query)
		return ui.DockSelection(ui.DockSelectionProps{Selection: clip.name, SelectionDetail: clip.detail, TrackDetail: track}), true
	case "code":
		return gosx.Fragment(p.panel("code"), p.panel("collaboration")), true
	}
	for _, r := range shellPanelRegions {
		if r.id == id {
			return p.panel(r.panel), true
		}
	}
	return gosx.Fragment(), false
}

// shellPanelRegions are the update regions that hold one panel each.
var shellPanelRegions = []struct{ id, panel string }{
	{"browser-instruments", "instruments"}, {"browser-generate", "generator"}, {"browser-takes", "takes"}, {"browser-sampler", "sampler"},
	{"center-arrange", "session"}, {"center-session", "live"}, {"center-mixer", "mixer"},
	{"dock-clip", "patterns"}, {"dock-device", "device"}, {"dock-automation", "automation"},
	{"dialog-history", "history"}, {"dialog-audio", "audio"}, {"dialog-export", "export"},
}

// shellRegionIDs lists every update region of the shell in page order.
var shellRegionIDs = []string{
	"project", "transport", "tempo", "actions",
	"browser-instruments", "browser-generate", "browser-takes", "browser-sampler",
	"center-arrange", "center-session", "center-mixer",
	"dock-header", "dock-clip", "dock-device", "dock-automation", "code", "status",
	"dialog-history", "dialog-audio", "dialog-export",
}

// renderRegion renders one update region's body and its digest.
func (p *shellPage) renderRegion(id string) (string, string, bool) {
	content, ok := p.regionContent(id)
	if !ok {
		return "", "", false
	}
	body := regionBody(gosx.RenderHTML(content))
	return body, regionDigest(body), true
}

// region renders an update region with its wrapper for the page.
func (p *shellPage) region(id string) gosx.Node {
	body, digest, _ := p.renderRegion(id)
	return gosx.RawHTML(regionWrapper(id, digest, p.view.Revision, p.view.SourceRevision, body))
}

// shellContent renders the shell and returns the workspace it was built from;
// ok is false when the app is unavailable and the node explains why.
func (s *studioApp) shellContent(ctx *server.Context) (gosx.Node, workspace, bool) {
	p, unavailable, ok := s.loadShell(ctx)
	if !ok {
		return unavailable, workspace{}, false
	}
	query, current, view, props := p.query, p.query, p.view, p.props
	tab, dock := p.tab, p.dock
	region := oneOf(query.Get("region"), shellPhones)
	ctx.Runtime().ManagedScript("/studio-startup.js", server.ManagedScriptOptions{Role: server.ManagedScriptRoleLifecycle}, gosx.BoolAttr("defer"))
	// The shell engine applies edit responses with GoSX's patch receiver.
	// Engine-only pages do not load it implicitly; register GoSX's own asset.
	if runtimeRoot() != "" {
		ctx.Runtime().ManagedScript("/gosx/patch.js", server.ManagedScriptOptions{Role: server.ManagedScriptRolePatch})
	}
	state := p.transport()
	browserOpen, codeOpen := shellPanes(ctx.Request)
	registerShell(ctx, tab, dock, browserOpen, codeOpen)
	registerArrangementTempos(ctx)

	keys := func(id string) string {
		for _, cmd := range shellCommands() {
			if cmd.ID == id {
				return command.KeyShortcuts(cmd.Keys...)
			}
		}
		return ""
	}
	top := ui.TopBar(p.region("project"), ui.TransportBar(
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "shell-playback")),
			p.region("transport"),
			ui.CommandButton(ui.CommandButtonProps{ID: "transport.record", Label: "Record", Glyph: "●", Keys: keys("transport.record")}),
			ui.CommandButton(ui.CommandButtonProps{ID: "transport.loop", Label: "Loop", Glyph: "↻", Keys: keys("transport.loop")}),
			ui.Position(ui.TransportProps{Playing: state.Playing, Bar: state.Bar, Step: state.Step, Backend: state.Backend, Error: state.Error, Listening: state.Listening}),
			p.region("tempo"),
		),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "shell-actions")),
			p.region("actions"),
			ui.PaneToggle(ui.PaneToggleProps{Show: "layout.library.show", Hide: "layout.library.hide", Label: "Library", Pane: "library"}),
			ui.PaneToggle(ui.PaneToggleProps{Show: "layout.code.show", Hide: "layout.code.hide", Label: "Code", Pane: "code"}),
			ui.DialogTrigger(ui.DialogTriggerProps{Target: "#export-dialog", Controls: "export-dialog", Label: "Export"}),
			ui.IconDialogTrigger(ui.IconDialogTriggerProps{Target: "#palette", Controls: "palette", Label: "Shortcuts", Glyph: "?"}),
		),
		ui.CaptureHint(),
	))

	// Every panel renders, hidden unless selected, so in-place tab switching
	// has its content. Each sits in its own update region inside the tab panel,
	// so a patch never touches GoSX's tab elements.
	centerPanel := func(id string) gosx.Node {
		return ui.TabPanel(ui.TabPanelProps{ID: "view-panel-" + id, Tab: "view-tab-" + id, Hidden: id != tab}, p.region("center-"+id))
	}
	dockPanel := func(id string) gosx.Node {
		return ui.TabPanel(ui.TabPanelProps{ID: "detail-panel-" + id, Tab: "detail-tab-" + id, Hidden: id != dock}, p.region("dock-"+id))
	}

	browser := ui.Browser(ui.BrowserProps{Open: browserOpen, Width: strconv.Itoa(shellBrowserWidth)},
		ui.BrowserSection(ui.BrowserSectionProps{ID: "browser-instruments", Signal: "$layout.instrumentsOpen", Title: "Instruments", Open: true}, p.region("browser-instruments")),
		ui.BrowserSection(ui.BrowserSectionProps{ID: "browser-generate", Signal: "$layout.generateOpen", Title: "Generate"}, p.region("browser-generate")),
		ui.BrowserSection(ui.BrowserSectionProps{ID: "browser-takes", Signal: "$layout.takesOpen", Title: "Takes"}, p.region("browser-takes")),
		ui.BrowserSection(ui.BrowserSectionProps{ID: "browser-sampler", Signal: "$layout.samplerOpen", Title: "Sampler"}, p.region("browser-sampler")),
	)
	center := ui.Center(ui.CenterProps{
		View:        tab,
		ArrangeHref: shellLink(current, "tab", "arrange"), SessionHref: shellLink(current, "tab", "session"), MixerHref: shellLink(current, "tab", "mixer"),
		ArrangeShown: tab == "arrange", SessionShown: tab == "session", MixerShown: tab == "mixer",
	}, centerPanel("arrange"), centerPanel("session"), centerPanel("mixer"))
	detail := ui.DetailDock(ui.DetailDockProps{
		ClipHref: shellLink(current, "dock", "clip"), DeviceHref: shellLink(current, "dock", "device"), AutomationHref: shellLink(current, "dock", "automation"),
		ClipShown: dock == "clip", DeviceShown: dock == "device", AutomationShown: dock == "automation", Height: strconv.Itoa(shellDockHeight),
	}, p.region("dock-header"), ui.ShellScroll(dockPanel("clip"), dockPanel("device"), dockPanel("automation")))
	code := ui.CodeLens(ui.CodeLensProps{Open: codeOpen, Width: strconv.Itoa(shellCodeWidth)}, p.region("code"))
	status := ui.StatusBar(p.region("status"))

	// This engine binds the surrounding document, rather than owning its
	// rendered children. A fresh masked CSRF token gives managed navigation a
	// fresh binding instead of retaining a listener on the previous shell.
	shellProps, _ := json.Marshal(map[string]string{"csrf": p.csrf, "revision": view.Revision, "sourceRevision": view.SourceRevision})
	dialogs := gosx.Fragment(
		ui.DialogFrame(ui.DialogFrameProps{ID: "history-dialog", Title: "History"}, p.region("dialog-history")),
		ui.DialogFrame(ui.DialogFrameProps{ID: "audio-dialog", Title: "Audio devices"}, p.region("dialog-audio")),
		ui.DialogFrame(ui.DialogFrameProps{ID: "export-dialog", Title: "Export"}, p.region("dialog-export")),
		ui.DialogFrame(ui.DialogFrameProps{ID: "palette", Title: "Keyboard shortcuts"}, shortcuts(), ui.ShortcutNotes()),
		// Level meters in the Mixer strips. The engine polls only while that view shows.
		meterEngine(ctx, engine.Config{Name: "CicadaMeters", Kind: engine.KindSurface, MountID: "cicada-strip-meters", Runtime: engine.RuntimeGoWASM, Props: []byte(`{"mode":"strips"}`), Capabilities: []engine.Capability{engine.CapFetch}, RequiredCapabilities: []engine.Capability{engine.CapWASM, engine.CapFetch}}, gosx.El("div", gosx.Attrs(gosx.BoolAttr("hidden")))),
		meterEngine(ctx, engine.Config{Name: "CicadaShell", Kind: engine.KindSurface, MountID: "cicada-shell-actions", Runtime: engine.RuntimeGoWASM, Props: shellProps, Capabilities: []engine.Capability{engine.CapFetch, engine.CapKeyboard}, RequiredCapabilities: []engine.Capability{engine.CapWASM, engine.CapFetch}}, gosx.El("div", gosx.Attrs(gosx.BoolAttr("hidden")))),
	)
	message, hasMessage := props.Message, props.HasMessage
	for name, result := range action.States(ctx.Request) {
		_ = name
		if result.Message() != "" {
			message, hasMessage = result.Message(), true
		}
	}
	phone := ui.PhoneNav(ui.PhoneNavProps{
		CenterHref: shellLink(current, "region", "center"), BrowserHref: shellLink(current, "region", "browser"),
		DetailHref: shellLink(current, "region", "detail"), CodeHref: shellLink(current, "region", "code"),
		BrowserOn: region == "browser", CenterOn: region == "center", DetailOn: region == "detail", CodeOn: region == "code",
	})
	workbench := ui.Dock(ui.DockProps{
		ID: "studio-workbench", LeftSignal: "$layout.browserWidth", RightSignal: "$layout.codeWidth", BottomSignal: "$layout.dockHeight",
		Left: strconv.Itoa(shellBrowserWidth), Right: strconv.Itoa(shellCodeWidth), Bottom: strconv.Itoa(shellDockHeight),
	}, top, browser, center, code, detail, status)
	return ui.ShellFrame(ui.ShellFrameProps{Region: region, Message: message, HasMessage: hasMessage, Href: ctx.Request.URL.RequestURI()}, workbench, phone, dialogs, ui.EditFeedback()), view, true
}

type dockClip struct{ name, detail string }

// dockSelection names what the detail dock edits: the Clip tab's pattern with
// the tracks that play it, and the Device and Automation tabs' track. It
// follows the same defaults as the editors, so the header matches them.
func dockSelection(v workspace, query url.Values) (dockClip, string) {
	if v.Project == nil {
		return dockClip{}, ""
	}
	var clip dockClip
	if len(v.Project.Patterns) > 0 {
		p := v.Project.Patterns[0]
		for _, candidate := range v.Project.Patterns {
			if candidate.ID == query.Get("pattern") {
				p = candidate
			}
		}
		// The tracks that play the pattern, in track order, from the scene
		// matrix or the placement lanes.
		var tracks []string
		for _, track := range v.Project.Tracks {
			plays := false
			for _, scene := range v.Project.Scenes {
				plays = plays || scene.Bindings[track.ID] == p.ID
			}
			if v.Project.Arrange != nil {
				for _, placement := range v.Project.Arrange.Placements {
					plays = plays || placement.Track == track.ID && placement.Content == p.ID
				}
			}
			if plays {
				tracks = append(tracks, track.ID)
			}
		}
		parts := tracks
		if p.Performance != nil {
			parts = append(parts, fmt.Sprintf("%d notes", len(p.Performance.Notes)))
		} else {
			parts = append(parts, p.Kind, fmt.Sprintf("%d steps", p.Steps))
		}
		clip = dockClip{name: p.ID, detail: strings.Join(parts, " · ")}
	}
	track := ""
	if t := deviceTrack(v.Project, query); t != nil {
		track = t.ID + " · " + t.Kind
	}
	return clip, track
}

// audioStatus is the status bar's audio line: the active output or why there
// is none.
func audioStatus(state transport) string {
	switch {
	case state.Error != "":
		return "Error"
	case state.Backend == "":
		return "Offline"
	case state.Playing:
		return "Playing · " + state.Backend
	default:
		return "Ready · " + state.Backend
	}
}

// savedStatus is the status bar's history line: the saved revision the page
// was built from.
func savedStatus(view workspace) string {
	revision := view.SourceRevision
	if revision == "" {
		revision = view.Revision
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	if revision == "" {
		return "Saved"
	}
	return "Saved · " + revision
}

// shortcuts renders the command table by group. It is the interim palette
// (S8 replaces it with the GoSX palette); reserved chords show as unavailable.
func shortcuts() gosx.Node {
	var groups []string
	rows := map[string][]gosx.Node{}
	for _, cmd := range shellCommands() {
		if shellToggleMirrors[cmd.ID] {
			continue
		}
		if _, seen := rows[cmd.Group]; !seen {
			groups = append(groups, cmd.Group)
		}
		rows[cmd.Group] = append(rows[cmd.Group], ui.ShortcutRow(ui.ShortcutRowProps{Title: cmd.Title, Keys: command.KeyShortcuts(cmd.Keys...), Unavailable: cmd.Reserved}))
	}
	var nodes []gosx.Node
	for _, group := range groups {
		nodes = append(nodes, ui.ShortcutGroup(ui.ShortcutGroupProps{Title: group}, rows[group]...))
	}
	return gosx.Fragment(nodes...)
}

// shellPanes is whether Library and Code open, from the cookie the shell engine
// writes when either changes: two characters, Library then Code, "1" for open.
// Without it Library opens and Code stays one gesture away.
func shellPanes(r *http.Request) (library, code bool) {
	library, code = true, false
	if cookie, err := r.Cookie("cicada_panes"); err == nil && len(cookie.Value) == 2 {
		library, code = cookie.Value[0] == '1', cookie.Value[1] == '1'
	}
	return library, code
}

// registerShell declares the shell's shared signals, persisted layout and commands.
func registerShell(ctx *server.Context, tab, dock string, browserOpen, codeOpen bool) {
	runtime := ctx.Runtime()
	layout := func(name string, initial any) controller.Output {
		return controller.Output{Name: name, Signal: "$layout." + name, Initial: initial}
	}
	var save, load []controller.StorageSlot
	for _, name := range []string{"browserWidth", "codeWidth", "dockHeight", "browserOpen", "codeOpen", "instrumentsOpen", "generateOpen", "takesOpen", "samplerOpen"} {
		slot := controller.StorageSlot{Key: name, Signal: "$layout." + name}
		save, load = append(save, slot), append(load, slot)
	}
	runtime.Controller(controller.Config{
		Name: "studio-shell",
		Root: "#studio-shell",
		Outputs: []controller.Output{
			{Name: "view", Signal: "$view", Initial: tab},
			{Name: "dock", Signal: "$dock", Initial: dock},
			{Name: "tool", Signal: "$tool", Initial: "select"},
			{Name: "selectionActive", Signal: "$selectionActive", Initial: false},
			{Name: "placementSelectionActive", Signal: "$placementSelectionActive", Initial: false},
			layout("browserWidth", shellBrowserWidth), layout("codeWidth", shellCodeWidth), layout("dockHeight", shellDockHeight),
			layout("browserOpen", browserOpen), layout("codeOpen", codeOpen),
			layout("instrumentsOpen", true), layout("generateOpen", false), layout("takesOpen", false), layout("samplerOpen", false),
		},
		Storage: &controller.Storage{Area: "local", Namespace: "studio:layout", Load: load, Save: save},
	})
	if err := runtime.Commands(shellCommands()...); err != nil {
		// The table is static, so a failure is a programming error that a test catches.
		panic(err)
	}
}

// shellKept lists the query keys a shell destination carries over from the
// page the user is on. Everything else comes from the destination itself, so a
// stale pattern step or lane never survives an explicit choice.
var shellKept = []string{"tab", "dock", "region", "file"}

var shellLinkPattern = regexp.MustCompile(`href="(/\?[^"]*)"`)

// shellDestination turns a legacy panel destination ("/?panel=<name>&<state>")
// into the matching /studio URL: the destination's own state plus the shell
// selection and file from "from". Other targets are returned unchanged.
func shellDestination(from *url.URL, target string, needPanel bool) string {
	parsed, err := url.Parse(target)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Path != "/" {
		return target
	}
	query := parsed.Query()
	if needPanel && query.Get("panel") == "" {
		return target
	}
	query.Del("panel")
	for _, name := range shellKept {
		if value := from.Query().Get(name); value != "" && query.Get(name) == "" {
			query.Set(name, value)
		}
	}
	rewritten := "/studio"
	if encoded := query.Encode(); encoded != "" {
		rewritten += "?" + encoded
	}
	return rewritten
}

// shellOrigin reports the /studio page a request was submitted from, by Referer.
func shellOrigin(r *http.Request) (*url.URL, bool) {
	referer, err := url.Parse(r.Header.Get("Referer"))
	if err != nil || referer.Path != "/studio" || (referer.Host != "" && referer.Host != r.Host) {
		return nil, false
	}
	return referer, true
}

// shellRedirect is the destination a handler should redirect to: the legacy
// target for callers on "/", the shell equivalent for callers on /studio.
func shellRedirect(r *http.Request, target string) string {
	if from, ok := shellOrigin(r); ok {
		return shellDestination(from, target, false)
	}
	return target
}

var (
	shellGetForm   = regexp.MustCompile(`(?s)<form([^>]*method="get"[^>]*)action="/"([^>]*)>(.*?)</form>`)
	shellPanelName = regexp.MustCompile(`<input[^>]*name="panel"[^>]*>`)
)

// shellLinks points a mounted panel's links and GET forms at /studio instead of
// "/". A GET form keeps the shell selection and file as hidden fields and drops
// the legacy "panel" field.
func shellLinks(current *url.URL, node gosx.Node) gosx.Node {
	rendered := gosx.RenderHTML(node)
	if !strings.Contains(rendered, `href="/?`) && !shellGetForm.MatchString(rendered) {
		return node
	}
	rendered = shellGetForm.ReplaceAllStringFunc(rendered, func(match string) string {
		parts := shellGetForm.FindStringSubmatch(match)
		var fields strings.Builder
		for _, name := range shellKept {
			if value := current.Query().Get(name); value != "" {
				fields.WriteString(`<input type="hidden" name="` + name + `" value="` + html.EscapeString(value) + `">`)
			}
		}
		return `<form` + parts[1] + `action="/studio"` + parts[2] + `>` + fields.String() + shellPanelName.ReplaceAllString(parts[3], "") + `</form>`
	})
	return gosx.RawHTML(shellLinkPattern.ReplaceAllStringFunc(rendered, func(match string) string {
		target := html.UnescapeString(shellLinkPattern.FindStringSubmatch(match)[1])
		return `href="` + html.EscapeString(shellDestination(current, target, true)) + `"`
	}))
}

// shellReturn keeps a mounted panel's form in the shell. Panels name their own
// return target in the legacy form "/?panel=<name>&<state>"; when the form was
// submitted from the shell, the target becomes the /studio equivalent.
func shellReturn(r *http.Request) {
	from, ok := shellOrigin(r)
	if !ok {
		return
	}
	if r.PostForm == nil {
		switch contentType := r.Header.Get("Content-Type"); {
		case strings.HasPrefix(contentType, "multipart/form-data"):
			// GoSX's managed forms send FormData, including forms without files.
			// Parse before rewriting their return target; the action handler has
			// not populated PostForm yet. The app bounds and cleans up the body.
			_ = r.ParseMultipartForm(2 << 20)
		case strings.HasPrefix(contentType, "application/x-www-form-urlencoded"):
			_ = r.ParseForm()
		}
	}
	if r.PostForm == nil {
		return
	}
	values := r.PostForm[action.ReturnTargetField]
	if len(values) == 0 {
		return
	}
	rewritten := shellDestination(from, values[0], true)
	r.PostForm[action.ReturnTargetField] = []string{rewritten}
	if r.Form != nil {
		r.Form[action.ReturnTargetField] = []string{rewritten}
	}
}

// shellNeedsScore lists the panels that read the parsed project. The Code lens,
// History, Audio and Export work from the source and the service, so a broken
// score can still be repaired and inspected in the shell.
var shellNeedsScore = map[string]bool{
	"session": true, "live": true, "mixer": true, "patterns": true, "instruments": true,
	"generator": true, "takes": true, "sampler": true, "device": true, "automation": true,
}

var shellPanelTitles = map[string]string{
	"session": "Arrange", "live": "Session", "mixer": "Mixer", "patterns": "Clip", "instruments": "Instruments",
	"generator": "Generate", "takes": "Takes", "sampler": "Sampler", "device": "Device", "automation": "Automation",
}

// shellPanel mounts a panel, or a short explanation when the score does not
// parse. A panel that still fails (for example on a half-formed project) is
// replaced by the same explanation, so one panel cannot take the page down.
func (s *studioApp) shellPanel(ctx *server.Context, v workspace, csrf, name string) (node gosx.Node) {
	unavailable := func(reason string) gosx.Node {
		return ui.Panel(ui.PanelProps{ID: "panel-" + name + "-unavailable", Title: shellPanelTitles[name], Description: reason})
	}
	if shellNeedsScore[name] && (v.Project == nil || !v.Valid && v.Error != "") {
		return unavailable("Fix the score in the Code lens to use this panel.")
	}
	defer func() {
		if recover() != nil {
			node = unavailable("This panel could not read the score. Fix the score in the Code lens.")
		}
	}()
	switch name {
	case "session":
		// Automation lives in its detail tab in the shell. The standalone
		// Session panel still renders its editors through panel().
		timeline := s.session(ctx, v, csrf, ctx.Request.URL.Query().Get("placement"))
		vocals := s.vocals(ctx, v, csrf)
		if v.Project.Edition != 2 {
			return timeline
		}
		jump := func(target, label string) gosx.Node {
			return gosx.El("a", gosx.Attrs(gosx.Attr("href", target), gosx.Attr("data-gosx-native", "true")), gosx.Text(label))
		}
		return gosx.Fragment(gosx.El("nav", gosx.Attrs(gosx.Attr("class", "arrange-workspace-nav"), gosx.Attr("aria-label", "Arrange workspaces")), jump("#arrangement", "Song timeline"), jump("#vocals", "Record & comp vocals")), timeline, vocals)
	case "device":
		return s.deviceDock(ctx, v, csrf)
	case "automation":
		return s.automationDock(ctx, v, csrf)
	default:
		return s.panel(ctx, v, csrf, name)
	}
}
