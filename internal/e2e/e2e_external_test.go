package e2e_test

// External end-to-end tests that exercise a running slmcache instance at
// localhost:8080 instead of spinning up an in-process httptest server.
//
// Run via: `make e2e-test` (which ensures the container is started and ready).
//
// These tests verify:
//  1. Creating a cache entry with a prompt containing multiple KubeCon question variants.
//  2. Searching with different phrasings yields a cache hit (entry appears in results).
//  3. A clearly unrelated / unique query yields a miss (no candidates).
//  4. Fetching the entry by ID returns the expected content.
//  5. Updating the entry (PUT) changes the stored response.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
)

// entryPayload models the JSON we send (minimal fields needed for creation/update).
type entryPayload struct {
	Prompt   string                 `json:"prompt"`
	Response string                 `json:"response"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type entryResult struct {
	ID       int64                  `json:"id"`
	Prompt   string                 `json:"prompt"`
	Response string                 `json:"response"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

func externalBaseURL() string {
	if v := os.Getenv("E2E_BASE_URL"); v != "" {
		return v
	}
	return "http://localhost:8080"
}

// postEntry creates an entry via the running API.
func extPostEntry(t *testing.T, base string, p *entryPayload) *entryResult {
	t.Helper()
	b, _ := json.Marshal(p)
	res, err := http.Post(base+"/entries", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST /entries error: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		buf := new(bytes.Buffer)
		buf.ReadFrom(res.Body)
		t.Fatalf("unexpected status %d body=%s", res.StatusCode, buf.String())
	}
	var out entryResult
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return &out
}

// putEntry updates an entry via the running API.
func extPutEntry(t *testing.T, base string, id int64, p *entryPayload) {
	t.Helper()
	b, _ := json.Marshal(p)
	req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/entries/%d", base, id), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /entries/%d error: %v", id, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		buf := new(bytes.Buffer)
		buf.ReadFrom(res.Body)
		t.Fatalf("unexpected update status %d body=%s", res.StatusCode, buf.String())
	}
}

// getEntry fetches an entry by id.
func extGetEntry(t *testing.T, base string, id int64) *entryResult {
	t.Helper()
	res, err := http.Get(fmt.Sprintf("%s/entries/%d", base, id))
	if err != nil {
		t.Fatalf("GET /entries/%d error: %v", id, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unexpected get status %d", res.StatusCode)
	}
	var out entryResult
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	return &out
}

// search queries the API; returns raw entry results slice.
func extSearch(t *testing.T, base, q string, limit int) []entryResult {
	t.Helper()
	urlStr := fmt.Sprintf("%s/search?q=%s&limit=%d", base, url.QueryEscape(q), limit)
	res, err := http.Get(urlStr)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		buf := new(bytes.Buffer)
		buf.ReadFrom(res.Body)
		t.Fatalf("unexpected search status %d body=%s", res.StatusCode, buf.String())
	}
	var out []entryResult
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	return out
}

// TestExternalE2E_KubeConQueries ensures multiple question variants all hit the same cached entry, and an unrelated query misses.
func TestExternalE2E_KubeConQueries(t *testing.T) {
	base := externalBaseURL()
	// Skip this test when no running service is available. This allows the
	// test suite to be run locally without starting the external container.
	resp, err := http.Get(base + "/search?q=health")
	if err != nil {
		t.Skipf("external slmcache not reachable at %s: %v", base, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("external slmcache not ready at %s (status %d)", base, resp.StatusCode)
	}

	backendResp, err := http.Get(base + "/slm-backend")
	if err != nil {
		msg := fmt.Sprintf("failed to fetch slm-backend info: %v", err)
		if os.Getenv("ENFORCE_OLLAMA") == "1" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	defer backendResp.Body.Close()
	if backendResp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("slm-backend unexpected status: %d", backendResp.StatusCode)
		if os.Getenv("ENFORCE_OLLAMA") == "1" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	var backendInfo struct {
		Backend string `json:"backend"`
	}
	if err := json.NewDecoder(backendResp.Body).Decode(&backendInfo); err != nil {
		msg := fmt.Sprintf("decode slm-backend failed: %v", err)
		if os.Getenv("ENFORCE_OLLAMA") == "1" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	if backendInfo.Backend != "ollama" {
		msg := fmt.Sprintf("expected ollama backend, got %q", backendInfo.Backend)
		if os.Getenv("ENFORCE_OLLAMA") == "1" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	// Table-driven cases for related phrasings
	cases := []struct {
		name     string
		prompt   string
		response string
		queries  []string
	}{
		{
			name:     "kubecon-basic",
			prompt:   "Where is KubeCon?",
			response: "KubeCon is in Chicago.",
			queries: []string{
				"What city is KubeCon in?",
				"Where in the world is KubeCon?",
				"Which city hosts KubeCon?",
			},
		},
		{
			name:     "kubecon-year",
			prompt:   "Which city hosts KubeCon 2025?",
			response: "KubeCon 2025 is in Chicago.",
			queries: []string{
				"KubeCon 2025 city",
				"Where is KubeCon 2025?",
				"Which location hosts KubeCon this year?",
			},
		},
		{
			name:     "kubecon-schedule",
			prompt:   "When does KubeCon start?",
			response: "The conference opens on November 18th.",
			queries: []string{
				"What day does KubeCon begin?",
				"Start date for KubeCon?",
				"When is the first day of KubeCon?",
			},
		},
		{
			name:     "kubecon-venue",
			prompt:   "Which venue hosts KubeCon Chicago?",
			response: "McCormick Place hosts the Chicago event.",
			queries: []string{
				"What venue is KubeCon Chicago at?",
				"Where is the Chicago KubeCon venue?",
				"KubeCon Chicago location",
			},
		},
	}

	var createdIDs []int64
	for _, c := range cases {
		entry := extPostEntry(t, base, &entryPayload{Prompt: c.prompt, Response: c.response, Metadata: map[string]interface{}{"case": c.name}})
		createdIDs = append(createdIDs, entry.ID)
		for _, q := range c.queries {
			results := extSearch(t, base, q, 5)
			if len(results) == 0 {
				t.Fatalf("case %s: expected at least one result for query %q", c.name, q)
			}
			found := false
			for _, r := range results {
				if r.ID == entry.ID {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("case %s: expected created entry id %d among results for %q", c.name, entry.ID, q)
			}
		}
	}

	// Miss case
	missQuery := "zyxqvnonmatchtoken"
	missResults := extSearch(t, base, missQuery, 5)
	if len(missResults) != 0 {
		t.Fatalf("expected 0 results for miss query %q, got %d", missQuery, len(missResults))
	}

	// At this point we know the backend is Ollama (or we skipped/fataled).

	// Verify update and delete for first created entry
	if len(createdIDs) > 0 {
		id := createdIDs[0]
		fetched := extGetEntry(t, base, id)
		if fetched.Response == "" {
			t.Fatalf("expected non-empty response for id %d", id)
		}
		// update
		newResp := fetched.Response + " (updated)"
		extPutEntry(t, base, id, &entryPayload{Prompt: fetched.Prompt, Response: newResp, Metadata: fetched.Metadata})
		updated := extGetEntry(t, base, id)
		if updated.Response != newResp {
			t.Fatalf("expected updated response %q got %q", newResp, updated.Response)
		}
		// delete
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/entries/%d", base, id), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("delete request failed: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("expected delete 204 got %d", resp.StatusCode)
		}
		// ensure gone
		resGet, _ := http.Get(fmt.Sprintf("%s/entries/%d", base, id))
		if resGet.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404 for deleted entry, got %d", resGet.StatusCode)
		}
	}
}
