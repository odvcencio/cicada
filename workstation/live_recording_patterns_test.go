package main

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestLiveOffersUnusedRecordingPatternsWithoutAddingLaunchTargets(t *testing.T) {
	score, ds := notation.Parse([]byte("cicada 2\ntrack keys piano {}\ntrack other piano {}\ntrack drums drums {}\npattern playing notes { c4 . }\npattern foreign notes { d4 . }\npattern fresh notes { . . }\npattern beat drums { bd: .... }\nscene main { keys=playing other=foreign }\nsong { main }\n"))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", Valid: true, Project: p})
	page := getPage(t, client, address+"/?panel=live")
	selected := regexp.MustCompile(`(?s)<select[^>]*data-live-pattern[^>]*>(.*?)</select>`).FindStringSubmatch(page)
	if len(selected) != 2 || !strings.Contains(selected[1], `value="fresh">fresh · library</option>`) || strings.Contains(selected[1], `value="foreign"`) || strings.Contains(selected[1], `value="beat"`) {
		t.Fatal("recording destinations are missing or incompatible")
	}
	if strings.Contains(page, "slot:keys:fresh") {
		t.Fatal("unbound recording target became a launch command")
	}
	for _, copy := range []string{"unused compatible pattern", "data-live-destination-hint", `data-gosx-key="create-pattern-@live"`} {
		if !strings.Contains(page, copy) {
			t.Fatalf("missing Live creation/help: %q", copy)
		}
	}
}

func TestLivePatternCreationReturnsToRecordingContext(t *testing.T) {
	p := &project.Project{Tracks: []project.Track{{ID: "keys", Kind: "piano"}}}
	address, client, edits := testAppWorkspace(t, &workspace{Revision: "current", Valid: true, Project: p})
	page := getPage(t, client, address+"/studio?tab=session")
	form := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "action": {"create"}, "createFor": {"@live"}, "newName": {"fresh"}, "track": {"keys"}, "scene": {""}, "length": {"16"}, "starter": {"empty"}, "__gosx_return_to": {"/studio?tab=session"}}
	response := post(t, client, address+"/__actions/pattern", form, false, address)
	defer response.Body.Close()
	target, err := url.Parse(response.Header.Get("Location"))
	if err != nil || target.Path != "/studio" || target.Query().Get("tab") != "session" || target.Query().Get("region") != "" && target.Query().Get("region") != "center" || target.Query().Get("dock") == "clip" {
		t.Fatal("creation left the recording context", response.StatusCode, response.Header.Get("Location"))
	}
	if len(*edits) != 1 || (*edits)[0]["scene"] != "" {
		t.Fatal("Live creation assigned a scene", *edits)
	}
	form.Set("length", "66")
	failed := post(t, client, address+"/__actions/pattern", form, false, address)
	defer failed.Body.Close()
	page = getPage(t, client, address+failed.Header.Get("Location"))
	match := regexp.MustCompile(`(?s)data-gosx-key="create-pattern-@live".*?</details>`).FindString(page)
	if !strings.Contains(match, `name="newName" value="fresh"`) || !strings.Contains(match, `name="length" value="66"`) || !strings.Contains(page, "pattern length must be 1–64 steps") || len(*edits) != 1 {
		t.Fatal("Live validation lost input or changed the domain")
	}
}
