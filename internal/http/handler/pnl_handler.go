package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/your-org/obi-scalp-bot/internal/datastore"
)

// PnlHandler はPnL関連のHTTPリクエストを処理します。
type PnlHandler struct {
	repo *datastore.Repository
}

// NewPnlHandler は新しいPnlHandlerを作成します。
func NewPnlHandler(repo *datastore.Repository) *PnlHandler {
	return &PnlHandler{repo: repo}
}

// RegisterRoutes はchiルーターにPnL関連のルートを登録します。
func (h *PnlHandler) RegisterRoutes(r chi.Router) {
	r.Get("/pnl/latest_metrics", h.GetLatestPnlMetrics)
}

// GetLatestPnlMetrics は最新のパフォーマンス指標を取得します。
func (h *PnlHandler) GetLatestPnlMetrics(w http.ResponseWriter, r *http.Request) {
	metrics, err := h.repo.FetchLatestPerformanceMetrics(r.Context())
	if err != nil {
		http.Error(w, "Failed to fetch latest PnL metrics", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metrics); err != nil {
		http.Error(w, "Failed to encode metrics to JSON", http.StatusInternalServerError)
	}
}
