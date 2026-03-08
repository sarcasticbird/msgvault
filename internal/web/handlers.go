package web

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"
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

func (h *Handler) handleBrowse(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	viewType := parseViewType(r)
	opts := parseAggregateOptions(r)

	rows, err := h.engine.Aggregate(ctx, viewType, opts)
	if err != nil {
		slog.Error("failed to aggregate", "error", err, "view", viewType)
		http.Error(w, "Failed to load data", http.StatusInternalServerError)
		return
	}

	stats, err := h.engine.GetTotalStats(ctx, query.StatsOptions{
		SourceID:              opts.SourceID,
		WithAttachmentsOnly:   opts.WithAttachmentsOnly,
		HideDeletedFromSource: opts.HideDeletedFromSource,
	})
	if err != nil {
		slog.Error("failed to get stats", "error", err)
	}

	data := templates.BrowseData{
		Stats:       stats,
		Rows:        rows,
		ViewType:    viewTypeToString(viewType),
		ViewLabel:   viewType.String(),
		SortField:   sortFieldToString(opts.SortField),
		SortDir:     sortDirToString(opts.SortDirection),
		Granularity: timeGranularityToString(opts.TimeGranularity),
		AccountID:   r.URL.Query().Get("account"),
		Attachments: opts.WithAttachmentsOnly,
		HideDeleted: opts.HideDeletedFromSource,
	}

	var buf bytes.Buffer
	if err := templates.Aggregates(data).Render(ctx, &buf); err != nil {
		slog.Error("failed to render aggregates", "error", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func (h *Handler) handleDrill(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	viewType := parseViewType(r)
	opts := parseAggregateOptions(r)
	filter := parseDrillFilter(r)

	rows, err := h.engine.SubAggregate(ctx, filter, viewType, opts)
	if err != nil {
		slog.Error("failed to sub-aggregate", "error", err, "view", viewType)
		http.Error(w, "Failed to load data", http.StatusInternalServerError)
		return
	}

	stats, err := h.engine.GetTotalStats(ctx, query.StatsOptions{
		SourceID:              opts.SourceID,
		WithAttachmentsOnly:   opts.WithAttachmentsOnly,
		HideDeletedFromSource: opts.HideDeletedFromSource,
	})
	if err != nil {
		slog.Error("failed to get stats", "error", err)
	}

	// Build drill filters map from current request params (deterministic order)
	drillFilters := make(map[string]string)
	drillKeys := []string{"sender", "sender_name", "recipient", "recipient_name", "domain", "label", "time_period"}
	for _, key := range drillKeys {
		if _, ok := r.URL.Query()[key]; ok {
			drillFilters[key] = r.URL.Query().Get(key)
		}
	}

	// Build breadcrumbs with full state preservation
	browseURL := templates.BrowseData{
		ViewType:    viewTypeToString(viewType),
		SortField:   sortFieldToString(opts.SortField),
		SortDir:     sortDirToString(opts.SortDirection),
		Granularity: timeGranularityToString(opts.TimeGranularity),
		AccountID:   r.URL.Query().Get("account"),
		Attachments: opts.WithAttachmentsOnly,
		HideDeleted: opts.HideDeletedFromSource,
	}
	breadcrumbs := []templates.Breadcrumb{
		{Label: "Browse", URL: browseURL.ViewTabURL(viewTypeToString(viewType))},
	}
	for _, key := range drillKeys {
		if v, ok := drillFilters[key]; ok {
			label := key + ": " + v
			if v == "" {
				label = key + ": (empty)"
			}
			breadcrumbs = append(breadcrumbs, templates.Breadcrumb{Label: label})
		}
	}

	data := templates.BrowseData{
		Stats:        stats,
		Rows:         rows,
		ViewType:     viewTypeToString(viewType),
		ViewLabel:    viewType.String(),
		SortField:    sortFieldToString(opts.SortField),
		SortDir:      sortDirToString(opts.SortDirection),
		Granularity:  timeGranularityToString(opts.TimeGranularity),
		AccountID:    r.URL.Query().Get("account"),
		Attachments:  opts.WithAttachmentsOnly,
		HideDeleted:  opts.HideDeletedFromSource,
		DrillFilters: drillFilters,
		Breadcrumbs:  breadcrumbs,
	}

	var buf bytes.Buffer
	if err := templates.Aggregates(data).Render(ctx, &buf); err != nil {
		slog.Error("failed to render drill-down", "error", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	filter := parseMessageFilter(r)
	page := parsePage(r)

	// Fetch one extra row to detect if there are more pages
	pageSize := filter.Pagination.Limit
	filter.Pagination.Limit = pageSize + 1

	messages, err := h.engine.ListMessages(ctx, filter)
	if err != nil {
		slog.Error("failed to list messages", "error", err)
		http.Error(w, "Failed to load messages", http.StatusInternalServerError)
		return
	}

	hasMore := len(messages) > pageSize
	if hasMore {
		messages = messages[:pageSize]
	}

	// Build filter map for template URL construction
	filters := make(map[string]string)
	filterKeys := []string{"sender", "sender_name", "recipient", "recipient_name", "domain", "label", "time_period", "conversation"}
	for _, key := range filterKeys {
		if _, ok := r.URL.Query()[key]; ok {
			filters[key] = r.URL.Query().Get(key)
		}
	}

	data := templates.MessagesData{
		Messages:    messages,
		Page:        page,
		PageSize:    pageSize,
		HasMore:     hasMore,
		SortField:   messageSortFieldToString(filter.Sorting.Field),
		SortDir:     sortDirToString(filter.Sorting.Direction),
		Filters:     filters,
		AccountID:   r.URL.Query().Get("account"),
		Attachments: filter.WithAttachmentsOnly,
		HideDeleted: filter.HideDeletedFromSource,
	}

	var buf bytes.Buffer
	if err := templates.Messages(data).Render(ctx, &buf); err != nil {
		slog.Error("failed to render messages", "error", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func (h *Handler) handleMessageDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid message ID", http.StatusBadRequest)
		return
	}

	msg, err := h.engine.GetMessage(ctx, id)
	if err != nil {
		slog.Error("failed to get message", "error", err, "id", id)
		http.Error(w, "Failed to load message", http.StatusInternalServerError)
		return
	}
	if msg == nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	// Build back URL from referer, restricted to same-origin paths only
	backURL := "/messages"
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Host == "" {
			backURL = ref
		} else if err == nil && u.Host == r.Host {
			backURL = u.RequestURI()
		}
	}

	data := templates.MessageDetailData{
		Message: msg,
		BackURL: backURL,
	}

	var buf bytes.Buffer
	if err := templates.MessageDetailPage(data).Render(ctx, &buf); err != nil {
		slog.Error("failed to render message detail", "error", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}
