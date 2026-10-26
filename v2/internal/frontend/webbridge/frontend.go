//go:build webbridge && (js || wasm)

// Package webbridge provides the browser (WASM) frontend for Wails
// applications compiled with GOOS=js GOARCH=wasm and the `webbridge` tag.
//
// It implements the full frontend.Frontend interface on top of js/wasm and
// speaks exactly the same IPC protocol as the desktop frontends, so the
// standard Wails runtime JS behaves identically in the browser:
//
//   - the frontend runtime calls window.WailsInvoke(message) for logs,
//     events and bound-method calls ("C"-messages);
//   - this package processes those messages with the standard dispatcher and
//     answers through window.wails.Callback(...);
//   - events are pushed to the runtime through window.wails.EventsNotify(...);
//   - the bindings database is exposed as window.wailsbindings, so the
//     standard runtime installs window.go.<ns>.<Struct>.<Method> proxies.
//
// The browser glue (../webwrapper/wasm-bridge.js) boots the WASM module and
// installs the window.WailsInvoke shim plus plumbing for
// __wailsWasmDispatch / __wailsWasmDomReady, which are implemented here.
//
// js.FuncOf handlers must be kept referenced from Go, otherwise the finalizer
// releases them and callbacks silently die — hence the package-level
// variables below.
package webbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"syscall/js"
	"time"

	"github.com/wailsapp/wails/v2/internal/binding"
	"github.com/wailsapp/wails/v2/internal/frontend"
	"github.com/wailsapp/wails/v2/internal/logger"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
)

// MessageProcessor is the subset of the frontend dispatcher this package drives.
type MessageProcessor interface {
	ProcessMessage(message string, sender frontend.Frontend) (string, error)
}

// Handlers are kept in package-level vars so they are never finalized while
// the program runs.
var (
	dispatchFunc js.Func // __wailsWasmDispatch(message string)
	domReadyFunc js.Func // __wailsWasmDomReady()

	installMu sync.Mutex
	current   *Frontend
	installed bool
)

// Frontend is the js/wasm implementation of frontend.Frontend.
type Frontend struct {
	ctx       context.Context
	options   *options.App
	logger    *logger.Logger
	bindings  *binding.Bindings
	processor MessageProcessor

	mu sync.Mutex // guards the pending queues below

	// pendingCallbacks holds desktop-style call-result messages produced
	// before the runtime JS had installed window.wails.Callback.
	pendingCallbacks []string
	// pendingEvents holds EventsNotify payloads buffered while window.wails
	// is not yet available.
	pendingEvents []string
}

// NewFrontend creates a new web (WASM) frontend.
func NewFrontend(ctx context.Context, appoptions *options.App, myLogger *logger.Logger, bindings *binding.Bindings, processor MessageProcessor) *Frontend {
	return &Frontend{
		ctx:       ctx,
		options:   appoptions,
		logger:    myLogger,
		bindings:  bindings,
		processor: processor,
	}
}

// Run starts the frontend. On desktop this launches the webview event loop;
// in the browser it installs the JS seam, publishes the bindings database,
// signals readiness and then parks the runtime so the WASM module stays
// alive.
func (f *Frontend) Run(ctx context.Context) error {
	f.ctx = ctx

	if err := f.install(); err != nil {
		return err
	}

	// Desktop frontends invoke OnStartup inside their own Run
	// implementations — mirror that ordering.
	if f.options.OnStartup != nil {
		f.options.OnStartup(ctx)
	}

	keepAlive()
	return nil
}

// install registers the __wailsWasm* handlers and publishes the bindings
// database to the runtime.
func (f *Frontend) install() error {
	installMu.Lock()
	defer installMu.Unlock()

	if !installed {
		dispatchFunc = js.FuncOf(func(_ js.Value, args []js.Value) any {
			return dispatchToProcessor(args)
		})
		domReadyFunc = js.FuncOf(func(_ js.Value, _ []js.Value) any {
			f.onDomReady()
			return "ok"
		})
		js.Global().Set("__wailsWasmDispatch", dispatchFunc)
		js.Global().Set("__wailsWasmDomReady", domReadyFunc)
		installed = true
	}
	current = f

	// Make sure the bindings are present before (or as) the runtime JS
	// evaluates, exactly like the desktop asset server does.
	f.publishBindings()

	// The browser glue may have detected DOM readiness before this WASM
	// module booted (module scripts are deferred); it then sets this flag
	// instead of calling an uninstalled handler.
	readyRequested := js.Global().Get("__wailsWasmDomReadyRequested")
	if readyRequested.Type() == js.TypeBoolean && readyRequested.Bool() {
		js.Global().Set("__wailsWasmDomReadyRequested", false)
		f.onDomReady()
	}

	// Readiness for glue boot logic, pollers and smoke harnesses.
	markReady()

	f.flushPending()
	return nil
}

// publishBindings exposes the bindings database as `window.wailsbindings`
// (desktop parity: the desktop asset server sets the same global before the
// runtime script evaluates). If the runtime already evaluated, feed it
// directly instead.
func (f *Frontend) publishBindings() {
	bindingsJSON, err := f.bindings.ToJSON()
	if err != nil {
		f.logger.Error("webbridge: cannot serialise the bindings database: %s", err.Error())
		return
	}

	if f.callWailsIfAvailable("SetBindings", bindingsJSON) {
		f.flushPending()
		return
	}
	// Runtime JS has not evaluated yet; it will pick the global up on its own.
	js.Global().Set("wailsbindings", bindingsJSON)
}

func (f *Frontend) onDomReady() {
	if f.options.OnDomReady != nil {
		go f.options.OnDomReady(f.ctx)
	}
}

// dispatchToProcessor is the JS→Go seam used by the browser glue's
// window.WailsInvoke shim. "C"/"c" bound-method calls run on their own
// goroutine so a slow bound method never stalls the JS event loop; results
// are delivered asynchronously through window.wails.Callback. Every other
// message type is processed synchronously, mirroring desktop dispatcher
// semantics.
func dispatchToProcessor(args []js.Value) any {
	if current == nil {
		return "not-ready"
	}
	if len(args) != 1 {
		return "bad-arity"
	}
	f := current
	message := args[0].String()
	if message == "" {
		return "bad-message"
	}

	if message[0] == 'C' || message[0] == 'c' {
		go func() {
			result, err := f.processor.ProcessMessage(message, f)
			if !isCallResponse(result) {
				if err != nil {
					f.logger.Error("webbridge: %s", err.Error())
				}
				return
			}
			f.Callback(result[1:])
		}()
		return "ok"
	}

	result, err := f.processor.ProcessMessage(message, f)
	if err != nil {
		f.logger.Debug("webbridge: message error (%s): %s", firstRunes(message, 24), err.Error())
	}
	_ = result
	return "ok"
}

func isCallResponse(result string) bool {
	return len(result) > 1 && result[0] == 'c'
}

// JS plumbing ----------------------------------------------------------------

// execJS runs a JS snippet in the browser (desktop parity for the injected
// snippets the desktop frontends produce).
func (f *Frontend) ExecJS(jsSnippet string) {
	defer func() {
		if r := recover(); r != nil {
			f.logger.Debug("webbridge: execJS failed: %v", r)
		}
	}()
	js.Global().Call("eval", jsSnippet)
}

// callWailsIfAvailable invokes window.wails.<fn>(arg) and reports whether it
// could be delivered. Undeliverable traffic stays queued and is flushed on
// the next successful delivery.
func (f *Frontend) callWailsIfAvailable(fn string, arg string) bool {
	wails, ok := wailsObject()
	if !ok {
		return false
	}
	target := wails.Get(fn)
	if target.Type() != js.TypeFunction {
		return false
	}
	target.Invoke(arg)
	return true
}

// flushPending delivers leftover Callback/EventsNotify traffic once
// window.wails has appeared.
func (f *Frontend) flushPending() {
	wails, ok := wailsObject()
	if !ok {
		return
	}

	f.mu.Lock()
	callbacks := f.pendingCallbacks
	events := f.pendingEvents
	f.pendingCallbacks = nil
	f.pendingEvents = nil
	f.mu.Unlock()

	callback := wails.Get("Callback")
	eventsNotify := wails.Get("EventsNotify")

	if callback.Type() == js.TypeFunction {
		for _, msg := range callbacks {
			callback.Invoke(msg)
		}
	}
	if eventsNotify.Type() == js.TypeFunction {
		for _, payload := range events {
			eventsNotify.Invoke(payload)
		}
	}
}

// wailsObject returns window.wails (falling back to the bare global for
// headless harnesses where window === globalThis), or false when the runtime
// JS has not evaluated yet.
func wailsObject() (js.Value, bool) {
	w := js.Global().Get("window")
	if w.IsUndefined() || w.IsNull() {
		w = js.Global()
	}
	wails := w.Get("wails")
	if wails.IsUndefined() || wails.IsNull() {
		return js.Value{}, false
	}
	return wails, true
}

// Frontend interface ---------------------------------------------------------

// Callback delivers a call result to the runtime. Desktop parity: the desktop
// frontends eval `window.wails.Callback("<json>")` with the dispatcher's
// response message.
func (f *Frontend) Callback(message string) {
	if f.callWailsIfAvailable("Callback", message) {
		f.flushPending()
		return
	}
	f.mu.Lock()
	f.pendingCallbacks = append(f.pendingCallbacks, message)
	f.mu.Unlock()
}

// Notify pushes an event to the runtime. Desktop parity with the desktop
// frontends' Notify.
func (f *Frontend) Notify(name string, data ...interface{}) {
	payload, err := json.Marshal(notifyMessage{Name: name, Data: data})
	if err != nil {
		f.logger.Error("webbridge: cannot marshal event: %s", err.Error())
		return
	}
	if f.callWailsIfAvailable("EventsNotify", string(payload)) {
		f.flushPending()
		return
	}
	f.mu.Lock()
	f.pendingEvents = append(f.pendingEvents, string(payload))
	f.mu.Unlock()
}

type notifyMessage struct {
	Name string        `json:"name"`
	Data []interface{} `json:"data"`
}

// RunMainLoop is a no-op in the browser (there is no OS event loop).
func (f *Frontend) RunMainLoop() {}

// Quit is a no-op in the browser (the tab owner controls the lifecycle).
func (f *Frontend) Quit() {
	f.logger.Debug("webbridge: Quit is a no-op in the browser")
}

// WindowClose is a no-op in the browser.
func (f *Frontend) WindowClose() {
	f.logger.Debug("webbridge: WindowClose is a no-op in the browser")
}

// Hide is a no-op in the browser.
func (f *Frontend) Hide() { f.debugNoop("Hide") }

// Show is a no-op in the browser.
func (f *Frontend) Show() { f.debugNoop("Show") }

// Dialogs --------------------------------------------------------------------

func webNotSupported(what string) error {
	return fmt.Errorf("not supported in this environment: %s", what)
}

func (f *Frontend) OpenFileDialog(frontend.OpenDialogOptions) (string, error) {
	return "", webNotSupported("dialogs")
}

func (f *Frontend) OpenMultipleFilesDialog(frontend.OpenDialogOptions) ([]string, error) {
	return nil, webNotSupported("dialogs")
}

func (f *Frontend) OpenDirectoryDialog(frontend.OpenDialogOptions) (string, error) {
	return "", webNotSupported("dialogs")
}

func (f *Frontend) SaveFileDialog(frontend.SaveDialogOptions) (string, error) {
	return "", webNotSupported("dialogs")
}

func (f *Frontend) MessageDialog(frontend.MessageDialogOptions) (string, error) {
	return "", webNotSupported("dialogs")
}

// Window ---------------------------------------------------------------------

func (f *Frontend) WindowSetTitle(title string) {
	f.ExecJS(fmt.Sprintf("window.document.title = %q;", title))
}
func (f *Frontend) WindowShow()             { f.debugNoop("WindowShow") }
func (f *Frontend) WindowHide()             { f.debugNoop("WindowHide") }
func (f *Frontend) WindowCenter()           { f.debugNoop("WindowCenter") }
func (f *Frontend) WindowToggleMaximise()   { f.debugNoop("WindowToggleMaximise") }
func (f *Frontend) WindowMaximise()         { f.debugNoop("WindowMaximise") }
func (f *Frontend) WindowUnmaximise()       { f.debugNoop("WindowUnmaximise") }
func (f *Frontend) WindowMinimise()         { f.debugNoop("WindowMinimise") }
func (f *Frontend) WindowUnminimise()       { f.debugNoop("WindowUnminimise") }
func (f *Frontend) WindowSetAlwaysOnTop(bool) { f.debugNoop("WindowSetAlwaysOnTop") }
func (f *Frontend) WindowSetPosition(int, int) { f.debugNoop("WindowSetPosition") }
func (f *Frontend) WindowSetSize(int, int)     { f.debugNoop("WindowSetSize") }
func (f *Frontend) WindowSetMinSize(int, int)  { f.debugNoop("WindowSetMinSize") }
func (f *Frontend) WindowSetMaxSize(int, int)  { f.debugNoop("WindowSetMaxSize") }
func (f *Frontend) WindowFullscreen()          { f.debugNoop("WindowFullscreen") }
func (f *Frontend) WindowUnfullscreen()        { f.debugNoop("WindowUnfullscreen") }
func (f *Frontend) WindowSetSystemDefaultTheme() { f.debugNoop("WindowSetSystemDefaultTheme") }
func (f *Frontend) WindowSetLightTheme()         { f.debugNoop("WindowSetLightTheme") }
func (f *Frontend) WindowSetDarkTheme()          { f.debugNoop("WindowSetDarkTheme") }

func (f *Frontend) WindowSetBackgroundColour(col *options.RGBA) {
	if col == nil {
		return
	}
	f.ExecJS(fmt.Sprintf("document.documentElement.style.backgroundColor = 'rgb(%d,%d,%d)';", col.R, col.G, col.B))
}

func (f *Frontend) WindowReload() {
	f.ExecJS("window.location.reload();")
}

func (f *Frontend) WindowReloadApp() { f.WindowReload() }

func (f *Frontend) WindowIsMaximised() bool  { return false }
func (f *Frontend) WindowIsMinimised() bool  { return false }
func (f *Frontend) WindowIsNormal() bool     { return true }
func (f *Frontend) WindowIsFullscreen() bool { return false }

func (f *Frontend) WindowPrint() { f.ExecJS("window.print();") }

func (f *Frontend) WindowGetPosition() (int, int) { return 0, 0 }

func (f *Frontend) WindowGetSize() (int, int) {
	w, ok := windowObject()
	if !ok {
		return 0, 0
	}
	return intValue(w.Get("innerWidth")), intValue(w.Get("innerHeight"))
}

func (f *Frontend) debugNoop(what string) {
	f.logger.Debug("webbridge: %s is a no-op in the browser", what)
}

// Screen ---------------------------------------------------------------------

func (f *Frontend) ScreenGetAll() ([]frontend.Screen, error) {
	return nil, webNotSupported("screen")
}

// Menus ----------------------------------------------------------------------

func (f *Frontend) MenuSetApplicationMenu(_ *menu.Menu) {
	f.debugNoop("MenuSetApplicationMenu")
}
func (f *Frontend) MenuUpdateApplicationMenu() {
	f.debugNoop("MenuUpdateApplicationMenu")
}

// Browser --------------------------------------------------------------------

func (f *Frontend) BrowserOpenURL(url string) {
	if w, ok := windowObject(); ok {
		w.Call("open", url, "_blank")
	}
}

// Clipboard ------------------------------------------------------------------

func (f *Frontend) ClipboardGetText() (string, error) {
	return "", webNotSupported("clipboard")
}

func (f *Frontend) ClipboardSetText(text string) error {
	clip := js.Global().Get("navigator").Get("clipboard")
	if !clip.IsUndefined() && !clip.IsNull() {
		clip.Call("writeText", text)
		return nil
	}
	return webNotSupported("clipboard")
}

// Notifications --------------------------------------------------------------

func (f *Frontend) InitializeNotifications() error          { return webNotSupported("notifications") }
func (f *Frontend) CleanupNotifications()                   {}
func (f *Frontend) IsNotificationAvailable() bool           { return false }
func (f *Frontend) RequestNotificationAuthorization() (bool, error) {
	return false, webNotSupported("notifications")
}
func (f *Frontend) CheckNotificationAuthorization() (bool, error) {
	return false, webNotSupported("notifications")
}
func (f *Frontend) OnNotificationResponse(cli func(result frontend.NotificationResult)) { _ = cli }
func (f *Frontend) SendNotification(frontend.NotificationOptions) error {
	return webNotSupported("notifications")
}
func (f *Frontend) SendNotificationWithActions(frontend.NotificationOptions) error {
	return webNotSupported("notifications")
}
func (f *Frontend) RegisterNotificationCategory(_ frontend.NotificationCategory) error {
	return webNotSupported("notifications")
}
func (f *Frontend) RemoveNotificationCategory(string) error      { return webNotSupported("notifications") }
func (f *Frontend) RemoveAllPendingNotifications() error         { return webNotSupported("notifications") }
func (f *Frontend) RemovePendingNotification(string) error       { return webNotSupported("notifications") }
func (f *Frontend) RemoveAllDeliveredNotifications() error       { return webNotSupported("notifications") }
func (f *Frontend) RemoveDeliveredNotification(string) error     { return webNotSupported("notifications") }
func (f *Frontend) RemoveNotification(string) error              { return webNotSupported("notifications") }

// helpers --------------------------------------------------------------------

func windowObject() (js.Value, bool) {
	w := js.Global().Get("window")
	if w.IsUndefined() || w.IsNull() {
		return js.Value{}, false
	}
	return w, true
}

func intValue(v js.Value) int {
	if v.Type() != js.TypeNumber {
		return 0
	}
	return int(v.Float())
}

func keepAlive() {
	// Wake the scheduler periodically: the Go WASM runtime tears the whole
	// program down as soon as main returns, which would unregister every
	// js.FuncOf handler and kill the bridge.
	for {
		time.Sleep(1 * time.Hour)
	}
}

func markReady() {
	js.Global().Set("__wailsWasmReady", true)
	if onReady := js.Global().Get("__wailsWasmOnReady"); onReady.Type() == js.TypeFunction {
		onReady.Invoke()
	}
}

func firstRunes(message string, count int) string {
	if len(message) <= count {
		return message
	}
	return message[:count] + "…"
}
