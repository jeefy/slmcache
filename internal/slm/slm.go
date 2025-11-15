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
		url := os.Getenv("SLM_OLLAMA_URL")
		if url == "" {
			url = "http://localhost:11434"
		}
		model := os.Getenv("SLM_OLLAMA_MODEL")
		if model == "" {
			model = "nomic-embed-text"
		}
		s := NewOllamaSLM(url, model)
		require := os.Getenv("SLM_REQUIRE_OLLAMA") == "1"
		// quick sanity embed to ensure Ollama is reachable; if not, handle per requirement flag
		if _, err := s.Embed("health-check"); err != nil {
			if require {
				panic(fmt.Sprintf("ollama backend required but embed failed: %v", err))
			}
			log.Printf("slm: ollama embed failed (%v), falling back to mock", err)
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
