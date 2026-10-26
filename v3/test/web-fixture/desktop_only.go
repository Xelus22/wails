package main

// DesktopOnly models a native-backed capability themed "not available on
// the web" (e.g. system notifications). It only registers on non-js
// builds, so the wasm analysis must report it as native-only drift.
type DesktopOnly struct{}

func (d *DesktopOnly) EjectDisc() error { return nil }
