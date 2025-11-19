package slm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestEnsureOllamaModelTriggersPull(t *testing.T) {
	var pulled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"version": "0.1.30"})
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ollamaTagsResponse{Models: []struct {
				Name  string `json:"name"`
				Model string `json:"model"`
			}{{Name: "other-model"}}})
		case "/api/pull":
			atomic.AddInt32(&pulled, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"success"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	if err := ensureOllamaModel(srv.URL, "nomic-embed-text"); err != nil {
		t.Fatalf("ensure model failed: %v", err)
	}
	if atomic.LoadInt32(&pulled) != 1 {
		t.Fatalf("expected pull to be triggered")
	}
}

func TestEnsureOllamaModelSkipsPullWhenPresent(t *testing.T) {
	var pulled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"version": "0.1.30"})
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ollamaTagsResponse{Models: []struct {
				Name  string `json:"name"`
				Model string `json:"model"`
			}{{Name: "nomic-embed-text"}}})
		case "/api/pull":
			atomic.AddInt32(&pulled, 1)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	if err := ensureOllamaModel(srv.URL, "nomic-embed-text"); err != nil {
		t.Fatalf("ensure model failed: %v", err)
	}
	if atomic.LoadInt32(&pulled) != 0 {
		t.Fatalf("expected no pull when model already present")
	}
}

func TestEnsureOllamaSupportsEmbeddingsVersionCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"version": "0.1.10"})
	}))
	defer srv.Close()
	if err := ensureOllamaSupportsEmbeddings(srv.URL); err == nil {
		t.Fatalf("expected error for old version")
	}
}

func TestCompareSemver(t *testing.T) {
	if compareSemver("0.1.9", "0.1.10") >= 0 {
		t.Fatalf("expected 0.1.9 < 0.1.10")
	}
	if compareSemver("1.0.0", "0.9.9") <= 0 {
		t.Fatalf("expected 1.0.0 > 0.9.9")
	}
	if compareSemver("v1.2.3", "1.2.3") != 0 {
		t.Fatalf("expected equal versions with leading v")
	}
}
