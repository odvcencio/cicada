package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"m31labs.dev/gosx/action"
)

func TestReactiveWriteReceiptDetectsInterveningSave(t *testing.T) {
	for _, ignoreResponse := range []bool{false, true} {
		t.Run(fmt.Sprintf("ignored-response=%v", ignoreResponse), func(t *testing.T) {
			readStarted, saved := make(chan struct{}), make(chan struct{})
			audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/pattern":
					_, _ = io.WriteString(w, `{"revision":"own-save","valid":true}`)
				case "/api/workspace":
					close(readStarted)
					select {
					case <-saved:
					case <-r.Context().Done():
						return
					}
					_ = json.NewEncoder(w).Encode(workspace{Revision: "intervening-save", Filename: "test.cicada", Source: "title \"Other edit\"\n", Valid: true})
				default:
					_, _ = io.WriteString(w, `{}`)
				}
			}))
			defer audio.Close()
			b, err := newBackend(audio.URL)
			if err != nil {
				t.Fatal(err)
			}
			s := &studioApp{backend: b}
			handler := s.mutation("/api/pattern", func(fields map[string]string) (any, error) { return fields, nil })
			if ignoreResponse {
				handler = func(ctx *action.Context) error {
					if err := b.call(ctx.Request.Context(), http.MethodPost, "/api/pattern", ctx.FormData, nil); err != nil {
						return err
					}
					ctx.Redirect("/?panel=patterns")
					return nil
				}
			}
			request := httptest.NewRequest(http.MethodPost, "/__actions/pattern", strings.NewReader(`{"revision":"initial","__cicada_location":"/?panel=patterns"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json")
			request.Header.Set("X-Cicada-Reactive", "1")
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				s.serveAction(response, request, "pattern", handler)
				close(done)
			}()
			// Another editor saves after the acknowledgment but before the read.
			select {
			case <-readStarted:
			case <-time.After(5 * time.Second):
				close(saved)
				t.Fatal("the projection read did not start")
			}
			close(saved)
			<-done
			var result action.Result
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			var receipt struct {
				Revision        string `json:"revision"`
				WriteRevision   string `json:"writeRevision"`
				Saved           bool   `json:"saved"`
				RefreshRequired bool   `json:"refreshRequired"`
			}
			if err := json.Unmarshal(result.Data, &receipt); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || !result.OK || receipt.Revision != "intervening-save" || receipt.WriteRevision != "own-save" || !receipt.Saved || !receipt.RefreshRequired {
				t.Fatalf("intervening save was accepted as our acknowledgment: status=%d ok=%v receipt=%+v", response.Code, result.OK, receipt)
			}
		})
	}
}
