package meeting

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
)

type wavWriter struct {
	file    *os.File
	rate    int
	samples int64
}

func newWAV(path string, rate int) (*wavWriter, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	w := &wavWriter{file: f, rate: rate}
	if _, e = f.Write(wavHeader(rate, 0)); e != nil {
		_ = f.Close()
		return nil, e
	}
	return w, nil
}
func wavHeader(rate int, samples int64) []byte {
	b := make([]byte, 44)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+samples*2))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(samples*2))
	return b
}
func (w *wavWriter) write(samples []float32, channels int) error {
	if channels < 1 || len(samples)%channels != 0 {
		return errors.New("Некорректный аудиофрейм")
	}
	b := make([]byte, len(samples)/channels*2)
	for i := 0; i < len(b)/2; i++ {
		var v float64
		for c := range channels {
			v += float64(samples[i*channels+c])
		}
		v /= float64(channels)
		if math.IsNaN(v) {
			v = 0
		}
		v = max(-1, min(1, v))
		n := int16(math.Round(v * 32767))
		if v <= -1 {
			n = -32768
		}
		binary.LittleEndian.PutUint16(b[i*2:], uint16(n))
	}
	n, e := w.file.Write(b)
	w.samples += int64(n / 2)
	return e
}
func (w *wavWriter) close() error {
	_, e := w.file.WriteAt(wavHeader(w.rate, w.samples), 0)
	return errors.Join(e, w.file.Sync(), w.file.Close())
}
func repairWAV(path string) error {
	f, e := os.OpenFile(path, os.O_RDWR, 0)
	if e != nil {
		return e
	}
	_, e = repairFile(f)
	return errors.Join(e, f.Close())
}
func repairFile(f *os.File) (int64, error) {
	b := make([]byte, 44)
	if _, e := f.ReadAt(b, 0); e != nil {
		return 0, e
	}
	if string(b[:4]) != "RIFF" || string(b[8:16]) != "WAVEfmt " || binary.LittleEndian.Uint16(b[20:]) != 1 || binary.LittleEndian.Uint16(b[22:]) != 1 || binary.LittleEndian.Uint16(b[34:]) != 16 {
		return 0, errArchive
	}
	rate := int(binary.LittleEndian.Uint32(b[24:]))
	info, e := f.Stat()
	if e != nil {
		return 0, e
	}
	n := (info.Size() - 44) / 2
	if n < 0 || n > int64(rate)*PartSeconds {
		return 0, errArchive
	}
	if e = f.Truncate(44 + n*2); e != nil {
		return 0, e
	}
	_, e = f.WriteAt(wavHeader(rate, n), 0)
	return n, e
}
