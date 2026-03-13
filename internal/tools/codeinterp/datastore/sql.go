package datastore

type SQLDataStore struct{}

func NewSQLDataStore() *SQLDataStore { return &SQLDataStore{} }

func (s *SQLDataStore) Write(string, []byte) error                     { return ErrNotSupported }
func (s *SQLDataStore) Read(string) ([]byte, error)                    { return nil, ErrNotSupported }
func (s *SQLDataStore) ReadRange(string, int64, int64) ([]byte, error) { return nil, ErrNotSupported }
func (s *SQLDataStore) List(string) ([]string, error)                  { return nil, ErrNotSupported }
func (s *SQLDataStore) Delete(string) error                            { return ErrNotSupported }
func (s *SQLDataStore) Close() error                                   { return nil }
