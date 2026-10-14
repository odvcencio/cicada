package main

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/command"
	"m31labs.dev/gosx/controller"
	"m31labs.dev/gosx/server"
	"m31labs.dev/gosx/session"
)

// Shell regions start at these sizes, in CSS pixels, until a stored layout loads.
const (
	shellBrowserWidth = 280
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

// shellLink keeps the other regions' selections when one of them changes.
func shellLink(tab, dock, region string, key, value string) string {
	query := url.Values{"tab": {tab}, "dock": {dock}, "region": {region}}
	query.Set(key, value)
	var parts []string
	for _, name := range []string{"tab", "dock", "region"} {
		fallback := map[string]string{"tab": shellTabs[0], "dock": shellDocks[0], "region": shellPhones[0]}[name]
		if query.Get(name) != fallback {
			parts = append(parts, name+"="+url.QueryEscape(query.Get(name)))
		}
	}
	if len(parts) == 0 {
		return "?" + key + "=" + url.QueryEscape(value)
	}
	return "?" + strings.Join(parts, "&")
}

func (s *studioApp) shell(ctx *server.Context) gosx.Node {
	query := ctx.Request.URL.Query()
	tab := oneOf(query.Get("tab"), shellTabs)
	dock := oneOf(query.Get("dock"), shellDocks)
	region := oneOf(query.Get("region"), shellPhones)
	// The Code lens always shows the score, so the loader selects its file.
	view, props, unavailable, ok := s.loadWorkspace(ctx, "code")
	if !ok {
		return unavailable
	}
	csrf := session.Token(ctx.Request)
	var state transport
	_ = s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/transport", nil, &state)
	registerShell(ctx, tab, dock)

	returnTo := ctx.Request.URL.RequestURI()
	file := func(name string) gosx.Node {
		return gosx.Fragment(ui.HiddenField(ui.HiddenFieldProps{Name: "file", Value: view.File}), ui.HiddenField(ui.HiddenFieldProps{Name: "fileRevision", Value: view.FileRevision}), ui.ActionButton(ui.ActionButtonProps{Label: name}))
	}
	form := func(id, name string, children ...gosx.Node) gosx.Node {
		return ui.ActionForm(ui.ActionFormProps{ID: id, Action: "/__actions/" + name, CSRF: csrf, Revision: view.Revision, ReturnTo: returnTo}, children...)
	}
	button := func(value, label string) gosx.Node {
		return ui.ActionButton(ui.ActionButtonProps{Name: "action", Value: value, Label: label})
	}
	keys := func(id string) string {
		for _, cmd := range shellCommands() {
			if cmd.ID == id {
				return command.KeyShortcuts(cmd.Keys...)
			}
		}
		return ""
	}
	top := ui.TopBar(ui.TopBarProps{Title: props.Title, Filename: props.Filename, Tempo: props.Tempo, Key: props.Key, Backend: engineLabel(state)},
		form("transport-form", "transport", button("play", "Play"), button("pause", "Pause"), button("stop", "Stop"), button("home", "Return to start")),
		ui.CommandButton(ui.CommandButtonProps{ID: "transport.record", Label: "Record", Keys: keys("transport.record")}),
		ui.CommandButton(ui.CommandButtonProps{ID: "transport.loop", Label: "Loop", Keys: keys("transport.loop")}),
		ui.Placeholder(ui.PlaceholderProps{Label: "Metronome"}),
		ui.Position(ui.TransportProps{Playing: state.Playing, Bar: state.Bar, Step: state.Step, Backend: state.Backend, Error: state.Error, Listening: state.Listening}),
		form("undo-form", "undo", file("Undo")), form("redo-form", "redo", file("Redo")),
		ui.DialogTrigger(ui.DialogTriggerProps{Target: "#history-dialog", Label: "History"}),
		ui.DialogTrigger(ui.DialogTriggerProps{Target: "#audio-dialog", Label: "Audio"}),
		ui.DialogTrigger(ui.DialogTriggerProps{Target: "#export-dialog", Label: "Export"}),
		ui.DialogTrigger(ui.DialogTriggerProps{Target: "#palette", Label: "Shortcuts"}),
		ui.CaptureHint(),
	)

	panel := func(name string) gosx.Node { return s.panel(ctx, view, csrf, name) }
	// Every panel renders, hidden unless selected, so in-place tab switching
	// has its content.
	centerPanel := func(id, name string) gosx.Node {
		return ui.TabPanel(ui.TabPanelProps{ID: "view-panel-" + id, Tab: "view-tab-" + id, Hidden: id != tab}, panel(name))
	}
	dockPanel := func(id string, content func() gosx.Node) gosx.Node {
		return ui.TabPanel(ui.TabPanelProps{ID: "detail-panel-" + id, Tab: "detail-tab-" + id, Hidden: id != dock}, content())
	}
	later := func(id, text string) func() gosx.Node {
		return func() gosx.Node {
			return ui.Panel(ui.PanelProps{ID: id, Title: "Not in this view yet", Description: text})
		}
	}

	browser := ui.Browser(ui.BrowserProps{Open: true, Width: strconv.Itoa(shellBrowserWidth)},
		ui.BrowserSection(ui.BrowserSectionProps{ID: "browser-instruments", Title: "Instruments", Open: true}, panel("instruments")),
		ui.BrowserSection(ui.BrowserSectionProps{ID: "browser-generate", Title: "Generate"}, panel("generator")),
		ui.BrowserSection(ui.BrowserSectionProps{ID: "browser-takes", Title: "Takes"}, panel("takes")),
		ui.BrowserSection(ui.BrowserSectionProps{ID: "browser-sampler", Title: "Sampler"}, panel("sampler")),
	)
	center := ui.Center(ui.CenterProps{
		View:        tab,
		ArrangeHref: shellLink(tab, dock, region, "tab", "arrange"), SessionHref: shellLink(tab, dock, region, "tab", "session"), MixerHref: shellLink(tab, dock, region, "tab", "mixer"),
		ArrangeShown: tab == "arrange", SessionShown: tab == "session", MixerShown: tab == "mixer",
	}, centerPanel("arrange", "session"), centerPanel("session", "live"), centerPanel("mixer", "mixer"))
	detail := ui.DetailDock(ui.DetailDockProps{
		ClipHref: shellLink(tab, dock, region, "dock", "clip"), DeviceHref: shellLink(tab, dock, region, "dock", "device"), AutomationHref: shellLink(tab, dock, region, "dock", "automation"),
		ClipShown: dock == "clip", DeviceShown: dock == "device", AutomationShown: dock == "automation", Height: strconv.Itoa(shellDockHeight),
	}, dockPanel("clip", func() gosx.Node { return panel("patterns") }),
		dockPanel("device", later("detail-device-later", "The Device view arrives with the Browser lane; use Instruments in the Browser for now.")),
		dockPanel("automation", later("detail-automation-later", "Automation lanes are in the Session panel under Arrange for now.")))
	code := ui.CodeLens(ui.CodeLensProps{Open: true, Width: strconv.Itoa(shellCodeWidth)}, panel("code"), panel("collaboration"))

	dialogs := gosx.Fragment(
		ui.DialogFrame(ui.DialogFrameProps{ID: "history-dialog", Title: "History"}, panel("history")),
		ui.DialogFrame(ui.DialogFrameProps{ID: "audio-dialog", Title: "Audio devices"}, panel("audio")),
		ui.DialogFrame(ui.DialogFrameProps{ID: "export-dialog", Title: "Export"}, panel("export")),
		ui.DialogFrame(ui.DialogFrameProps{ID: "palette", Title: "Keyboard shortcuts"}, shortcuts(), ui.ShortcutNotes()),
	)
	message, hasMessage := props.Message, props.HasMessage
	for name, result := range action.States(ctx.Request) {
		_ = name
		if result.Message() != "" {
			message, hasMessage = result.Message(), true
		}
	}
	phone := ui.PhoneNav(ui.PhoneNavProps{
		CenterHref: shellLink(tab, dock, region, "region", "center"), BrowserHref: shellLink(tab, dock, region, "region", "browser"),
		DetailHref: shellLink(tab, dock, region, "region", "detail"), CodeHref: shellLink(tab, dock, region, "region", "code"),
		BrowserOn: region == "browser", CenterOn: region == "center", DetailOn: region == "detail", CodeOn: region == "code",
	})
	workbench := ui.Dock(ui.DockProps{
		ID: "studio-workbench", LeftSignal: "$layout.browserWidth", RightSignal: "$layout.codeWidth", BottomSignal: "$layout.dockHeight",
		Left: strconv.Itoa(shellBrowserWidth), Right: strconv.Itoa(shellCodeWidth), Bottom: strconv.Itoa(shellDockHeight),
	}, top, browser, center, code, detail)
	return ui.ShellFrame(ui.ShellFrameProps{Region: region, Message: message, HasMessage: hasMessage}, phone, workbench, dialogs)
}

func engineLabel(state transport) string {
	if state.Backend == "" {
		return "OFFLINE"
	}
	return strings.ToUpper(state.Backend)
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

// registerShell declares the shell's shared signals, persisted layout and commands.
func registerShell(ctx *server.Context, tab, dock string) {
	runtime := ctx.Runtime()
	layout := func(name string, initial any) controller.Output {
		return controller.Output{Name: name, Signal: "$layout." + name, Initial: initial}
	}
	var save, load []controller.StorageSlot
	for _, name := range []string{"browserWidth", "codeWidth", "dockHeight", "browserOpen", "codeOpen"} {
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
			layout("browserWidth", shellBrowserWidth), layout("codeWidth", shellCodeWidth), layout("dockHeight", shellDockHeight),
			layout("browserOpen", true), layout("codeOpen", true),
		},
		Storage: &controller.Storage{Area: "local", Namespace: "studio:layout", Load: load, Save: save},
	})
	if err := runtime.Commands(shellCommands()...); err != nil {
		// The table is static, so a failure is a programming error that a test catches.
		panic(err)
	}
}

// shellReturn keeps a mounted panel's form in the shell. Panels name their own
// return target in the legacy form "/?panel=<name>&<state>"; when the form was
// submitted from the shell (by Referer), the target becomes "/studio" with the
// panel's state and the shell's current tab, dock and region. Targets that are
// not legacy panel URLs, and submissions from elsewhere, are left alone.
func shellReturn(r *http.Request) {
	if r.PostForm == nil && strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		_ = r.ParseForm()
	}
	if r.PostForm == nil {
		return
	}
	values := r.PostForm[action.ReturnTargetField]
	if len(values) == 0 {
		return
	}
	referer, err := url.Parse(r.Header.Get("Referer"))
	if err != nil || referer.Path != "/studio" || (referer.Host != "" && referer.Host != r.Host) {
		return
	}
	target, err := url.Parse(values[0])
	if err != nil || target.IsAbs() || target.Host != "" || target.Path != "/" {
		return
	}
	query := target.Query()
	if query.Get("panel") == "" {
		return
	}
	query.Del("panel")
	for _, name := range []string{"tab", "dock", "region"} {
		if value := referer.Query().Get(name); value != "" && query.Get(name) == "" {
			query.Set(name, value)
		}
	}
	rewritten := "/studio"
	if encoded := query.Encode(); encoded != "" {
		rewritten += "?" + encoded
	}
	r.PostForm[action.ReturnTargetField] = []string{rewritten}
	if r.Form != nil {
		r.Form[action.ReturnTargetField] = []string{rewritten}
	}
}
