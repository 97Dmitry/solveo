package audiowindow

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestAssetsSelectAudioEntryAndPreserveSharedChunks(t *testing.T) {
	assets := Assets(fstest.MapFS{"index.html": {Data: []byte("main")}, "audio.html": {Data: []byte("audio")}, "assets/shared.js": {Data: []byte("chunk")}})
	for path, want := range map[string]string{"index.html": "audio", "assets/shared.js": "chunk"} {
		got, err := fs.ReadFile(assets, path)
		if err != nil || string(got) != want {
			t.Fatal(path, string(got), err)
		}
	}
}
func TestChildEnvironmentDoesNotReuseWailsDevServers(t *testing.T) {
	got := ChildEnvironment([]string{"Path=tools", "devserver=localhost:34115", "FRONTENDDEVSERVERURL=http://localhost:5173", "assetdir=frontend/dist", "LOCALAPPDATA=local"})
	if len(got) != 2 || got[0] != "Path=tools" || got[1] != "LOCALAPPDATA=local" {
		t.Fatal("dev settings escaped into child", got)
	}
}
