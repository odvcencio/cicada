package main

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel"
)

const secondaryPart = "title \"Studio\"\ntrack bass acid {}\ntrack drums drums {}\npattern pulse acid steps=4 { 1 . 5 . }\npattern beat drums steps=4 { bd: x... sd: .... }\nscene main { bass=pulse drums=beat }\nsong { main }\n"

// secondaryStudio plays a project whose notes live in part.cicada. The entry
// file never changes, so only a fingerprint over every source can tell two
// edits to the part file apart.
func secondaryStudio(t *testing.T) (*studio, *liveplay.Player, string) {
	t.Helper()
	dir := t.TempDir()
	entry := []byte("cicada 2\n")
	for name, text := range map[string]string{
		"main.cicada": string(entry), "part.cicada": secondaryPart,
		"cicada.mod": "project secondary\ncicada 2\nentry \"main.cicada\"\nsource \"part.cicada\"\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "main.cicada")
	p, err := compileStudioSource(path, entry)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildLivePlan(path, entry, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	plan.prepareScore(&initial)
	stream, err := liveplay.New(initial, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stream.Close)
	fingerprint, err := playSourceHash(path)
	if err != nil {
		t.Fatal(err)
	}
	transport := newStudioTransport(path)
	transport.stream, transport.playing, transport.last, transport.sampleRate = stream, true, fingerprint, liveSampleRate
	transport.livePlan, transport.arena = plan, liveplay.NewPatchArena(16)
	return &studio{path: path, lastGoodSource: entry, lastGoodProject: p, transport: transport}, stream, dir
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

func TestSecondarySourceOffersAreActivatedByTheirOwnEvents(t *testing.T) {
	for _, order := range []string{"in order", "out of order"} {
		t.Run(order, func(t *testing.T) {
			s, stream, dir := secondaryStudio(t)
			tr := s.transport
			writePart(t, dir, "1 . 5 .", "1 . 5 3")
			tr.poll()
			first := tr.last
			writePart(t, dir, "1 . 5 3", "1 . 5 1")
			tr.poll()
			second := tr.last
			if first == second || !tr.snapshot().Pending || len(tr.offers) != 2 {
				t.Fatal("two part edits did not produce two distinct pending offers")
			}
			event := func(fingerprint [32]byte) liveplay.Event {
				return liveplay.Event{Kind: "edit", Bar: 2, Name: "x", Revision: offerRevision(fingerprint)}
			}
			steps := []struct {
				fingerprint [32]byte
				pending     bool
			}{{first, true}, {second, false}}
			if order == "out of order" {
				steps = []struct {
					fingerprint [32]byte
					pending     bool
				}{{second, false}, {first, false}}
			}
			for i, step := range steps {
				tr.markLanded(event(step.fingerprint))
				if tr.snapshot().Pending != step.pending {
					t.Fatalf("event %d: pending %v, want %v", i, tr.snapshot().Pending, step.pending)
				}
				if tr.livePlan == nil {
					t.Fatalf("event %d cleared the live plan", i)
				}
			}
			// Land the real offer in the player, then save a compatible gain.
			renderStudioPreview(t, stream, 48_000*40)
			before := stream.LandedRevision()
			writePart(t, dir, "track bass acid {}", "track bass acid { level=-3dB }")
			fingerprint, err := playSourceHash(s.path)
			if err != nil {
				t.Fatal(err)
			}
			tr.pollMu.Lock()
			tr.activateLocked(s.path, s.lastGoodSource, fingerprint)
			tr.pollMu.Unlock()
			renderStudioPreview(t, stream, 256)
			if stream.LandedRevision() != before+1 || tr.last != fingerprint {
				t.Fatalf("compatible save did not patch: landed %d (was %d)", stream.LandedRevision(), before)
			}
			_ = kernel.ParamMixGain
		})
	}
}

func offerRevision(fingerprint [32]byte) string { return studioOfferToken(fingerprint) }

// Generated lifecycle: random part-file edits (some repeat earlier content, so
// tokens recur), polls, and landing events delivered in any order, including
// stale and duplicate ones. The reference keeps the offers not yet reported
// landed, oldest first; an event consumes the first offer with its token and
// every offer before it, and an unknown token changes nothing.
func TestSecondarySourceOfferLedgerGeneratedLifecycle(t *testing.T) {
	variants := []string{"1 . 5 3", "1 . 5 1", "1 . 5 .", "1 . 3 3"}
	for seed := int64(0); seed < 24; seed++ {
		random := rand.New(rand.NewSource(seed))
		s, _, dir := secondaryStudio(t)
		tr := s.transport
		type offer struct {
			fingerprint [32]byte
			plan        *livePlan
		}
		var all, outstanding []offer
		current := "1 . 5 ."
		var wantPlan *livePlan
		wantPending := false
		for step := 0; step < 40; step++ {
			switch op := random.Intn(3); {
			case op == 0 && len(outstanding) < 60:
				next := variants[random.Intn(len(variants))]
				if next == current {
					continue
				}
				writePart(t, dir, current, next)
				current = next
				before := len(tr.offers)
				tr.poll()
				if len(tr.offers) != before+1 {
					t.Fatalf("seed %d step %d: poll recorded %d offers", seed, step, len(tr.offers)-before)
				}
				latest := offer{fingerprint: tr.last, plan: tr.offers[len(tr.offers)-1].plan}
				all, outstanding, wantPending = append(all, latest), append(outstanding, latest), true
			case len(all) > 0:
				chosen := all[random.Intn(len(all))]
				if op == 1 && len(outstanding) > 0 {
					chosen = outstanding[0] // The player's order.
				}
				tr.markLanded(liveplay.Event{Kind: "edit", Bar: 2, Revision: offerRevision(chosen.fingerprint)})
				for i, o := range outstanding {
					if o.fingerprint == chosen.fingerprint {
						wantPlan, outstanding = o.plan, outstanding[i+1:]
						wantPending = len(outstanding) > 0
						break
					}
				}
			}
			if got := tr.snapshot().Pending; got != wantPending {
				t.Fatalf("seed %d step %d: pending %v, want %v", seed, step, got, wantPending)
			}
			if tr.livePlan != wantPlan && (len(all) > 0 && wantPlan != nil) {
				t.Fatalf("seed %d step %d: live plan %p, want %p", seed, step, tr.livePlan, wantPlan)
			}
			if len(tr.offers) != len(outstanding) {
				t.Fatalf("seed %d step %d: ledger %d, reference %d", seed, step, len(tr.offers), len(outstanding))
			}
		}
	}
}
