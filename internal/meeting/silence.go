package meeting

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
)

// timestampPadding covers rounded/approximate vendor word times. Only exact PCM
// zero is treated as silence: even one nonzero least-significant bit is retained.
const timestampPadding = .25

type pcmEvidence struct {
	file      *os.File
	rate      int
	samples   int64
	offset    float64
	neighbors []*pcmEvidence
}

func (m *Manager) openPCM(id string, p Part) (*pcmEvidence, error) {
	f, e := m.store.OpenAudio(id, p.File)
	if e != nil {
		return nil, e
	}
	header := make([]byte, 44)
	_, e = f.ReadAt(header, 0)
	info, statErr := f.Stat()
	if e != nil || statErr != nil || info.Size() != 44+p.Samples*2 || !bytes.Equal(header, wavHeader(p.Rate, p.Samples)) {
		_ = f.Close()
		return nil, errors.New("Аудиофайл повреждён или не соответствует метаданным встречи")
	}
	return &pcmEvidence{file: f, rate: p.Rate, samples: p.Samples, offset: p.Offset}, nil
}

// Open only contiguous neighbors used by overlap. Evidence must follow the same
// timeline as ASR, including padding across a part boundary.
func (m *Manager) openEvidence(d Document, index int) (*pcmEvidence, error) {
	part := d.Parts[index]
	pcm, e := m.openPCM(d.ID, part)
	if e != nil {
		return nil, e
	}
	end := part.Offset + float64(part.Samples)/float64(part.Rate)
	for i, q := range d.Parts {
		if i == index || q.Track != part.Track || q.Rate != part.Rate || !q.Complete {
			continue
		}
		before := abs(q.Offset+float64(q.Samples)/float64(q.Rate)-part.Offset) < .001
		after := abs(end-q.Offset) < .001
		if !before && !after {
			continue
		}
		neighbor, e := m.openPCM(d.ID, q)
		if e != nil {
			pcm.close()
			return nil, e
		}
		pcm.neighbors = append(pcm.neighbors, neighbor)
	}
	return pcm, nil
}
func (p *pcmEvidence) close() {
	for _, neighbor := range p.neighbors {
		_ = neighbor.file.Close()
	}
	_ = p.file.Close()
}
func (p *pcmEvidence) signal(ctx context.Context, start, end float64) (bool, error) {
	first := max(int64(0), int64(math.Floor(start*float64(p.rate))))
	last := min(p.samples, int64(math.Ceil(end*float64(p.rate))))
	if last <= first {
		return false, nil
	}
	offset, remaining := 44+first*2, (last-first)*2
	var buffer [32768]byte
	for remaining > 0 {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		size := min(remaining, int64(len(buffer)))
		if _, e := p.file.ReadAt(buffer[:size], offset); e != nil {
			return false, errors.New("Не удалось проверить аудио на цифровую тишину")
		}
		for _, b := range buffer[:size] {
			if b != 0 {
				return true, nil
			}
		}
		offset += size
		remaining -= size
	}
	return false, nil
}
func (p *pcmEvidence) word(ctx context.Context, start, end float64) (bool, error) {
	signal, e := p.signal(ctx, start-timestampPadding, end+timestampPadding)
	if e != nil || signal {
		return signal, e
	}
	for _, neighbor := range p.neighbors {
		signal, e = neighbor.signal(ctx, p.offset+start-timestampPadding-neighbor.offset, p.offset+end+timestampPadding-neighbor.offset)
		if e != nil || signal {
			return signal, e
		}
	}
	return false, nil
}

func (m *Manager) filterSavedPart(ctx context.Context, d *Document, p Part, pcm *pcmEvidence, anySignal bool) error {
	kept := make([]Segment, 0, len(d.Segments))
	changed := false
	for _, s := range d.Segments {
		signal := true
		if s.Part == p.File {
			signal = anySignal
			if signal {
				var e error
				signal, e = pcm.word(ctx, s.Start-p.Offset, s.End-p.Offset)
				if e != nil {
					return e
				}
			}
		}
		if signal {
			kept = append(kept, s)
		} else {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	d.Segments = kept
	d.Revision++
	d.Leaves = map[string]Summary{}
	return m.store.Save(*d)
}
