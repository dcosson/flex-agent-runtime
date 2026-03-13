package datastore

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

type MemoryDataStore struct {
	mu       sync.RWMutex
	data     map[string][]byte
	maxBytes int64
	used     int64
	maxKey   int
	maxValue int64
}

func NewMemoryDataStore(maxBytes int64, maxKey int, maxValue int64) *MemoryDataStore {
	if maxBytes <= 0 {
		maxBytes = 16 * 1024 * 1024
	}
	return &MemoryDataStore{
		data:     map[string][]byte{},
		maxBytes: maxBytes,
		maxKey:   maxKey,
		maxValue: maxValue,
	}
}

func (m *MemoryDataStore) Write(key string, data []byte) error {
	if err := ValidateKey(key, m.maxKey); err != nil {
		return err
	}
	if m.maxValue > 0 && int64(len(data)) > m.maxValue {
		return fmt.Errorf("value exceeds max size")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := int64(len(m.data[key]))
	nextUsed := m.used - prev + int64(len(data))
	if nextUsed > m.maxBytes {
		return fmt.Errorf("memory datastore capacity exceeded")
	}
	cp := append([]byte(nil), data...)
	m.data[key] = cp
	m.used = nextUsed
	return nil
}

func (m *MemoryDataStore) Read(key string) ([]byte, error) {
	if err := ValidateKey(key, m.maxKey); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[key]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	return append([]byte(nil), v...), nil
}

func (m *MemoryDataStore) ReadRange(key string, offset, limit int64) ([]byte, error) {
	buf, err := m.Read(key)
	if err != nil {
		return nil, err
	}
	if offset < 0 {
		offset = 0
	}
	if limit < 0 {
		limit = 0
	}
	if offset > int64(len(buf)) {
		return []byte{}, nil
	}
	end := offset + limit
	if limit == 0 || end > int64(len(buf)) {
		end = int64(len(buf))
	}
	return append([]byte(nil), buf[offset:end]...), nil
}

func (m *MemoryDataStore) List(prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0)
	for k := range m.data {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *MemoryDataStore) Delete(key string) error {
	if err := ValidateKey(key, m.maxKey); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.data[key]; ok {
		m.used -= int64(len(v))
		delete(m.data, key)
	}
	return nil
}

func (m *MemoryDataStore) Search(keyPrefix string, pattern string) ([]Match, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	matches := make([]Match, 0)
	for k, v := range m.data {
		if !strings.HasPrefix(k, keyPrefix) {
			continue
		}
		lines := bytes.Split(v, []byte("\n"))
		for i, line := range lines {
			loc := re.FindIndex(line)
			if loc != nil {
				matches = append(matches, Match{Key: k, Line: i + 1, Column: loc[0] + 1, Content: string(line)})
			}
		}
	}
	return matches, nil
}

func (m *MemoryDataStore) Close() error { return nil }
