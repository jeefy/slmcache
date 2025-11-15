package store

import (
	"context"
	"errors"
	"math"
	"sync"

	"github.com/jeefy/slmcache/internal/models"
)

// Store is the abstract interface for a vector-backed store. Implementations
// can be in-memory (for tests) or backed by a real vector DB.
type Store interface {
	CreateEntryWithVector(ctx context.Context, e *models.Entry, vec []float64) (int64, error)
	UpdateEntryWithVector(ctx context.Context, id int64, e *models.Entry, vec []float64) error
	GetEntry(ctx context.Context, id int64) (*models.Entry, error)
	SearchByVector(ctx context.Context, vec []float64, limit int) ([]int64, []float64, error)
	AllIDs() []int64
	DeleteEntry(ctx context.Context, id int64) error
}

// inMemoryStore is the in-memory implementation of Store used for testing and
// local development.
type inMemoryStore struct {
	mu      sync.RWMutex
	entries map[int64]*models.Entry
	vectors [][]float64
	ids     []int64
	nextID  int64
}

// New returns a new in-memory Store implementation. To swap in a real vector
// DB, implement the Store interface and provide an alternative constructor.
func New() (Store, error) {
	return &inMemoryStore{
		entries: make(map[int64]*models.Entry),
		vectors: [][]float64{},
		ids:     []int64{},
		nextID:  1,
	}, nil
}

func (s *inMemoryStore) CreateEntryWithVector(ctx context.Context, e *models.Entry, vec []float64) (int64, error) {
	if e == nil {
		return 0, errors.New("nil entry")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	e.ID = id
	s.entries[id] = e
	s.ids = append(s.ids, id)
	v := make([]float64, len(vec))
	copy(v, vec)
	s.vectors = append(s.vectors, v)
	return id, nil
}

func (s *inMemoryStore) UpdateEntryWithVector(ctx context.Context, id int64, e *models.Entry, vec []float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[id]; !ok {
		return errors.New("not found")
	}
	e.ID = id
	s.entries[id] = e
	for i, sid := range s.ids {
		if sid == id {
			v := make([]float64, len(vec))
			copy(v, vec)
			s.vectors[i] = v
			return nil
		}
	}
	s.ids = append(s.ids, id)
	v := make([]float64, len(vec))
	copy(v, vec)
	s.vectors = append(s.vectors, v)
	return nil
}

func (s *inMemoryStore) GetEntry(ctx context.Context, id int64) (*models.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return e, nil
}

func (s *inMemoryStore) SearchByVector(ctx context.Context, vec []float64, limit int) ([]int64, []float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 10
	}
	scores := make([]float64, len(s.vectors))
	for i, v := range s.vectors {
		scores[i] = cosine(vec, v)
	}
	type pair struct {
		idx   int
		score float64
	}
	sel := []pair{}
	for i, sc := range scores {
		if len(sel) < limit {
			sel = append(sel, pair{i, sc})
			continue
		}
		minIdx := 0
		for j := 1; j < len(sel); j++ {
			if sel[j].score < sel[minIdx].score {
				minIdx = j
			}
		}
		if sc > sel[minIdx].score {
			sel[minIdx] = pair{i, sc}
		}
	}
	ids := []int64{}
	outScores := []float64{}
	for _, p := range sel {
		ids = append(ids, s.ids[p.idx])
		outScores = append(outScores, p.score)
	}
	return ids, outScores, nil
}

func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	da := 0.0
	db := 0.0
	dot := 0.0
	for i := range a {
		dot += a[i] * b[i]
		da += a[i] * a[i]
		db += b[i] * b[i]
	}
	if da == 0 || db == 0 {
		return 0
	}
	return dot / (math.Sqrt(da) * math.Sqrt(db))
}

// AllIDs returns a snapshot of stored ids (safe to call concurrently).
func (s *inMemoryStore) AllIDs() []int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]int64, len(s.ids))
	copy(out, s.ids)
	return out
}

func (s *inMemoryStore) DeleteEntry(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[id]; !ok {
		return errors.New("not found")
	}
	delete(s.entries, id)
	// remove from ids and vectors keeping order
	newIDs := make([]int64, 0, len(s.ids))
	newVecs := make([][]float64, 0, len(s.vectors))
	for i, sid := range s.ids {
		if sid == id {
			continue
		}
		newIDs = append(newIDs, sid)
		newVecs = append(newVecs, s.vectors[i])
	}
	s.ids = newIDs
	s.vectors = newVecs
	return nil
}
