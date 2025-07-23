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
	r.Get("/pnl/latest_report", h.GetLatestPnlReport)
}

// GetLatestPnlReport は最新のPnLレポートを取得します。
func (h *PnlHandler) GetLatestPnlReport(w http.ResponseWriter, r *http.Request) {
	report, err := h.repo.FetchLatestPnlReport(r.Context())
	if err != nil {
		http.Error(w, "Failed to fetch latest PnL report", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(report); err != nil {
		http.Error(w, "Failed to encode report to JSON", http.StatusInternalServerError)
	}
}
