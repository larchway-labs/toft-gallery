package main

import "strings"

// The base path, and the asymmetry that is invisible until something breaks.
//
// A Go app is told its mount point through BASE_PATH and X-Forwarded-Prefix,
// and **the proxy strips the prefix on the way in**. So this app receives
// `/requests/12` and must emit `/apps/{slug}/requests/12`.
//
// That applies to every href, every form action, and — this is the htmx part —
// every hx-get, hx-post and hx-delete URL, because they are all link-shaped and
// none of them is rewritten.
//
// **The asymmetry**: the proxy puts the prefix *back* onto a redirect's
// `Location` header, so a 303 must carry a bare path. But `HX-Redirect`,
// `HX-Location` and `HX-Push-Url` are not `Location`, nothing rewrites them,
// and they must carry the prefix themselves.
//
// One helper renders every app-relative URL and the handlers use it for both
// header families, because two rules that differ by one header is exactly the
// shape a person gets right on the day they read this and wrong three months
// later.
type urls struct{ prefix string }

func newURLs(basePath string) urls {
	return urls{prefix: strings.TrimRight(basePath, "/")}
}

// Path renders a link the browser will follow: hrefs, form actions, every htmx
// attribute, and the three HX-* headers.
func (u urls) Path(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return u.prefix + p
}

// Bare renders a path for a `Location` header, which the proxy re-prefixes. The
// two are different functions rather than one with a flag, so a call site says
// which rule it meant.
func (u urls) Bare(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}
