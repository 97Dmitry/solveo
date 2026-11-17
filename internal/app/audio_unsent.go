package app

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
)

// «Send new phrases» (hotkey send-transcript and the audio window button): every interviewer phrase after
// the last phrase included in an accepted submission is sent without the question builder. A phrase still
// being recognized at the press is awaited, never sent as a partial; speech that starts later is not
// included.

// unsentFinalWait is a variable only for tests.
var unsentFinalWait = 6 * time.Second

var errNoUnsent = errors.New("Нет новых реплик собеседника")

// unsentSystemLocked lists the IDs of system phrases after the last sent phrase, in transcript order.
func (a *audioState) unsentSystemLocked() []string {
	start := 0
	for i, s := range a.segments {
		if s.Sent {
			start = i + 1
		}
	}
	ids := []string{}
	for _, s := range a.segments[start:] {
		if s.Track == "system" {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// changeLocked returns a channel closed by the next audioChangedLocked.
func (a *audioState) changeLocked() <-chan struct{} {
	if a.changed == nil {
		a.changed = make(chan struct{})
	}
	return a.changed
}

// SendUnsentTranscript validates the request and returns; a registered worker waits for pending finals and
// submits. Failures after the return are reported through ReportError and AudioView.SendError.
func (c *Controller) SendUnsentTranscript(chatID string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("приложение завершается")
	}
	if chatID != c.active.ID {
		c.mu.Unlock()
		return ErrStale
	}
	if err := c.availableLocked(); err != nil {
		c.mu.Unlock()
		return err
	}
	ctx := c.ctx
	c.mu.Unlock()

	a := &c.audio
	a.mu.Lock()
	if a.sendingUnsent {
		a.mu.Unlock()
		return errors.New("Отправка уже ждёт окончания фразы")
	}
	ids := a.unsentSystemLocked()
	if len(ids) == 0 {
		a.mu.Unlock()
		return errNoUnsent
	}
	a.sendingUnsent, a.sendError = true, ""
	c.audioChangedLocked()
	a.mu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.finishUnsent(nil)
		return errors.New("приложение завершается")
	}
	c.workers.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		_, err := c.sendUnsent(ctx, chatID, ids)
		if ctx.Err() != nil {
			err = nil
		}
		c.finishUnsent(err)
		if err != nil {
			c.ReportError(err)
		}
	}()
	return nil
}

func (c *Controller) finishUnsent(err error) {
	a := &c.audio
	a.mu.Lock()
	a.sendingUnsent = false
	if err != nil {
		a.sendError = err.Error()
	}
	c.audioChangedLocked()
	a.mu.Unlock()
}

func (c *Controller) sendUnsent(ctx context.Context, chatID string, ids []string) (string, error) {
	timer := time.NewTimer(unsentFinalWait)
	defer timer.Stop()
	a := &c.audio
	a.mu.Lock()
	for {
		pending := false
		for _, s := range a.segments {
			if !s.Final && slices.Contains(ids, s.ID) {
				pending = true
				break
			}
		}
		if !pending {
			break
		}
		changed := a.changeLocked()
		a.mu.Unlock()
		select {
		case <-changed:
		case <-timer.C:
			return "", errors.New("Реплика собеседника ещё распознаётся; отправьте ещё раз после паузы")
		case <-ctx.Done():
			return "", ctx.Err()
		}
		a.mu.Lock()
	}
	// Empty finals vanish from the transcript; phrases sent meanwhile are not sent twice.
	final := []string{}
	for _, s := range a.segments {
		if s.Final && !s.Sent && slices.Contains(ids, s.ID) {
			final = append(final, s.ID)
		}
	}
	a.mu.Unlock()
	if len(final) == 0 {
		return "", errNoUnsent
	}
	return c.submitSegments(SubmitTranscriptInput{ChatID: chatID, RequestID: chat.ID(), SegmentIDs: final}, true)
}
