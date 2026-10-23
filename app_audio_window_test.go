//go:build bindings

package main

import (
	"github.com/97Dmitry/solveo/internal/audiowindow"
	"testing"
)

func TestAudioRevealWaitsForRendererAndIgnoresOldSnapshots(t *testing.T) {
	shown := 0
	app := &AudioWindowApp{show: func() { shown++ }}
	app.reveal(3)
	if shown != 0 {
		t.Fatal("window shown before renderer readiness")
	}
	app.isReady = true
	app.reveal(3)
	app.reveal(2)
	app.reveal(3)
	if shown != 1 {
		t.Fatal("old snapshot repeated reveal")
	}
	app.reveal(4)
	if shown != 2 {
		t.Fatal("new open request was lost")
	}
}

func TestAudioDockCommandsStayInRenderer(t *testing.T) {
	attached, detached := false, false
	app := &AudioWindowApp{dockLeft: func() error { attached = true; return nil }, float: func() error { detached = true; return nil }}
	for _, name := range []string{"dockLeft", "float"} {
		if err := app.Execute(audiowindow.Command{Name: name}); err != nil {
			t.Fatal("layout command reached missing parent client", err)
		}
	}
	if !attached || !detached {
		t.Fatal("layout callbacks were not invoked")
	}
}
