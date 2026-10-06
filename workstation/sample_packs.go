package main

import (
	"net/http"
	"time"

	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
)

func (s *studioApp) samplePacks(ctx *server.Context, v workspace, csrf string) gosx.Node {
	state := action.States(ctx.Request)["sample-pack"]
	tier := state.Value("tier")
	if tier == "" {
		tier = "hq16"
	}
	id := state.Value("id")
	if id == "" {
		id = "grand"
	}
	quality := gosx.El("select", gosx.Attrs(gosx.Attr("name", "tier")),
		gosx.El("option", gosx.Attrs(gosx.Attr("value", "hq16"), gosx.Attr("selected", tier == "hq16")), gosx.Text("PCM16 FLAC · smaller download")),
		gosx.El("option", gosx.Attrs(gosx.Attr("value", "lossless"), gosx.Attr("selected", tier == "lossless")), gosx.Text("Exact · original PCM")))
	form := s.form(v, csrf, "instruments", "sample-pack",
		field("Catalog URL", textInput("catalogURL", state.Value("catalogURL"))),
		field("Catalog SHA-256", textInput("sha256", state.Value("sha256"))),
		field("Pack ID", textInput("id", id)),
		field("Download quality", quality), submit("", "", "Download sample pack"))
	return ui.Panel(ui.PanelProps{ID: "sample-packs", Title: "Sample packs", Description: "Download from a trusted pack catalog. PCM16 FLAC preserves quiet recordings with a stored scale; Exact retains the original decoded samples. Existing scores keep their pinned quality."}, form)
}

func (s *studioApp) downloadSamplePack(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	f := ctx.FormData
	tier := f["tier"]
	if tier == "" {
		tier = "hq16"
	}
	if tier != "hq16" && tier != "lossless" {
		return action.Validation("Choose a download quality.", nil, f)
	}
	// Large verified banks have a longer preparation deadline than controls.
	backend := *s.backend
	client := *backend.client
	client.Timeout = 10 * time.Minute
	backend.client = &client
	var result struct {
		Declaration string `json:"declaration"`
	}
	if err := backend.call(ctx.Request.Context(), http.MethodPost, "/api/instrument-pack/download", map[string]string{"catalogURL": f["catalogURL"], "sha256": f["sha256"], "id": f["id"], "tier": tier}, &result); err != nil {
		return action.Validation(err.Error(), nil, f)
	}
	ctx.FormData = nil
	ctx.RedirectBackWithMessage("/?panel=instruments", "Downloaded. Add this declaration in Score: "+result.Declaration)
	return nil
}
