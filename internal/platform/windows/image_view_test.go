package windows

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
)

func TestImageFitAndPanKeepImageVisible(t *testing.T) {
	if got := fitImageScale(1920, 1080, 960, 600); got != 0.5 {
		t.Fatalf("fit = %v", got)
	}
	if got := fitImageScale(100, 50, 960, 600); got != 1 {
		t.Fatalf("small image was enlarged: %v", got)
	}
	v := imageView{width: 1920, height: 1080, scale: 1, panX: 10000, panY: -10000}
	x, y, width, height := v.layout(960, 600)
	if x != 0 || y != -480 || width != 1920 || height != 1080 {
		t.Fatalf("pan escaped bounds: %d %d %d %d", x, y, width, height)
	}
	v.width, v.height, v.panX, v.panY = 100, 50, 99, -99
	x, y, _, _ = v.layout(960, 600)
	if x != 430 || y != 275 {
		t.Fatalf("small image not centered: %d %d", x, y)
	}
	if got := fitImageScale(1920, 1080, 0, 0); got <= 0 {
		t.Fatalf("invalid minimized scale: %v", got)
	}
}

func TestScreenshotPixelsUseTopDownBGRA(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.png")
	im := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	im.Set(0, 0, color.NRGBA{R: 255, A: 255})
	im.Set(1, 0, color.NRGBA{B: 255, A: 255})
	im.Set(0, 1, color.NRGBA{G: 255, A: 255})
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, im); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	pixels, width, height, err := loadScreenshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if width != 2 || height != 2 {
		t.Fatalf("dimensions %dx%d", width, height)
	}
	want := []byte{0, 0, 255, 255, 255, 0, 0, 255, 0, 255, 0, 255, 255, 255, 255, 255}
	if string(pixels) != string(want) {
		t.Fatalf("BGRA or row orientation wrong: %v", pixels)
	}
	if err := os.WriteFile(path, []byte("not a png"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadScreenshot(path); err == nil {
		t.Fatal("accepted corrupt PNG")
	}
}

func TestViewerNavigationSurvivesQueueCleanup(t *testing.T) {
	store, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var sources []string
	for _, name := range []string{"first.png", "second.png"} {
		path := filepath.Join(t.TempDir(), name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, path)
	}
	doc := chat.New()
	inputs := []history.Source{}
	for _, p := range sources {
		id := chat.ID()
		doc.Attachments = append(doc.Attachments, chat.Attachment{ID: id})
		inputs = append(inputs, history.Source{AttachmentID: id, TempPath: p})
	}
	doc, err = store.Save(history.SaveInput{Chat: doc, Sources: inputs})
	if err != nil {
		t.Fatal(err)
	}
	var archived []string
	for _, a := range doc.Attachments {
		path, err := store.ImagePath(doc.ID, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		archived = append(archived, path)
	}
	viewerPaths := remapImagePaths(sources, sources, archived)
	for _, path := range sources {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := loadScreenshot(viewerPaths[1]); err != nil {
		t.Fatal("cannot navigate after sending queue:", err)
	}
	if viewerPaths[0] != archived[0] || sources[0] == archived[0] {
		t.Fatal("rebound paths mutated the original queue")
	}
}
