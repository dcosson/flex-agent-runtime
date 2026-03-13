package datastore

import "errors"

var ErrNotSupported = errors.New("codeinterp datastore: operation not supported")

type DataStore interface {
	Write(key string, data []byte) error
	Read(key string) ([]byte, error)
	ReadRange(key string, offset, limit int64) ([]byte, error)
	List(prefix string) ([]string, error)
	Delete(key string) error
	Close() error
}

type SearchableDataStore interface {
	Search(keyPrefix string, pattern string) ([]Match, error)
}

type Match struct {
	Key     string
	Line    int
	Column  int
	Content string
}
