package audiowindow

import (
	"image"
	"testing"
)

func TestLeftDockLayoutFitsAndTracksParentHeight(t *testing.T) {
	work := image.Rect(-1920, 0, 0, 1080)
	main := image.Rect(-900, 100, -200, 800)
	got, err := LeftDock(main, work, 420)
	if err != nil || got != image.Rect(-1328, 100, -908, 800) {
		t.Fatal(got, err)
	}
	if _, err := LeftDock(image.Rect(-1800, 100, -1000, 800), work, 420); err == nil {
		t.Fatal("offscreen docking accepted")
	}
	if _, err := LeftDock(main, work, 0); err == nil {
		t.Fatal("zero width accepted")
	}
}
func TestDockVisibilityDoesNotResurrectUserClosedWindow(t *testing.T) {
	d := DockState{Attached: true}
	if action := d.Visibility(false, true); action != DockHide {
		t.Fatal(action)
	}
	if action := d.Visibility(true, false); action != DockShow {
		t.Fatal(action)
	}
	d.Visibility(false, true)
	d.ManualHide()
	if action := d.Visibility(true, false); action != DockKeep {
		t.Fatal("user-closed window resurrected")
	}
	d.Attached = false
	if action := d.Visibility(false, true); action != DockKeep {
		t.Fatal("floating window follows parent visibility")
	}
}
