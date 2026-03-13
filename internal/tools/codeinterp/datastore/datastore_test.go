package datastore

import (
	"errors"
	"path/filepath"
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

func TestFSDataStore_ListReadRangeSearchDelete(t *testing.T) {
	tDir := t.TempDir()
	ds, err := NewFSDataStore(filepath.Join(tDir, "store"), 1024, 64, 256)
	if err != nil {
		t.Fatalf("new fs datastore: %v", err)
	}
	t.Cleanup(func() { _ = ds.Close() })

	if err := ds.Write("p/a.txt", []byte("foo\nbar\nbaz")); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := ds.Write("p/b.txt", []byte("zip\nbar")); err != nil {
		t.Fatalf("write b: %v", err)
	}
	keys, err := ds.List("p/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d (%v)", len(keys), keys)
	}
	r, err := ds.ReadRange("p/a.txt", 4, 3)
	if err != nil {
		t.Fatalf("read range: %v", err)
	}
	if string(r) != "bar" {
		t.Fatalf("read range mismatch: %q", string(r))
	}
	matches, err := ds.Search("p/", "bar")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %+v", matches)
	}
	if err := ds.Delete("p/b.txt"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := ds.Read("p/b.txt"); err == nil {
		t.Fatalf("expected missing key after delete")
	}
}

func TestBlobAndSQLDataStoreReturnNotSupported(t *testing.T) {
	blob := NewBlobDataStore()
	if err := blob.Write("k", []byte("v")); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("blob write expected ErrNotSupported, got %v", err)
	}
	if _, err := blob.Read("k"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("blob read expected ErrNotSupported, got %v", err)
	}
	if _, err := blob.ReadRange("k", 0, 1); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("blob read_range expected ErrNotSupported, got %v", err)
	}
	if _, err := blob.List(""); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("blob list expected ErrNotSupported, got %v", err)
	}
	if err := blob.Delete("k"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("blob delete expected ErrNotSupported, got %v", err)
	}

	sql := NewSQLDataStore()
	if err := sql.Write("k", []byte("v")); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("sql write expected ErrNotSupported, got %v", err)
	}
	if _, err := sql.Read("k"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("sql read expected ErrNotSupported, got %v", err)
	}
	if _, err := sql.ReadRange("k", 0, 1); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("sql read_range expected ErrNotSupported, got %v", err)
	}
	if _, err := sql.List(""); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("sql list expected ErrNotSupported, got %v", err)
	}
	if err := sql.Delete("k"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("sql delete expected ErrNotSupported, got %v", err)
	}
}
