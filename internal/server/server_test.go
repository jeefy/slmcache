package server

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jeefy/slmcache/internal/models"
)

// mockStore is a small in-memory mock implementing store.Store used by unit
// tests to isolate server logic.
type mockStore struct {
	mu      sync.RWMutex
	entries map[int64]*models.Entry
	vectors [][]float64
	ids     []int64
	nextID  int64
}

func newMockStore() *mockStore {
	return &mockStore{entries: make(map[int64]*models.Entry), vectors: [][]float64{}, ids: []int64{}, nextID: 1}
}

func (m *mockStore) CreateEntryWithVector(ctx context.Context, e *models.Entry, vec []float64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextID
	m.nextID++
	e.ID = id
	// shallow copy
	ce := *e
	m.entries[id] = &ce
	m.ids = append(m.ids, id)
	v := make([]float64, len(vec))
	copy(v, vec)
	m.vectors = append(m.vectors, v)
	return id, nil
}

func (m *mockStore) UpdateEntryWithVector(ctx context.Context, id int64, e *models.Entry, vec []float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entries[id]; !ok {
		return http.ErrMissingFile
	}
	e.ID = id
	ce := *e
	m.entries[id] = &ce
	for i, sid := range m.ids {
		if sid == id {
			v := make([]float64, len(vec))
			copy(v, vec)
			m.vectors[i] = v
			return nil
		}
	}
	m.ids = append(m.ids, id)
	v := make([]float64, len(vec))
	copy(v, vec)
	m.vectors = append(m.vectors, v)
	return nil
}

func (m *mockStore) GetEntry(ctx context.Context, id int64) (*models.Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[id]
	if !ok {
		return nil, http.ErrMissingFile
	}
	ce := *e
	return &ce, nil
}

func (m *mockStore) SearchByVector(ctx context.Context, vec []float64, limit int) ([]int64, []float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// trivial cosine similarity search
	scores := make([]float64, len(m.vectors))
	for i, v := range m.vectors {
		scores[i] = cosine(vec, v)
	}
	// collect results naive order
	ids := make([]int64, len(m.ids))
	copy(ids, m.ids)
	return ids, scores, nil
}

func (m *mockStore) AllIDs() []int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]int64, len(m.ids))
	copy(out, m.ids)
	return out
}

func (m *mockStore) DeleteEntry(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entries[id]; !ok {
		return http.ErrMissingFile
	}
	delete(m.entries, id)
	newIDs := make([]int64, 0, len(m.ids))
	newVecs := make([][]float64, 0, len(m.vectors))
	for i, sid := range m.ids {
		if sid == id {
			continue
		}
		newIDs = append(newIDs, sid)
		newVecs = append(newVecs, m.vectors[i])
	}
	m.ids = newIDs
	m.vectors = newVecs
	return nil
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

func TestServer_CreateAndSearch(t *testing.T) {
	ms := newMockStore()
	srv := New(ms)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	// create entry
	e := &models.Entry{Prompt: "How to bake a cake", Response: "Use flour, eggs"}
	b, _ := json.Marshal(e)
	res, err := http.Post(ts.URL+"/entries", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("post failed: %v", err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 got %d", res.StatusCode)
	}
	var got models.Entry
	_ = json.NewDecoder(res.Body).Decode(&got)
	res.Body.Close()
	if got.ID == 0 {
		t.Fatalf("expected non-zero id")
	}

	// search
	resp, err := http.Get(ts.URL + "/search?q=bake+cake&limit=5")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 got %d", resp.StatusCode)
	}
	var out []*models.Entry
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if len(out) == 0 {
		t.Fatalf("expected search results")
	}
}
