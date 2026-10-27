//go:build js

package application

import (
	"errors"
	"unsafe"
)

// webApp implements platformApp for web (GOOS=js GOARCH=wasm) builds.
//
// This is a compile-only arm so that a js/wasm object can be produced by
// `wails3 build -platform web`. The actual web bridge semantics — installing
// the `__wailsWasmCall` dispatcher, wiring event traffic, window/DomReady
// lifecycle — belong to the upcoming web dispatcher milestone; until then the
// bindings runtime on the JS side buffers calls and reports a pending bridge
// (see internal/runtime/runtime wasm.ts and the bootstrap glue).
type webApp struct {
	app *App
}

// newPlatformApp creates the web-mode platform app.
func newPlatformApp(app *App) *webApp {
	return &webApp{app: app}
}

func (w *webApp) run() error {
	w.app.warning("Web/WASM target: this build does not install the bridge dispatcher yet - " +
		"binding calls from the frontend will buffer and time out. Run the desktop target for a fully working application.")
	return nil
}

func (w *webApp) destroy() {
	w.app.cleanup()
}

func (w *webApp) setApplicationMenu(menu *Menu) {
	// No-op: the web target has no native menus yet.
}

func (w *webApp) name() string {
	return w.app.options.Name
}

func (w *webApp) getCurrentWindowID() uint {
	return 0
}

func (w *webApp) showAboutDialog(name string, description string, icon []byte) {
	// No-op: the web target has no native about dialog yet.
}

func (w *webApp) setIcon(icon []byte) {
	// No-op: the web target has no native window chrome.
}

func (w *webApp) on(id uint) {
	// No-op: no platform-specific event handling on the web target yet.
}

func (w *webApp) dispatchOnMainThread(id uint) {
	// WebAssembly execution is single-threaded: run the callback inline.
	mainThreadFunctionStoreLock.Lock()
	fn, ok := mainThreadFunctionStore[id]
	if ok {
		delete(mainThreadFunctionStore, id)
	}
	mainThreadFunctionStoreLock.Unlock()

	if ok && fn != nil {
		fn()
	}
}

func (w *webApp) hide() {
	// No-op: the web target has no native window to hide.
}

func (w *webApp) show() {
	// No-op: the web target has no native window to show.
}

func (w *webApp) getPrimaryScreen() (*Screen, error) {
	return nil, errors.New("screen information not available on the web target")
}

func (w *webApp) getScreens() ([]*Screen, error) {
	return nil, errors.New("screen information not available on the web target")
}

func (w *webApp) GetFlags(options Options) map[string]any {
	flags := make(map[string]any)
	flags["web"] = true
	if options.Flags != nil {
		for k, v := range options.Flags {
			flags[k] = v
		}
	}
	return flags
}

func (w *webApp) isOnMainThread() bool {
	// Single-threaded: always on the main thread.
	return true
}

func (w *webApp) isDarkMode() bool {
	return false
}

func (w *webApp) getAccentColor() string {
	return ""
}

// logPlatformInfo logs platform info for the web target.
func (a *App) logPlatformInfo() {
	a.info("Platform Info:", "mode", "web/wasm")
}

// platformEnvironment returns environment info for the web target.
func (a *App) platformEnvironment() map[string]any {
	return map[string]any{
		"mode": "web-wasm",
	}
}

// fatalHandler is a no-op on the web target: fatal errors are surfaced via
// the standard logging mechanisms.
func fatalHandler(errFunc func(error)) {
}

// newClipboardImpl returns a clipboard implementation stub for the web
// target (no native clipboard access yet).
func newClipboardImpl() clipboardImpl {
	return &webClipboard{}
}

type webClipboard struct{}

func (c *webClipboard) setText(text string) bool {
	return false
}

func (c *webClipboard) text() (string, bool) {
	return "", false
}

// newDialogImpl returns a dialog implementation stub for the web target.
func newDialogImpl(d *MessageDialog) messageDialogImpl {
	return &webDialog{}
}

type webDialog struct{}

func (d *webDialog) show() {
	// No-op on the web target.
}

func newOpenFileDialogImpl(d *OpenFileDialogStruct) openFileDialogImpl {
	return &webFileDialog{}
}

type webFileDialog struct{}

func (d *webFileDialog) show() (chan string, error) {
	ch := make(chan string, 1)
	close(ch)
	return ch, errors.New("file dialogs are not available on the web target")
}

func newSaveFileDialogImpl(d *SaveFileDialogStruct) saveFileDialogImpl {
	return &webFileDialog{}
}

// newMenuImpl creates a menu implementation for the web target.
func newMenuImpl(menu *Menu) menuImpl {
	return &webMenu{}
}

// webMenu is a no-op menu for the web target.
type webMenu struct{}

func (m *webMenu) update() {
	// No-op on the web target
}

// newPlatformLock creates a platform-specific single instance lock for the
// web target.
func newPlatformLock(manager *singleInstanceManager) (platformLock, error) {
	return &webLock{}, nil
}

// webLock is a basic lock for the web target.
type webLock struct{}

func (l *webLock) acquire(uniqueID string) error {
	return nil
}

func (l *webLock) release() {
	// No-op on the web target
}

func (l *webLock) notify(data string) error {
	return errors.New("single instance not supported on the web target")
}

// newSystemTrayImpl creates a system tray implementation for the web target.
func newSystemTrayImpl(s *SystemTray) systemTrayImpl {
	return &webSystemTray{parent: s}
}

// webSystemTray is a no-op system tray for the web target.
type webSystemTray struct {
	parent *SystemTray
}

func (t *webSystemTray) setLabel(label string)            {}
func (t *webSystemTray) setTooltip(tooltip string)        {}
func (t *webSystemTray) run()                             {}
func (t *webSystemTray) setIcon(icon []byte)              {}
func (t *webSystemTray) setMenu(menu *Menu)               {}
func (t *webSystemTray) setIconPosition(pos IconPosition) {}
func (t *webSystemTray) setTemplateIcon(icon []byte)      {}
func (t *webSystemTray) destroy()                         {}
func (t *webSystemTray) setDarkModeIcon(icon []byte)      {}
func (t *webSystemTray) bounds() (*Rect, error) {
	return nil, errors.New("system tray not available on the web target")
}
func (t *webSystemTray) getScreen() (*Screen, error) {
	return nil, errors.New("system tray not available on the web target")
}
func (t *webSystemTray) positionWindow(w Window, o int) error {
	return errors.New("system tray not available on the web target")
}
func (t *webSystemTray) openMenu() {}
func (t *webSystemTray) Show()     {}
func (t *webSystemTray) Hide()     {}

// newWindowImpl creates a webview window implementation for the web target.
func newWindowImpl(parent *WebviewWindow) *webWebviewWindow {
	return &webWebviewWindow{parent: parent}
}

// webWebviewWindow is a no-op webview window for the web target (the web
// target renders the frontend in the user's browser, not in a WebviewWindow).
type webWebviewWindow struct {
	parent *WebviewWindow
}

// All webviewWindowImpl methods as no-ops for the web target
func (w *webWebviewWindow) setTitle(title string)           {}
func (w *webWebviewWindow) setSize(width, height int)       {}
func (w *webWebviewWindow) setAlwaysOnTop(alwaysOnTop bool) {}
func (w *webWebviewWindow) setURL(url string)               {}
func (w *webWebviewWindow) setResizable(resizable bool)     {}
func (w *webWebviewWindow) setMinSize(width, height int)    {}
func (w *webWebviewWindow) setMaxSize(width, height int)    {}
func (w *webWebviewWindow) execJS(js string)                {}
func (w *webWebviewWindow) setBackgroundColour(color RGBA)  {}
func (w *webWebviewWindow) run()                            {}
func (w *webWebviewWindow) center()                         {}
func (w *webWebviewWindow) size() (int, int)                { return 0, 0 }
func (w *webWebviewWindow) width() int                      { return 0 }
func (w *webWebviewWindow) height() int                     { return 0 }
func (w *webWebviewWindow) destroy()                        {}
func (w *webWebviewWindow) reload()                         {}
func (w *webWebviewWindow) forceReload()                    {}
func (w *webWebviewWindow) openDevTools()                   {}
func (w *webWebviewWindow) zoomReset()                      {}
func (w *webWebviewWindow) zoomIn()                         {}
func (w *webWebviewWindow) zoomOut()                        {}
func (w *webWebviewWindow) getZoom() float64                { return 1.0 }
func (w *webWebviewWindow) setZoom(zoom float64)            {}
func (w *webWebviewWindow) close()                          {}
func (w *webWebviewWindow) zoom()                           {}
func (w *webWebviewWindow) setHTML(html string)             {}
func (w *webWebviewWindow) on(eventID uint)                 {}
func (w *webWebviewWindow) minimise()                       {}
func (w *webWebviewWindow) unminimise()                     {}
func (w *webWebviewWindow) maximise()                       {}
func (w *webWebviewWindow) unmaximise()                     {}
func (w *webWebviewWindow) fullscreen()                     {}
func (w *webWebviewWindow) unfullscreen()                   {}
func (w *webWebviewWindow) isMinimised() bool               { return false }
func (w *webWebviewWindow) isMaximised() bool               { return false }
func (w *webWebviewWindow) isFullscreen() bool              { return false }
func (w *webWebviewWindow) isNormal() bool                  { return true }
func (w *webWebviewWindow) isVisible() bool                 { return false }
func (w *webWebviewWindow) isFocused() bool                 { return false }
func (w *webWebviewWindow) focus()                          {}
func (w *webWebviewWindow) show()                           {}
func (w *webWebviewWindow) hide()                           {}
func (w *webWebviewWindow) getScreen() (*Screen, error) {
	return nil, errors.New("screens not available on the web target")
}
func (w *webWebviewWindow) setFrameless(frameless bool)                       {}
func (w *webWebviewWindow) openContextMenu(menu *Menu, data *ContextMenuData) {}
func (w *webWebviewWindow) nativeWindow() unsafe.Pointer                      { return nil }
func (w *webWebviewWindow) startDrag() error {
	return errors.New("drag not available on the web target")
}
func (w *webWebviewWindow) startResize(border string) error {
	return errors.New("resize not available on the web target")
}
func (w *webWebviewWindow) print() error                                        { return errors.New("print not available on the web target") }
func (w *webWebviewWindow) setEnabled(enabled bool)                             {}
func (w *webWebviewWindow) physicalBounds() Rect                                { return Rect{} }
func (w *webWebviewWindow) setPhysicalBounds(bounds Rect)                       {}
func (w *webWebviewWindow) bounds() Rect                                        { return Rect{} }
func (w *webWebviewWindow) setBounds(bounds Rect)                               {}
func (w *webWebviewWindow) position() (int, int)                                { return 0, 0 }
func (w *webWebviewWindow) setPosition(x int, y int)                            {}
func (w *webWebviewWindow) centerOnScreen(_ *Screen)                            {}
func (w *webWebviewWindow) relativePosition() (int, int)                        { return 0, 0 }
func (w *webWebviewWindow) setRelativePosition(x int, y int)                    {}
func (w *webWebviewWindow) flash(enabled bool)                                  {}
func (w *webWebviewWindow) handleKeyEvent(acceleratorString string)             {}
func (w *webWebviewWindow) getBorderSizes() *LRTB                               { return &LRTB{} }
func (w *webWebviewWindow) setMinimiseButtonState(state ButtonState)            {}
func (w *webWebviewWindow) setMaximiseButtonState(state ButtonState)            {}
func (w *webWebviewWindow) setCloseButtonState(state ButtonState)               {}
func (w *webWebviewWindow) setFullscreenButtonState(state ButtonState)          {}
func (w *webWebviewWindow) isIgnoreMouseEvents() bool                           { return false }
func (w *webWebviewWindow) setIgnoreMouseEvents(ignore bool)                    {}
func (w *webWebviewWindow) cut()                                                {}
func (w *webWebviewWindow) copy()                                               {}
func (w *webWebviewWindow) paste()                                              {}
func (w *webWebviewWindow) undo()                                               {}
func (w *webWebviewWindow) delete()                                             {}
func (w *webWebviewWindow) selectAll()                                          {}
func (w *webWebviewWindow) redo()                                               {}
func (w *webWebviewWindow) showMenuBar()                                        {}
func (w *webWebviewWindow) hideMenuBar()                                        {}
func (w *webWebviewWindow) toggleMenuBar()                                      {}
func (w *webWebviewWindow) setMenu(menu *Menu)                                  {}
func (w *webWebviewWindow) snapAssist()                                         {}
func (w *webWebviewWindow) attachModal(modalWindow *WebviewWindow)              {}
func (w *webWebviewWindow) setContentProtection(enabled bool)                   {}
func (w *webWebviewWindow) setNonClientHitTestRegions([]nonClientHitTestRegion) {}
