//go:build webbridge && (js || wasm)

// This file is the js/wasm counterpart of app_production.go. It is selected
// by the `webbridge` build tag and provides:
//
//   - CreateApp: the same lifecycle wiring as the desktop build (context
//     values, logger, menu manager, bindings database, events, dispatcher)
//     but with the browser frontend instead of a webview frontend;
//   - App.Run: mirrors the desktop Run ordering (frontend.Run → Shutdown hook);
//   - generation mode: when WAILS_WEB_BINDINGS=1 is set in the environment
//     (set by `wails generate webbindings`, which runs the program under
//     Node), the app prints its bindings database behind the
//     `wailsweb-bindings: ` sentinel and exits, instead of installing the
//     bridge.
//
// The generated catalog is the *real* Bind list reflected on the js/wasm
// build, so `_wasm.go` service variants are honoured automatically.
package app

import (
	"context"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2/internal/binding"
	"github.com/wailsapp/wails/v2/internal/frontend/dispatcher"
	"github.com/wailsapp/wails/v2/internal/frontend/runtime"
	"github.com/wailsapp/wails/v2/internal/frontend/webbridge"
	"github.com/wailsapp/wails/v2/internal/logger"
	"github.com/wailsapp/wails/v2/internal/menumanager"
	"github.com/wailsapp/wails/v2/pkg/options"
)

// BindingsSentinel prefixes the catalog line printed by generation mode and
// scanned by `wails generate webbindings`. Keep in sync with
// cmd/wails/generate.go.
const BindingsSentinel = "wailsweb-bindings: "

// Run mirrors the desktop application loop: run the frontend (which, in the
// browser, installs the bridge and then parks the runtime), then invoke the
// shutdown hook.
func (a *App) Run() error {
	err := a.frontend.Run(a.ctx)
	if a.shutdownCallback != nil {
		a.shutdownCallback(a.ctx)
	}
	return err
}

// CreateApp creates the application for a js/wasm build.
func CreateApp(appoptions *options.App) (*App, error) {
	ctx := context.Background()

	// Merge default options
	options.MergeDefaults(appoptions)

	debug := IsDebug()
	devtoolsEnabled := IsDevtoolsEnabled()
	ctx = context.WithValue(ctx, "debug", debug)
	ctx = context.WithValue(ctx, "devtoolsEnabled", devtoolsEnabled)

	// Set up logger
	myLogger := logger.New(appoptions.Logger)
	if IsDebug() {
		myLogger.SetLogLevel(appoptions.LogLevel)
	} else {
		myLogger.SetLogLevel(appoptions.LogLevelProduction)
	}
	ctx = context.WithValue(ctx, "logger", myLogger)
	ctx = context.WithValue(ctx, "obfuscated", IsObfuscated())

	bindings, err := newBindings(myLogger, appoptions)
	if err != nil {
		return nil, err
	}

	// Generation mode: emit the catalog for `wails generate webbindings`
	// and stop. No frontend, no bridge, no lifecycle hooks.
	if os.Getenv("WAILS_WEB_BINDINGS") == "1" {
		catalogJSON, err := bindings.ToJSON()
		if err != nil {
			return nil, err
		}
		fmt.Printf("%s%s\n", BindingsSentinel, catalogJSON)
		os.Exit(0)
	}

	// Create the menu manager
	menuManager := menumanager.NewManager()

	// Process the application menu
	if appoptions.Menu != nil {
		err = menuManager.SetApplicationMenu(appoptions.Menu)
		if err != nil {
			return nil, err
		}
	}

	// Create the events subsystem (same as the desktop build).
	eventHandler := runtime.NewEvents(myLogger)
	ctx = context.WithValue(ctx, "events", eventHandler)

	// Attach logger to context
	if debug {
		ctx = context.WithValue(ctx, "buildtype", "debug")
	} else {
		ctx = context.WithValue(ctx, "buildtype", "production")
	}

	messageDispatcher := dispatcher.NewDispatcher(ctx, myLogger, bindings, eventHandler, appoptions.ErrorFormatter, appoptions.DisablePanicRecovery)
	appFrontend := webbridge.NewFrontend(ctx, appoptions, myLogger, bindings, messageDispatcher)
	eventHandler.AddFrontend(appFrontend)

	ctx = context.WithValue(ctx, "frontend", appFrontend)
	result := &App{
		ctx:              ctx,
		frontend:         appFrontend,
		logger:           myLogger,
		menuManager:      menuManager,
		startupCallback:  appoptions.OnStartup,
		shutdownCallback: appoptions.OnShutdown,
		debug:            debug,
		devtoolsEnabled:  devtoolsEnabled,
		options:          appoptions,
	}

	return result, nil
}

// newBindings is the bindings database used by both App mode and generation
// mode — by construction it is exactly the database the desktop build uses
// for `wails generate module`.
func newBindings(myLogger *logger.Logger, appoptions *options.App) (*binding.Bindings, error) {
	// Create binding exemptions - Ugly hack. There must be a better way
	bindingExemptions := []interface{}{
		appoptions.OnStartup,
		appoptions.OnShutdown,
		appoptions.OnDomReady,
		appoptions.OnBeforeClose,
	}

	result := binding.NewBindings(myLogger, appoptions.Bind, bindingExemptions, IsObfuscated(), appoptions.EnumBind)
	return result, nil
}
