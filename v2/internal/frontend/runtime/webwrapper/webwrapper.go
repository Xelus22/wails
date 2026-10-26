package webwrapper

import "embed"

//go:embed wasm-bridge.js WASM_README.txt wails-runtime.js
var WebWrapper embed.FS
