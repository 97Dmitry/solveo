package app

import (
	"encoding/base64"
	"errors"
	"os"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/97Dmitry/solveo/internal/attachments"
	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/workstate"
)

// beginAttachment reserves the same operation/capacity as capture, before I/O.
func (c *Controller) beginAttachment(chatID string, adding bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.availableLocked(); err != nil {
		return "", err
	}
	if chatID != c.active.ID {
		return "", ErrStale
	}
	if c.active.ProviderID != c.deps.Provider.Describe().ID {
		return "", errors.New("подключение LLM недоступно для этого чата")
	}
	if adding && len(c.queues[chatID]) >= workstate.MaxQueue {
		return "", ErrQueueFull
	}
	id := chat.ID()
	c.op = operation{Operation: Operation{ID: id, Kind: kindCapture, Phase: phaseCapturing, Message: "Подготавливаю изображения"}, chatID: chatID}
	c.intent = nil
	c.err = ""
	c.workers.Add(1)
	c.changedLocked()
	return id, nil
}

func (c *Controller) ImportAttachment(chatID, label, dataBase64 string) error {
	return c.addAttachment(chatID, label, func() ([]byte, error) {
		if len(dataBase64) > base64.StdEncoding.EncodedLen(attachments.MaxBytes) {
			return nil, errors.New("изображение превышает лимит 32 МиБ")
		}
		data, err := base64.StdEncoding.Strict().DecodeString(dataBase64)
		if err != nil {
			return nil, errors.New("не удалось прочитать данные изображения")
		}
		return data, nil
	})
}

func (c *Controller) ReuseAttachment(chatID, id string) error {
	// Select immutable metadata only. The shared operation checks active chat and
	// availability again before any file I/O; archived paths are never queue-owned.
	c.mu.Lock()
	var source chat.Attachment
	if chatID == c.active.ID {
		referenced := false
		for _, m := range c.active.Messages {
			for _, a := range m.AttachmentIDs {
				if a == id {
					referenced = true
				}
			}
		}
		if referenced {
			for _, a := range c.active.Attachments {
				if a.ID == id {
					source = a
					break
				}
			}
		}
	}
	c.mu.Unlock()
	return c.addAttachment(chatID, "Копия снимка", func() ([]byte, error) {
		if source.Path == "" {
			return nil, errors.New("сохранённый снимок не найден")
		}
		p, err := c.deps.Store.AttachmentPath(chatID, source)
		if err != nil {
			return nil, err
		}
		return attachments.ReadFile(p)
	})
}

func attachmentLabel(label string) string {
	label = path.Base(strings.ReplaceAll(label, `\`, "/"))
	label = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ':' || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, label)
	chars := []rune(strings.TrimSpace(label))
	if len(chars) > 160 {
		chars = chars[:160]
	}
	if len(chars) == 0 || string(chars) == "." {
		return "Изображение"
	}
	label = string(chars)
	for len(label) > workstate.MaxLabelBytes {
		_, size := utf8.DecodeLastRuneInString(label)
		label = label[:len(label)-size]
	}
	return label
}

func (c *Controller) addAttachment(chatID, label string, load func() ([]byte, error)) (err error) {
	opID, err := c.beginAttachment(chatID, true)
	if err != nil {
		return err
	}
	defer c.workers.Done()
	defer func() { c.captureError(opID, err) }()
	c.publish()
	data, err := load()
	if err != nil {
		return err
	}
	data, err = attachments.Normalize(data)
	if err != nil {
		return err
	}
	id := chat.ID()
	p, err := c.deps.Work.WriteImage(id, data)
	if err != nil {
		return err
	}
	c.mu.Lock()
	// Close drains this worker. An import interrupted by shutdown has not yet
	// acknowledged the input and must not mutate the closed controller.
	if c.closed || c.op.ID != opID {
		c.mu.Unlock()
		_ = os.Remove(p)
		return errors.New("добавление изображения прервано завершением приложения")
	}
	c.queues[chatID] = append(c.queues[chatID], queued{ID: id, Path: p, Label: attachmentLabel(label)})
	c.changedLocked()
	c.mu.Unlock()
	c.saveWorkNow()
	return nil
}

func (c *Controller) ReorderAttachments(chatID string, ids []string) error {
	c.mu.Lock()
	if err := c.availableLocked(); err != nil {
		c.mu.Unlock()
		return err
	}
	if chatID != c.active.ID {
		c.mu.Unlock()
		return ErrStale
	}
	queue := c.queues[chatID]
	if len(ids) != len(queue) {
		c.mu.Unlock()
		return ErrStale
	}
	byID := make(map[string]queued, len(queue))
	for _, q := range queue {
		byID[q.ID] = q
	}
	ordered := make([]queued, 0, len(ids))
	for _, id := range ids {
		q, ok := byID[id]
		if !ok {
			c.mu.Unlock()
			return ErrStale
		}
		ordered = append(ordered, q)
		delete(byID, id)
	}
	opID := chat.ID()
	c.op = operation{Operation: Operation{ID: opID, Kind: kindCapture, Phase: phaseSaving, Message: "Сохраняю порядок изображений"}, chatID: chatID}
	c.intent = nil
	c.workers.Add(1)
	c.queues[chatID] = ordered
	c.changedLocked()
	c.mu.Unlock()
	defer c.workers.Done()
	c.publish()
	c.saveWorkNow()
	c.captureError(opID, nil)
	return nil
}
