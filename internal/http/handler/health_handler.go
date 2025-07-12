package handler

import (
	"log"
	"net/http"
)

// HealthCheckHandler is a simple handler that returns HTTP 200 OK.
// It can be used for health checks by Docker or other services.
func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		log.Printf("Error writing health check response: %v", err)
	}
}
