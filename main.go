package main

import (
	"embed"
	"fmt"
	"net/http"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:            "MintDesk Robinhood",
		Width:            1260,
		Height:           820,
		MinWidth:         1080,
		MinHeight:        700,
		BackgroundColour: &options.RGBA{R: 14, G: 18, B: 24, A: 1},
		AssetServer:      &assetserver.Options{Assets: assets, Middleware: noCacheAssetMiddleware},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
	})
	if err != nil {
		fmt.Println("Mint Desk failed to start:", err)
	}
}

func noCacheAssetMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		response.Header().Set("Pragma", "no-cache")
		response.Header().Set("Expires", "0")
		next.ServeHTTP(response, request)
	})
}
