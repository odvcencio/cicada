package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/liveplay"
)

const secondaryPart = "title \"Studio\"\ntrack bass acid {}\ntrack drums drums {}\npattern pulse acid steps=4 { 1 . 5 . }\npattern beat drums steps=4 { bd: x... sd: .... }\nscene main { bass=pulse drums=beat }\nsong { main }\n"

// secondaryStudio plays a project whose song lives in part.cicada. The entry
// file never changes, so only a fingerprint over every source can tell two
// edits to the part file apart.
func secondaryStudio(t *testing.T) (*studio, *liveplay.Player, string) {
	return secondaryStudioFiles(t, "cicada 2\n", secondaryPart)
}

// secondaryStudioFiles builds the studio with newStudio and plays a real
// liveplay.Player, so activation, polling and landing events all run as in
// production. Tests render the player to produce the landing events.
func secondaryStudioFiles(t *testing.T, entryText, part string) (*studio, *liveplay.Player, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	for name, text := range map[string]string{
		"main.cicada": entryText, "part.cicada": part,
		"cicada.mod": "project secondary\ncicada 2\nentry \"main.cicada\"\nsource \"part.cicada\"\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := newStudio(filepath.Join(dir, "main.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	initial, err := compileLiveScore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildLivePlan(s.path, []byte(entryText), liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	plan.prepareScore(&initial)
	stream, err := liveplay.New(initial, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stream.Close)
	fingerprint, err := playSourceHash(s.path)
	if err != nil {
		t.Fatal(err)
	}
	tr := s.transport
	tr.stream, tr.playing, tr.last, tr.sampleRate = stream, true, fingerprint, liveSampleRate
	tr.livePlan, tr.arena = plan, liveplay.NewPatchArena(16)
	return s, stream, dir
}

func writePart(t *testing.T, dir, from, to string) {
	t.Helper()
	file := filepath.Join(dir, "part.cicada")
	data, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(data), from) {
		t.Fatalf("part edit %q: %v", from, err)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(data), from, to, 1)), 0600); err != nil {
		t.Fatal(err)
	}
}

// landOffers renders two bars, which crosses a bar boundary at least once, and
// hands every event the player emitted to the transport, as the watcher does.
func landOffers(t *testing.T, s *studio, stream *liveplay.Player) (edits []liveplay.Event) {
	t.Helper()
	renderStudioPreview(t, stream, 2*barFrames(stream))
	for {
		select {
		case event := <-stream.Events():
			s.transport.markLanded(event)
			if event.Kind == "edit" {
				edits = append(edits, event)
			}
		default:
			return edits
		}
	}
}

func barFrames(stream *liveplay.Player) int64 {
	return int64(liveSampleRate) * 4 * 60_000 / stream.BPMMilli()
}

// Every publication has its own token even when content repeats. A, B, A with
// no rendering in between leaves only the newest in the player; its landing
// event must settle the whole queue.
func TestRepeatedContentPublicationsSettleOnTheRealLandingEvent(t *testing.T) {
	for name, renderBetween := range map[string]bool{"delayed rendering": false, "rendered between": true} {
		t.Run(name, func(t *testing.T) {
			s, stream, dir := secondaryStudio(t)
			tr := s.transport
			content := "Studio"
			publish := func(next string) {
				writePart(t, dir, "title \""+content+"\"", "title \""+next+"\"")
				content = next
				tr.poll()
				if renderBetween {
					landOffers(t, s, stream)
				}
			}
			publish("Studio A")
			publish("Studio B")
			publish("Studio A") // Same content as the first publication.
			if !renderBetween && (len(tr.offers) != 3 || !tr.snapshot().Pending) {
				t.Fatalf("ledger %d, pending %v before rendering", len(tr.offers), tr.snapshot().Pending)
			}
			events := landOffers(t, s, stream)
			if !renderBetween && (len(events) != 1 || events[0].Revision != "p3") {
				t.Fatalf("only the newest offer should land: %+v", events)
			}
			if tr.snapshot().Pending || len(tr.offers) != 0 || tr.livePlan == nil {
				t.Fatalf("pending %v, ledger %d, live plan %v after the landing", tr.snapshot().Pending, len(tr.offers), tr.livePlan != nil)
			}
		})
	}
}

// A secondary-file save goes through its own studio session but shares the
// entry transport. A compatible gain change must patch the playing engine and
// leave poll with nothing to publish.
func TestSecondaryFileSaveThroughFilesRoutePatchesWithoutReplacing(t *testing.T) {
	s, stream, dir := secondaryStudio(t)
	tr := s.transport
	renderStudioPreview(t, stream, 256)
	part, err := os.ReadFile(filepath.Join(dir, "part.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(part), "track bass acid {}", "track bass acid { level=-3dB }", 1)
	response := studioCall(t, s.routes(), "/api/files", studioEdit{File: "part.cicada", Revision: studioRevision(part), Source: edited})
	if response.Code != http.StatusOK {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	renderStudioPreview(t, stream, 256)
	if stream.LandedRevision() != 1 {
		t.Fatalf("secondary save did not patch: landed %d", stream.LandedRevision())
	}
	published := tr.publications
	tr.poll()
	if tr.publications != published || tr.snapshot().Pending || len(tr.offers) != 0 {
		t.Fatalf("poll queued a replacement after the patch: %d publications, pending %v", tr.publications-published, tr.snapshot().Pending)
	}
	// An unchanged save through the same route is also a no-op for the player.
	response = studioCall(t, s.routes(), "/api/files", studioEdit{File: "part.cicada", Revision: studioRevision([]byte(edited)), Source: edited})
	if response.Code != http.StatusOK {
		t.Fatalf("unchanged save: %d %s", response.Code, response.Body.String())
	}
	tr.poll()
	if tr.publications != published {
		t.Fatal("an unchanged secondary save queued a replacement")
	}
}

// recordingStudio keeps the recordable pattern in the entry file and an
// unrelated track in part.cicada.
func recordingStudio(t *testing.T) (*studio, *liveplay.Player, string) {
	return secondaryStudioFiles(t, "cicada 2\n"+studioScore, "track pad acid {}\npattern padline acid steps=4 { 1 . . . }\n")
}

func recordPartTake(t *testing.T, s *studio) {
	t.Helper()
	entry, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	response := studioCall(t, s.routes(), "/api/record", studioEdit{Revision: studioRevision(entry), Track: "bass", Pattern: "pulse", Take: []studioTakeNote{{Tick: 0, EndTick: 100, Note: 60, Velocity: 90}}})
	if response.Code != http.StatusOK {
		t.Fatalf("record: %d %s", response.Code, response.Body.String())
	}
}

func queuedLines(s *studio) int {
	n := 0
	for _, event := range s.history.snapshot() {
		if event.Kind == "queued" {
			n++
		}
	}
	return n
}

// Handoff rule: a take arms its content; the next publication made after it
// consumes the arm. That publication is the take's own offer only if its
// content matches. Otherwise the take's offer was never published and the
// publication is logged as queued.
func TestRecordedTakeHandoffCoversOnlyTheNextPublication(t *testing.T) {
	setup := func(t *testing.T) (*studio, *liveplay.Player, string, []byte) {
		s, stream, dir := recordingStudio(t)
		original, _ := os.ReadFile(s.path)
		recordPartTake(t, s)
		return s, stream, dir, original
	}
	t.Run("take's own offer is not logged, a later edit is", func(t *testing.T) {
		s, stream, dir, _ := setup(t)
		s.transport.poll()
		if queuedLines(s) != 0 || !s.transport.snapshot().Pending {
			t.Fatalf("take offer: %d queued lines, pending %v", queuedLines(s), s.transport.snapshot().Pending)
		}
		landOffers(t, s, stream)
		writePart(t, dir, "1 . . .", "1 . 3 .")
		s.transport.poll()
		if queuedLines(s) != 1 {
			t.Fatalf("a later part edit logged %d queued lines, want 1", queuedLines(s))
		}
	})
	t.Run("secondary edit before the take is polled", func(t *testing.T) {
		s, _, dir, _ := setup(t)
		writePart(t, dir, "1 . . .", "1 . 3 .")
		s.transport.poll()
		if queuedLines(s) != 1 {
			t.Fatalf("secondary edit consumed the take's handoff: %d queued lines", queuedLines(s))
		}
	})
	t.Run("revert before the take is polled", func(t *testing.T) {
		s, _, _, original := setup(t)
		taken, _ := os.ReadFile(s.path)
		if err := os.WriteFile(s.path, original, 0600); err != nil {
			t.Fatal(err)
		}
		s.transport.poll() // Content equals the playing score: nothing to publish.
		if queuedLines(s) != 0 || len(s.transport.offers) != 0 {
			t.Fatalf("revert published: %d lines, %d offers", queuedLines(s), len(s.transport.offers))
		}
		if err := os.WriteFile(s.path, taken, 0600); err != nil {
			t.Fatal(err)
		}
		s.transport.poll() // The take's content again, but by a manual edit.
		if queuedLines(s) != 1 {
			t.Fatalf("stale handoff suppressed a manual edit: %d queued lines", queuedLines(s))
		}
	})
	t.Run("other content then the take's content", func(t *testing.T) {
		s, _, dir, _ := setup(t)
		taken, _ := os.ReadFile(s.path)
		writePart(t, dir, "1 . . .", "1 . 3 .")
		s.transport.poll()
		writePart(t, dir, "1 . 3 .", "1 . . .")
		if err := os.WriteFile(s.path, taken, 0600); err != nil {
			t.Fatal(err)
		}
		s.transport.poll()
		if queuedLines(s) != 2 {
			t.Fatalf("queued lines %d, want 2", queuedLines(s))
		}
	})
}

// Generated lifecycle against the real player. The reference numbers
// publications itself, counting one per content change, and does not use the
// transport's matching: after a render of one bar it expects exactly one edit
// event, for the newest publication, and an empty queue; between renders it
// expects pending exactly when a publication has not landed.
func TestPublicationLedgerGeneratedLifecycleAgainstRealPlayer(t *testing.T) {
	titles := []string{"Studio", "Studio A", "Studio B", "Studio C"}
	for seed := int64(0); seed < 12; seed++ {
		random := rand.New(rand.NewSource(seed))
		s, stream, dir := secondaryStudio(t)
		tr := s.transport
		content, played := "Studio", "Studio"
		newest, outstanding := 0, 0
		published := 0
		for step := 0; step < 24; step++ {
			if random.Intn(4) != 0 {
				next := titles[random.Intn(len(titles))]
				if next != content {
					writePart(t, dir, "title \""+content+"\"", "title \""+next+"\"")
					content = next
				}
				tr.poll()
				if content != played { // The reference's own change detection.
					published++
					newest, outstanding = published, outstanding+1
					played = content
				}
			} else {
				// Landing renders a whole bar after the newest publication.
				renderStudioPreview(t, stream, 2*barFrames(stream))
				var landed []string
				for drained := false; !drained; {
					select {
					case event := <-stream.Events():
						tr.markLanded(event)
						if event.Kind == "edit" {
							landed = append(landed, event.Revision)
						}
					default:
						drained = true
					}
				}
				if outstanding > 0 {
					if len(landed) != 1 || landed[0] != fmt.Sprintf("p%d", newest) {
						t.Fatalf("seed %d step %d: landing events %v, want only p%d", seed, step, landed, newest)
					}
					outstanding = 0
				} else if len(landed) != 0 {
					t.Fatalf("seed %d step %d: unexpected landing %v", seed, step, landed)
				}
			}
			if got := tr.snapshot().Pending; got != (outstanding > 0) {
				t.Fatalf("seed %d step %d: pending %v with %d outstanding", seed, step, got, outstanding)
			}
			if len(tr.offers) > outstanding+published || outstanding == 0 && len(tr.offers) != 0 {
				t.Fatalf("seed %d step %d: ledger %d with %d outstanding", seed, step, len(tr.offers), outstanding)
			}
			if tr.livePlan == nil {
				t.Fatalf("seed %d step %d: live plan lost", seed, step)
			}
		}
	}
}

// More than maxStudioOffers publications without a landing drop the oldest
// ledger entries. Pending stays true, and a late event for a dropped token
// changes nothing: the player already replaced that offer, and the newest
// offer's own event settles the queue.
func TestPublicationLedgerOverflowDropsOldest(t *testing.T) {
	s, stream, dir := secondaryStudio(t)
	tr := s.transport
	const total = maxStudioOffers + 6
	content := "Studio"
	for i := 0; i < total; i++ {
		next := fmt.Sprintf("Studio %d", i)
		writePart(t, dir, "title \""+content+"\"", "title \""+next+"\"")
		content = next
		tr.poll()
	}
	if len(tr.offers) != maxStudioOffers || !tr.snapshot().Pending {
		t.Fatalf("ledger %d (cap %d), pending %v", len(tr.offers), maxStudioOffers, tr.snapshot().Pending)
	}
	if tr.offers[0].token != studioPublicationToken(total-maxStudioOffers+1) {
		t.Fatalf("overflow kept %s first", tr.offers[0].token)
	}
	plan := tr.livePlan
	tr.markLanded(liveplay.Event{Kind: "edit", Bar: 2, Revision: studioPublicationToken(1)}) // Dropped.
	if len(tr.offers) != maxStudioOffers || !tr.snapshot().Pending || tr.livePlan != plan {
		t.Fatal("an event for a dropped offer changed the transport")
	}
	if events := landOffers(t, s, stream); len(events) != 1 || events[0].Revision != studioPublicationToken(total) {
		t.Fatalf("real landing: %+v", events)
	}
	if len(tr.offers) != 0 || tr.snapshot().Pending {
		t.Fatal("the newest offer did not settle the queue")
	}
}
