package backup

import (
	"context"
	"io"
	"sort"
	"strings"
	"sync"
)

// Memory is an in-process ObjectStore used by tests and by local runs
// that have no bucket configured for the real adapter.
type Memory struct {
	mu   sync.Mutex
	objs map[string][]byte
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{objs: map[string][]byte{}} }

// Put stores the reader's bytes under name.
func (m *Memory) Put(_ context.Context, name string, r io.Reader, _ int64) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[name] = b
	return nil
}

// List returns the objects whose name starts with prefix, sorted by name.
func (m *Memory) List(_ context.Context, prefix string) ([]Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Object
	for name, b := range m.objs {
		if strings.HasPrefix(name, prefix) {
			out = append(out, Object{Name: name, Size: int64(len(b))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Delete removes an object. Deleting a missing object is not an error.
func (m *Memory) Delete(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objs, name)
	return nil
}

// Get returns a stored object's bytes, or nil.
func (m *Memory) Get(name string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.objs[name]
}

// All lists every object regardless of prefix.
func (m *Memory) All() []Object {
	objs, _ := m.List(context.Background(), "")
	return objs
}
