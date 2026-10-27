/*
 _    _ ____  _        _
| |  | | __ )| |      | | ___   __ _
| |  | |  _ \| |      | |/ _ \ / _` |
| |__| | |_) | |___   | | (_) | (_| |
 \____/|____/|_____|__|_|\___/ \__, |
                               |___/
Wails Web (WASM) bootstrap — extracted into frontend/bindings/wasm/.
                DO NOT EDIT (re-extracted on every `wails3 build -platform web`)

This glue boots the compiled WebAssembly backend and hands the Wails
runtime over to it. It must be imported FIRST in your frontend entry
point, before anything that touches the Wails runtime:

    import './bindings/wasm/wasm-glue.js';
    // ...your entry point, which imports @wailsio/runtime

Import order is load-bearing:
1. `./go-exec.js` registers `globalThis.Go` (the wasm_exec.js of the exact
   Go release that compiled <dist>/wasm/app.wasm).
2. The loader flag below tells @wailsio/runtime that a WASM backend is
   on its way: runtime calls made until the Go module registers its
   bridge entry point are buffered, in order.
3. When `globalThis.__wailsWasmCall` appears, the bridge transport is
   activated and buffered calls drain.
*/

import './go-exec.js';
import { activateWasmBridge } from '@wailsio/runtime';

globalThis.__wailsWasmLoader = true;

const BOOT_TIMEOUT_MS = 15_000;
const POLL_INTERVAL_MS = 50;

console.info('[wails] WASM bootstrap ', 'loading the WebAssembly backend...');

/* ------------------------------------------------------------------ URLs */
/* The module is looked up at (in order):
     1. the URL given by a "?wasmUrl=" query override
     2. <document base>/wasm/app.wasm  — the artifact of
        `wails3 build -platform web`
     3. <document base>/wasm/main.wasm
*/
function candidateUrls() {
    const override = new URLSearchParams(document.location.search).get('wasmUrl');
    if (override) {
        // The app wins: an explicit override bypasses every other candidate.
        return [override];
    }
    const base = new URL('.', document.baseURI).href.replace(/\/$/, '');
    return [
        `${base}/wasm/app.wasm`,
        `${base}/wasm/main.wasm`,
    ];
}

/* ------------------------------------------------------------------ boot */

function goConstructor() {
    const Go = globalThis.Go;
    if (!Go) {
        throw new Error('go-exec.js did not register globalThis.Go — is it a wasm_exec.js copy of a Go release?');
    }
    return new Go();
}

async function bootModule(candidate) {
    const response = await fetch(candidate.relUrl, { cache: 'force-cache' });
    if (!response.ok) {
        throw new Error(`HTTP ${response.status} for ${candidate.relUrl}`);
    }

    const go = goConstructor();
    const contentType = response.headers.get('content-type') || '';
    let instance;
    if (contentType.includes('application/wasm')) {
        ({ instance } = await WebAssembly.instantiateStreaming(response, go.importObject));
    } else {
        // Wrong MIME (static hosts): buffered compile fallback.
        const bytes = await response.arrayBuffer();
        ({ instance } = await WebAssembly.instantiate(bytes, go.importObject));
    }

    // The module keeps the application alive; do not await the end of main.
    // Its outcome is reported loudly when it arrives.
    go.run(instance).then(() => {
        console.error('[wails] WASM backend has stopped (main returned). The bridge is disabled until the page is reloaded and the backend is re-booted.');
    }).catch((err) => {
        console.error('[wails] WASM backend crashed:', err);
    });

    waitForBridge();
}

async function loadAndRunWasm() {
    const candidates = candidateUrls().map((relUrl) => ({ relUrl }));
    let lastError = null;
    for (const candidate of candidates) {
        try {
            await bootModule(candidate);
            return;
        } catch (err) {
            lastError = err;
        }
    }
    console.error(`[wails] WASM bootstrap failed: ${lastError}. Keep working: check that <dist>/wasm/app.wasm was served and set ?wasmUrl=... to point the bootstrap at a custom module location.`);
}

/* Polls for the Go module's bridge entry point and activates the runtime
   transport the moment it exists. If the module died before registering,
   the single guidance warning above the timeout applies. */
function waitForBridge() {
    const startedAt = Date.now();
    const timer = setInterval(() => {
        if (typeof globalThis.__wailsWasmCall === 'function') {
            clearInterval(timer);
            console.info('[wails] WASM bootstrap: bridge is live, activating the runtime transport.');
            // Recorded so a runtime bundle that evaluates AFTER this glue
            // (script order quirks) still activates at import time.
            if (typeof window !== 'undefined') {
                window.__wailsWasmActivatePending = true;
            }
            activateWasmBridge();
            return;
        }
        if (Date.now() - startedAt > BOOT_TIMEOUT_MS) {
            clearInterval(timer);
            console.warn(`[wails] WASM bootstrap: the Go module did not register its bridge entry point (__wailsWasmCall) within ${Math.round(BOOT_TIMEOUT_MS / 1000)}s. Binding calls will stay buffered in the runtime. Check the console above: either the module failed to boot (see the [wails] lines) or this build predates the web dispatcher milestone (wails web RuntimeCall dispatch) and calls cannot be answered yet.`);
        }
    }, POLL_INTERVAL_MS);
}

loadAndRunWasm();
