package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Create an instance of the app structure
	app := NewApp()
	defer app.Close()

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "TCGScanner",
		Width:  1024,
		Height: 768,
		// Below this the scan screen squeezes the camera view (sidebar and
		// capture panel are fixed-width).
		MinWidth:         1024,
		MinHeight:        640,
		WindowStartState: options.Maximised,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// The UI's background (#161826), so no other colour flashes at startup.
		BackgroundColour: &options.RGBA{R: 22, G: 24, B: 38, A: 1},
		OnStartup:        app.startup,
		Bind: []any{
			app,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
