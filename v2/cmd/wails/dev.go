package main

import (
	"os"
	"time"

	"github.com/pkg/browser"
	"github.com/pterm/pterm"
	"github.com/wailsapp/wails/v2/cmd/wails/flags"
	"github.com/wailsapp/wails/v2/cmd/wails/internal/dev"
	"github.com/wailsapp/wails/v2/internal/colour"
	"github.com/wailsapp/wails/v2/pkg/clilogger"
	"github.com/wailsapp/wails/v2/pkg/commands/build"
)

func devApplication(f *flags.Dev) error {
	if f.NoColour {
		pterm.DisableColor()
		colour.ColourEnabled = false
	}

	quiet := f.Verbosity == flags.Quiet

	// Create logger
	logger := clilogger.New(os.Stdout)
	logger.Mute(quiet)

	if quiet {
		pterm.DisableOutput()
	} else {
		app.PrintBanner()
	}

	err := f.Process()
	if err != nil {
		return err
	}

	if build.IsWebPlatform(f.Platform) {
		return devWebApplication(f, logger)
	}

	return dev.Application(f, logger)
}

// devWebApplication builds the project as a wasm/webbridge bundle and serves
// the dist directory over plain HTTP. v1 of the web dev flow has no watcher:
// changes require re-running the build and refreshing the browser.
func devWebApplication(f *flags.Dev, logger *clilogger.CLILogger) error {
	buildOptions := &build.Options{
		Logger:         logger,
		OutputType:     "web",
		Mode:           build.Dev,
		LDFlags:        f.LdFlags,
		Compiler:       f.Compiler,
		ForceBuild:     f.ForceBuild,
		IgnoreFrontend: f.SkipFrontend,
		SkipModTidy:    f.SkipModTidy,
		Verbosity:      f.Verbosity,
		ProjectData:    f.ProjectConfig(),
	}
	result, err := build.WebBuild(buildOptions)
	if err != nil {
		return err
	}

	pterm.Warning.Println("wails dev -platform web does not reload automatically yet — rebuild with `wails build -platform web` and refresh the browser to pick up changes.")

	if f.Browser {
		go func() {
			time.Sleep(500 * time.Millisecond)
			if err := browser.OpenURL("http://" + f.DevServer); err != nil {
				pterm.Warning.Println("cannot open the browser: " + err.Error())
			}
		}()
	}
	return build.ServeWebApp(result.DistDirectory, f.DevServer)
}
