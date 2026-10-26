// wails wasm probe runner.
//
// Modern Go toolchains ship lib/wasm/wasm_exec.js as a library only (no
// Node auto-run), so `wails generate webbindings` executes its probe through
// this runner instead:
//
//   node wasm-runner.cjs <goRoot>/lib/wasm/wasm_exec.js <module.wasm>
//
// Environment: only WAILS_* variables are forwarded to the Go module (the
// wasm_exec environment block has a small size budget).
'use strict';

const fs = require('node:fs');
const path = require('node:path');

const wasmExecPath = process.argv[2];
const wasmPath = process.argv[3];

if (!wasmExecPath || !wasmPath) {
  console.error('usage: node wasm-runner.cjs <wasm_exec.js> <module.wasm>');
  process.exit(2);
}

require(path.resolve(wasmExecPath));

const Go = globalThis.Go;
if (typeof Go !== 'function') {
  console.error('wasm_exec.js did not define globalThis.Go');
  process.exit(3);
}

const go = new Go();
go.env = Object.fromEntries(
  Object.entries(process.env).filter(([key]) => key.startsWith('WAILS_') || key.startsWith('GOWEB'))
);
go.exit = (code) => {
  process.exitCode = code === undefined ? 0 : code;
};

async function main() {
  const bytes = fs.readFileSync(wasmPath);
  const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
  await go.run(instance);
}

main().catch((err) => {
  console.error('[wails-web] probe failed:', err);
  process.exit(1);
});
