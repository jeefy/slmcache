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

func TestMetadataOperations(t *testing.T) {
	st, err := store.New()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	ctx := context.Background()
	id, err := st.CreateEntryWithVector(ctx, &models.Entry{Prompt: "Doc", Response: "Answer"}, []float64{0.5, 0.5})
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if err := st.UpdateEntryMetadata(ctx, id, map[string]interface{}{"source": "faq", "lang": "en"}, false); err != nil {
		t.Fatalf("update metadata: %v", err)
	}
	entries, err := st.FindEntriesByMetadata(ctx, map[string]string{"source": "faq"})
	if err != nil {
		t.Fatalf("find metadata: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != id {
		t.Fatalf("expected entry returned from metadata query")
	}
	if err := st.DeleteEntryMetadata(ctx, id, "source"); err != nil {
		t.Fatalf("delete metadata: %v", err)
	}
	entries, err = st.FindEntriesByMetadata(ctx, map[string]string{"source": "faq"})
	if err != nil {
		t.Fatalf("find metadata after delete: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries after metadata removal")
	}
}
