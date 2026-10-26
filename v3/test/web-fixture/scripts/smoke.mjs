// Node smoke driver for the v3 wasm bridge transport.
//
// Mirrors v2/test/web-fixture/scripts/smoke.cjs: it drives the *shipped*
// runtime (the npm package's dist/ output) against a fake Go wasm module
// that implements the bridge wire contract documented in
// internal/runtime/desktop/@wailsio/runtime/src/wasm.ts:
//
//   globalThis.__wailsWasmLoader      — glue marker, set before import
//   globalThis.__wailsWasmCall(body)  — Go entry; body = RuntimeCall POST body
//                                       (+ window-name/call-id encodings)
//   globalThis.__wailsWasmCallResult — JS answer envelope channel
//   activateWasmBridge()             — drain + switch
//
// A DOM is stubbed to the minimum the runtime imports behind `hasDOM`,
// as a browser hosting a wasm Wails app would be a full DOM.
//
// Usage: node scripts/smoke.mjs
import { strict as assert } from 'node:assert';

const checks = [];
function ok(name, fn) {
    try {
        fn();
        checks.push([name, true, '']);
        console.log(`  ✓ ${name}`);
    } catch (err) {
        checks.push([name, false, String(err && err.message || err)]);
        console.log(`  ✗ ${name}: ${err && err.message || err}`);
    }
}

// ---- environment (before any runtime import) -------------------------------
globalThis.window = globalThis;          // minimal DOM surface: hasDOM=true
globalThis.document = {};
globalThis.__wailsWasmLoader = true;     // bootstrap glue marker

const RUNTIME = '../../../internal/runtime/desktop/@wailsio/runtime/dist/';
const { ByName, ByID } = await import(`${RUNTIME}calls.js`);const { On, Emit, WailsEvent } = await import(`${RUNTIME}events.js`);

// Binding IDs transcribed from frontend/bindings/wasm.catalog.json.
const IDs = {
    DummyPing: '141153708',
    SettingsFail: '65539639',
    SettingsGetTheme: '2292196002',
    SettingsSetName: '165854050',
    WebOnlyFlushQueue: '24250257',
};

// ---- fake Go wasm module ----------------------------------------------------
const received = [];      // every request body handed to __wailsWasmCall
const inbound = [];       // events received by the fake dispatcher
let booted = false;
let goHandler = null;

// A first sketch of the v3 webbridge dispatcher (M4): the RuntimeCall
// decoder shared with the HTTP handler, over the bridge body.
function fakeGo(body) {
    const { object } = body;
    if (object === 0) { // Call
        const { methodID, methodName, args: callArgs = [] } = body.args || {};
        if (methodName === 'main.Settings.GetTheme' || methodID === IDs.SettingsGetTheme) {
            return { ok: true, data: 'dark' };
        }
        if (methodName === 'main.Settings.SetName' || methodID === IDs.SettingsSetName) {
            return { ok: true, data: 'hello ' + String(callArgs[0]) };
        }
        if (methodName === 'main.Settings.Fail' || methodID === IDs.SettingsFail) {
            return { ok: false, error: { kind: 'RuntimeError', message: 'boom' } };
        }
        if (methodName === 'main.Dummy.Ping' || methodID === IDs.DummyPing) {
            return { ok: true, data: 'pong' };
        }
        if (methodName === 'main.WebOnly.FlushQueue' || methodID === IDs.WebOnlyFlushQueue) {
            return { ok: true, data: 'flushed' };
        }
        return { ok: false, error: { kind: 'ReferenceError', message: `unknown method ${methodID ?? methodName}` } };
    }
    if (object === 3) { // Events.Emit
        inbound.push(body.args instanceof WailsEvent
            ? { name: body.args.name, data: body.args.data }
            : body.args);
        return { ok: true, data: null };
    }
    // CancelCall & others: acknowledged, empty reply.
    return { ok: true, data: null };
}

function boot() {
    booted = true;
    goHandler = (request) => {
        const body = JSON.parse(request);
        received.push(body);
        const reply = fakeGo(body);
        // Real Go module answers through a task schedule, not inline:
        queueMicrotask(() =>
            globalThis.__wailsWasmCallResult(body['call-id'], JSON.stringify(reply)));
    };
    globalThis.__wailsWasmCall = goHandler;
}

const { activateWasmBridge, isBridgeLive } = await import(`${RUNTIME}wasm.js`);

// ---- 1. contract plumbing ---------------------------------------------------
ok('answer channel installed in loader mode', () =>
    assert.equal(typeof globalThis.__wailsWasmCallResult, 'function'));

// ---- 2. buffered flow: call before the Go module boots ----------------------
const buffered1 = ByID(IDs.SettingsGetTheme);
const buffered2 = ByName('main.Settings.SetName', 'Ada');

await new Promise((r) => setTimeout(r, 0));

ok('pre-boot requests stay buffered', () =>
    assert.equal(received.length, 0));

boot();
activateWasmBridge();
ok('bridge live after activation', () => assert.ok(isBridgeLive()));

ok('buffered GetTheme resolves after activation', async () =>
    assert.equal(await buffered1, 'dark'));
ok('buffered SetName resolves with marshalled args', async () =>
    assert.equal(await buffered2, 'hello Ada'));

// ---- 3. wire-body shape parity with the desktop HTTP POST -------------------
await ByID(IDs.DummyPing);
const pingBody = received.at(-1);
ok('body object/method route the Call service', () => {
    assert.equal(pingBody.object, 0);
    assert.equal(pingBody.method, 0);
});
ok('caller args nested untouched like the HTTP body', () => {
    assert.equal(pingBody.args.methodID, IDs.DummyPing);
    assert.ok('call-id' in pingBody.args); // protocol ID preserved for cancels
});
ok('synthetic transport-level call-id at top level', () => {
    assert.ok(pingBody['call-id']);
    assert.notEqual(pingBody['call-id'], pingBody.args['call-id']);
});
ok('no window-name field for anonymous calls', () =>
    assert.ok(!('window-name' in pingBody)));

// ---- 4. error envelope → typed rejection ------------------------------------
const errPromise = ByID(IDs.SettingsFail).catch((e) => e);
await new Promise((r) => setTimeout(r, 0));
ok('bridge error envelope rejects as RuntimeError', async () => {
    const err = await errPromise;
    assert.equal(err.name, 'RuntimeError');
    assert.equal(err.message, 'boom');
});

// ---- 5. unknown method resilience --------------------------------------------
const unknownPromise = ByName('main.Ghost.Spoke').catch((e) => e);
await new Promise((r) => setTimeout(r, 0));
ok('unknown method rejects as ReferenceError', async () => {
    const err = await unknownPromise;
    assert.equal(err.name, 'ReferenceError');
});

// ---- 6. JS→Go event emission through the bridge ------------------------------
const emitResult = await Emit('user.added', { user: 'Ada' });
const emitBody = received.at(-1);
ok('events ride the same RuntimeCall bridge', () => {
    assert.equal(emitBody.object, 3);   // objectNames.Events
    assert.equal(emitBody.method, 0);   // EmitMethod
    assert.equal(emitBody.args.name, 'user.added');
    assert.deepEqual(emitBody.args.data, { user: 'Ada' });
    assert.equal(emitResult, null);     // Events service returns no payload
});

// ---- 7. Go→JS inbound events via dispatchWailsEvent --------------------------
let seen = null;
const cancel = await Promise.resolve(On('greeted', (ev) => { seen = ev; }));
boot2: {
    // The Go module delivers inbound events straight to the runtime seam.
    globalThis._wails.dispatchWailsEvent({ name: 'greeted', data: ['hello', 'from go'] });
}
await new Promise((r) => setTimeout(r, 0));
ok('inbound Go event reaches On listeners', () => {
    assert.ok(seen, 'listener did not fire');
    assert.equal(seen.name, 'greeted');
    assert.deepEqual(seen.data, ['hello', 'from go']);
});

// ---- 8. cancellation route ----------------------------------------------------
const cancelBody = await Promise.resolve((async () => {
    const p = ByID(IDs.WebOnlyFlushQueue);
    p.catch(() => { /* expected: CancelError when the cancel wins the race */ });
    p.cancel();
    await new Promise((r) => setTimeout(r, 0));
    return received.find((b) => b.object === 10);
})());
ok('cancellation flows as CancelCall route', () => {
    assert.ok(cancelBody, 'no CancelCall request reached the bridge');
    assert.ok(cancelBody.args && 'call-id' in cancelBody.args);
});

// ---- summary ------------------------------------------------------------------
const failed = checks.filter(([, pass]) => !pass);
console.log(`\n${checks.length - failed.length}/${checks.length} checks passed`);
if (failed.length > 0) {
    process.exitCode = 1;
    for (const [name, , msg] of failed) {
        console.error(`FAILED: ${name}\n  ${msg}`);
    }
}
