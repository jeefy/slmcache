package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/jeefy/slmcache/internal/models"
	"github.com/jeefy/slmcache/internal/server"
	"github.com/jeefy/slmcache/internal/store"
)

func postEntry(base string, e *models.Entry) (*models.Entry, error) {
	b, _ := json.Marshal(e)
	res, err := http.Post(base+"/entries", "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var got models.Entry
	_ = json.NewDecoder(res.Body).Decode(&got)
	return &got, nil
}

func search(base, q string) ([]*models.Entry, error) {
	res, err := http.Get(base + "/search?q=" + q + "&limit=5")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var out []*models.Entry
	_ = json.NewDecoder(res.Body).Decode(&out)
	return out, nil
}

func main() {
	st, _ := store.New()
	srv := server.New(st)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	created, _ := postEntry(ts.URL, &models.Entry{Prompt: "How to bake a cake", Response: "Use flour, eggs, and bake"})
	fmt.Printf("created: %+v\n", created)
	candidates, _ := search(ts.URL, "bake+cake")
	fmt.Printf("candidates for 'bake cake': %v\n", candidates)

	missCandidates, _ := search(ts.URL, "quantum+entanglement+unicorn")
	fmt.Printf("candidates for unrelated: %v\n", missCandidates)

	newEntry, _ := postEntry(ts.URL, &models.Entry{Prompt: "Explain quantum entanglement", Response: "It's a quantum correlation."})
	fmt.Printf("created new: %+v\n", newEntry)
	got, _ := search(ts.URL, "quantum+entanglement")
	fmt.Printf("search for 'quantum entanglement' -> %v\n", got)
}
