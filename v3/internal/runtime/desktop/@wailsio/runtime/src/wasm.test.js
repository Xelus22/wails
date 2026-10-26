// @vitest-environment node
import { describe, it, expect, afterEach, vi } from 'vitest';

// These tests pin the wasm bridge transport contract:
//
//   globalThis.__wailsWasmLoader      (glue marker, sync)
//   globalThis.__wailsWasmCall        (Go entry, async)
//   globalThis.__wailsWasmCallResult  (JS answer channel)
//   activateWasmBridge()              (buffer drain + transport switch)
//
// Each test leans a fresh module instance (vi.resetModules) and sets the
// environment markers *before* importing ./wasm, mirroring how the real
// bootstrap glue scripts run ahead of the runtime bundle.

const g = globalThis;

function clearBridgeGlobals() {
    delete g.__wailsWasmLoader;
    delete g.__wailsWasmCall;
    delete g.__wailsWasmCallResult;
    delete g.__wailsWasmActivatePending;
}

async function freshWasm(bridgeLive = false) {
    if (bridgeLive) {
        g.__wailsWasmCall = vi.fn();
    } else {
        g.__wailsWasmLoader = true;
    }
    return await import('./wasm');
}

/** Installs a faithful fake of the Go module's `__wailsWasmCall` entry. */
function fakeBridge(replyFor) {
    let requests = [];
    g.__wailsWasmCall = (request) => {
        requests.push(request);
        const parsed = JSON.parse(request);
        const reply = replyFor(parsed);
        if (reply !== undefined) {
            g.__wailsWasmCallResult(parsed['call-id'], JSON.stringify(reply));
        }
    };
    return {
        requests: () => requests.map(r => JSON.parse(r)),
        replies: (data, parsed) => g.__wailsWasmCallResult(parsed['call-id'], JSON.stringify({ ok: true, data })),
        call: () => g.__wailsWasmCall,
    };
}

describe('wasm bridge transport', () => {
    afterEach(() => {
        clearBridgeGlobals();
        vi.resetModules();
        vi.restoreAllMocks();
    });

    it('routes nothing when no bridge is present', async () => {
        const { routeInWasm } = await import('./wasm');
        expect(routeInWasm(0, 0, '', { args: [] })).toBeNull();
        expect(g.__wailsWasmCallResult).toBeUndefined();
    });

    it('installs the answer channel in loader mode and buffers until activation', async () => {
        const wasm = await freshWasm(false);

        expect(wasm.isWasm()).toBe(true);
        expect(g.__wailsWasmCallResult).toBeTypeOf('function');

        const routed = wasm.routeInWasm(0, 0, '', { methodID: 42, args: ['a'] });
        expect(routed).toBeInstanceOf(Promise);

        // No bridge yet: request memorised, promise pending.
        let settled = false;
        routed.then(() => { settled = true; });
        await Promise.resolve();
        expect(settled).toBe(false);

        // Bridge becomes live; activation must drain the buffer.
        const bridge = fakeBridge((parsed) => ({ ok: true, data: 'pong' }));
        wasm.activateWasmBridge();

        await expect(routed).resolves.toBe('pong');
        expect(bridge.requests().length).toBe(1);
        const body = bridge.requests()[0];
        expect(body.object).toBe(0);
        expect(body.method).toBe(0);
        // Caller args are nested exactly like the HTTP POST body; the
        // transport-level call-id rides at the top level.
        expect(body.args).toEqual({ methodID: 42, args: ['a'] });
        expect(body['call-id']).toBeTruthy();
    });

    it('dispatches straight to the bridge when it is already live', async () => {
        const wasm = await freshWasm(true);
        const bridge = fakeBridge((parsed) => ({ ok: true, data: 'pong' }));

        const result = wasm.routeInWasm(0, 0, '', {methodName: 'main.Dummy.Ping', args: [] });
        expect(result).toBeInstanceOf(Promise);
        await expect(result).resolves.toBe('pong');

        const body = bridge.requests()[0];
        expect(body.args.methodName).toBe('main.Dummy.Ping');
        expect(body['window-name']).toBeUndefined();
    });

    it('forwards the window name to the bridge', async () => {
        const wasm = await freshWasm(true);
        const bridge = fakeBridge((parsed) => ({ ok: true, data: null }));

        const result = wasm.routeInWasm(6, 1, 'secondary', null);
        await expect(result).resolves.toBeNull();

        const body = bridge.requests()[0];
        expect(body.object).toBe(6);
        expect(body['window-name']).toBe('secondary');
    });

    it('rejects with the mapped error type from the envelope', async () => {
        const wasm = await freshWasm(false);

        const routed = wasm.routeInWasm(0, 0, '', { methodID: 99, args: [] });
        fakeBridge(() => ({ ok: false, error: { kind: 'RuntimeError', message: 'boom for wasm' } }));
        wasm.activateWasmBridge();

        await expect(routed).rejects.satisfies((err) => err.name === 'RuntimeError' && err.message === 'boom for wasm');
        await expect(routed).rejects.toThrow(wasm.RuntimeError);
    });

    it('fails fast when the bridge cannot take a synchronous call', async () => {
        const wasm = await freshWasm(false);

        const routed = wasm.routeInWasm(0, 0, '', { methodID: 1, args: [] });

        g.__wailsWasmCall = () => { throw new Error('module not booted'); };
        wasm.activateWasmBridge();

        await expect(routed).rejects.toThrow('wasm bridge: call dispatch failed');
    });

    it('preserves call order when draining the buffer', async () => {
        const wasm = await freshWasm(false);

        const first = wasm.routeInWasm(0, 0, '', { methodID: 1, args: [] });
        const second = wasm.routeInWasm(0, 0, '', { methodID: 2, args: [] });

        const order = [];
        g.__wailsWasmCall = (request) => {
            const parsed = JSON.parse(request);
            order.push(parsed.args.methodID);
            g.__wailsWasmCallResult(parsed['call-id'], JSON.stringify({ ok: true, data: { id: parsed.args.methodID } }));
        };
        wasm.activateWasmBridge();

        expect(order).toEqual([1, 2]);
        await expect(first).resolves.toEqual({ id: 1 });
        await expect(second).resolves.toEqual({ id: 2 });
    });

    it('leaves the protocol call-id inside args untouched for cancellation matching', async () => {
        // calls.ts issues Call(requests with {"call-id": nanoid, methodName, args})
        // nested as args, and Cancel repeats that same ID. The bridge must
        // keep both intact and never reuse the nested ID as its own reply key.
        const wasm = await freshWasm(true);
        const bridge = fakeBridge((parsed) => ({ ok: true, data: 'ok' }));

        const callPromise = wasm.routeInWasm(
            0, 0, '',
            { 'call-id': 'abc123', methodName: 'main.Settings.GetTheme', args: [] },
        );
        const cancelPromise = wasm.routeInWasm(10, 0, '', { 'call-id': 'abc123' });
        await expect(callPromise).resolves.toBe('ok');
        await expect(cancelPromise).resolves.toBe('ok');

        const bodies = bridge.requests();
        expect(bodies[0].args['call-id']).toBe('abc123');
        expect(bodies[1].args['call-id']).toBe('abc123');
        // Distinct transport-level keys, so the two waiters don't collide.
        expect(bodies[0]['call-id']).not.toBe(bodies[1]['call-id']);
    });

    it('routes event emissions through the RuntimeCall bridge (object=Events)', async () => {
        const wasm = await freshWasm(false);

        const promise = wasm.routeInWasm(3, 0, '', { name: 'user.added', data: { user: 'Ada' } });
        let body;
        g.__wailsWasmCall = (request) => {
            body = JSON.parse(request);
            g.__wailsWasmCallResult(body['call-id'], JSON.stringify({ ok: true, data: null }));
        };
        wasm.activateWasmBridge();

        await expect(promise).resolves.toBeNull();
        expect(body.object).toBe(3); // objectNames.Events
        expect(body.method).toBe(0); // EmitMethod
        expect(body.args).toEqual({ name: 'user.added', data: { user: 'Ada' } });
        expect(body['call-id']).toBeTruthy();
    });
});
