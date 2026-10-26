package webwrapper

import "embed"

//go:embed wasm-bridge.js WASM_README.txt
var WebWrapper embed.FS
