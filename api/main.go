package main

import (
	"context"
	"log"
	"net/http"
	"os"
)

// Allows the React dev server at :5173 to call the API without browser CORS blocks
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		// Handle preflight before routing — router returns 404 for OPTIONS on unknown paths
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	log.Println("Flowgate API listening on :3000")

	// Hub must be created before routes so handlers can close over it
	hub := newHub()

	// Connect to PostgreSQL once at startup — pool is reused across all requests
	// Read DB URL from env so Docker can inject the container hostname
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}
	pool, err := connectDB(context.Background(), dbURL)
	if err != nil {
		log.Fatal(err)
	}

	// Register routes — Go 1.22 method prefix syntax handles GET vs POST vs DELETE on the same path
	http.HandleFunc("POST /api/requests", postRequests(pool, hub))
	http.HandleFunc("GET /api/requests", getRequests(pool))
	http.HandleFunc("DELETE /api/requests", deleteRequests(pool))
	http.HandleFunc("POST /api/requests/{id}/replay", replayRequests(pool))
	http.HandleFunc("/ws", serveWs(hub))

	// run() must be a goroutine — it blocks forever managing the clients map
	go hub.run()

	// Wrap default mux with CORS so the browser at :5173 can reach the API
	err = http.ListenAndServe(":3000", corsMiddleware(http.DefaultServeMux))
	if err != nil {
		log.Fatal(err)
	}
}
