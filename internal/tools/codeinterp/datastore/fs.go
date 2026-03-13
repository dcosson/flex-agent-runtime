package datastore

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type FSDataStore struct {
	root     string
	maxBytes int64
	maxKey   int
	maxValue int64
}

func NewFSDataStore(root string, maxBytes int64, maxKey int, maxValue int64) (*FSDataStore, error) {
	if root == "" {
		return nil, fmt.Errorf("root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &FSDataStore{root: root, maxBytes: maxBytes, maxKey: maxKey, maxValue: maxValue}, nil
}

func (f *FSDataStore) pathForKey(key string) (string, error) {
	if err := ValidateKey(key, f.maxKey); err != nil {
		return "", err
	}
	p := filepath.Join(f.root, filepath.Clean(key))
	if !strings.HasPrefix(p, f.root) {
		return "", fmt.Errorf("path escapes root")
	}
	return p, nil
}

func (f *FSDataStore) Write(key string, data []byte) error {
	if f.maxValue > 0 && int64(len(data)) > f.maxValue {
		return fmt.Errorf("value exceeds max size")
	}
	p, err := f.pathForKey(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func (f *FSDataStore) Read(key string) ([]byte, error) {
	p, err := f.pathForKey(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

func (f *FSDataStore) ReadRange(key string, offset, limit int64) ([]byte, error) {
	p, err := f.pathForKey(key)
	if err != nil {
		return nil, err
	}
	fh, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	if offset < 0 {
		offset = 0
	}
	if _, err := fh.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return io.ReadAll(fh)
	}
	buf := make([]byte, limit)
	n, err := io.ReadFull(fh, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

func (f *FSDataStore) List(prefix string) ([]string, error) {
	entries := make([]string, 0)
	err := filepath.WalkDir(f.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(f.root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, prefix) {
			entries = append(entries, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)
	return entries, nil
}

func (f *FSDataStore) Delete(key string) error {
	p, err := f.pathForKey(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (f *FSDataStore) Search(keyPrefix string, pattern string) ([]Match, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	keys, err := f.List(keyPrefix)
	if err != nil {
		return nil, err
	}
	out := make([]Match, 0)
	for _, key := range keys {
		buf, err := f.Read(key)
		if err != nil {
			continue
		}
		lines := bytes.Split(buf, []byte("\n"))
		for i, line := range lines {
			loc := re.FindIndex(line)
			if loc != nil {
				out = append(out, Match{Key: key, Line: i + 1, Column: loc[0] + 1, Content: string(line)})
			}
		}
	}
	return out, nil
}

func (f *FSDataStore) Close() error { return nil }
