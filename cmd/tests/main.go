package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hello", func(w http.ResponseWriter, r *http.Request) {
		response := map[string]string{
			"message": "hello from mock backend",
			"time":    time.Now().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})
	log.Println("mock backend listening on :9000")
	if err := http.ListenAndServe(":9000", mux); err != nil {
		log.Fatalf("mock backend failed: %v", err)
	}
}