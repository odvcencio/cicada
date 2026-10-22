package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"

	"m31labs.dev/cicada/host/padsampler"
	"m31labs.dev/gosx/action"
)

func (s *studioApp) samplerAction(ctx *action.Context) error {
	return s.mutation("/api/sampler", func(f map[string]string) (any, error) {
		p := map[string]any{"action": f["action"], "revision": f["revision"], "source_revision": f["source_revision"], "bankRevision": f["bankRevision"]}
		values := map[string]int{}
		var fields []string
		switch f["action"] {
		case "pad":
			fields = []string{"pad", "start", "end", "pitch", "choke"}
		case "chop", "asset", "sample-placement":
			fields = []string{"count"}
		case "resample":
			fields = []string{"destination"}
		}
		for _, name := range fields {
			n, err := integer(f, name)
			if err != nil {
				return nil, err
			}
			values[name] = n
		}
		whole := func(name string) int { return values[name] }
		switch f["action"] {
		case "pad":
			pad := padsampler.Pad{Name: f["name"], Source: f["source"], Mode: f["mode"], Start: int64(whole("start")), End: int64(whole("end")), Pitch: whole("pitch"), Choke: whole("choke"), Reverse: f["reverse"] == "on", BypassEffects: f["bypassEffects"] == "on"}
			var err error
			for name, dest := range map[string]*float64{"gainDB": &pad.GainDB, "attackMS": &pad.AttackMS, "releaseMS": &pad.ReleaseMS} {
				*dest, err = finiteNumber(f, name)
				if err != nil {
					return nil, err
				}
			}
			p["pad"], p["settings"] = whole("pad"), pad
		case "effects":
			e := padsampler.Clean()
			switch f["preset"] {
			case "clean":
			case "dust":
				e.Cutoff = 2600
				e.Drive = 9
				e.DelayMix = .08
			case "space":
				e.Cutoff = 12000
				e.DelayMix = .3
				e.ReverbMix = .18
			default:
				var err error
				for name, dest := range map[string]*float64{"cutoff": &e.Cutoff, "drive": &e.Drive, "delayMS": &e.DelayMS, "feedback": &e.Feedback, "delayMix": &e.DelayMix, "reverbMix": &e.ReverbMix} {
					*dest, err = finiteNumber(f, name)
					if err != nil {
						return nil, err
					}
				}
			}
			p["effects"] = e
		case "chop", "asset", "sample-placement":
			p["count"], p["source"], p["placement"] = whole("count"), f["source"], f["placement"]
		case "resample":
			var perf padsampler.Performance
			if err := json.Unmarshal([]byte(f["performance"]), &perf); err != nil {
				return nil, fmt.Errorf("record a pad performance first")
			}
			p["performance"], p["newName"], p["track"], p["at"], p["destination"], p["place"] = perf, f["newName"], f["track"], f["at"], whole("destination"), f["place"] == "on"
		case "undo":
		default:
			return nil, fmt.Errorf("unknown sampler action")
		}
		return p, nil
	})(ctx)
}
func (s *studioApp) samplerMedia(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	response, err := s.backend.request(r.Context(), http.MethodGet, "/api/sampler/sound/"+url.PathEscape(id), nil)
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		http.Error(w, string(data), response.StatusCode)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, io.LimitReader(response.Body, (64<<20)+44))
}
func (s *studioApp) importSampler(w http.ResponseWriter, r *http.Request) {
	_, role := s.collaboration.member(r, false)
	if role != "owner" && role != "editor" {
		http.Error(w, "Viewers cannot import samples.", 403)
		return
	}
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, "choose a WAV no larger than 64 MiB", 400)
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, h, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "choose a WAV file", 422)
		return
	}
	defer f.Close()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for _, key := range []string{"revision", "bankRevision"} {
		_ = form.WriteField(key, r.FormValue(key))
	}
	out, err := form.CreateFormFile("audio", h.Filename)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if _, err = io.Copy(out, io.LimitReader(f, 64<<20+1)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = form.Close()
	u := *s.backend.url
	u.Path = "/api/sampler/import"
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, u.String(), &body)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+s.backend.token)
	client := *s.backend.client
	client.Timeout = 60 * time.Second
	response, err := client.Do(request)
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		http.Error(w, string(data), response.StatusCode)
		return
	}
	http.Redirect(w, r, shellRedirect(r, "/?panel=sampler&pad=1"), http.StatusSeeOther)
}
