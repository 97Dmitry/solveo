package app

import (
	"errors"
	"fmt"
	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/platform"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (c *Controller) BeginCapture(mode string) error {
	if mode != "quick-region" && mode != "queue-region" && mode != "quick-full" {
		return errors.New("invalid capture mode")
	}
	c.mu.Lock()
	if e := c.availableLocked(); e != nil {
		c.mu.Unlock()
		return e
	}
	if len(c.queues[c.active.ID]) >= 20 {
		c.mu.Unlock()
		return ErrQueueFull
	}
	if c.deps.Capturer == nil {
		c.mu.Unlock()
		return errors.New("screen capture unavailable")
	}
	if c.active.ProviderID != c.deps.Provider.Describe().ID {
		c.mu.Unlock()
		return errors.New("provider unavailable")
	}
	id := chat.ID()
	c.op = operation{Operation: Operation{ID: id, Kind: "capture", Phase: "selecting", Message: "Выберите область, Esc — отмена"}, chatID: c.active.ID}
	c.selectionMode = mode
	c.intent = nil
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	if mode == "quick-full" {
		c.finishSelection(platform.Event{Kind: "selection", OperationID: id})
		return nil
	}
	if c.deps.NativeHost == nil {
		c.captureError(id, errors.New("region selection unavailable"))
		return errors.New("region selection unavailable")
	}
	if e := c.deps.NativeHost.BeginSelection(id); e != nil {
		c.captureError(id, e)
		return e
	}
	return nil
}
func (c *Controller) captureError(id string, e error) {
	c.mu.Lock()
	if c.op.ID == id {
		c.op = operation{Operation: Operation{Phase: "idle"}}
		if e != nil {
			c.err = e.Error()
		}
		c.changedLocked()
	}
	c.mu.Unlock()
	c.publish()
}
func (c *Controller) HandleNativeEvent(e platform.Event) {
	switch e.Kind {
	case "selection", "selection-cancelled":
		c.finishSelection(e)
	case "quick-region", "queue-region", "quick-full":
		c.ReportError(c.BeginCapture(e.Kind))
	case "submit":
		_, err := c.RequestSubmit()
		c.ReportError(err)
	case "log":
		c.ReportError(c.ShowLog())
	case "pin":
		c.mu.Lock()
		pin := !c.settings.AlwaysOnTop
		c.mu.Unlock()
		_, err := c.SetAlwaysOnTop(pin)
		c.ReportError(err)
	case "theme":
		c.mu.Lock()
		cfg := c.settings
		c.mu.Unlock()
		if cfg.Theme == "dark" {
			cfg.Theme = "light"
		} else {
			cfg.Theme = "dark"
		}
		_, err := c.UpdateSettings(cfg)
		c.ReportError(err)
	case "toggle", "show":
		if c.deps.Window != nil {
			if e.Kind == "toggle" {
				c.deps.Window.Toggle()
			} else {
				c.deps.Window.Show()
			}
		}
	case "error":
		c.ReportError(e.Err)
	case "cancel":
		s := c.Snapshot()
		c.ReportError(c.Cancel(s.Operation.ID))
	case "fatal":
		c.ReportError(e.Err)
		if c.deps.Window != nil {
			c.deps.Window.Show()
		}
	}
}
func (c *Controller) finishSelection(e platform.Event) {
	c.mu.Lock()
	if c.op.ID != e.OperationID || c.op.Phase != "selecting" {
		c.mu.Unlock()
		return
	}
	if e.Err != nil || e.Kind == "selection-cancelled" {
		c.mu.Unlock()
		c.captureError(e.OperationID, e.Err)
		return
	}
	mode := c.selectionMode
	chatID := c.op.chatID
	c.op.Phase = "capturing"
	c.op.Message = "Сохраняю снимок"
	c.changedLocked()
	c.workers.Add(1)
	c.mu.Unlock()
	c.publish()
	go func() {
		defer c.workers.Done()
		id := chat.ID()
		p := filepath.Join(c.deps.TempDir, id+".png")
		var err error
		if mode == "quick-full" {
			err = c.deps.Capturer.SavePrimary(p)
		} else {
			err = c.deps.Capturer.SaveRegion(p, e.Region)
		}
		if err != nil {
			os.Remove(p)
			c.captureError(e.OperationID, err)
			return
		}
		c.mu.Lock()
		if c.op.ID != e.OperationID || c.closed {
			c.mu.Unlock()
			os.Remove(p)
			return
		}
		c.queues[chatID] = append(c.queues[chatID], queued{ID: id, Path: p, Label: fmt.Sprintf("Снимок %d", len(c.queues[chatID])+1)})
		if mode == "queue-region" {
			c.op = operation{Operation: Operation{Phase: "idle"}}
		}
		c.changedLocked()
		c.mu.Unlock()
		c.publish()
		if mode != "queue-region" {
			_, err = c.submit(SubmitInput{RequestID: chat.ID(), ChatID: chatID, AttachmentIDs: []string{id}}, e.OperationID)
			if err != nil {
				c.captureError(e.OperationID, err)
			}
		}
	}()
}
func (c *Controller) RequestSubmit() (SubmitIntent, error) {
	c.mu.Lock()
	if e := c.availableLocked(); e != nil {
		c.mu.Unlock()
		return SubmitIntent{}, e
	}
	intent := SubmitIntent{ID: chat.ID(), ChatID: c.active.ID, DraftRevision: c.drafts[c.active.ID].Revision, ExpiresAt: c.deps.Now().Add(3 * time.Second).UnixMilli()}
	c.intent = &intent
	c.mu.Unlock()
	if c.deps.Emit != nil {
		c.deps.Emit("solveo:submit", intent)
	}
	return intent, nil
}

// Snapshot refresh after a renderer reload invalidates native submit handshakes.
func (c *Controller) RendererReady() Snapshot {
	c.mu.Lock()
	c.intent = nil
	c.mu.Unlock()
	return c.Snapshot()
}
func (c *Controller) RemoveAttachment(chatID, id string) error {
	return c.clearQueue(chatID, []string{id})
}
func (c *Controller) ClearQueue(chatID string) error { return c.clearQueue(chatID, nil) }
func (c *Controller) clearQueue(chatID string, ids []string) error {
	c.mu.Lock()
	if e := c.availableLocked(); e != nil {
		c.mu.Unlock()
		return e
	}
	if chatID != c.active.ID {
		c.mu.Unlock()
		return ErrStale
	}
	paths := c.cleanupQueueLocked(chatID, ids)
	c.changedLocked()
	c.mu.Unlock()
	c.removeFiles(paths)
	c.publish()
	return nil
}
func (c *Controller) ResolveImage(chatID, id string) (string, error) {
	c.mu.Lock()
	for _, q := range c.queues[chatID] {
		if q.ID == id {
			p := q.Path
			c.mu.Unlock()
			return p, nil
		}
	}
	c.mu.Unlock()
	return c.deps.Store.ImagePath(chatID, id)
}
func (c *Controller) ResolveImageURL(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil {
		return "", e
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/attachments/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("invalid image URL")
	}
	return c.ResolveImage(parts[0], parts[1])
}
func (c *Controller) OpenImage(chatID, id string) error {
	if c.deps.NativeHost == nil {
		return errors.New("image viewer unavailable")
	}
	c.mu.Lock()
	views := []platform.ViewImage{}
	for _, q := range c.queues[chatID] {
		views = append(views, platform.ViewImage{ID: q.ID, Path: q.Path, Label: q.Label})
	}
	doc := chat.Clone(c.active)
	c.mu.Unlock()
	if chatID != doc.ID {
		return ErrStale
	}
	for _, a := range doc.Attachments {
		found := false
		for _, v := range views {
			if v.ID == a.ID {
				found = true
			}
		}
		if !found {
			p, e := c.deps.Store.ImagePath(chatID, a.ID)
			if e != nil {
				return e
			}
			views = append(views, platform.ViewImage{ID: a.ID, Path: p})
		}
	}
	for i, v := range views {
		if v.ID == id {
			return c.deps.NativeHost.ShowImages(views, i)
		}
	}
	return errors.New("image not found")
}
func (c *Controller) ShowLog() error {
	if c.deps.NativeHost == nil {
		return errors.New("log window unavailable")
	}
	return c.deps.NativeHost.ShowLog()
}
