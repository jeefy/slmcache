package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jeefy/slmcache/internal/models"
	"github.com/jeefy/slmcache/internal/slm"
	"github.com/jeefy/slmcache/internal/store"
)

type Server struct {
	store store.Store
	slm   slm.SLM
	mux   *http.ServeMux

	entryTTL      time.Duration
	purgeInterval time.Duration
	janitorStop   chan struct{}
	janitorWG     sync.WaitGroup
	closeOnce     sync.Once
}

type metadataRequest struct {
	Metadata map[string]interface{} `json:"metadata"`
	Replace  bool                   `json:"replace,omitempty"`
}

func New(st store.Store) *Server {
	entryTTL := durationFromEnv("SLC_ENTRY_TTL", 24*time.Hour)
	purgeEvery := durationFromEnv("SLC_PURGE_INTERVAL", time.Minute)
	s := &Server{
		store:         st,
		slm:           slm.NewDefaultSLM(),
		mux:           http.NewServeMux(),
		entryTTL:      entryTTL,
		purgeInterval: purgeEvery,
		janitorStop:   make(chan struct{}),
	}
	s.routes()
	s.startJanitor()
	return s
}

// Close stops background goroutines started by the server.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		if s.janitorStop != nil {
			close(s.janitorStop)
		}
		s.janitorWG.Wait()
	})
}

func (s *Server) Router() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("/entries", s.handleEntries)
	s.mux.HandleFunc("/entries/", s.handleEntryByID)
	s.mux.HandleFunc("/slm-backend", s.handleSLMBackend)
	s.mux.HandleFunc("/search", s.handleSearch)
}

// POST /entries
func (s *Server) handleEntries(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var e models.Entry
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			// include a brief hint about expected JSON structure
			http.Error(w, "bad request: expected JSON {prompt,response,metadata?}; "+err.Error(), http.StatusBadRequest)
			return
		}
		// embed prompt using the local SLM
		vec, err := s.slm.Embed(e.Prompt)
		if err != nil {
			http.Error(w, "embed error", http.StatusInternalServerError)
			return
		}
		id, err := s.store.CreateEntryWithVector(r.Context(), &e, vec)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		e.ID = id
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(e)
	case http.MethodGet:
		filters := metadataFiltersFromQuery(r.URL.Query())
		entries, err := s.store.FindEntriesByMetadata(r.Context(), filters)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fresh := make([]*models.Entry, 0, len(entries))
		for _, entry := range entries {
			if s.expireIfNeeded(r.Context(), entry) {
				continue
			}
			fresh = append(fresh, entry)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fresh)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// /entries/{id}
func (s *Server) handleEntryByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/entries/")
	rest = strings.TrimSuffix(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if len(parts) > 1 {
		s.handleEntryMetadata(w, r, id, parts[1:])
		return
	}
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		e, err := s.store.GetEntry(ctx, id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if s.expireIfNeeded(ctx, e) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(e)
	case http.MethodPut:
		if existing, err := s.store.GetEntry(ctx, id); err == nil {
			if s.expireIfNeeded(ctx, existing) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
		} else {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var e models.Entry
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		vec, err := s.slm.Embed(e.Prompt)
		if err != nil {
			http.Error(w, "embed error", http.StatusInternalServerError)
			return
		}
		if err := s.store.UpdateEntryWithVector(ctx, id, &e, vec); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := s.store.DeleteEntry(ctx, id); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleEntryMetadata(w http.ResponseWriter, r *http.Request, id int64, segments []string) {
	if len(segments) == 0 {
		http.Error(w, "missing metadata path", http.StatusBadRequest)
		return
	}
	if segments[0] != "metadata" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	extra := segments[1:]
	switch r.Method {
	case http.MethodGet:
		e, err := s.store.GetEntry(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if s.expireIfNeeded(r.Context(), e) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		resp := e.Metadata
		if resp == nil {
			resp = map[string]interface{}{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	case http.MethodPatch:
		var payload metadataRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if payload.Metadata == nil {
			http.Error(w, "metadata payload required", http.StatusBadRequest)
			return
		}
		if e, err := s.store.GetEntry(r.Context(), id); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		} else if s.expireIfNeeded(r.Context(), e) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := s.store.UpdateEntryMetadata(r.Context(), id, payload.Metadata, payload.Replace); err != nil {
			s.respondStoreError(w, err)
			return
		}
		e, err := s.store.GetEntry(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if s.expireIfNeeded(r.Context(), e) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		resp := e.Metadata
		if resp == nil {
			resp = map[string]interface{}{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	case http.MethodDelete:
		if len(extra) > 1 {
			http.Error(w, "too many path segments", http.StatusBadRequest)
			return
		}
		var keys []string
		if len(extra) == 1 && extra[0] != "" {
			keys = []string{extra[0]}
		}
		if e, err := s.store.GetEntry(r.Context(), id); err == nil {
			if s.expireIfNeeded(r.Context(), e) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
		} else {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := s.store.DeleteEntryMetadata(r.Context(), id, keys...); err != nil {
			s.respondStoreError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) respondStoreError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// DELETE /entries/{id}
// and handler for /slm-backend
func (s *Server) handleSLMBackend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// try to assert BackendName method
	type namer interface {
		BackendName() string
	}
	if n, ok := s.slm.(namer); ok {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"backend": n.BackendName()})
		return
	}
	http.Error(w, "unknown", http.StatusInternalServerError)
}

// GET /search?q=...&limit=...
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query().Get("q")
	filters := metadataFiltersFromQuery(r.URL.Query())
	limitStr := r.URL.Query().Get("limit")
	limit := 10
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil {
			limit = v
		}
	}
	// embed query and perform vector search
	vec, err := s.slm.Embed(q)
	if err != nil {
		http.Error(w, "embed error", http.StatusInternalServerError)
		return
	}
	ctx := context.Background()
	ids, scores, err := s.store.SearchByVector(ctx, vec, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// build entries list (filter by a minimal similarity threshold)
	minScore := 0.2
	if v := strings.TrimSpace(os.Getenv("SLM_MIN_SCORE")); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			minScore = parsed
		}
	}
	if n, ok := s.slm.(interface{ BackendName() string }); ok {
		if n.BackendName() == "ollama" && strings.TrimSpace(os.Getenv("SLM_MIN_SCORE")) == "" {
			// Empirically, nomic-embed-text yields ~0.9 for paraphrases and ~0.4 for
			// unrelated text, so we bias the default threshold higher when using
			// the Ollama backend to reduce false positives.
			minScore = 0.8
		}
	}
	out := []*models.Entry{}
	for i, id := range ids {
		if scores[i] < minScore {
			continue
		}
		e, err := s.store.GetEntry(ctx, id)
		if err != nil {
			continue
		}
		if s.expireIfNeeded(ctx, e) {
			continue
		}
		if matchesFilters(e, filters) {
			out = append(out, e)
		}
	}
	// fallback: if no results from vector similarity (e.g., zero vectors),
	// do a simple substring/token match on stored prompts to help tests and
	// provide reasonable behavior for very small/mock embeddings.
	qlow := strings.ToLower(q)
	qTokens := strings.Fields(qlow)
	// collect fallback matches (token-based) in any case and append missing ones
	fallback := []*models.Entry{}
	for _, sid := range s.store.AllIDs() {
		e, err := s.store.GetEntry(ctx, sid)
		if err != nil {
			continue
		}
		if s.expireIfNeeded(ctx, e) {
			continue
		}
		etoks := strings.Fields(strings.ToLower(e.Prompt))
		match := 0
		for _, qt := range qTokens {
			for _, et := range etoks {
				if strings.Contains(et, qt) {
					match++
					break
				}
			}
		}
		if match == len(qTokens) {
			fallback = append(fallback, e)
		}
	}
	// append fallback matches that aren't already in out
	seen := map[int64]struct{}{}
	for _, e := range out {
		seen[e.ID] = struct{}{}
	}
	for _, f := range fallback {
		if _, ok := seen[f.ID]; ok {
			continue
		}
		if matchesFilters(f, filters) {
			out = append(out, f)
			seen[f.ID] = struct{}{}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func metadataFiltersFromQuery(values url.Values) map[string]string {
	filters := map[string]string{}
	for key, vals := range values {
		if strings.HasPrefix(key, "metadata.") && len(vals) > 0 {
			name := strings.TrimPrefix(key, "metadata.")
			if name == "" {
				continue
			}
			filters[name] = vals[len(vals)-1]
		}
	}
	if grouped, ok := values["metadata"]; ok {
		for _, raw := range grouped {
			parts := strings.SplitN(raw, ":", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if key == "" {
				continue
			}
			filters[key] = val
		}
	}
	if len(filters) == 0 {
		return nil
	}
	return filters
}

func matchesFilters(entry *models.Entry, filters map[string]string) bool {
	if len(filters) == 0 {
		return true
	}
	if entry == nil || entry.Metadata == nil {
		return false
	}
	for key, expected := range filters {
		val, ok := entry.Metadata[key]
		if !ok {
			return false
		}
		if expected == "" {
			continue
		}
		if toString(val) != expected {
			return false
		}
	}
	return true
}

func toString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprint(v)
	}
}

func (s *Server) startJanitor() {
	if s.entryTTL <= 0 || s.janitorStop == nil {
		return
	}
	interval := s.purgeInterval
	if interval <= 0 {
		interval = time.Minute
	}
	s.purgeExpired(context.Background())
	s.janitorWG.Add(1)
	ticker := time.NewTicker(interval)
	go func() {
		defer s.janitorWG.Done()
		for {
			select {
			case <-ticker.C:
				s.purgeExpired(context.Background())
			case <-s.janitorStop:
				ticker.Stop()
				return
			}
		}
	}()
}

func (s *Server) purgeExpired(ctx context.Context) int {
	if s.entryTTL <= 0 {
		return 0
	}
	cutoff := time.Now().Add(-s.entryTTL)
	removed := 0
	for _, id := range s.store.AllIDs() {
		e, err := s.store.GetEntry(ctx, id)
		if err != nil || e == nil {
			continue
		}
		if entryExpiredAt(e, cutoff) {
			_ = s.store.DeleteEntry(ctx, id)
			removed++
		}
	}
	return removed
}

func (s *Server) isExpired(e *models.Entry) bool {
	if s.entryTTL <= 0 {
		return false
	}
	cutoff := time.Now().Add(-s.entryTTL)
	return entryExpiredAt(e, cutoff)
}

func (s *Server) expireIfNeeded(ctx context.Context, e *models.Entry) bool {
	if !s.isExpired(e) {
		return false
	}
	if e != nil {
		_ = s.store.DeleteEntry(ctx, e.ID)
	}
	return true
}

func entryExpiredAt(e *models.Entry, cutoff time.Time) bool {
	if e == nil {
		return false
	}
	ts := e.UpdatedAt
	if ts.IsZero() || e.CreatedAt.After(ts) {
		ts = e.CreatedAt
	}
	if ts.IsZero() {
		return false
	}
	return ts.Before(cutoff)
}

func durationFromEnv(key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}
