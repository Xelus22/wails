//go:build !js

package main

import "github.com/wailsapp/wails/v3/pkg/application"

func registeredServices() []application.Service {
	return []application.Service{
		application.NewService(&Settings{}),
		application.NewService(&Dummy{}),
		application.NewService(&DesktopOnly{}),
	}
}
