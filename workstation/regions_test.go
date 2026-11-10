package main

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestRegionDigestBlanksOnlyTheFiveRevisionFields(t *testing.T) {
	body := func(values map[string]string) string {
		field := func(name string) string { return values[name] }
		return regionBody(`<form method="post" action="/__actions/pattern">` +
			`<input type="hidden" name="csrf_token" value="` + field("csrf_token") + `" />` +
			`<input type="hidden" name="revision" value="` + field("revision") + `" />` +
			`<input name="source_revision" type="hidden" value="` + field("source_revision") + `">` +
			`<input type="hidden" value="` + field("fileRevision") + `" name="fileRevision">` +
			`<input type="hidden" name="diskRevision" value="` + field("diskRevision") + `"/>` +
			`<input type="hidden" name="__gosx_return_to" value="` + field("__gosx_return_to") + `" />` +
			`<input type="hidden" name="file" value="` + field("file") + `" />` +
			`<input type="text" name="revision" value="` + field("visible") + `" />` +
			`<input type="hidden" name="revisions" value="` + field("revisions") + `" />` +
			`<button type="submit" data-value="` + field("button") + `" value="4">` + field("text") + `</button></form>`)
	}
	base := map[string]string{"csrf_token": "masked:one", "revision": "r1", "source_revision": "s1", "fileRevision": "f1", "diskRevision": "d1",
		"__gosx_return_to": "/studio?step=1", "file": "main.cicada", "visible": "typed", "revisions": "x", "button": "a", "text": "●"}
	digest := regionDigest(body(base))
	if len(digest) != 16 {
		t.Fatalf("digest %q is not a 16-digit prefix", digest)
	}
	with := func(name, value string) string {
		changed := map[string]string{}
		for key, v := range base {
			changed[key] = v
		}
		changed[name] = value
		return regionDigest(body(changed))
	}
	// The CSRF token is masked again for each response, and the revision fields
	// advance under rule R1, so neither may make a region look changed.
	for _, name := range []string{"csrf_token", "revision", "source_revision", "fileRevision", "diskRevision"} {
		if got := with(name, "changed-value"); got != digest {
			t.Errorf("changing hidden %s changed the digest", name)
		}
	}
	// Any other change is content the page must receive.
	for _, name := range []string{"__gosx_return_to", "file", "visible", "revisions", "button", "text"} {
		if got := with(name, "changed-value"); got == digest {
			t.Errorf("changing %s left the digest unchanged", name)
		}
	}
	if regionDigest(body(base)+" ") == digest || regionDigest(strings.Replace(body(base), `action="/__actions/pattern"`, `action="/__actions/arrange"`, 1)) == digest {
		t.Error("a markup change outside the inputs left the digest unchanged")
	}
	// A tag that only starts like <input is not an input.
	inputmode := regionBody(`<inputmode type="hidden" name="revision" value="a">`)
	if regionDigest(inputmode) == regionDigest(strings.Replace(inputmode, `value="a"`, `value="b"`, 1)) {
		t.Error("an element named like input was treated as an input")
	}
}

// regionElements finds the update-region wrappers of a rendered page.
func regionElements(t *testing.T, page string) []*html.Node {
	t.Helper()
	document, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	var found []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			for _, attr := range node.Attr {
				if attr.Key == "data-update-region" {
					found = append(found, node)
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return found
}

func attr(node *html.Node, name string) string {
	for _, a := range node.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func TestShellUpdateRegionsAreUniqueWrappersWithOneElementChild(t *testing.T) {
	address, client, _ := testApp(t)
	for _, query := range []string{"", "?tab=session&dock=device", "?tab=mixer&dock=automation&region=code", "?pattern=p&step=3"} {
		page := getPage(t, client, address+"/studio"+query)
		regions := regionElements(t, page)
		seen := map[string]bool{}
		for _, region := range regions {
			id := attr(region, "data-update-region")
			if seen[id] {
				t.Errorf("%q: region %s appears twice", query, id)
			}
			seen[id] = true
			if attr(region, "id") != "update-"+id || attr(region, "data-region-revision") != "current" || len(attr(region, "data-region-digest")) != 16 {
				t.Errorf("%q: region %s wrapper: id=%q revision=%q digest=%q", query, id, attr(region, "id"), attr(region, "data-region-revision"), attr(region, "data-region-digest"))
			}
			// GoSX's patch receiver patches the first element child of the
			// element it finds by id, so the wrapper has exactly one.
			var children []*html.Node
			for child := region.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == html.ElementNode {
					children = append(children, child)
				} else if child.Type == html.TextNode && strings.TrimSpace(child.Data) != "" {
					t.Errorf("%q: region %s has text outside its body", query, id)
				}
			}
			if len(children) != 1 || attr(children[0], "class") != "update-region__body" {
				t.Errorf("%q: region %s has %d element children", query, id, len(children))
			}
			for parent := region.Parent; parent != nil; parent = parent.Parent {
				if parent.Type == html.ElementNode && attr(parent, "data-update-region") != "" {
					t.Errorf("%q: region %s is nested in %s", query, id, attr(parent, "data-update-region"))
				}
			}
		}
		for _, id := range shellRegionIDs {
			if !seen[id] {
				t.Errorf("%q: region %s is missing", query, id)
			}
		}
		if len(seen) != len(shellRegionIDs) {
			t.Errorf("%q: %d regions on the page, %d declared", query, len(seen), len(shellRegionIDs))
		}
	}
}

func TestShellRegionDigestsIgnoreTheRemaskedToken(t *testing.T) {
	address, client, _ := testApp(t)
	digests := func() (map[string]string, string) {
		page := getPage(t, client, address+"/studio?tab=session")
		out := map[string]string{}
		for _, region := range regionElements(t, page) {
			out[attr(region, "data-update-region")] = attr(region, "data-region-digest")
		}
		return out, tokenFromPage(t, page)
	}
	first, firstToken := digests()
	second, secondToken := digests()
	if firstToken == secondToken {
		t.Skip("the session did not mask the token again; nothing to compare")
	}
	for id, digest := range first {
		if second[id] != digest {
			t.Errorf("region %s changed digest between two renders of the same state", id)
		}
	}
}

func TestShellBarAndStepReadoutStaysOutsideEveryRegion(t *testing.T) {
	// The readout is a GoSX live binding. A server snapshot inside a region
	// would move it backwards during playback.
	address, client, _ := testApp(t)
	page := getPage(t, client, address+"/studio")
	if !strings.Contains(page, `data-gosx-live-src="/api/transport"`) {
		t.Fatal("the shell lost its live position readout")
	}
	for _, region := range regionElements(t, page) {
		var walk func(*html.Node)
		walk = func(node *html.Node) {
			if node.Type == html.ElementNode && attr(node, "data-gosx-live-src") == "/api/transport" {
				t.Errorf("region %s holds the live position readout", attr(region, "data-update-region"))
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
		walk(region)
	}
}
