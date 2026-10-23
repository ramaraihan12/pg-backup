// Package store is the small slice of object storage that pg-backup needs.
package store

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned by Get when the key does not exist.
var ErrNotFound = errors.New("object not found")

// Object is one entry in a listing.
type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// Store reads and writes objects by key.
type Store interface {
	// Put streams r into key. The length does not have to be known up front.
	Put(ctx context.Context, key string, r io.Reader) error
	// Get returns the object body. A missing key gives ErrNotFound.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes a key. Deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
	// List returns every object under prefix, sorted by key.
	List(ctx context.Context, prefix string) ([]Object, error)
}

// Memory is an in-process Store for tests.
type Memory struct {
	mu      sync.Mutex
	objects map[string][]byte
	times   map[string]time.Time
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{objects: map[string][]byte{}, times: map[string]time.Time{}}
}

func (m *Memory) Put(ctx context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = b
	m.times[key] = time.Now()
	return nil
}

func (m *Memory) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

func (m *Memory) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	delete(m.times, key)
	return nil
}

func (m *Memory) List(ctx context.Context, prefix string) ([]Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Object
	for k, b := range m.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, Object{Key: k, Size: int64(len(b)), LastModified: m.times[k]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
