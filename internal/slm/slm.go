package slm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

// SLM defines the small language model interface used for embedding and decision.
type SLM interface {
	Embed(prompt string) ([]float64, error)
	// Decide returns chosen entry ID and whether to reuse (hit). Simple policy.
	Decide(prompt string, candidateIDs []int64, candidateEmbeddings [][]float64, candidateScores []float64) (chosenID int64, reuse bool, reason string, err error)
}

// NewMockSLM returns a deterministic lightweight SLM suitable for tests and local use.
func NewMockSLM() SLM { return &mockSLM{dim: 64, threshold: 0.75} }

const minOllamaEmbeddingsVersion = "0.1.25"

// NewDefaultSLM returns the default SLM backend. By default it will try to use
// Ollama (local HTTP API). If Ollama is unreachable or embedding calls fail,
// it gracefully falls back to the deterministic mock SLM so tests and local
// runs keep working.
func NewDefaultSLM() SLM {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("SLM_BACKEND")))
	if backend == "" {
		backend = "ollama"
	}
	switch backend {
	case "mock":
		return NewMockSLM()
	case "ollama":
		require := os.Getenv("SLM_REQUIRE_OLLAMA") == "1"
		baseURL := strings.TrimSpace(os.Getenv("SLM_OLLAMA_URL"))
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		model := os.Getenv("SLM_OLLAMA_MODEL")
		if model == "" {
			model = "nomic-embed-text"
		}
		if err := ensureOllamaModel(baseURL, model); err != nil {
			if require {
				panic(fmt.Sprintf("ollama model %s required but unavailable: %v", model, err))
			}
			log.Printf("slm: ollama model check failed (%v), falling back to mock", err)
			return NewMockSLM()
		}
		s := NewOllamaSLM(baseURL, model)
		// quick sanity embed to ensure Ollama is reachable; if not, handle per requirement flag
		if _, err := s.Embed("health-check"); err != nil {
			msg := fmt.Sprintf("ollama embed failed (SLM_OLLAMA_URL=%s): %v", baseURL, err)
			if require {
				panic(fmt.Sprintf("ollama backend required but embed failed: %s. Ensure 'ollama serve' is running and reachable at %s", err, baseURL))
			}
			log.Printf("slm: %s; falling back to mock", msg)
			return NewMockSLM()
		}
		return s
	default:
		return NewMockSLM()
	}
}

// --- mockSLM (existing deterministic implementation) ---

type mockSLM struct {
	dim       int
	threshold float64
}

// simple deterministic embedding: token hashing into dim-sized vector
func (m *mockSLM) Embed(prompt string) ([]float64, error) {
	v := make([]float64, m.dim)
	// lower-case, split tokens
	toks := strings.Fields(strings.ToLower(prompt))
	for i, t := range toks {
		// simple hash: sum of bytes + position
		h := 0
		for j := 0; j < len(t); j++ {
			h = h*31 + int(t[j])
		}
		idx := (i + h) % m.dim
		if idx < 0 {
			idx += m.dim
		}
		v[idx] += float64(h%10 + 1)
	}
	// normalize
	norm := 0.0
	for _, x := range v {
		norm += x * x
	}
	if norm == 0 {
		return v, nil
	}
	norm = math.Sqrt(norm)
	for i := range v {
		v[i] /= norm
	}
	return v, nil
}

func (m *mockSLM) Decide(prompt string, candidateIDs []int64, candidateEmbeddings [][]float64, candidateScores []float64) (int64, bool, string, error) {
	// pick highest score and compare to threshold
	bestIdx := -1
	best := -1.0
	for i, s := range candidateScores {
		if s > best {
			best = s
			bestIdx = i
		}
	}
	if bestIdx == -1 || best < m.threshold {
		return 0, false, "no candidate exceeded threshold", nil
	}
	return candidateIDs[bestIdx], true, "similarity above threshold", nil
}

// BackendName identifies the mock backend.
func (m *mockSLM) BackendName() string { return "mock" }

// --- Ollama-backed SLM ---

type ollamaSLM struct {
	baseURL string
	model   string
	client  *http.Client
	// threshold used for Decide fallback selection
	threshold float64
}

// NewOllamaSLM constructs an SLM that talks to an Ollama HTTP endpoint.
// baseURL should be like "http://localhost:11434". model is passed in
// requests if the Ollama endpoint supports it.
func NewOllamaSLM(baseURL, model string) SLM {
	return &ollamaSLM{
		baseURL:   strings.TrimRight(baseURL, "/"),
		model:     model,
		client:    &http.Client{Timeout: 6 * time.Second},
		threshold: 0.75,
	}
}

// embedRequest/Response try to be compatible with common embedding APIs
// (OpenAI-like). Ollama's HTTP shape may differ; this adapter tries a
// couple of common shapes and falls back to the mock SLM when the remote
// server doesn't respond as expected.
type embedRequest struct {
	Model  string        `json:"model,omitempty"`
	Prompt string        `json:"prompt,omitempty"`
	Input  []interface{} `json:"input,omitempty"`
}

type embedDataItem struct {
	Embedding []float64 `json:"embedding"`
}

type embedResponse struct {
	Data []embedDataItem `json:"data"`
}

type embedSingleResponse struct {
	Embedding []float64 `json:"embedding"`
}

func (o *ollamaSLM) Embed(prompt string) ([]float64, error) {
	// try common embedding endpoint path(s)
	tried := []string{"/api/embeddings", "/api/embed", "/embed"}
	reqBody := embedRequest{Model: o.model, Prompt: prompt, Input: []interface{}{prompt}}
	bodyB, _ := json.Marshal(reqBody)
	var lastErr error
	for _, p := range tried {
		url := o.baseURL + p
		req, _ := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(bodyB))
		req.Header.Set("Content-Type", "application/json")
		resp, err := o.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, string(data))
			continue
		}
		// try to decode OpenAI-like response
		var er embedResponse
		if err := json.Unmarshal(data, &er); err == nil && len(er.Data) > 0 {
			return er.Data[0].Embedding, nil
		}
		// try Ollama single embedding shape
		var single embedSingleResponse
		if err := json.Unmarshal(data, &single); err == nil && len(single.Embedding) > 0 {
			return single.Embedding, nil
		}
		// if that failed, try to parse direct float array
		var arr []float64
		if err := json.Unmarshal(data, &arr); err == nil && len(arr) > 0 {
			return arr, nil
		}
		lastErr = errors.New("unrecognized embedding response shape")
	}
	// nothing worked; return error for caller to handle (caller may fallback)
	return nil, fmt.Errorf("ollama embedding failed: %v", lastErr)
}

func (o *ollamaSLM) Decide(prompt string, candidateIDs []int64, candidateEmbeddings [][]float64, candidateScores []float64) (int64, bool, string, error) {
	// Primary: choose highest scoring candidate above threshold.
	bestIdx := -1
	best := -1.0
	for i, s := range candidateScores {
		if s > best {
			best = s
			bestIdx = i
		}
	}
	if bestIdx == -1 || best < o.threshold {
		return 0, false, "no candidate exceeded threshold", nil
	}
	return candidateIDs[bestIdx], true, "similarity above threshold (ollama policy)", nil
}

// BackendName identifies the ollama backend.
func (o *ollamaSLM) BackendName() string { return "ollama" }

type ollamaTagsResponse struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

func ensureOllamaModel(baseURL, model string) error {
	trimmed := strings.TrimRight(baseURL, "/")
	if trimmed == "" {
		trimmed = baseURL
	}
	if model == "" {
		return errors.New("missing model name")
	}
	if err := ensureOllamaSupportsEmbeddings(trimmed); err != nil {
		return err
	}
	exists, err := ollamaModelExists(trimmed, model)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return pullOllamaModel(trimmed, model)
}

func ollamaModelExists(baseURL, model string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/tags", nil)
	if err != nil {
		return false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("ollama tags status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return false, err
	}
	for _, m := range tags.Models {
		if modelMatches(m.Name, model) || modelMatches(m.Model, model) {
			return true, nil
		}
	}
	return false, nil
}

func pullOllamaModel(baseURL, model string) error {
	body, _ := json.Marshal(map[string]string{"name": model})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama pull %s failed: status %d %s", model, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func modelMatches(have, want string) bool {
	if have == "" || want == "" {
		return false
	}
	if have == want {
		return true
	}
	if strings.HasPrefix(have, want+":") {
		return true
	}
	return false
}

func ensureOllamaSupportsEmbeddings(baseURL string) error {
	version, err := fetchOllamaVersion(baseURL)
	if err != nil {
		return fmt.Errorf("ollama version check failed: %w", err)
	}
	if compareSemver(version, minOllamaEmbeddingsVersion) < 0 {
		return fmt.Errorf("ollama version %s does not support embeddings; upgrade to %s or newer", version, minOllamaEmbeddingsVersion)
	}
	return nil
}

func fetchOllamaVersion(baseURL string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Version == "" {
		return "", errors.New("empty version field")
	}
	return normalizeVersion(out.Version), nil
}

func normalizeVersion(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.TrimPrefix(trimmed, "v")
	if idx := strings.IndexAny(trimmed, " -"); idx != -1 {
		trimmed = trimmed[:idx]
	}
	return trimmed
}

func compareSemver(a, b string) int {
	split := func(v string) []int {
		v = normalizeVersion(v)
		parts := strings.Split(v, ".")
		res := make([]int, len(parts))
		for i, p := range parts {
			if p == "" {
				continue
			}
			fmt.Sscanf(p, "%d", &res[i])
		}
		return res
	}
	av := split(a)
	bv := split(b)
	maxLen := len(av)
	if len(bv) > maxLen {
		maxLen = len(bv)
	}
	for len(av) < maxLen {
		av = append(av, 0)
	}
	for len(bv) < maxLen {
		bv = append(bv, 0)
	}
	for i := 0; i < maxLen; i++ {
		if av[i] < bv[i] {
			return -1
		}
		if av[i] > bv[i] {
			return 1
		}
	}
	return 0
}
