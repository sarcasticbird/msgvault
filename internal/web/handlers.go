package web

import (
	"bytes"
	"log/slog"
	"net/http"

	"github.com/wesm/msgvault/internal/query"
	"github.com/wesm/msgvault/internal/web/templates"
)

func (h *Handler) handlePlaceholder(title, page string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		if err := templates.Placeholder(title, page).Render(r.Context(), &buf); err != nil {
			slog.Error("failed to render placeholder", "error", err)
			http.Error(w, "Failed to render page", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = buf.WriteTo(w)
	}
}

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	stats, err := h.engine.GetTotalStats(ctx, query.StatsOptions{})
	if err != nil {
		slog.Error("failed to get stats", "error", err)
		http.Error(w, "Failed to load stats", http.StatusInternalServerError)
		return
	}

	accounts, err := h.engine.ListAccounts(ctx)
	if err != nil {
		slog.Error("failed to list accounts", "error", err)
		http.Error(w, "Failed to load accounts", http.StatusInternalServerError)
		return
	}

	data := templates.DashboardData{
		Stats:    stats,
		Accounts: accounts,
	}

	var buf bytes.Buffer
	if err := templates.Dashboard(data).Render(ctx, &buf); err != nil {
		slog.Error("failed to render dashboard", "error", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}
