package datastore

import (
	"strings"
	"testing"
)

func TestMemoryDataStore_SearchAndRange(t *testing.T) {
	ds := NewMemoryDataStore(1024, 64, 256)
	if err := ds.Write("a/one.txt", []byte("alpha\nbeta\ngamma")); err != nil {
		t.Fatalf("write: %v", err)
	}
	r, err := ds.ReadRange("a/one.txt", 6, 4)
	if err != nil {
		t.Fatalf("read range: %v", err)
	}
	if string(r) != "beta" {
		t.Fatalf("read range mismatch: %q", string(r))
	}
	matches, err := ds.Search("a/", "gam")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(matches) != 1 || matches[0].Line != 3 {
		t.Fatalf("unexpected matches: %+v", matches)
	}
}

func TestFSDataStore_EnforcesMaxBytes(t *testing.T) {
	tDir := t.TempDir()
	ds, err := NewFSDataStore(tDir, 8, 64, 1024)
	if err != nil {
		t.Fatalf("new fs datastore: %v", err)
	}
	t.Cleanup(func() { _ = ds.Close() })

	if err := ds.Write("a.txt", []byte("12345678")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := ds.Write("b.txt", []byte("X")); err == nil || !strings.Contains(err.Error(), "capacity exceeded") {
		t.Fatalf("expected capacity exceeded error, got %v", err)
	}
	if err := ds.Write("a.txt", []byte("12")); err != nil {
		t.Fatalf("overwrite smaller should succeed: %v", err)
	}
	if err := ds.Write("b.txt", []byte("123456")); err != nil {
		t.Fatalf("write within adjusted capacity: %v", err)
	}
}
