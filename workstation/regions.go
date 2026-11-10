package main

import (
	"crypto/sha256"
	"encoding/hex"
	"html"
	"strings"
)

// Update regions are the parts of the /studio shell that an edit response
// patches in place. Each is a wrapper
//
//	<div id="update-<id>" class="update-region" data-update-region="<id>"
//	     data-region-digest="…" data-region-revision="…">
//	  <div class="update-region__body">…</div>
//	</div>
//
// with exactly one element child, because GoSX's patch receiver patches the
// first element child of the element it finds by id. The digest names the
// body's content, so the browser can say what it shows and the server can
// answer "unchanged" instead of sending the same HTML again.

// regionBlankedFields are the hidden inputs whose values change on every
// render or every save without changing what the region shows: the CSRF token
// is masked again for each response, and the revision fields advance under
// rule R1. Only these five are blanked; any other change alters the digest.
var regionBlankedFields = map[string]bool{"csrf_token": true, "revision": true, "source_revision": true, "fileRevision": true, "diskRevision": true}

func regionBody(content string) string {
	return `<div class="update-region__body">` + content + `</div>`
}

func regionWrapper(id, digest, revision, sourceRevision, body string) string {
	return `<div id="update-` + id + `" class="update-region" data-update-region="` + id + `" data-region-digest="` + digest + `" data-region-revision="` + html.EscapeString(revision) + `" data-region-source-revision="` + html.EscapeString(sourceRevision) + `">` + body + `</div>`
}

// regionDigest is the first 16 hex digits of the SHA-256 of a region body
// with the values of the blanked hidden inputs removed.
func regionDigest(body string) string {
	sum := sha256.New()
	rest := body
	for {
		start := strings.Index(rest, "<input")
		if start < 0 {
			break
		}
		end := strings.IndexByte(rest[start:], '>')
		if end < 0 {
			break
		}
		end += start + 1
		sum.Write([]byte(rest[:start]))
		sum.Write([]byte(blankRevisionInput(rest[start:end])))
		rest = rest[end:]
	}
	sum.Write([]byte(rest))
	return hex.EncodeToString(sum.Sum(nil)[:8])
}

// blankRevisionInput empties the value of a hidden input named in
// regionBlankedFields. Every other tag is returned unchanged.
func blankRevisionInput(tag string) string {
	if len(tag) > 6 && tag[6] != ' ' && tag[6] != '\t' && tag[6] != '\n' && tag[6] != '/' && tag[6] != '>' {
		return tag // <inputmode…> or another element name
	}
	attrs := htmlAttrs(tag)
	if !strings.EqualFold(attrs["type"], "hidden") || !regionBlankedFields[attrs["name"]] {
		return tag
	}
	at := strings.Index(tag, ` value="`)
	if at < 0 {
		return tag
	}
	close := strings.IndexByte(tag[at+8:], '"')
	if close < 0 {
		return tag
	}
	return tag[:at+8] + tag[at+8+close:]
}

// htmlAttrs reads the double-quoted attributes of one start tag. GoSX writes
// every attribute value double-quoted and escaped, so a quote never appears
// inside a value.
func htmlAttrs(tag string) map[string]string {
	attrs := map[string]string{}
	rest := tag
	for {
		eq := strings.Index(rest, `="`)
		if eq < 0 {
			return attrs
		}
		name := rest[:eq]
		if space := strings.LastIndexAny(name, " \t\n"); space >= 0 {
			name = name[space+1:]
		}
		close := strings.IndexByte(rest[eq+2:], '"')
		if close < 0 {
			return attrs
		}
		if _, seen := attrs[name]; !seen {
			attrs[name] = rest[eq+2 : eq+2+close]
		}
		rest = rest[eq+2+close+1:]
	}
}
