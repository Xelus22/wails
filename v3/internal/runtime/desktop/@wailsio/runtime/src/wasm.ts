/*
 _	   __	  _ __
| |	 / /___ _(_) /____
| | /| / / __ `/ / / ___/
|__/|__/\__,_/_/_/____/
The electron alternative for Go
(c) Lea Anthony 2019-present
*/

declare global {
    interface Window {
        /** Set by the wasm bootstrap glue to request transport activation. */
        __wailsWasmActivatePending?: boolean;
    }
}

/**
 * Wasm bridge transport layer.
 *
 * Web (GOOS=js GOARCH=wasm) builds have no HTTP dev server: the Wails
 * runtime lives inside the compiled WASM module and speaks the same
 * RuntimeCall protocol the desktop HTTP transport uses, but over a set
 * of global bridge entry points installed by the bootstrap glue and the
 * Go module itself:
 *
 * - `globalThis.__wailsWasmLoader` — set *synchronously* by the bootstrap
 *   glue when it is imported, before the WASM module has booted. Its
 *   presence switches the runtime from HTTP calls to bridged mode;
 *   requests issued before the bridge is live are buffered.
 * - `globalThis.__wailsWasmCall(request)` — synchronous JS→Go entry
 *   installed by the Go module. `request` is the RuntimeCall wire body
 *   (`{object, method, args?, call-id, window-name?}`), identical to the
 *   desktop HTTP POST body except that the `x-wails-window-name` header
 *   is re-encoded as the `window-name` body field, and a fresh
 *   transport-level `call-id` correlation key is added (the one inside
 *   `args` belongs to the protocol and stays untouched). The reply
 *   arrives asynchronously through `__wailsWasmCall`.
 * - `globalThis.__wailsWasmCallResult(callID, response)` — JS entry
 *   installed by this module when bridged mode is on. `response` is the
 *   reply envelope: `{ok: true, data: <any JSON>}` or
 *   `{ok: false, error: <CallErrorType>}`.
 * - `activateWasmBridge()` — runtime entry the glue calls (or sets
 *   `window.__wailsWasmActivatePending` to have the runtime call it
 *   itself on import) once `__wailsWasmCall` exists; it flushes buffered
 *   requests and switches the transport over.
 *
 * Inbound events are delivered by the Go module through the existing
 * `window._wails.dispatchWailsEvent(event)` entry point — no extra
 * plumbing is needed, as long as the bootstrap glue evaluates the
 * runtime bundle before dispatching (it does).
 *
 * User-supplied custom transports (setTransport) retain priority over
 * the HTTP fallback in desktop builds; in bridged mode the bridge wins
 * because there is no HTTP server to fall back to.
 */

import type { RuntimeTransport } from "./runtime.js";
import { errorFromCallJSON, type CallErrorType } from "./callerror.js";

type Envelope = {
    ok: true,
    data: any,
} | {
    ok: false,
    error: CallErrorType,
}

type PendingResponse = {
    resolve: (data: any) => void,
    reject: (err: Error) => void,
}

type BufferedRequest = {
    request: string,
}

const callIDField = "call-id";

function userGlobal(): Record<string, any> {
    return globalThis as unknown as Record<string, any>;
}

/**
 * Returns true when this page hosts a Wails Wasm backend: either the
 * bootstrap glue already announced itself (loader mode) or the Go
 * module is already live.
 * False in desktop/webview and SSR contexts, where the runtime's own
 * transports apply.
 */
export function isWasm(): boolean {
    return isBridgeLive() || typeof userGlobal().__wailsWasmLoader !== "undefined";
}

/**
 * Returns true when the WASM backend module is already answering calls.
 */
export function isBridgeLive(): boolean {
    return typeof userGlobal().__wailsWasmCall === "function";
}

/**
 * Calls issued before the WASM backend booted, drained by
 * [activateWasmBridge] in their original order.
 */
const bufferedRequests: BufferedRequest[] = [];

/**
 * Resolvers waiting for the Go module's reply, keyed by call ID.
 */
const pendingResponses = new Map<string, PendingResponse>();

let nextCallID = Math.floor(Math.random() * 0xffffff);

function backingCallID(): string {
    let id: string;
    do {
        nextCallID = (nextCallID + 1) % 0x3fffffff;
        id = String(nextCallID);
    } while (pendingResponses.has(id));
    return id;
}

function deliverResult(callID: string, response: string): void {
    const waiting = pendingResponses.get(callID);
    if (!waiting) {
        // Late or duplicated replies are dropped, mirroring how the HTTP
        // transport silently times out. Debug aid for bridge development:
        console.debug("wasm bridge: reply for unknown call", callID);
        return;
    }
    pendingResponses.delete(callID);

    let envelope: Envelope;
    try {
        envelope = JSON.parse(response);
    } catch (err) {
        waiting.reject(new Error(`wasm bridge: invalid reply envelope for call ${callID}: ${err}`));
        return;
    }
    if (envelope.ok) {
        waiting.resolve(envelope.data);
    } else {
        waiting.reject(errorFromCallJSON(envelope.error));
    }
}

function dispatchRequest(request: string, callID: string): void {
    try {
        userGlobal().__wailsWasmCall(request);
    } catch (err) {
        // The call never reached the Go module: fail its waiter here
        // instead of leaving it hanging forever.
        const waiting = pendingResponses.get(callID);
        if (waiting) {
            pendingResponses.delete(callID);
            waiting.reject(new Error(`wasm bridge: call dispatch failed: ${err}`));
        }
    }
}

let answerChannelInstalled = false;

/**
 * Installs the JS answer channel (`__wailsWasmCallResult`) the Go module
 * invokes to deliver call replies. Called at import time when the bridge
 * is already flagged, and again from [routeInWasm] when bridged mode is
 * entered (covers loader flags that appear after the runtime was
 * imported).
 */
function ensureAnswerChannel(): void {
    if (answerChannelInstalled) {
        return;
    }
    answerChannelInstalled = true;
    userGlobal().__wailsWasmCallResult = deliverResult;
}

/**
 * Routes a runtime call through the WASM bridge.
 *
 * @returns A promise resolving with the response data when the call was
 * routed (or buffered), or null to let the runtime's own transports
 * handle the call.
 */
export function routeInWasm(objectID: number, method: number, windowName: string, args: any): Promise<any> | null {
    if (!isWasm()) {
        return null;
    }
    ensureAnswerChannel();

    // Body shape mirrors the desktop HTTP POST body exactly: `object`,
    // `method`, and the caller's `args` payload nested as-is (bound-call
    // requests carry their protocol-level call ID inside those args and
    // must stay untouched, e.g. for cancellation matching). Two body
    // fields re-encode transport details the HTTP path conveyed through
    // headers:
    //   - `window-name` replaces the x-wails-window-name header;
    //   - `call-id` is a fresh transport-level correlation key, used by
    //     the Go module to address the reply envelope. It is unrelated
    //     to any call ID inside args.
    // The Go bridge dispatcher reads this body with the same RuntimeCall
    // decoder the HTTP handler uses.
    let body: Record<string, any> = { object: objectID, method: method };
    if (windowName) {
        body["window-name"] = windowName;
    }
    if (args !== null && args !== undefined) {
        body.args = args;
    }
    const callID: string = backingCallID();
    body[callIDField] = callID;

    const request = JSON.stringify(body);

    return new Promise<any>((resolve, reject) => {
        pendingResponses.set(callID, { resolve, reject });

        if (isBridgeLive()) {
            dispatchRequest(request, callID);
        } else {
            bufferedRequests.push({ request });
        }
    });
}

let bridgeTransport: RuntimeTransport | null = null;

function createBridgeTransport(): RuntimeTransport {
    return {
        call: (objectID: number, method: number, windowName: string, args: any): Promise<any> => {
            return routeInWasm(objectID, method, windowName, args) as Promise<any>;
        },
    };
}

/**
 * Returns the bridge transport once the bridge was activated, or null.
 * The runtime consults this before falling back to HTTP.
 */
export function bridgeModeTransport(): RuntimeTransport | null {
    return bridgeTransport;
}

/**
 * Transitions the runtime onto the Wasm bridge.
 *
 * Called by the bootstrap glue once the Go module's `__wailsWasmCall`
 * entry point exists. Drains buffered requests immediately (in call
 * order) and switches the transport used by all future calls.
 */
export function activateWasmBridge(): void {
    if (bridgeTransport === null) {
        bridgeTransport = createBridgeTransport();
    }
    // Detach the queue before draining: dispatches may synchronously push
    // new requests through routeInWasm (when the Go module answers inline),
    // which must not be mistaken for pre-activation traffic.
    const requests = [...bufferedRequests];
    bufferedRequests.length = 0;
    for (const buffered of requests) {
        const request = buffered.request;
        let callID = "";
        try {
            callID = JSON.parse(request)[callIDField];
        } catch {
            // Unreachable: buffered requests are always valid JSON.
        }
        dispatchRequest(request, callID);
    }
}

if (isWasm()) {
    ensureAnswerChannel();

    // Late imports: the bootstrap glue may have completed the Go module
    // boot before this bundle evaluated (activation request recorded in
    // the loader marker? see the glue contract).
    if (typeof window !== "undefined" && window.__wailsWasmActivatePending) {
        activateWasmBridge();
    }
}
