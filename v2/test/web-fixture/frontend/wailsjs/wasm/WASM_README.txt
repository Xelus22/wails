Wails Web (WASM) runtime bootstrap
==================================

This directory is extracted into your frontend by

    wails generate webbindings

and contains:

    wasm-bridge.js   Browser glue: loads the WASM module, installs the
                     window.WailsInvoke shim and routes the standard Wails
                     runtime traffic to the Go backend.
    go-exec.js       Copy of your Go toolchain's lib/wasm/wasm_exec.js
                     (regenerated on every `wails generate webbindings`,
                     so the runtime always matches your Go version).

Usage: import the bridge in your frontend entry point before anything that
touches the Wails runtime:

    import '../wailsjs/wasm/wasm-bridge.js';

The bridge looks for the compiled module at (in order):

    1. the URL given by a "?wasmUrl=" query override
    2. <document base>/wasm/app.wasm
    3. <document base>/wasm/main.wasm

`wails build -web` emits the module to <frontend dist>/wasm/app.wasm, which
matches candidate #2 out of the box.

Desktop parity: the app keeps using the standard `wailsjs/go/*` bindings and
`wailsjs/runtime/*` modules. The bridge installs window.wailsbindings plus
window.WailsInvoke once the WASM module is live, so the standard runtime JS
builds the window.go proxies exactly like it does in the desktop build.

Not supported by the browser backend (they return a clear error, like the
desktop API's failing paths): native dialogs, clipboard text read, system
notifications, screens, menus and most window state operations.
