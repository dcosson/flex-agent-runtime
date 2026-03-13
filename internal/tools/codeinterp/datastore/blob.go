package datastore

type BlobDataStore struct{}

func NewBlobDataStore() *BlobDataStore { return &BlobDataStore{} }

func (b *BlobDataStore) Write(string, []byte) error                     { return ErrNotSupported }
func (b *BlobDataStore) Read(string) ([]byte, error)                    { return nil, ErrNotSupported }
func (b *BlobDataStore) ReadRange(string, int64, int64) ([]byte, error) { return nil, ErrNotSupported }
func (b *BlobDataStore) List(string) ([]string, error)                  { return nil, ErrNotSupported }
func (b *BlobDataStore) Delete(string) error                            { return ErrNotSupported }
func (b *BlobDataStore) Close() error                                   { return nil }
