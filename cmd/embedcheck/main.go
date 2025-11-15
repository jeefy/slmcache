package main

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/jeefy/slmcache/internal/models"
	"github.com/jeefy/slmcache/internal/slm"
	"github.com/jeefy/slmcache/internal/store"
)

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

func main() {
	m := slm.NewMockSLM()
	st, _ := store.New()

	// create and search first entry
	e1 := &models.Entry{Prompt: "How to bake a cake", Response: "Use flour, eggs"}
	v1, _ := m.Embed(e1.Prompt)
	id1, _ := st.CreateEntryWithVector(context.Background(), e1, v1)
	fmt.Printf("created id1=%d\n", id1)

	q := "bake cake"
	vq, _ := m.Embed(q)
	ids, scores, _ := st.SearchByVector(context.Background(), vq, 5)
	fmt.Printf("search ids=%v scores=%v\n", ids, scores)
	// fallback token match
	qlow := strings.ToLower(q)
	qTokens := strings.Fields(qlow)
	for _, sid := range st.AllIDs() {
		e, _ := st.GetEntry(context.Background(), sid)
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
			fmt.Printf("fallback matched id=%d prompt=%q\n", sid, e.Prompt)
		}
	}

	// create and search second entry
	e2 := &models.Entry{Prompt: "Explain quantum entanglement", Response: "It's a quantum correlation."}
	v2, _ := m.Embed(e2.Prompt)
	id2, _ := st.CreateEntryWithVector(context.Background(), e2, v2)
	fmt.Printf("created id2=%d\n", id2)
	q2 := "quantum entanglement"
	vq2, _ := m.Embed(q2)
	ids2, scores2, _ := st.SearchByVector(context.Background(), vq2, 5)
	fmt.Printf("search2 ids=%v scores=%v\n", ids2, scores2)
	// fallback token match
	qlow2 := strings.ToLower(q2)
	qTokens2 := strings.Fields(qlow2)
	for _, sid := range st.AllIDs() {
		e, _ := st.GetEntry(context.Background(), sid)
		etoks := strings.Fields(strings.ToLower(e.Prompt))
		match := 0
		for _, qt := range qTokens2 {
			for _, et := range etoks {
				if strings.Contains(et, qt) {
					match++
					break
				}
			}
		}
		if match == len(qTokens2) {
			fmt.Printf("fallback matched2 id=%d prompt=%q\n", sid, e.Prompt)
		}
	}
}
