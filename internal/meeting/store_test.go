package meeting

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundtripAndProtection(t *testing.T) {
	s := NewStore(t.TempDir())
	d, err := s.New("Weekly", "", []string{"system"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(d.ID)
	if err != nil || got.Title != "Weekly" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err = s.Load("../escape"); err == nil {
		t.Fatal("traversal accepted")
	}
	if _, err = s.OpenAudio(d.ID, "../../escape"); err == nil {
		t.Fatal("audio traversal accepted")
	}
	if err = os.WriteFile(filepath.Join(s.dir, d.ID, "meeting.json"), []byte(`{"version":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.Save(d); err == nil {
		t.Fatal("newer document overwritten")
	}
}
func TestWAVDownmixAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wav")
	w, err := newWAV(path, 48000)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.write([]float32{2, 2, -2, -2, .5, .5}, 2); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash before header finalization.
	if err = w.file.Close(); err != nil {
		t.Fatal(err)
	}
	if err = repairWAV(path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 50 || binary.LittleEndian.Uint32(b[40:]) != 6 || int16(binary.LittleEndian.Uint16(b[44:])) != 32767 || int16(binary.LittleEndian.Uint16(b[46:])) != -32768 {
		t.Fatalf("bad PCM/header: %v", b)
	}
}

func TestRecoverKeepsGoodPartsWhenLastFileMissing(t *testing.T) {
	s := NewStore(t.TempDir())
	d, e := s.New("Meeting", "", []string{"mic"})
	if e != nil {
		t.Fatal(e)
	}
	d.Parts = []Part{{File: "mic-0000.wav", Track: "mic", Rate: 16000, Samples: 1, Complete: true}, {File: "mic-0001.wav", Track: "mic", Rate: 16000, Offset: 300}}
	if e = s.Save(d); e != nil {
		t.Fatal(e)
	}
	good := append(wavHeader(16000, 1), 0, 0)
	if e = os.WriteFile(filepath.Join(s.dir, d.ID, "mic-0000.wav"), good, 0600); e != nil {
		t.Fatal(e)
	}
	recovered, e := s.Recover(d.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(recovered.Parts) != 1 || recovered.Status != "interrupted" {
		t.Fatalf("%+v", recovered)
	}
}

func TestListDoesNotLoadTranscriptBodies(t *testing.T) {
	s := NewStore(t.TempDir())
	d, e := s.New("Meeting", "", []string{"mic"})
	if e != nil {
		t.Fatal(e)
	}
	// A damaged body must not prevent listing a valid bounded header; detail loading reports it.
	path := filepath.Join(s.dir, d.ID, "meeting.json")
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	at := bytes.Index(b, []byte(`"parts"`))
	if at < 0 {
		t.Fatal("missing parts")
	}
	b = append(b[:at], []byte(`"parts":[`)...)
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	list, e := s.List()
	if e != nil || len(list) != 1 {
		t.Fatalf("%+v %v", list, e)
	}
	if _, e = s.Load(d.ID); e == nil {
		t.Fatal("damaged body accepted")
	}
}
