// Headless smoke test for the v2 webbridge runtime.
//
// Bootstraps the compiled js/wasm module under Node *without* the
// WAILS_WEB_BINDINGS generation flag — i.e. in real bridge mode — fakes the
// runtime JS seams (globalThis.wails.Callback / EventsNotify / SetBindings,
// exactly what a browser has after evaluating runtime_prod_desktop.js) and
// drives the desktop IPC protocol directly through __wailsWasmDispatch /
// __wailsWasmDomReady.
//
// Usage: node scripts/smoke.cjs [path/to/app.wasm]
// (defaults to build/app.wasm). Exits 0 on success, 1 on failure.
'use strict';

const fs = require('node:fs');
const path = require('node:path');

const wasmPath = process.argv[2] || path.join(__dirname, '..', 'build', 'app.wasm');
const wasmExecPath = process.env.WAILS_SMOKE_WASM_EXEC;

if (!wasmExecPath) {
  console.error('smoke.cjs: set WAILS_SMOKE_WASM_EXEC to $(go env GOROOT)/lib/wasm/wasm_exec.js');
  process.exit(1);
}

if (!fs.existsSync(wasmPath)) {
  console.error(`smoke.cjs: ${wasmPath} not found — build it first with:`);
  console.error('  GOOS=js GOARCH=wasm CGO_ENABLED=0 go build -tags webbridge -o build/app.wasm .');
  process.exit(1);
}

/* ------------------------------------------------------------- harness */

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function until(predicate, timeoutMs, what) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await sleep(10);
  }
  throw new Error(`timeout waiting for ${what}`);
}

const callbacks = []; // window.wails.Callback messages
const events = [];    // window.wails.EventsNotify payloads
const bindings = [];  // window.wails.SetBindings catalogs

// window.wails shape parity: the stock runtime_prod_desktop.js installs
// exactly these three entry points the frontend pushes to.
globalThis.wails = {
  SetBindings(json) { bindings.push(json); },
  Callback(message) { callbacks.push(message); },
  EventsNotify(payload) { events.push(payload); },
};

let failures = 0;
function check(name, condition, detail) {
  if (condition) {
    console.log(`  ok  ${name}`);
  } else {
    failures++;
    console.error(` FAIL ${name}${detail ? ` — ${detail}` : ''}`);
  }
}

function dispatch(message) {
  const fn = globalThis.__wailsWasmDispatch;
  if (typeof fn !== 'function') throw new Error('bridge handler not installed');
  return fn(message);
}

/* ---------------------------------------------------------------- boot */

async function main() {
  // wasm_exec.js (library-only in modern Go) then the compiled module.
  require(path.resolve(wasmExecPath));
  const Go = globalThis.Go;
  if (typeof Go !== 'function') throw new Error('wasm_exec.js did not define globalThis.Go');

  const go = new Go();
  // Only forward WAILS_-relevant env; keep it empty here so the app takes
  // bridge mode, never generation mode (WAILS_WEB_BINDINGS=1).
  go.env = {};
  go.exit = (code) => { process.exitCode = code === undefined ? 0 : code; };

  const bytes = fs.readFileSync(wasmPath);
  const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
  const running = go.run(instance); // parks forever in bridge mode
  running.catch((err) => {
    throw new Error(`wasm module crashed: ${err}`);
  });

  await until(() => globalThis.__wailsWasmReady === true, 15000, 'bridge ready');

  /* -------------------------------------------------- 1. bindings seam */
  check('runtime received the bindings catalog via SetBindings', bindings.length === 1, JSON.stringify(bindings).slice(0, 120));
  if (bindings.length === 1) {
    const db = JSON.parse(bindings[0]);
    check('catalog exposes main.Settings.GetTheme', !!db.main?.Settings?.GetTheme);
    check('catalog exposes main.Dummy.Ping', !!db.main?.Dummy?.Ping);
  } else {
    check('catalog exposes main.Settings.GetTheme', false, 'no catalog');
    check('catalog exposes main.Dummy.Ping', false, 'no catalog');
  }

  /* ------------------------------------------------ 2. lifecycle hooks */
  check('OnStartup hook fired during Run', globalThis.__wailsSmokeStartupSeen === true);
  check('OnDomReady not fired before the frontend signals it', globalThis.__wailsSmokeDomReadySeen !== true);
  const domReady = globalThis.__wailsWasmDomReady;
  check('__wailsWasmDomReady seam installed', typeof domReady === 'function');
  domReady();
  await until(() => globalThis.__wailsSmokeDomReadySeen === true, 5000, 'OnDomReady hook');
  check('OnDomReady hook fired after the signal', globalThis.__wailsSmokeDomReadySeen === true);

  /* ------------------------------------------------- 3. call transport */
  dispatch('C' + JSON.stringify({ callbackID: 't1', name: 'main.Settings.GetTheme', args: [] }));
  await until(
    () => callbacks.some((m) => { try { return JSON.parse(m).callbackid === 't1'; } catch { return false; } }),
    5000,
    'GetTheme callback'
  );
  const callMsg = callbacks.map((m) => { try { return JSON.parse(m); } catch { return null; } })
    .find((m) => m && m.callbackid === 't1');
  check('GetTheme result delivered ("dark")', callMsg?.result === 'dark', JSON.stringify(callMsg));
  check('GetTheme error is null', callMsg?.error === null || callMsg?.error === undefined, JSON.stringify(callMsg));
  check('go-exec proxies: the messages matched the desktop shape "c"+JSON', callbacks[0].startsWith('{"'), callbacks[0].slice(0, 60));

  /* ---------------------------------------------- 4. call error path */
  dispatch('C' + JSON.stringify({ callbackID: 't2', name: 'main.Settings.Fail', args: [] }));
  await until(
    () => callbacks.some((m) => { try { return JSON.parse(m).callbackid === 't2'; } catch { return false; } }),
    5000,
    'Fail callback'
  );
  const failMsg = callbacks.map((m) => { try { return JSON.parse(m); } catch { return null; } })
    .find((m) => m && m.callbackid === 't2');
  check('Fail error message delivered ("boom")', failMsg?.error === 'boom', JSON.stringify(failMsg));
  check('Fail result is null', failMsg?.result === null, JSON.stringify(failMsg));

  /* --------------------------------------- 5. unknown-method rejection */
  const badReply = dispatch('C' + JSON.stringify({ callbackID: 't3', name: 'main.Nope.Missing', args: [] }));
  check('unknown method handled without crashing the bridge', badReply === 'ok', String(badReply));

  /* ---------------------------------------------- 6. methods w/ args */
  // Desktop parity: the runtime sends argument values inside the call
  // payload (single JSON encoding rounds-trip; each element parses back
  // exactly). So `args: ["Ada"]` here, not a pre-encoded '"Ada"'.
  dispatch('C' + JSON.stringify({ callbackID: 't4', name: 'main.Settings.SetName', args: ['Ada'] }));
  await until(
    () => callbacks.some((m) => { try { return JSON.parse(m).callbackid === 't4'; } catch { return false; } }),
    5000,
    'SetName callback'
  );
  const argMsg = callbacks.map((m) => { try { return JSON.parse(m); } catch { return null; } })
    .find((m) => m && m.callbackid === 't4');
  check('SetName("Ada") -> "hello Ada"', argMsg?.result === 'hello Ada', JSON.stringify(argMsg));

  /* ------------------------------------------- 7. Go→JS event bridge */
  // Desktop parity: an *emitting* frontend never receives its own event
  // back from the backend (Events.Notify skips the sender — multi-window
  // semantics). JS→Go 'EE' must therefore NOT loop back to us:
  const eventsBefore = events.length;
  dispatch('EE' + JSON.stringify({ name: 'ping', data: ['x'] }));
  dispatch('C' + JSON.stringify({ callbackID: 't5', name: 'main.Dummy.Ping', args: [] }));
  await until(
    () => callbacks.some((m) => { try { return JSON.parse(m).callbackid === 't5'; } catch { return false; } }),
    5000,
    'Ping callback (event-settle sync)'
  );
  const pingMsg = callbacks.map((m) => { try { return JSON.parse(m); } catch { return null; } })
    .find((m) => m && m.callbackid === 't5');
  check('Ping result delivered ("pong")', pingMsg?.result === 'pong', JSON.stringify(pingMsg));
  check('JS→Go event emit does not loop back to the sender (desktop parity)', events.length === eventsBefore, JSON.stringify(events));

  // Go→JS: the app emits through runtime.EventsEmit; delivery must arrive
  // through window.wails.EventsNotify.
  dispatch('C' + JSON.stringify({ callbackID: 't6', name: 'main.Settings.EmitFromGo', args: [] }));
  await until(
    () => events.some((p) => { try { return JSON.parse(p).name === 'gosmoke'; } catch { return false; } }),
    5000,
    'EventsNotify delivery of a runtime.EventsEmit'
  );
  const evMsg = events.map((p) => { try { return JSON.parse(p); } catch { return null; } })
    .find((p) => p && p.name === 'gosmoke');
  check('Go event delivered through EventsNotify', evMsg?.name === 'gosmoke', JSON.stringify(events));
  check('Go event data preserved', JSON.stringify(evMsg?.data) === '["from-go"]', JSON.stringify(evMsg));

  /* --------------------------------------------------------------- done */
  console.log('');
  if (failures > 0) {
    console.error(`SMOKE FAILED: ${failures} check(s) failed`);
    process.exit(1);
  }
  console.log('SMOKE PASSED (webbridge: bindings, lifecycle, calls, errors, events)');
  process.exit(0);
}

main().catch((err) => {
  console.error(`smoke.cjs: ${err.message || err}`);
  process.exit(1);
});
