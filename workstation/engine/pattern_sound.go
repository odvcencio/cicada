package main

import "net/url"

// patternSoundURL carries an audition choice only within its phrase's editor.
// It never turns a destination for another pattern or origin into this one.
func patternSoundURL(raw, base, pattern, track string, current bool) (string, bool) {
	if pattern == "" || track == "" {
		return raw, false
	}
	origin, err := url.Parse(base)
	if err != nil || origin.Scheme == "" || origin.Host == "" {
		return raw, false
	}
	target, err := url.Parse(raw)
	if err != nil {
		return raw, false
	}
	target = origin.ResolveReference(target)
	if target.Scheme != origin.Scheme || target.Host != origin.Host || target.User != nil || target.Path != "/" && target.Path != "/studio" {
		return raw, false
	}
	q, err := url.ParseQuery(target.RawQuery)
	if err != nil {
		return raw, false
	}
	if q.Get("pattern") != pattern && (!current || q.Get("pattern") != "") {
		return raw, false
	}
	q.Set("pattern", pattern)
	q.Set("track", track)
	target.RawQuery = q.Encode()
	next := target.RequestURI()
	if target.Fragment != "" {
		next += "#" + target.EscapedFragment()
	}
	return next, true
}
