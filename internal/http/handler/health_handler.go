package handler

import (
	"net/http"
)

// HealthCheckHandler is a simple handler that returns HTTP 200 OK.
// It can be used for health checks by Docker or other services.
func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
