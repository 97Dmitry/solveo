// Package secrets keeps credentials out of settings files: Windows Credential Manager in production,
// memory in tests and on other systems. Errors name the secret, never its value.
package secrets

import "sync"

type Store interface {
	// Get reports whether the secret exists.
	Get(name string) (string, bool, error)
	Set(name, value string) error
	// Delete succeeds when the secret is already absent.
	Delete(name string) error
}

type memory struct {
	mu     sync.Mutex
	values map[string]string
}

// NewMemory keeps secrets in RAM only.
func NewMemory() Store { return &memory{values: map[string]string{}} }

func (m *memory) Get(name string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.values[name]
	return v, ok, nil
}

func (m *memory) Set(name, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[name] = value
	return nil
}

func (m *memory) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, name)
	return nil
}
