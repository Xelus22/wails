//go:build js

package main

import "syscall/js"

// Smoke-signal helpers: the headless smoke driver (scripts/smoke.cjs) cannot
// capture Go's stdout, so lifecycle hooks instead flip globalThis flags that
// the driver can poll.

func signalStartup() {
	js.Global().Set("__wailsSmokeStartupSeen", true)
}

func signalDomReady() {
	js.Global().Set("__wailsSmokeDomReadySeen", true)
}
