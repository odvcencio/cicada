package main

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestSecondaryGridSourcesShowLanesPhrasesAndDisplayedReceipts(t *testing.T) {
	files := []projectFile{
		{Name: "main.cicada", Source: "cicada 2\ntrack drums drums {}\ntrack lead acid {}\nscene main { drums=beat lead=line }\nsong { main*2 }\n"},
		{Name: "drums.cicada", Source: "// authored lane order\npattern beat drums steps=4 { sd: .... bd: x... }\n"},
		{Name: "notes.cicada", Source: "pattern line acid steps=4 { use motif }\n"},
		{Name: "phrases.cicada", Source: "phrase motif { c4 . e4 . }\n"},
	}
	var sources []notation.SourceFile
	for _, f := range files {
		sources = append(sources, notation.SourceFile{Path: f.Name, Source: []byte(f.Source)})
	}
	score, ds := notation.ParseFiles(sources, 2)
	if score == nil || hasGridErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || hasGridErrors(ds) {
		t.Fatal(ds)
	}
	v := &workspace{Files: files, Source: files[0].Source, Revision: "current", SourceRevision: "whole-project", Filename: "main.cicada", Edition: 2, Valid: true, Project: p}
	address, client, _ := testAppWorkspace(t, v)
	body := getPage(t, client, address+"/studio?pattern=beat&dock=clip&lane=sd")
	sd, bd := strings.Index(body, `aria-label="beat sd step 1"`), strings.Index(body, `aria-label="beat bd step 1"`)
	if sd < 0 || bd < sd {
		t.Fatalf("secondary drum lanes lost their authored order: sd=%d bd=%d", sd, bd)
	}
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	var visit func(*html.Node, bool)
	visit = func(n *html.Node, patterns bool) {
		for _, a := range n.Attr {
			if a.Key == "id" && a.Val == "patterns" {
				patterns = true
			}
		}
		if patterns && n.Type == html.ElementNode && n.Data == "form" {
			mutation := false
			for _, a := range n.Attr {
				if a.Key == "action" && a.Val == "/__actions/pattern" {
					mutation = true
				}
			}
			if mutation {
				found, operation, hasStatus := false, "", false
				var fields func(*html.Node)
				fields = func(n *html.Node) {
					name, value := "", ""
					for _, a := range n.Attr {
						if a.Key == "name" {
							name = a.Val
						}
						if a.Key == "value" {
							value = a.Val
						}
						if a.Key == "class" && a.Val == "form-status" {
							hasStatus = true
						}
					}
					if name == "source_revision" && value == v.SourceRevision {
						found = true
					}
					if name == "action" {
						operation = value
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						fields(c)
					}
				}
				fields(n)
				if operation == "create" || operation == "duplicate" || operation == "bind" {
					return
				}
				if !found {
					t.Fatal("a grid mutation omitted the displayed full-project receipt")
				}
				switch operation {
				case "step", "chord", "settings", "range", "resize":
					if !hasStatus {
						t.Fatal("editable grid fields lack form-local feedback", operation)
					}
				}
				checked++
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c, patterns)
		}
	}
	visit(doc, false)
	if checked < 5 {
		t.Fatal("test did not reach the grid editing forms", checked)
	}
	body = getPage(t, client, address+"/studio?pattern=line&dock=clip")
	if !strings.Contains(body, "This pattern uses shared phrases.") {
		t.Fatal("cross-file phrase relationship disappeared from the editor")
	}
}

func hasGridErrors(ds []notation.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}

func TestGridActionsForwardTheDisplayedProjectReceipt(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=patterns"))
	for _, operation := range []string{"toggle", "pitch", "step", "settings", "range", "resize", "chord"} {
		values := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "source_revision": {"displayed-project"}, "action": {operation}, "pattern": {"p"}, "step": {"1"}, "pitch": {"60"}, "mode": {"note"}, "ratchet": {"1"}, "chance": {"100"}, "swing": {"55"}, "gate": {"65"}, "transpose": {"0"}, "operation": {"reverse"}, "first": {"1"}, "last": {"4"}, "target": {"5"}, "amount": {"1"}, "length": {"8"}, "chord_root": {"0"}, "chord_octave": {"4"}, "chord_inversion": {"0"}, "chord_shape": {"major"}, "hold": {"2"}}
		r := post(t, client, address+"/__actions/pattern", values, false, address)
		r.Body.Close()
		if r.StatusCode != 303 || len(*edits) == 0 || (*edits)[len(*edits)-1]["source_revision"] != "displayed-project" {
			t.Fatal("grid action lost its displayed receipt", operation, r.StatusCode, *edits)
		}
	}
}
