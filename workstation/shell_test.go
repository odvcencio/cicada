package main

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

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
		"transport.toggle": "Space", "transport.home": "Home", "view.session": "Tab", "dock.toggle": "Shift+Tab",
		"edit.undo": "Mod+Z", "edit.redo": "Mod+Shift+Z", "palette.open": "Mod+K,?", "arrange.consolidate": "Mod+J",
		"live.scene.1": "F1", "live.scene.8": "F8", "transport.record": "F9", "tool.draw": "B", "select.left": "ArrowLeft",
	} {
		if got[id].ID == "" || strings.Join(got[id].Keys, ",") != keys {
			t.Errorf("command %s keys=%v, want %s", id, got[id].Keys, keys)
		}
	}
	for _, id := range []string{"transport.resume", "transport.loop", "edit.split", "zoom.selection", "grid.snap", "arrange.consolidate", "arrange.group", "notes.quantize"} {
		if !got[id].Reserved {
			t.Errorf("command %s must stay reserved", id)
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
