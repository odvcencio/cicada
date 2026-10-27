package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/limits"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// A profile names the limits cicada check enforces. Every score meets the
// desktop profile's, which the project validator applies. The browser profile
// adds the limits of the portable image that cicada wam2 exports.
const (
	profileDesktop = "desktop"
	profileBrowser = "browser"
)

// imageTooLarge starts the encoder's messages for an image over its size limit.
const imageTooLarge = "project image exceeds"

// portableRates are the sample rates of the images cicada wam2 exports and of
// the Game Director manifest. portableRate is the one most hosts run at.
const portableRate = 48_000

var portableRates = []int{portableRate, 44_100, 96_000}

// browserTrack is one track's share of the portable image's voice budget.
type browserTrack struct {
	ID, Kind string
	Voices   int
}

// browserProfile is what a score takes from the portable image. checkBrowser
// fills it as far as it got, so a refusal can still list the tracks.
type browserProfile struct {
	Tracks     []browserTrack
	Voices     int
	ImageBytes int  // the largest image over portableRates
	AtLeast    bool // the encoder refused the image, so ImageBytes is the audio it holds
}

// checkBrowser compiles the score as cicada wam2 does, at each rate it exports,
// and encodes the kernel image. The image encoder enforces the limits (16
// tracks, 32 allocated voices, a 2 MiB image, no master processor), so a score
// that passes here is not refused for them at export. A refusal comes back as
// a diagnostic.
func checkBrowser(p *project.Project, root string) (browserProfile, *notation.Diagnostic) {
	var profile browserProfile
	refuse := func(code string, rate int, err error) *notation.Diagnostic {
		scope := "browser profile"
		if rate != portableRate {
			scope += fmt.Sprintf(" at %d Hz", rate)
		}
		return &notation.Diagnostic{Code: code, Severity: "error", Message: scope + ": " + err.Error(), Position: notation.Position{Line: 1, Column: 1}}
	}
	for _, rate := range portableRates {
		cfg, err := schedule.CompileResident(p, root, rate, 128)
		if err != nil {
			return profile, refuse("CICADA-PARAM", rate, err)
		}
		if profile.Tracks == nil {
			for i, track := range p.Tracks {
				voices := project.TrackVoices(&cfg.Track[i])
				profile.Tracks = append(profile.Tracks, browserTrack{ID: track.ID, Kind: track.Kind, Voices: voices})
				profile.Voices += voices
			}
		}
		image, err := kernelimage.Encode(cfg)
		if err != nil {
			if strings.Contains(err.Error(), imageTooLarge) {
				// A refused image is not returned. The audio is most of what makes
				// it too big, and the image holds at least that much.
				profile.ImageBytes, profile.AtLeast = max(profile.ImageBytes, residentAudioBytes(&cfg)), true
			}
			return profile, refuse("CICADA-LIMIT", rate, err)
		}
		profile.ImageBytes = max(profile.ImageBytes, len(image))
	}
	return profile, nil
}

// residentAudioBytes is the audio that the kernel image stores: four bytes per
// sample, per channel, of every asset and of every region of a sampler pack.
func residentAudioBytes(cfg *engine.Config) int {
	samples := 0
	for _, asset := range cfg.Assets {
		samples += len(asset.Left) + len(asset.Right)
	}
	counted := map[*float32]bool{} // zones may share a region
	for i := 0; i < cfg.Tracks; i++ {
		pack, ok := cfg.Track[i].Prepared.(*engine.PackFactory)
		if !ok {
			continue
		}
		for _, zone := range pack.Zones {
			for _, pcm := range [][]float32{zone.Region.Left, zone.Region.Right} {
				if len(pcm) > 0 && !counted[&pcm[0]] {
					counted[&pcm[0]] = true
					samples += len(pcm)
				}
			}
		}
	}
	return 4 * samples
}

// checkBrowserScore applies the browser profile to a score that passed the
// desktop checks and lists what each track reserves. It reports whether the
// score fits.
func checkBrowserScore(path string, inspection scoreInspection, stdout, stderr io.Writer) bool {
	profile, refusal := checkBrowser(inspection.semantic, filepath.Dir(path))
	if refusal != nil {
		// The refusal belongs to the whole score, so it has no source line to show.
		refusal.Position.File = path
		fmt.Fprintln(stderr, refusal.Error())
		if suggestion := checkSuggestion(*refusal); suggestion != "" {
			fmt.Fprintf(stderr, "  %s\n", suggestion)
		}
		profile.write(stderr)
		return false
	}
	fmt.Fprintln(stdout, path)
	profile.write(stdout)
	return true
}

func (profile browserProfile) write(w io.Writer) {
	if len(profile.Tracks) == 0 {
		return
	}
	fmt.Fprintf(w, "  browser profile: %d of %d tracks, %d of %d voices reserved", len(profile.Tracks), limits.PortableMaxTracks, profile.Voices, limits.PortableMaxVoices)
	if profile.ImageBytes > 0 {
		bound := ""
		if profile.AtLeast {
			bound = "at least "
		}
		fmt.Fprintf(w, ", image %s%d of %d bytes", bound, profile.ImageBytes, kernelimage.MaxImageBytes)
	}
	fmt.Fprintln(w)
	idWidth, kindWidth := len("track"), len("kind")
	for _, track := range profile.Tracks {
		idWidth, kindWidth = max(idWidth, len(track.ID)), max(kindWidth, len(track.Kind))
	}
	fmt.Fprintf(w, "    %-*s  %-*s  voices\n", idWidth, "track", kindWidth, "kind")
	for _, track := range profile.Tracks {
		fmt.Fprintf(w, "    %-*s  %-*s  %6d\n", idWidth, track.ID, kindWidth, track.Kind, track.Voices)
	}
}
