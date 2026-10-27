//go:build js

package assetserver

import "net/url"

// js/wasm builds have no webview origin: the asset URLs are resolved against
// the static web server (wails3 dev -platform web) that serves the frontend
// dist directory, so fall back to its default dev address.
var baseURL = url.URL{
	Scheme: "http",
	Host:   "localhost:34115",
}
