package store

import (
	"context"
	"errors"

	"github.com/jeefy/slmcache/internal/models"
)

// ExternalVectorDB is an adapter skeleton for a production vector database.
// It's a minimal example showing how to satisfy the Store interface. Fill in
// the TODOs with real client code (Milvus, Faiss, Pinecone, etc.).
type ExternalVectorDB struct {
	// add client fields here, e.g. HTTP client or SDK handle
}

// NewExternalVectorDB constructs a new ExternalVectorDB adapter. Replace the
// connection params with whatever your backend needs.
func NewExternalVectorDB(conn string) (Store, error) {
	_ = conn
	// TODO: initialize client and return adapter
	return &ExternalVectorDB{}, nil
}

func (e *ExternalVectorDB) CreateEntryWithVector(ctx context.Context, entry *models.Entry, vec []float64) (int64, error) {
	// TODO: implement insert into vector DB and a metadata store
	return 0, errors.New("not implemented: CreateEntryWithVector")
}

func (e *ExternalVectorDB) UpdateEntryWithVector(ctx context.Context, id int64, entry *models.Entry, vec []float64) error {
	// TODO: implement update
	return errors.New("not implemented: UpdateEntryWithVector")
}

func (e *ExternalVectorDB) GetEntry(ctx context.Context, id int64) (*models.Entry, error) {
	// TODO: fetch metadata from backing store
	return nil, errors.New("not implemented: GetEntry")
}

func (e *ExternalVectorDB) SearchByVector(ctx context.Context, vec []float64, limit int) ([]int64, []float64, error) {
	// TODO: run vector similarity query, return ids and scores
	return nil, nil, errors.New("not implemented: SearchByVector")
}

func (e *ExternalVectorDB) AllIDs() []int64 {
	// Optional: implement if backend can list ids; otherwise return empty slice
	return []int64{}
}

func (e *ExternalVectorDB) DeleteEntry(ctx context.Context, id int64) error {
	// TODO: implement delete in external backend
	return errors.New("not implemented: DeleteEntry")
}

func (e *ExternalVectorDB) UpdateEntryMetadata(ctx context.Context, id int64, metadata map[string]interface{}, replace bool) error {
	// TODO: implement metadata update semantics in backend
	return errors.New("not implemented: UpdateEntryMetadata")
}

func (e *ExternalVectorDB) DeleteEntryMetadata(ctx context.Context, id int64, keys ...string) error {
	// TODO: implement metadata key removal
	return errors.New("not implemented: DeleteEntryMetadata")
}

func (e *ExternalVectorDB) FindEntriesByMetadata(ctx context.Context, filters map[string]string) ([]*models.Entry, error) {
	// TODO: run metadata-only query in backend store
	return nil, errors.New("not implemented: FindEntriesByMetadata")
}
