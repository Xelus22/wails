package commands

import "embed"

// webAssets holds the static assets of the web (WASM) pipeline: the
// browser bootstrap glue and its README. They are extracted into
// <frontend>/bindings/wasm by the web build (see webbuild.go).
//
//go:embed webassets
var webAssets embed.FS
