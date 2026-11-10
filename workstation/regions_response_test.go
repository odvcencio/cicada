package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// pageDigests reads the digest of every update region on a /studio page.
func pageDigests(t *testing.T, page string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, region := range regionElements(t, page) {
		out[attr(region, "data-update-region")] = attr(region, "data-region-digest")
	}
	return out
}

func TestShellEditReturnsTheChangedRegionsAndNamesTheUnchanged(t *testing.T) {
	address, client, edits := testApp(t)
	location := "/studio?tab=arrange&dock=clip"
	page := getPage(t, client, address+location)
	digests := pageDigests(t, page)
	// The browser shows Clip and the Undo forms as rendered, and an older
	// render of the status bar.
	request := "dock-clip:" + digests["dock-clip"] + ",actions:" + digests["actions"] + ",status:0000000000000000"
	response, result := reactivePost(t, client, address, "pattern", map[string]string{
		"csrf_token": tokenFromPage(t, page), "revision": "current", "action": "range", "pattern": "p",
		"operation": "clear", "first": "1", "last": "1", "target": "1", "amount": "0",
		"__cicada_location": location, "__cicada_regions": request,
	})
	if response.StatusCode != 200 || !result.OK || len(*edits) != 1 {
		t.Fatalf("edit failed: %d %+v edits=%d", response.StatusCode, result, len(*edits))
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"revision", "sourceRevision", "fileRevision", "writeRevision", "location", "title", "regions", "unchanged"} {
		if _, ok := data[key]; !ok {
			t.Errorf("response lacks %q: %s", key, result.Data)
		}
	}
	if _, ok := data["html"]; ok && string(data["html"]) != `""` {
		t.Error("a region response must not carry the whole page")
	}
	var projection workspaceProjection
	if err := json.Unmarshal(result.Data, &projection); err != nil {
		t.Fatal(err)
	}
	if projection.Revision != "current" || projection.SourceRevision != "current-sources" || projection.WriteRevision != "current" || projection.RefreshRequired || projection.Location != location || projection.Title != "Test · Cicada Studio" {
		t.Errorf("revision fields: %+v", projection)
	}
	if len(projection.Regions) != 1 || projection.Regions[0].ID != "status" || projection.Regions[0].Digest != digests["status"] ||
		!strings.HasPrefix(projection.Regions[0].HTML, `<div class="update-region__body">`) || regionDigest(projection.Regions[0].HTML) != digests["status"] {
		t.Errorf("changed regions: %+v", projection.Regions)
	}
	if strings.Join(projection.Unchanged, ",") != "dock-clip,actions" {
		t.Errorf("unchanged regions: %v", projection.Unchanged)
	}
}

func TestShellEditWithoutRegionsKeepsTheReceipt(t *testing.T) {
	address, client, _ := testApp(t)
	page := getPage(t, client, address+"/studio")
	_, result := reactivePost(t, client, address, "pattern", map[string]string{
		"csrf_token": tokenFromPage(t, page), "revision": "current", "action": "range", "pattern": "p",
		"operation": "clear", "first": "1", "last": "1", "target": "1", "amount": "0", "__cicada_location": "/studio",
	})
	var projection workspaceProjection
	if err := json.Unmarshal(result.Data, &projection); err != nil || projection.Regions != nil || projection.Unchanged != nil || projection.HTML != "" || projection.Revision != "current" {
		t.Errorf("engines expect a receipt: %+v err=%v", projection, err)
	}
}

func TestShellEditRejectsUnknownRegionsBeforeTheWrite(t *testing.T) {
	address, client, edits := testApp(t)
	page := getPage(t, client, address+"/studio")
	for _, regions := range []string{"bogus:00", "dock-clip:a,dock-clip:b", strings.Repeat("status:a,", 40) + "status:a"} {
		response, result := reactivePost(t, client, address, "pattern", map[string]string{
			"csrf_token": tokenFromPage(t, page), "revision": "current", "action": "range", "pattern": "p",
			"operation": "clear", "first": "1", "last": "1", "target": "1", "amount": "0",
			"__cicada_location": "/studio", "__cicada_regions": regions,
		})
		if response.StatusCode != 422 || result.OK || len(*edits) != 0 {
			t.Errorf("regions %.40q reached the write: %d edits=%d", regions, response.StatusCode, len(*edits))
		}
	}
}

func TestShellEditRefusedByTheServiceReturnsNoRegions(t *testing.T) {
	address, client, _ := testApp(t)
	page := getPage(t, client, address+"/studio")
	digests := pageDigests(t, page)
	response, result := reactivePost(t, client, address, "pattern", map[string]string{
		"csrf_token": tokenFromPage(t, page), "revision": "old", "action": "pitch", "pattern": "p", "step": "4", "pitch": "72", "octave": "3",
		"__cicada_location": "/studio", "__cicada_regions": "dock-clip:" + digests["dock-clip"],
	})
	if response.StatusCode != http.StatusConflict || result.OK || len(result.Data) != 0 || result.Values["pitch"] != "72" {
		t.Fatalf("conflict: %d %+v", response.StatusCode, result)
	}
}

func TestShellRegionsEndpointReadsOnlyTheStudioPage(t *testing.T) {
	address, client, edits := testApp(t)
	page := getPage(t, client, address+"/studio?tab=session")
	digests := pageDigests(t, page)
	get := func(query url.Values) (int, workspaceProjection, string) {
		response, err := client.Get(address + "/__studio/regions?" + query.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		var projection workspaceProjection
		_ = json.Unmarshal(data, &projection)
		return response.StatusCode, projection, response.Header.Get("Cache-Control")
	}
	status, projection, cache := get(url.Values{"ids": {"center-session:" + digests["center-session"] + ",center-mixer:"}, "location": {"/studio?tab=session"}})
	if status != 200 || !strings.Contains(cache, "no-store") || projection.Revision != "current" || projection.Location != "/studio?tab=session" {
		t.Fatalf("region read: %d %q %+v", status, cache, projection)
	}
	if strings.Join(projection.Unchanged, ",") != "center-session" || len(projection.Regions) != 1 || projection.Regions[0].ID != "center-mixer" || projection.Regions[0].Digest != digests["center-mixer"] {
		t.Errorf("region read body: regions=%+v unchanged=%v", projection.Regions, projection.Unchanged)
	}
	// The same location check as /__workspace and the edit responses, and only
	// the shell has update regions.
	for _, location := range []string{"https://evil.example/studio", "//evil.example/studio", "/studio/../admin", "/other", "/", "/?panel=patterns", strings.Repeat("x", 8193)} {
		if status, _, _ := get(url.Values{"ids": {"status:"}, "location": {location}}); status != http.StatusBadRequest {
			t.Errorf("location %.40q: status %d", location, status)
		}
	}
	for _, ids := range []string{"", "bogus:", "status:a,status:b"} {
		if status, _, _ := get(url.Values{"ids": {ids}, "location": {"/studio"}}); status != http.StatusBadRequest {
			t.Errorf("ids %q: status %d", ids, status)
		}
	}
	if len(*edits) != 0 {
		t.Errorf("a region read wrote %d edits", len(*edits))
	}
	if response, err := client.Post(address+"/__studio/regions?ids=status:&location=/studio", "application/json", nil); err != nil || response.StatusCode == 200 {
		t.Errorf("the region read must be GET only: %v", response.StatusCode)
	}
}
