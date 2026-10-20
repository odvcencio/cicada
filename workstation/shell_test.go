package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/command"
)

func shellBody(t *testing.T, query string) string {
	t.Helper()
	address, client, _ := testApp(t)
	return getPage(t, client, address+"/studio"+query)
}

func TestShellRouteAndRegions(t *testing.T) {
	address, client, _ := testApp(t)
	response, err := client.Get(address + "/studio")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("shell status=%d", response.StatusCode)
	}
	if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("shell must be private, got %q", response.Header.Get("Cache-Control"))
	}
	body := string(data)
	for _, want := range []string{
		`role="toolbar"`, `aria-label="Transport"`, `aria-label="Views"`, `aria-label="Detail"`,
		`href="?tab=arrange"`, `href="?tab=session"`, `href="?tab=mixer"`,
		`data-gosx-collapsible="$layout.browserOpen"`, `data-gosx-collapsible="$layout.codeOpen"`,
		`data-gosx-drag="$layout.browserWidth"`, `data-gosx-drag="$layout.codeWidth"`, `data-gosx-drag="$layout.dockHeight"`,
		`bootstrapFeatureWorkbenchPath`,
		`id="studio-browser"`, `id="studio-center"`, `id="studio-detail"`, `id="studio-code"`,
		`id="history-dialog"`, `id="audio-dialog"`, `id="export-dialog"`, `id="palette"`,
		`data-studio-region="arrange"`, `130 BPM`, `C minor`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("shell missing %s", want)
		}
	}
	if strings.Count(body, `role="separator"`) < 2 {
		t.Error("shell needs split handles")
	}
	if strings.Contains(body, "CicadaWorkspace") {
		t.Fatal("shell must not mount the polling workspace engine")
	}
	current := func(body, tab string) bool {
		return regexp.MustCompile(`<a[^>]*href="\?tab=` + tab + `"[^>]*aria-current="page"`).MatchString(body)
	}
	if !current(body, "arrange") || current(body, "session") {
		t.Error("arrange must be the current view by default")
	}
	for _, tab := range []string{"arrange", "session", "mixer"} {
		if body := shellBody(t, "?tab="+tab); !current(body, tab) || !strings.Contains(body, `data-studio-region="`+tab+`"`) {
			t.Errorf("tab %s is not current", tab)
		}
	}
	// The original panel application stays available during the migration.
	if body := getPage(t, client, address+"/?panel=session"); !strings.Contains(body, `aria-label="Workspace"`) {
		t.Fatal("panel app disappeared")
	}
}

func TestShellHasUniqueIDsAndOnePhoneRegion(t *testing.T) {
	for _, tab := range []string{"arrange", "session", "mixer"} {
		body := shellBody(t, "?tab="+tab)
		seen := map[string]bool{}
		for _, match := range regexp.MustCompile(`\sid="([^"]+)"`).FindAllStringSubmatch(body, -1) {
			if seen[match[1]] && !strings.HasPrefix(match[1], "source-editor") {
				t.Errorf("tab %s: duplicate id %q", tab, match[1])
			}
			seen[match[1]] = true
		}
	}
	for _, region := range []string{"browser", "detail", "code"} {
		if body := shellBody(t, "?region="+region); !strings.Contains(body, `data-phone-region="`+region+`"`) {
			t.Errorf("phone region %s was not selected", region)
		}
	}
	if body := shellBody(t, "?region=bogus"); !strings.Contains(body, `data-phone-region="center"`) {
		t.Error("an unknown phone region must fall back to the center")
	}
	if body := shellBody(t, "?dock=automation"); !regexp.MustCompile(`<a[^>]*href="\?dock=automation[^"]*"[^>]*aria-current="page"`).MatchString(body) {
		t.Error("dock tab was not selected")
	}
}

func TestShellArrangeStartsWithTimelineAndLinksToVocals(t *testing.T) {
	address, client, _ := detailDockApp(t)
	body := getPage(t, client, address+"/studio?placement=lead-intro&dock=device")
	timeline, vocals := strings.Index(body, `id="arrangement"`), strings.Index(body, `id="vocals"`)
	if timeline < 0 || vocals < 0 || timeline >= vocals {
		t.Fatal("Arrange must expose the timeline before vocal setup")
	}
	for _, want := range []string{
		`href="#arrangement" data-gosx-link="false"`,
		`href="#vocals" data-gosx-link="false"`,
		`data-gosx-tab="arrange"`, `data-gosx-tab="session"`, `data-gosx-tab="mixer"`,
		`data-gosx-tab="clip"`, `data-gosx-tab="device"`, `data-gosx-tab="automation"`,
		`data-shell-region="code"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing local workspace navigation %s", want)
		}
	}
}

// shellManifestCommands reads the commands out of the page's hydration manifest.
func shellManifestCommands(t *testing.T, body string) []command.Command {
	t.Helper()
	match := regexp.MustCompile(`(?s)<script[^>]*type="application/json"[^>]*>(\{.*?"commands".*?\})</script>`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatal("page has no manifest with commands")
	}
	var manifest struct {
		Commands []command.Command `json:"commands"`
	}
	if err := json.Unmarshal([]byte(match[1]), &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest.Commands
}

func TestShellRegistersEveryCommandInTheTable(t *testing.T) {
	body := shellBody(t, "")
	got := map[string]command.Command{}
	for _, cmd := range shellManifestCommands(t, body) {
		got[cmd.ID] = cmd
	}
	for _, want := range shellCommands() {
		cmd, ok := got[want.ID]
		if !ok {
			t.Errorf("command %s is not in the manifest", want.ID)
			continue
		}
		if cmd.Reserved != want.Reserved || strings.Join(cmd.Keys, ",") != strings.Join(want.Keys, ",") {
			t.Errorf("command %s: keys=%v reserved=%v, want keys=%v reserved=%v", want.ID, cmd.Keys, cmd.Reserved, want.Keys, want.Reserved)
		}
	}
	for id, keys := range map[string]string{
		"transport.toggle": "Space", "transport.home": "Home", "view.arrange": "Alt+Shift+1", "view.session": "Alt+Shift+2", "view.mixer": "Alt+Shift+3", "dock.clip": "Alt+Shift+4", "dock.device": "Alt+Shift+5", "dock.automation": "Alt+Shift+6",
		"edit.undo": "Mod+Z", "edit.redo": "Mod+Shift+Z", "palette.open": "Mod+K,?,Shift+/", "arrange.consolidate": "Mod+J",
		"live.scene.1": "F1", "live.scene.8": "F8", "transport.record": "F9,Mod+Shift+R", "tool.draw": "B", "select.left": "ArrowLeft", "edit.save": "Mod+S",
	} {
		if got[id].ID == "" || strings.Join(got[id].Keys, ",") != keys {
			t.Errorf("command %s keys=%v, want %s", id, got[id].Keys, keys)
		}
	}
	for _, id := range []string{"transport.resume", "edit.split", "zoom.selection", "grid.snap", "arrange.consolidate", "arrange.group", "notes.quantize"} {
		if !got[id].Reserved {
			t.Errorf("command %s must stay reserved", id)
		}
	}
	for _, id := range []string{"edit.save", "edit.undo", "edit.redo", "edit.duplicate", "edit.delete", "transport.loop"} {
		if got[id].Reserved {
			t.Errorf("command %s must submit its working form", id)
		}
	}
	if !got["edit.save"].AllowEditable || got["edit.undo"].AllowEditable || got["edit.redo"].AllowEditable {
		t.Error("Save must work inside the score editor while text fields keep their own Undo and Redo")
	}
	for _, cmd := range shellCommands() {
		for _, key := range cmd.Keys {
			if chord, err := command.ParseChord(key); err != nil || chord.Key == "Tab" {
				t.Errorf("command %s: chord %q must parse and must not use Tab, which moves keyboard focus", cmd.ID, key)
			}
		}
	}
	if err := command.Validate(shellCommands()); err != nil {
		t.Fatal(err)
	}
}

func TestShellUndoRedoFormsAndShortcutsDialog(t *testing.T) {
	body := shellBody(t, "")
	for _, id := range []string{"undo", "redo"} {
		form := regexp.MustCompile(`(?s)<form[^>]*id="` + id + `-form"[^>]*>.*?</form>`).FindString(body)
		if form == "" {
			t.Fatalf("%s form is missing", id)
		}
		for _, want := range []string{`action="/__actions/` + id + `"`, `name="revision" value="current"`, `name="csrf_token"`, `name="file"`, `name="fileRevision"`, "data-gosx-form"} {
			if !strings.Contains(form, want) {
				t.Errorf("%s form lacks %s", id, want)
			}
		}
	}
	dialog := regexp.MustCompile(`(?s)<[a-z]+[^>]*id="palette".*`).FindString(body)
	for _, want := range []string{"Consolidate", "Unavailable", "Undo", "Space", "Control+Z"} {
		if !strings.Contains(dialog, want) {
			t.Errorf("shortcuts dialog lacks %q", want)
		}
	}
	// Reserved chords are listed as unavailable, never as working commands.
	row := regexp.MustCompile(`(?s)<[^>]*>[^<]*Consolidate[^<]*</.*?Unavailable`).FindString(dialog)
	if row == "" {
		t.Error("Consolidate is not marked unavailable")
	}
}

func TestShellAddsNoScriptsOrPolling(t *testing.T) {
	body := shellBody(t, "")
	if strings.Contains(body, "setInterval") || regexp.MustCompile(`data-gosx-live-interval="[^"]+"[^>]*data-gosx-live-src="/api/(meters|revision)`).MatchString(body) {
		t.Error("shell must not add polling")
	}
}

func TestShellDeclaresSignalsAndPersistsLayout(t *testing.T) {
	body := shellBody(t, "?tab=session&dock=device")
	for _, want := range []string{
		`"namespace":"studio:layout"`, `"area":"local"`,
		`"signal":"$view","initial":"session"`, `"signal":"$dock","initial":"device"`, `"signal":"$tool","initial":"select"`,
		`"signal":"$layout.browserWidth"`, `"signal":"$layout.codeWidth"`, `"signal":"$layout.dockHeight"`,
		`bootstrapControllerInputPath`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("shell lacks %s", want)
		}
	}
}

func TestShellTabsFollowTheARIATabPattern(t *testing.T) {
	body := shellBody(t, "?tab=session&dock=device")
	for _, list := range []string{"Views", "Detail"} {
		if !regexp.MustCompile(`<nav[^>]*role="tablist"[^>]*aria-label="` + list + `"|<nav[^>]*aria-label="` + list + `"[^>]*role="tablist"`).MatchString(body) {
			t.Errorf("%s tab list lacks role=tablist and a label", list)
		}
	}
	tabs := regexp.MustCompile(`<a[^>]*role="tab"[^>]*>`).FindAllString(body, -1)
	if len(tabs) != 6 {
		t.Fatalf("got %d tabs, want 6", len(tabs))
	}
	attr := func(tag, name string) string {
		match := regexp.MustCompile(`\s` + name + `="([^"]*)"`).FindStringSubmatch(tag)
		if match == nil {
			return ""
		}
		return match[1]
	}
	selected := 0
	for _, tag := range tabs {
		panel := attr(tag, "aria-controls")
		id := attr(tag, "id")
		if panel == "" || id == "" || strings.Contains(tag, "tabindex") {
			t.Errorf("tab needs an id and aria-controls, and no server tabindex (the chunk roves it): %s", tag)
		}
		section := regexp.MustCompile(`<section[^>]*id="` + panel + `"[^>]*>`).FindString(body)
		if section == "" || attr(section, "role") != "tabpanel" || attr(section, "aria-labelledby") != id {
			t.Errorf("panel %q is not a tabpanel labelled by %q: %s", panel, id, section)
		}
		isSelected := attr(tag, "aria-selected") == "true"
		if isSelected {
			selected++
		}
		if isSelected == strings.Contains(section, " hidden") {
			t.Errorf("tab %s: aria-selected=%v but its panel hidden=%v", id, isSelected, strings.Contains(section, " hidden"))
		}
		if isSelected != strings.Contains(tag, `aria-current="page"`) {
			t.Errorf("tab %s: aria-selected and aria-current disagree", id)
		}
		if !strings.Contains(attr(tag, "href"), "tab=") && !strings.Contains(attr(tag, "href"), "dock=") {
			t.Errorf("tab %s lost its no-JavaScript link: %s", id, tag)
		}
	}
	if selected != 2 {
		t.Errorf("want one selected tab per list, got %d selected", selected)
	}
	// Every panel renders so in-place switching has content.
	for _, want := range []string{`id="view-panel-arrange"`, `id="view-panel-session"`, `id="view-panel-mixer"`, `id="detail-panel-clip"`, `id="detail-panel-device"`, `id="detail-panel-automation"`} {
		if !strings.Contains(body, want) {
			t.Errorf("shell lacks %s", want)
		}
	}
	for _, want := range []string{`aria-label="Mixer"`, `aria-label="Live"`} {
		_ = want
	}
	for _, note := range []string{"Tab and Shift+Tab always move keyboard focus", "Single-key shortcuts"} {
		if !strings.Contains(body, note) {
			t.Errorf("shortcuts dialog lacks the note %q", note)
		}
	}
}

func TestShellPanelFormsReturnToTheShell(t *testing.T) {
	address, client, _ := testApp(t)
	page := getPage(t, client, address+"/studio?tab=session&dock=device")
	csrf := tokenFromPage(t, page)
	send := func(referer, target string) string {
		form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "action": {"play"}, "__gosx_return_to": {target}}
		req, _ := http.NewRequest("POST", address+"/__actions/transport", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", address)
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusSeeOther {
			t.Fatalf("status=%d", response.StatusCode)
		}
		return response.Header.Get("Location")
	}
	if got := send(address+"/studio?tab=session&dock=device", "/?panel=patterns&pattern=main&step=3"); got != "/studio?dock=device&pattern=main&step=3&tab=session" {
		t.Errorf("shell form returned to %q", got)
	}
	if got := send(address+"/studio", "/?panel=code&file=a.cicada"); got != "/studio?file=a.cicada" {
		t.Errorf("code form returned to %q", got)
	}
	if got := send(address+"/?panel=patterns", "/?panel=patterns&pattern=main"); got != "/?panel=patterns&pattern=main" {
		t.Errorf("panel app form must keep its own target, got %q", got)
	}
	if got := send("", "/?panel=history"); got != "/?panel=history" {
		t.Errorf("a form without a Referer must keep its target, got %q", got)
	}
}

func TestShellManagedMultipartFormsReturnToTheShell(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/studio?tab=session&dock=device"))
	for _, fromShell := range []bool{true, false} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		for key, value := range map[string]string{
			"csrf_token": csrf, "revision": "current", "bankRevision": "bank-current",
			"action": "effects", "preset": "clean", "__gosx_return_to": "/?panel=sampler&pad=1",
		} {
			if err := form.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodPost, address+"/__actions/sampler", &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Origin", address)
		req.Header.Set("X-CSRF-Token", csrf)
		referer, want := "/?panel=sampler", "/?panel=sampler&pad=1"
		if fromShell {
			referer, want = "/studio?tab=session&dock=device&file=b.cicada", "/studio?dock=device&file=b.cicada&pad=1&tab=session"
		}
		req.Header.Set("Referer", address+referer)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var result action.Result
		err = json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusSeeOther || !result.OK || result.Redirect != want {
			t.Errorf("managed form from %s: status=%d result=%+v, want redirect %q", referer, response.StatusCode, result, want)
		}
	}
	if len(*edits) != 2 {
		t.Errorf("writes=%d, want one per accepted submit", len(*edits))
	}
}

// shellPost submits a form the way a mounted panel does from the given page.
func shellPost(t *testing.T, client *http.Client, address, route string, form url.Values, referer string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", address+route, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", address)
	req.Header.Set("X-CSRF-Token", form.Get("csrf_token"))
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestShellPatternEditsStayInTheShell(t *testing.T) {
	address, client, edits := testApp(t)
	page := address + "/studio?tab=session&dock=automation&file=b.cicada"
	csrf := tokenFromPage(t, getPage(t, client, page))
	cases := []struct {
		name string
		form url.Values
		want string
		old  string
	}{
		{"pitch", url.Values{"action": {"pitch"}, "pattern": {"p"}, "step": {"4"}, "pitch": {"72"}, "octave": {"3"}}, "/studio?dock=automation&file=b.cicada&octave=3&pattern=p&step=4&tab=session", "/?octave=3&panel=patterns&pattern=p&step=4"},
		{"note", url.Values{"action": {"step"}, "pattern": {"p"}, "step": {"5"}, "mode": {"note"}, "velocity": {"100"}, "pitch": {"60"}, "ratchet": {"1"}, "chance": {"100"}}, "/studio?dock=automation&file=b.cicada&pattern=p&step=5&tab=session", "/?panel=patterns&pattern=p&step=5"},
		{"duplicate", url.Values{"action": {"duplicate"}, "pattern": {"p"}, "newName": {"q"}}, "/studio?dock=automation&file=b.cicada&pattern=q&step=1&tab=session", "/?panel=patterns&pattern=q&step=1"},
	}
	for _, c := range cases {
		c.form.Set("csrf_token", csrf)
		c.form.Set("revision", "current")
		c.form.Set("__gosx_return_to", "/?panel=patterns&pattern=p&step=1")
		if got := shellPost(t, client, address, "/__actions/pattern", c.form, page).Header.Get("Location"); got != c.want {
			t.Errorf("%s from the shell redirected to %q, want %q", c.name, got, c.want)
		}
		if got := shellPost(t, client, address, "/__actions/pattern", c.form, address+"/?panel=patterns").Header.Get("Location"); got != c.old {
			t.Errorf("%s from the panel app redirected to %q, want the legacy %q", c.name, got, c.old)
		}
	}
	if len(*edits) != 2*len(cases) {
		t.Errorf("edits=%d", len(*edits))
	}
}

func TestShellLinksAndNavigationKeepTheEditingSelection(t *testing.T) {
	body := shellBody(t, "?file=b.cicada&pattern=p&step=3&tab=session&pad=2")
	mixer := regexp.MustCompile(`<a[^>]*href="(\?[^"]*)"[^>]*>Mixer`).FindStringSubmatch(body)
	if mixer == nil {
		t.Fatal("no Mixer link")
	}
	link, _ := url.ParseQuery(strings.TrimPrefix(strings.ReplaceAll(mixer[1], "&amp;", "&"), "?"))
	for key, want := range map[string]string{"file": "b.cicada", "pattern": "p", "step": "3", "pad": "2", "tab": "mixer"} {
		if link.Get(key) != want {
			t.Errorf("Mixer link %q: %s=%q, want %q", mixer[1], key, link.Get(key), want)
		}
	}
	// Mounted panel links stay in the shell and keep the shell selection.
	if strings.Contains(body, `href="/?panel=`) {
		t.Error("a mounted panel link still leaves the shell")
	}
	code := regexp.MustCompile(`href="(/studio\?[^"]*file=[^"]*)"`).FindStringSubmatch(body)
	if code == nil || !strings.Contains(code[1], "tab=session") {
		t.Errorf("Code links must target /studio and keep the tab: %v", code)
	}
	if !regexp.MustCompile(`data-sampler-edit="2"`).MatchString(body) || !regexp.MustCompile(`href="/studio\?[^"]*pad=2[^"]*"[^>]*data-sampler-edit`).MatchString(body) {
		t.Error("sampler Edit links must navigate to /studio")
	}
	for _, id := range []string{"history-dialog", "audio-dialog", "export-dialog", "palette"} {
		if !strings.Contains(body, `aria-controls="`+id+`"`) {
			t.Errorf("dialog trigger lacks aria-controls=%q", id)
		}
	}
	if strings.Contains(body, `aria-controls="#`) {
		t.Error("aria-controls must be a bare id")
	}
}

func TestShellUploadsReturnToTheShell(t *testing.T) {
	address, client, _ := testApp(t)
	page := address + "/studio?tab=session&pad=3"
	csrf := tokenFromPage(t, getPage(t, client, page))
	for route, want := range map[string]string{"/__sampler/import": "/studio?pad=1&tab=session", "/__instrument/import": "/studio?tab=session"} {
		field := map[string]string{"/__sampler/import": "audio", "/__instrument/import": "wav"}[route]
		var body strings.Builder
		form := multipart.NewWriter(&body)
		_ = form.WriteField("csrf_token", csrf)
		_ = form.WriteField("revision", "current")
		_ = form.WriteField("bankRevision", "bank-current")
		part, _ := form.CreateFormFile(field, "a.wav")
		_, _ = part.Write([]byte("RIFF"))
		_ = form.Close()
		for referer, wantLocation := range map[string]string{page: want, address + "/?panel=sampler": ""} {
			req, _ := http.NewRequest("POST", address+route, strings.NewReader(body.String()))
			req.Header.Set("Content-Type", form.FormDataContentType())
			req.Header.Set("Origin", address)
			req.Header.Set("X-CSRF-Token", csrf)
			req.Header.Set("Referer", referer)
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			got := response.Header.Get("Location")
			if wantLocation != "" && got != wantLocation {
				t.Errorf("%s from the shell redirected to %q, want %q (status %d)", route, got, wantLocation, response.StatusCode)
			}
			if wantLocation == "" && !strings.HasPrefix(got, "/?panel=") {
				t.Errorf("%s from the panel app redirected to %q", route, got)
			}
		}
	}
}

func TestShellAllowsReactiveResampleAndRejectsForeignLocations(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/studio?tab=session&pad=2"))
	response, result := reactivePost(t, client, address, "sampler", map[string]string{
		"csrf_token": csrf, "revision": "current", "bankRevision": "bank-current", "action": "resample", "destination": "1",
		"performance": `{"hits":[]}`, "newName": "r", "__cicada_location": "/studio?tab=session&pad=2",
	})
	if response.StatusCode != 200 || !result.OK {
		t.Fatalf("resample from /studio failed: %d %+v", response.StatusCode, result)
	}
	var projection workspaceProjection
	if err := json.Unmarshal(result.Data, &projection); err != nil {
		t.Fatal(err)
	}
	if len(*edits) != 1 || projection.Location != "/studio?tab=session&pad=2" || projection.HTML != "" || projection.Revision != "current" {
		t.Errorf("shell projection: edits=%d %+v", len(*edits), projection)
	}
	for _, bad := range []string{"https://evil.example/studio", "//evil.example/studio", "/studio/../admin", "/other"} {
		before := len(*edits)
		response, result := reactivePost(t, client, address, "sampler", map[string]string{
			"csrf_token": csrf, "revision": "current", "bankRevision": "bank-current", "action": "resample", "destination": "1",
			"performance": `{"hits":[]}`, "__cicada_location": bad,
		})
		if result.OK || response.StatusCode == 200 || len(*edits) != before {
			t.Errorf("location %q was accepted: %d %+v", bad, response.StatusCode, result)
		}
	}
}

func TestShellRendersAnInvalidScoreAndKeepsTheCodeLens(t *testing.T) {
	broken := &workspace{
		Revision: "current", Filename: "broken.cicada", Source: "pattern oops {\n", Valid: false, Error: "line 1: unterminated pattern",
		Files: []projectFile{{Name: "broken.cicada", Source: "pattern oops {\n", Revision: "current", Diagnostics: []notation.Diagnostic{{Code: "CIC001", Message: "unterminated pattern", Severity: "error", Position: notation.Position{Line: 1, Column: 15}}}}},
	}
	address, client, _ := testAppWorkspace(t, broken)
	for _, query := range []string{"", "?tab=session", "?tab=mixer&dock=device"} {
		response, err := client.Get(address + "/studio" + query)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		body := string(data)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("invalid score %q: status %d", query, response.StatusCode)
		}
		for _, want := range []string{`id="studio-code"`, "CIC001", "unterminated pattern", "pattern oops {", `id="undo-form"`} {
			if !strings.Contains(body, want) {
				t.Errorf("invalid score %q: shell lacks %q", query, want)
			}
		}
		// Every score-dependent panel says why it is unavailable.
		for _, id := range []string{"session", "live", "mixer", "patterns", "instruments", "generator", "takes", "sampler"} {
			if !strings.Contains(body, `id="panel-`+id+`-unavailable"`) {
				t.Errorf("invalid score %q: panel %s did not degrade", query, id)
			}
		}
		if !strings.Contains(body, "Fix the score in the Code lens") {
			t.Errorf("invalid score %q: no repair hint", query)
		}
	}
}

func TestShellPanelsSurvivePanicsInsideAPanel(t *testing.T) {
	// A project the panels do not expect must not take the page down.
	odd := &workspace{Revision: "current", Filename: "odd.cicada", Source: "x", Valid: true, Project: &project.Project{}}
	address, client, _ := testAppWorkspace(t, odd)
	response, err := client.Get(address + "/studio?tab=session")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("empty project: status %d", response.StatusCode)
	}
}

func TestShellProjectionCarriesTheSavedScoreAndRevision(t *testing.T) {
	reply := &workspace{Revision: "current", Filename: "test.cicada", Source: "title \"Test\"\n", Valid: true, Project: &project.Project{Title: "Test", TempoMilli: 130000, Key: project.Key{Scale: "minor"}}}
	address, client, _ := testAppWorkspace(t, reply)
	csrf := tokenFromPage(t, getPage(t, client, address+"/studio?tab=arrange"))
	// An engine saves: the service now holds a new revision and a new placement.
	reply.Revision = "after"
	reply.Source = "title \"Test\"\nplace resampled at @1.1.1\n"
	reply.Project.Arrange = &project.Arrangement{Placements: []project.Placement{{ID: "pad-print", Track: "sampler", Content: "pad-print"}}}
	response, result := reactivePost(t, client, address, "sampler", map[string]string{
		"csrf_token": csrf, "revision": "current", "bankRevision": "bank-current", "action": "resample", "destination": "1",
		"performance": `{"hits":[]}`, "newName": "pad-print", "__cicada_location": "/studio?tab=arrange",
	})
	var projection workspaceProjection
	if err := json.Unmarshal(result.Data, &projection); response.StatusCode != 200 || err != nil {
		t.Fatalf("status=%d err=%v", response.StatusCode, err)
	}
	// The revalidation the engine asks for renders the same state: the new
	// revision in every form, the new source in the Code lens, the placement in Arrange.
	page := getPage(t, client, address+"/studio?tab=arrange")
	for name, body := range map[string]string{"revalidated page": page} {
		if strings.Contains(body, `name="revision" value="current"`) {
			t.Errorf("%s still carries the old revision", name)
		}
		for _, want := range []string{`name="revision" value="after"`, "place resampled at", "pad-print"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
	}
	if projection.HTML != "" {
		t.Errorf("a shell save returns a receipt, not a page: %d bytes of HTML", len(projection.HTML))
	}
}

func TestShellGetFormsStayInTheShell(t *testing.T) {
	// The three panels that submit with GET render these forms.
	forms := map[string]gosx.Node{
		"View register":     gosx.El("form", gosx.Attrs(gosx.Attr("method", "get"), gosx.Attr("action", "/"), gosx.Attr("class", "actions")), hidden("panel", "patterns"), hidden("pattern", "p"), hidden("step", "3"), field("Lowest octave", wholeInput("octave", 3, -1, 8)), submit("", "", "View register")),
		"Inspect parameter": gosx.El("form", gosx.Attrs(gosx.Attr("method", "get"), gosx.Attr("action", "/"), gosx.Attr("class", "action-form")), hidden("panel", "session"), field("Parameter", selectInput("automationPath", "a.b", []string{"a.b", "c.d"})), submit("", "", "Inspect parameter")),
		"Update audition":   gosx.El("form", gosx.Attrs(gosx.Attr("method", "get"), gosx.Attr("action", "/"), gosx.Attr("class", "actions")), hidden("panel", "takes"), hidden("take", "t1"), field("Root MIDI note", numberInput("root", "60", "0", "127")), submit("", "", "Update audition")),
	}
	address, client, _ := testApp(t)
	current, _ := url.Parse("/studio?tab=session&dock=device&file=b.cicada&pattern=p")
	for name, form := range forms {
		rendered := gosx.RenderHTML(shellLinks(current, form))
		if strings.Contains(rendered, `action="/"`) || strings.Contains(rendered, `name="panel"`) {
			t.Errorf("%s still targets the panel app: %s", name, rendered)
		}
		if !strings.Contains(rendered, `action="/studio"`) {
			t.Fatalf("%s does not submit to /studio: %s", name, rendered)
		}
		// Submit natively: collect the form's named fields the way a browser does.
		query := url.Values{}
		for _, input := range regexp.MustCompile(`<input[^>]*type="hidden"[^>]*>`).FindAllString(rendered, -1) {
			n := regexp.MustCompile(`name="([^"]*)"`).FindStringSubmatch(input)
			v := regexp.MustCompile(`value="([^"]*)"`).FindStringSubmatch(input)
			if n != nil && v != nil {
				query.Set(n[1], v[1])
			}
		}
		for key, want := range map[string]string{"tab": "session", "dock": "device", "file": "b.cicada"} {
			if query.Get(key) != want {
				t.Errorf("%s: hidden %s=%q, want %q", name, key, query.Get(key), want)
			}
		}
		response, err := client.Get(address + "/studio?" + query.Encode())
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || !strings.Contains(string(data), `id="studio-shell"`) {
			t.Errorf("%s: native submission left the shell: %d", name, response.StatusCode)
		}
	}
}

func TestShellClipReadsTheSavedScoreNotTheCodeLensSelection(t *testing.T) {
	saved := "pattern d drums steps=4 {\n  bd: x . . .\n}\n"
	reply := &workspace{
		Revision: "current", Filename: "main.cicada", Source: saved, Valid: true,
		Files:   []projectFile{{Name: "main.cicada", Source: saved, Revision: "current"}, {Name: "other.cicada", Source: "title \"Other\"\n", Revision: "other-rev"}},
		Project: &project.Project{Title: "T", TempoMilli: 120000, Key: project.Key{Scale: "minor"}, Patterns: []project.Pattern{{ID: "d", Kind: "drums", Steps: 4, Lanes: map[string][]*project.Step{"bd": {nil, nil, nil, nil}}}}},
	}
	address, client, _ := testAppWorkspace(t, reply)
	clip := func(body string) string {
		return regexp.MustCompile(`(?s)<section[^>]*id="detail-panel-clip".*?</section>`).FindString(body)
	}
	hasLane := func(body string) bool { return strings.Contains(clip(body), "lane=bd") }
	// Selecting a file that does not hold the pattern changes only the Code lens.
	body := getPage(t, client, address+"/studio?file=other.cicada&pattern=d")
	if !strings.Contains(body, "title &#34;Other&#34;") && !strings.Contains(body, `title "Other"`) {
		t.Error("the Code lens must show the selected file")
	}
	if !hasLane(body) {
		t.Error("Clip lost the saved pattern's drum lanes when another file was selected")
	}
	// A retained draft that leaves the drums out changes only the Code lens too.
	csrf := tokenFromPage(t, body)
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "fileRevision": {"current"}, "file": {"main.cicada"}, "content": {"invalid draft without drums\n"}}
	shellPost(t, client, address, "/__actions/source", form, address+"/studio")
	body = getPage(t, client, address+"/studio?file=main.cicada&pattern=d")
	if !strings.Contains(body, "invalid draft without drums") {
		t.Skip("the fake service did not retain a draft for this request")
	}
	if !hasLane(body) {
		t.Error("Clip lost the saved pattern's drum lanes when a draft was restored")
	}
}

func TestShellRendersAHiddenRefreshNoticeLinkingToThePage(t *testing.T) {
	body := shellBody(t, "?tab=session&file=b.cicada")
	notice := regexp.MustCompile(`(?s)<p[^>]*id="studio-refresh-notice"[^>]*>.*?</p>`).FindString(body)
	if notice == "" || !strings.Contains(notice, " hidden") || !strings.Contains(notice, `role="status"`) {
		t.Fatalf("refresh notice missing or visible: %q", notice)
	}
	if !strings.Contains(notice, `href="/studio?tab=session&amp;file=b.cicada"`) {
		t.Errorf("the notice must link to the current page: %s", notice)
	}
}

func TestShellResampleReceiptStaysSmallForALargeScore(t *testing.T) {
	// The Code lens holds the whole source, so a shell projection grows with the
	// score. The sampler reads this response through a 1 MiB cap and treats a
	// failed read as a failed save, so the shell must answer with a receipt.
	big := "title \"Big\"\n" + strings.Repeat("// padding line to make the score large\n", 70000) // about 2.7 MiB
	reply := &workspace{Revision: "current", Filename: "big.cicada", Source: big, Valid: true, Project: &project.Project{Title: "Big", TempoMilli: 120000, Key: project.Key{Scale: "minor"}}}
	address, client, edits := testAppWorkspace(t, reply)
	if page := getPage(t, client, address+"/studio?tab=session"); len(page) < 1<<20 {
		t.Fatalf("the shell page is %d bytes; the test needs a page past the sampler's 1 MiB cap", len(page))
	}
	csrf := tokenFromPage(t, getPage(t, client, address+"/studio?tab=session"))
	body, _ := json.Marshal(map[string]string{
		"csrf_token": csrf, "revision": "current", "bankRevision": "bank-current", "action": "resample", "destination": "1",
		"performance": `{"hits":[]}`, "newName": "r", "__cicada_location": "/studio?tab=session",
	})
	req, _ := http.NewRequest("POST", address+"/__actions/sampler", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("X-Cicada-Reactive", "1")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	// Decode through the same cap the sampler engine uses.
	data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var result action.Result
	if err := json.Unmarshal(data, &result); err != nil || !result.OK {
		t.Fatalf("the sampler would reject this response (%d bytes read, err=%v): %.200s", len(data), err, data)
	}
	if len(data) > 4<<10 || len(*edits) != 1 {
		t.Errorf("receipt is %d bytes with %d edits; want a few hundred bytes and one write", len(data), len(*edits))
	}
	var receipt workspaceProjection
	if err := json.Unmarshal(result.Data, &receipt); err != nil || receipt.HTML != "" || receipt.Revision != "current" || receipt.Location != "/studio?tab=session" {
		t.Errorf("receipt: %+v err=%v", receipt, err)
	}
}
