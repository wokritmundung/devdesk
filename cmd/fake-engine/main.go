// fake-engine emulates the slice of the vLLM dev API that the engine-native
// checkpoint path uses (/sleep, /wake_up, /is_sleeping, /v1/models). It lets
// the full checkpoint/restore round trip run on GPU-less machines
// (Codespaces, CI). Not a load simulator; timing numbers from it are
// transport-only.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"sync"
	"time"
)

func main() {
	listen := flag.String("listen", ":8000", "listen address")
	model := flag.String("model", "fake/llama-emulated", "model id to report")
	sleepDelay := flag.Duration("sleep-delay", 150*time.Millisecond, "simulated time to quiesce")
	wakeDelay := flag.Duration("wake-delay", 400*time.Millisecond, "simulated time to wake")
	flag.Parse()

	var mu sync.Mutex
	sleeping := false

	mux := http.NewServeMux()
	mux.HandleFunc("POST /sleep", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(*sleepDelay)
		mu.Lock()
		sleeping = true
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /wake_up", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(*wakeDelay)
		mu.Lock()
		sleeping = false
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /is_sleeping", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(map[string]bool{"is_sleeping": sleeping})
	})
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": *model}},
		})
	})
	log.Printf("fake-engine listening on %s (model=%s)", *listen, *model)
	log.Fatal(http.ListenAndServe(*listen, mux))
}
