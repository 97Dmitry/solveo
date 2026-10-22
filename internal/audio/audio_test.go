package audio

import (
	"testing"
	"time"
)

func collect(f *framer) *[]Frame {
	var frames []Frame
	f.sink = func(fr Frame) { frames = append(frames, fr) }
	return &frames
}

func TestFramerSplitsIntoHundredMillisecondFrames(t *testing.T) {
	f := &framer{rate: 48000, channels: 2}
	frames := collect(f)
	f.push(make([]float32, 48000/4*2)) // 250 ms
	var sizes []int
	for _, fr := range *frames {
		if fr.Rate != 48000 || fr.Channels != 2 {
			t.Fatalf("format %d/%d", fr.Rate, fr.Channels)
		}
		sizes = append(sizes, len(fr.Samples))
	}
	if len(sizes) != 3 || sizes[0] != 9600 || sizes[1] != 9600 || sizes[2] != 4800 {
		t.Fatalf("frame sizes %v", sizes)
	}
}

func TestFramerCopiesSamples(t *testing.T) {
	f := &framer{rate: 16000, channels: 1}
	frames := collect(f)
	src := []float32{1, 2, 3}
	f.push(src)
	src[0] = 9
	if (*frames)[0].Samples[0] != 1 {
		t.Fatal("frame shares the capture buffer")
	}
}

func TestSilenceFillerCoversPacketGaps(t *testing.T) {
	start := time.Unix(0, 0)
	s := &silenceFiller{rate: 48000, channels: 2}
	s.packet(start)
	total := 0
	for ms := 10; ms <= 1000; ms += 10 {
		total += len(s.fill(start.Add(time.Duration(ms) * time.Millisecond)))
	}
	if total != 48000*2 {
		t.Fatalf("filled %d samples for 1 s of silence, want %d", total, 48000*2)
	}
}

func TestSilenceFillerWaitsBeforeFilling(t *testing.T) {
	start := time.Unix(0, 0)
	s := &silenceFiller{rate: 16000, channels: 1}
	s.packet(start)
	if n := len(s.fill(start.Add(20 * time.Millisecond))); n != 0 {
		t.Fatalf("filled %d samples within the packet jitter window", n)
	}
	s.packet(start.Add(25 * time.Millisecond))
	if n := len(s.fill(start.Add(50 * time.Millisecond))); n != 0 {
		t.Fatalf("a fresh packet must reset the gap, filled %d", n)
	}
	if n := len(s.fill(start.Add(100 * time.Millisecond))); n != 1200 {
		t.Fatalf("filled %d, want 75 ms = 1200 samples", n)
	}
}

func TestConvertFormats(t *testing.T) {
	f32 := []byte{0, 0, 0, 63, 0, 0, 128, 190} // 0.5, -0.25
	got, err := convert(f32, formatFloat32)
	if err != nil || got[0] != 0.5 || got[1] != -0.25 {
		t.Fatalf("float32: %v %v", got, err)
	}
	pcm := []byte{0, 64, 0, 192} // 16384, -16384
	got, err = convert(pcm, formatPCM16)
	if err != nil || got[0] != 0.5 || got[1] != -0.5 {
		t.Fatalf("pcm16: %v %v", got, err)
	}
}

func TestFramerKeepsFramesUnderTheProtocolLimit(t *testing.T) {
	for _, format := range []struct{ rate, channels int }{{96000, 2}, {48000, 8}, {192000, 2}, {48000, 2}} {
		f := &framer{rate: format.rate, channels: format.channels}
		frames := collect(f)
		f.push(make([]float32, format.rate/4*format.channels))
		total := 0
		for _, fr := range *frames {
			if 4*len(fr.Samples) > MaxFrameBytes || len(fr.Samples)%format.channels != 0 {
				t.Fatalf("%d Hz x%d: frame of %d samples", format.rate, format.channels, len(fr.Samples))
			}
			total += len(fr.Samples)
		}
		if total != format.rate/4*format.channels {
			t.Fatalf("%d Hz x%d: lost samples", format.rate, format.channels)
		}
	}
}
