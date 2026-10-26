// Package main is a minimal Wails v3 app exercising the js/wasm
// (webbridge) analysis path:
//
//	wails3 generate bindings --wasm --wasm-catalog
package main

import (
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func main() {
	app := application.New(application.Options{
		Services: registeredServices(),
	})

	err := app.Run()
	if err != nil {
		log.Fatal(err)
	}
}
