package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/jeefy/slmcache/internal/server"
	"github.com/jeefy/slmcache/internal/store"
)

func main() {
	// initialize vector-backed store and an embedded (co-located) SLM
	st, err := store.New()
	if err != nil {
		log.Fatalf("init store: %v", err)
	}

	srv := server.New(st)
	defer srv.Close()

	addr := ":8080"
	log.Printf("starting slmcache on %s", addr)
	s := &http.Server{
		Addr:         addr,
		Handler:      srv.Router(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server failed: %v", err)
	}

	// graceful shutdown example if extended
	_ = s.Shutdown(context.Background())
}
