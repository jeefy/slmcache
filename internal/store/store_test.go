package store_test

import (
	"context"
	"testing"

	"github.com/jeefy/slmcache/internal/models"
	"github.com/jeefy/slmcache/internal/store"
)

func TestCreateAndSearch(t *testing.T) {
	st, err := store.New()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	ctx := context.Background()
	e := &models.Entry{Prompt: "How to bake a cake", Response: "Use flour, eggs"}
	// provide a simple vector for testing
	vec := []float64{1.0, 0.0, 0.0}
	id, err := st.CreateEntryWithVector(ctx, e, vec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == 0 {
		t.Fatalf("expected id != 0")
	}

	resIDs, scores, err := st.SearchByVector(ctx, vec, 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resIDs) == 0 {
		t.Fatalf("expected at least one search result")
	}
	if scores[0] <= 0 {
		t.Fatalf("expected positive similarity score")
	}
}
