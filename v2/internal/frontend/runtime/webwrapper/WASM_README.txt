Wails Web (WASM) runtime bootstrap
==================================

This directory is extracted into your frontend by

    wails generate webbindings

and contains:

    wasm-bridge.js   Browser glue: loads the WASM module, installs the
                     window.WailsInvoke shim and routes the standard Wails
                     runtime traffic to the Go backend.
    wails-runtime.js The stock Wails desktop runtime bundle (a byte-exact
                     copy of the production desktop runtime). It defines
                     window.runtime / window.wails and wires the window.go
                     method proxies from the bindings catalog.
    go-exec.js       Copy of your Go toolchain's lib/wasm/wasm_exec.js
                     (regenerated on every `wails generate webbindings`,
                     so the runtime always matches your Go version).

Usage: import the bootstrap in your frontend entry point — import order is
load-bearing, `wails-bridge` first, the runtime last:

    import '../wailsjs/wasm/wasm-bridge.js';
    import '../wailsjs/wasm/wails-runtime.js';

(Your bundler inlines them in this order; `wails-runtime.js` reads
`window.wailsbindings` when it evaluates, and the bridge buffers runtime
traffic until the WASM module is live.)

The bridge looks for the compiled module at (in order):

    1. the URL given by a "?wasmUrl=" query override
    2. <document base>/wasm/app.wasm
    3. <document base>/wasm/main.wasm

`wails build -platform web` (or `wails dev -platform web`) emits the module
to <frontend dist>/wasm/app.wasm and serves the dist directory, which
matches candidate #2 out of the box.

Desktop parity: the app keeps using the standard `wailsjs/go/*` bindings and
`wailsjs/runtime/*` modules. The bridge installs window.wailsbindings plus
window.WailsInvoke once the WASM module is live, so the standard runtime JS
builds the window.go proxies exactly like it does in the desktop build.

Not supported by the browser backend (they return a clear error, like the
desktop API's failing paths): native dialogs, clipboard text read, system
notifications, screens, menus and most window state operations.
