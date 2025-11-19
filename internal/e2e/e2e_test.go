package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/jeefy/slmcache/internal/models"
	"github.com/jeefy/slmcache/internal/server"
	"github.com/jeefy/slmcache/internal/store"
)

func setupServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	st, err := store.New()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	srv := server.New(st)
	ts := httptest.NewServer(srv.Router())
	return ts, func() {
		srv.Close()
		ts.Close()
	}
}

func postEntry(t *testing.T, baseURL string, e *models.Entry) *models.Entry {
	t.Helper()
	b, _ := json.Marshal(e)
	res, err := http.Post(baseURL+"/entries", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("post entry: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected status create: %d", res.StatusCode)
	}
	var got models.Entry
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	return &got
}

func search(t *testing.T, baseURL, q string) []*models.Entry {
	t.Helper()
	res, err := http.Get(baseURL + "/search?q=" + url.QueryEscape(q) + "&limit=5")
	if err != nil {
		t.Fatalf("search get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status search: %d", res.StatusCode)
	}
	var out []*models.Entry
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	return out
}

// TestE2E demonstrates an embedded SLM (simulated inside the test) making
// retrieval-and-decision calls against the local slmcache service. It shows
// both a cache hit (candidate returned and selected) and a cache miss (no
// candidates returned).
func TestE2E_cacheHitAndMiss(t *testing.T) {
	ts, cleanup := setupServer(t)
	defer cleanup()

	// create a cached entry with metadata
	created := postEntry(t, ts.URL, &models.Entry{Prompt: "How to bake a cake", Response: "Use flour, eggs, and bake", Metadata: map[string]interface{}{"source": "faq"}})

	// simulated SLM: query for a similar prompt -> should return candidate(s)
	candidates := search(t, ts.URL, "bake cake")
	if len(candidates) == 0 {
		t.Fatalf("expected at least one candidate for 'bake cake', got 0")
	}

	// SLM decision (simple heuristic in this example): pick first candidate
	chosen := candidates[0]
	if chosen.ID != created.ID {
		t.Fatalf("expected chosen id %d to equal created id %d", chosen.ID, created.ID)
	}

	// metadata query should surface the entry
	metaResp, err := http.Get(ts.URL + "/entries?metadata.source=faq")
	if err != nil {
		t.Fatalf("metadata query: %v", err)
	}
	if metaResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 metadata query got %d", metaResp.StatusCode)
	}
	var metaEntries []*models.Entry
	if err := json.NewDecoder(metaResp.Body).Decode(&metaEntries); err != nil {
		t.Fatalf("decode metadata entries: %v", err)
	}
	metaResp.Body.Close()
	if len(metaEntries) != 1 || metaEntries[0].ID != created.ID {
		t.Fatalf("expected metadata query to return created entry")
	}

	// cache miss: query something unrelated
	missCandidates := search(t, ts.URL, "quantum entanglement unicorn")
	if len(missCandidates) != 0 {
		t.Fatalf("expected zero candidates for unrelated query, got %d", len(missCandidates))
	}

	// the SLM would now decide to generate a new response and store it.
	// (Optional) we can simulate that by posting a new entry and verifying search returns it.
	newEntry := postEntry(t, ts.URL, &models.Entry{Prompt: "Explain quantum entanglement", Response: "It's a quantum correlation."})
	got := search(t, ts.URL, "quantum entanglement")
	t.Logf("search results for 'quantum entanglement': %+v", got)
	if len(got) == 0 {
		t.Fatalf("expected candidate for new entry, got 0")
	}
	found := false
	for _, c := range got {
		if c.ID == newEntry.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected new entry to be found via search")
	}

	// clean up context usage (if needed)
	_ = context.Background()
}
