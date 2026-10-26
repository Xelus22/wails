//go:build js

package main

// WebOnly models a web-exclusive service (e.g. page lifecycle plumbing).
type WebOnly struct{}

func (w *WebOnly) FlushQueue() error { return nil }
