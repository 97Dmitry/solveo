package app

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/profiles"
)

// loadProfiles is called at startup or while the controller owns the operation.
// No other library operation can run during this I/O.
func (c *Controller) loadProfiles() {
	if c.deps.ProfilesStore == nil {
		c.mu.Lock()
		c.profilesErr = "хранилище профилей не подключено"
		c.mu.Unlock()
		return
	}
	list, err := c.deps.ProfilesStore.Load()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.profilesErr = err.Error()
		return
	}
	sortProfiles(list)
	c.profiles = append([]profiles.Profile{}, list...)
	c.profilesErr = ""
}
func (c *Controller) profileLocked(id string, revision uint64) (profiles.Profile, error) {
	if c.profilesErr != "" {
		return profiles.Profile{}, errors.New(c.profilesErr)
	}
	for _, p := range c.profiles {
		if p.ID == id && p.Revision == revision {
			return p, nil
		}
	}
	return profiles.Profile{}, ErrStale
}
func (c *Controller) NewChatFromProfile(id string, revision uint64) (Snapshot, error) {
	if id == "" {
		return Snapshot{}, ErrStale
	}
	return c.switchChat("", id, revision)
}
func (c *Controller) ReloadProfiles() (Snapshot, error) {
	c.mu.Lock()
	if err := c.availableLocked(); err != nil {
		c.mu.Unlock()
		return Snapshot{}, err
	}
	c.op = operation{Operation: Operation{ID: chat.ID(), Kind: kindConfig, Phase: phaseSaving}}
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	c.loadProfiles()
	c.mu.Lock()
	c.op = operation{Operation: Operation{Phase: phaseIdle}}
	c.changedLocked()
	var err error
	if c.profilesErr != "" {
		err = errors.New(c.profilesErr)
	}
	c.mu.Unlock()
	c.publish()
	return c.Snapshot(), err
}
func (c *Controller) SaveProfile(in ProfileInput) (Snapshot, error) {
	return c.changeProfile(in, false)
}
func (c *Controller) DeleteProfile(id string, revision uint64) (Snapshot, error) {
	if id == "" {
		return Snapshot{}, ErrStale
	}
	return c.changeProfile(ProfileInput{ID: id, ExpectedRevision: revision}, true)
}
func (c *Controller) changeProfile(in ProfileInput, remove bool) (Snapshot, error) {
	c.mu.Lock()
	if err := c.availableLocked(); err != nil {
		c.mu.Unlock()
		return Snapshot{}, err
	}
	if c.profilesErr != "" {
		err := errors.New(c.profilesErr)
		c.mu.Unlock()
		return Snapshot{}, err
	}
	list := append([]profiles.Profile{}, c.profiles...)
	now := c.deps.Now().UTC()
	p := profiles.Profile{ID: chat.ID(), Name: strings.TrimSpace(in.Name), Config: in.Config, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if in.ID != "" {
		old, err := c.profileLocked(in.ID, in.ExpectedRevision)
		if err != nil {
			c.mu.Unlock()
			return Snapshot{}, err
		}
		p.ID = old.ID
		p.CreatedAt = old.CreatedAt
		p.Revision = old.Revision + 1
		if p.UpdatedAt.Before(p.CreatedAt) {
			p.UpdatedAt = p.CreatedAt
		}
		for i := range list {
			if list[i].ID == old.ID {
				list = append(list[:i], list[i+1:]...)
				break
			}
		}
	} else if in.ExpectedRevision != 0 {
		c.mu.Unlock()
		return Snapshot{}, ErrStale
	}
	if !remove {
		if err := profiles.Validate(p); err != nil {
			c.mu.Unlock()
			return Snapshot{}, err
		}
		if len(list) >= profiles.MaxProfiles {
			c.mu.Unlock()
			return Snapshot{}, errors.New("можно сохранить не более 100 профилей")
		}
		list = append([]profiles.Profile{p}, list...)
	}
	c.op = operation{Operation: Operation{ID: chat.ID(), Kind: kindConfig, Phase: phaseSaving}}
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	sortProfiles(list)
	err := c.deps.ProfilesStore.Save(list)
	c.mu.Lock()
	if err == nil {
		c.profiles = list
		c.err = ""
	} else {
		c.err = err.Error()
	}
	c.op = operation{Operation: Operation{Phase: phaseIdle}}
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	return c.Snapshot(), err
}

func (c *Controller) profileViewsLocked() []ProfileView {
	out := make([]ProfileView, 0, len(c.profiles))
	for _, p := range c.profiles {
		out = append(out, ProfileView{ID: p.ID, Name: p.Name, Config: p.Config, Revision: p.Revision, CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339Nano)})
	}
	return out
}

func sortProfiles(list []profiles.Profile) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].UpdatedAt.Equal(list[j].UpdatedAt) {
			return list[i].ID < list[j].ID
		}
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
}
