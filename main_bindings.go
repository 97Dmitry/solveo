//go:build bindings

package main

import (
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"log"
)

func main() {
	if err := wails.Run(&options.App{Bind: []interface{}{&App{}}}); err != nil {
		log.Fatal(err)
	}
}
