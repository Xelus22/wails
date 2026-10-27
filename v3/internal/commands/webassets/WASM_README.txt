Wails Web (WASM) bootstrap (v3)
===============================

This directory is (re-)extracted by

    wails3 build -platform web        (or: wails3 dev -platform web)

and contains:

    wasm-glue.js    Browser glue: announces bridge mode to the Wails
                    runtime, loads the WASM module and activates the
                    runtime's bridge transport once the Go module is live.
    go-exec.js      Copy of your Go toolchain's lib/wasm/wasm_exec.js,
                    regenerated on every web build so it always matches
                    the Go release that compiled <dist>/wasm/app.wasm.

Usage: import the glue FIRST in your frontend entry point, before
anything that touches the Wails runtime (import order is load-bearing):

    import './bindings/wasm/wasm-glue.js';
    // ... then your app code; @wailsio/runtime may be imported anywhere
    // AFTER the glue.

The glue looks for the compiled module at (in order):

    1. the URL given by a "?wasmUrl=" query override
    2. <document base>/wasm/app.wasm
    3. <document base>/wasm/main.wasm

`wails3 build -platform web` emits the module to <frontend dist>/wasm/
app.wasm and serves that dist directory (wails3 dev -platform web), so
candidate #2 matches out of the box.

Notes for this pipeline-parity stage
------------------------------------
- The glue statically imports `@wailsio/runtime` (the npm package).
  Bundling a generated runtime instead (-b bindings option) is not
  supported by the bootstrap yet.
- The Go side of the bridge (the `__wailsWasmCall` RuntimeCall
  dispatcher) is covered by a follow-up milestone. Until then, binding
  calls buffer in the runtime and a guidance warning explains it; a
  compiled module still boots and reports its lifecycle on the console.
