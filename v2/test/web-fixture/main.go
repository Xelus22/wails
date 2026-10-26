// Package main is a minimal Wails app used to exercise the js/wasm
// (webbridge) build path end-to-end:
//
//	go build -tags webbridge -o build/app.wasm .
//	wails generate webbindings
//	node scripts/smoke.cjs
package main

import (
	"context"
	"errors"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Settings is a bound service with the classic shapes: results, errors,
// arguments and Go→JS events.
type Settings struct {
	ctx context.Context
}

func (s *Settings) GetTheme() string { return "dark" }

func (s *Settings) SetName(name string) (string, error) { return "hello " + name, nil }

func (s *Settings) Fail() (string, error) { return "", errors.New("boom") }

// EmitFromGo pushes an event to the frontend — the app-side analogue of a
// backend plumbing event (covers events.Emit → frontend Notify bridge).
func (s *Settings) EmitFromGo() {
	runtime.EventsEmit(s.ctx, "gosmoke", "from-go")
}

type Dummy struct{}

func (d *Dummy) Ping() string { return "pong" }

func (d *Dummy) NoResults() {}

func main() {
	settings := &Settings{}
	err := wails.Run(&options.App{
		Bind: []interface{}{settings, &Dummy{}},
		OnStartup: func(ctx context.Context) {
			settings.ctx = ctx
			signalStartup()
		},
		OnDomReady: func(ctx context.Context) {
			signalDomReady()
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
