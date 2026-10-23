//go:build bindings

package main

import (
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
)

func main() {
	if err := wails.Run(&options.App{Bind: []interface{}{&App{}, &AudioWindowApp{}}}); err != nil {
		log.Fatal(err)
	}
}
