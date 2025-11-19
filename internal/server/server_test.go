package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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
	ce := cloneEntry(e)
	m.entries[id] = ce
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
	ce := cloneEntry(e)
	m.entries[id] = ce
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
	ce := cloneEntry(e)
	return ce, nil
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

func (m *mockStore) UpdateEntryMetadata(ctx context.Context, id int64, metadata map[string]interface{}, replace bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[id]
	if !ok {
		return http.ErrMissingFile
	}
	updated := cloneEntry(entry)
	if replace {
		if metadata == nil {
			updated.Metadata = nil
		} else {
			updated.Metadata = cloneMetadata(metadata)
		}
	} else {
		if updated.Metadata == nil {
			updated.Metadata = make(map[string]interface{}, len(metadata))
		}
		for k, v := range metadata {
			updated.Metadata[k] = v
		}
	}
	m.entries[id] = updated
	return nil
}

func (m *mockStore) DeleteEntryMetadata(ctx context.Context, id int64, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[id]
	if !ok {
		return http.ErrMissingFile
	}
	updated := cloneEntry(entry)
	if len(keys) == 0 {
		updated.Metadata = nil
	} else if updated.Metadata != nil {
		for _, key := range keys {
			delete(updated.Metadata, key)
		}
	}
	m.entries[id] = updated
	return nil
}

func (m *mockStore) FindEntriesByMetadata(ctx context.Context, filters map[string]string) ([]*models.Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []*models.Entry{}
	for _, id := range m.ids {
		entry, ok := m.entries[id]
		if !ok {
			continue
		}
		if len(filters) == 0 || metadataMatches(entry, filters) {
			ce := cloneEntry(entry)
			out = append(out, ce)
		}
	}
	return out, nil
}

func cloneEntry(e *models.Entry) *models.Entry {
	if e == nil {
		return &models.Entry{}
	}
	copy := *e
	if e.Metadata != nil {
		copy.Metadata = cloneMetadata(e.Metadata)
	}
	return &copy
}

func cloneMetadata(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func metadataMatches(e *models.Entry, filters map[string]string) bool {
	if len(filters) == 0 {
		return true
	}
	if e.Metadata == nil {
		return false
	}
	for k, v := range filters {
		val, ok := e.Metadata[k]
		if !ok {
			return false
		}
		if v == "" {
			continue
		}
		if fmt.Sprint(val) != v {
			return false
		}
	}
	return true
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
	defer srv.Close()
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

func TestServer_MetadataManagement(t *testing.T) {
	ms := newMockStore()
	srv := New(ms)
	defer srv.Close()
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	// create entry
	e := &models.Entry{Prompt: "Favorite color", Response: "Blue"}
	body, _ := json.Marshal(e)
	resp, err := http.Post(ts.URL+"/entries", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 got %d", resp.StatusCode)
	}
	var created models.Entry
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// patch metadata
	patchPayload := map[string]interface{}{
		"metadata": map[string]interface{}{"source": "faq", "locale": "en-US"},
	}
	patchBody, _ := json.Marshal(patchPayload)
	req, _ := http.NewRequest(http.MethodPatch, fmt.Sprintf("%s/entries/%d/metadata", ts.URL, created.ID), bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	patchResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch metadata: %v", err)
	}
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 patch got %d", patchResp.StatusCode)
	}
	var meta map[string]interface{}
	_ = json.NewDecoder(patchResp.Body).Decode(&meta)
	patchResp.Body.Close()
	if meta["source"] != "faq" {
		t.Fatalf("expected source metadata")
	}

	// ensure metadata GET works
	getResp, err := http.Get(fmt.Sprintf("%s/entries/%d/metadata", ts.URL, created.ID))
	if err != nil {
		t.Fatalf("get metadata: %v", err)
	}
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 get metadata got %d", getResp.StatusCode)
	}
	var fetched map[string]interface{}
	_ = json.NewDecoder(getResp.Body).Decode(&fetched)
	getResp.Body.Close()
	if fetched["locale"] != "en-US" {
		t.Fatalf("expected locale metadata")
	}

	// query entries by metadata
	listResp, err := http.Get(ts.URL + "/entries?metadata.source=faq")
	if err != nil {
		t.Fatalf("list metadata: %v", err)
	}
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 metadata list got %d", listResp.StatusCode)
	}
	var listed []*models.Entry
	_ = json.NewDecoder(listResp.Body).Decode(&listed)
	listResp.Body.Close()
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("expected one entry filtered by metadata")
	}

	// search with metadata filter should find entry
	searchResp, err := http.Get(ts.URL + "/search?q=color&metadata.source=faq")
	if err != nil {
		t.Fatalf("search metadata: %v", err)
	}
	if searchResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 search got %d", searchResp.StatusCode)
	}
	var searchOut []*models.Entry
	_ = json.NewDecoder(searchResp.Body).Decode(&searchOut)
	searchResp.Body.Close()
	if len(searchOut) == 0 {
		t.Fatalf("expected search results when metadata matches")
	}

	// delete metadata key
	delReq, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/entries/%d/metadata/source", ts.URL, created.ID), nil)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("delete metadata: %v", err)
	}
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 delete got %d", delResp.StatusCode)
	}
	delResp.Body.Close()

	// metadata should no longer include source
	checkResp, err := http.Get(fmt.Sprintf("%s/entries/%d/metadata", ts.URL, created.ID))
	if err != nil {
		t.Fatalf("check metadata: %v", err)
	}
	var after map[string]interface{}
	_ = json.NewDecoder(checkResp.Body).Decode(&after)
	checkResp.Body.Close()
	if _, ok := after["source"]; ok {
		t.Fatalf("expected source metadata removed")
	}
}

func TestServer_PurgeExpiredEntries(t *testing.T) {
	t.Setenv("SLC_ENTRY_TTL", "1s")
	t.Setenv("SLC_PURGE_INTERVAL", "10m")
	ms := newMockStore()
	srv := New(ms)
	defer srv.Close()
	vec := []float64{1, 0, 0}
	id, err := ms.CreateEntryWithVector(context.Background(), &models.Entry{Prompt: "old", Response: "data"}, vec)
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	ms.mu.Lock()
	if entry, ok := ms.entries[id]; ok {
		past := time.Now().Add(-2 * time.Second)
		entry.CreatedAt = past
		entry.UpdatedAt = past
	}
	ms.mu.Unlock()
	removed := srv.purgeExpired(context.Background())
	if removed != 1 {
		t.Fatalf("expected 1 removed entry, got %d", removed)
	}
	if _, err := ms.GetEntry(context.Background(), id); err == nil {
		t.Fatalf("expected entry to be deleted")
	}
}
