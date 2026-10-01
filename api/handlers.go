package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CheckOrigin allows all origins — safe for local dev, lock down before exposing publicly
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Returns captured requests from DB, optionally filtered by method, status prefix, and URL keyword
func getRequests(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Query().Get("method")
		status := r.URL.Query().Get("status")
		search := r.URL.Query().Get("search")

		query := "SELECT * FROM requests WHERE 1=1"
		args := []any{}
		i := 1
		if method != "" {
			query += fmt.Sprintf(" AND method = $%d", i)
			args = append(args, method)
			i++
		}
		if status != "" {
			query += fmt.Sprintf(" AND CAST(res_status AS TEXT) LIKE $%d", i)
			args = append(args, status+"%")
			i++
		}
		if search != "" {
			query += fmt.Sprintf(" AND url ILIKE $%d", i)
			args = append(args, "%"+search+"%")
			i++
		}
		query += " ORDER BY captured_at DESC"

		rows, err := pool.Query(r.Context(), query, args...)
		if err != nil {
			http.Error(w, "DB query error:", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		var requests []Request
		for rows.Next() {
			var req Request
			err := rows.Scan(&req.ID, &req.CapturedAt, &req.Method, &req.URL, &req.Host, &req.Path, &req.Protocol, &req.ReqHeaders, &req.ReqBody, &req.ResStatus, &req.ResHeaders, &req.ResBody, &req.DurationMs, &req.TLS)
			if err != nil {
				http.Error(w, "DB request error:", http.StatusInternalServerError)
				return
			}
			requests = append(requests, req)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(requests)
	}
}

// Receives a captured request from the proxy, inserts it into the DB, then pushes it to all WS clients
func postRequests(pool *pgxpool.Pool, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Request
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			http.Error(w, "DB error:", http.StatusBadRequest)
			return
		}
		// RETURNING fills in the DB-generated id and timestamp so live WebSocket items can be replayed immediately
		err = pool.QueryRow(r.Context(), "INSERT INTO requests (method, url, host, path, protocol, req_headers, req_body, res_status, res_headers, res_body, duration_ms, tls) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id, captured_at", req.Method, req.URL, req.Host, req.Path, req.Protocol, req.ReqHeaders, req.ReqBody, req.ResStatus, req.ResHeaders, req.ResBody, req.DurationMs, req.TLS).Scan(&req.ID, &req.CapturedAt)
		if err != nil {
			http.Error(w, "DB insertion error:", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		j, err := json.Marshal(req)
		if err != nil {
			log.Println(err)
			return
		}
		hub.broadcast <- j
	}
}

// Deletes all captured requests from the DB
func deleteRequests(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := pool.Exec(r.Context(), "DELETE FROM requests")
		if err != nil {
			http.Error(w, "DB deletion error:", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Fetches a captured request by ID from the DB and re-fires it, returning the new response
func replayRequests(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		row := pool.QueryRow(r.Context(), "SELECT method, url, req_headers, req_body FROM requests WHERE id = $1", id)
		var req Request
		err := row.Scan(&req.Method, &req.URL, &req.ReqHeaders, &req.ReqBody)
		if err != nil {
			http.Error(w, "DB row scan error:", http.StatusInternalServerError)
			return
		}
		// Re-send the original body too, otherwise POST/PUT replays go out empty
		request, err := http.NewRequest(req.Method, req.URL, strings.NewReader(req.ReqBody))
		if err != nil {
			http.Error(w, "HTTP request error:", http.StatusInternalServerError)
			return
		}
		for key, values := range req.ReqHeaders {
			request.Header.Set(key, values[0])
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			http.Error(w, "HTTP response error:", http.StatusInternalServerError)
			return
		}
		j, err := io.ReadAll(response.Body)
		defer response.Body.Close()
		if err != nil {
			http.Error(w, "Read response error:", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"res_status": response.StatusCode,
			"res_body":   string(j),
		})
	}
}

// Upgrades HTTP to WebSocket, registers with hub, blocks until client disconnects, then unregisters
func serveWs(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, "Hub serve error:", http.StatusInternalServerError)
			return
		}
		hub.register <- conn
		// Read loop keeps connection alive — ReadMessage errors signal client disconnect
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
		hub.unregister <- conn
	}
}
