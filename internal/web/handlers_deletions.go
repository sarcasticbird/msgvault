package web

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/wesm/msgvault/internal/deletion"
	"github.com/wesm/msgvault/internal/web/templates"
)

// validManifestID matches the format produced by deletion.generateID:
// YYYYMMDD-HHMMSS-<sanitized description up to 20 chars>
var validManifestID = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[a-zA-Z0-9_-]{1,20}$`)

func (h *Handler) handleDeletions(w http.ResponseWriter, r *http.Request) {
	if h.deletions == nil {
		http.Error(w, "Deletion staging not available", http.StatusServiceUnavailable)
		return
	}

	pending, _ := h.deletions.ListPending()
	inProgress, _ := h.deletions.ListInProgress()
	completed, _ := h.deletions.ListCompleted()
	failed, _ := h.deletions.ListFailed()

	data := templates.DeletionsData{
		Pending:    pending,
		InProgress: inProgress,
		Completed:  completed,
		Failed:     failed,
	}

	var buf bytes.Buffer
	if err := templates.DeletionsPage(data).Render(r.Context(), &buf); err != nil {
		slog.Error("failed to render deletions", "error", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// handleStageBatch stages multiple messages for deletion from checkbox selection.
// Accepts gmail_id[] form values posted from message list checkboxes.
func (h *Handler) handleStageBatch(w http.ResponseWriter, r *http.Request) {
	if h.deletions == nil {
		http.Error(w, "Deletion staging not available", http.StatusServiceUnavailable)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	gmailIDs := r.Form["gmail_id"]
	if len(gmailIDs) == 0 {
		http.Redirect(w, r, "/messages", http.StatusSeeOther)
		return
	}

	// Look up account from the first message; leave empty if lookup fails
	// (the CLI will resolve per-ID at execution time)
	var account string
	msg, err := h.engine.GetMessageBySourceID(ctx, gmailIDs[0])
	if err == nil && msg != nil {
		account = msg.AccountEmail
	}

	description := fmt.Sprintf("Web selection (%d messages)", len(gmailIDs))

	manifest := deletion.NewManifest(description, gmailIDs)
	manifest.Filters = deletion.Filters{Account: account}
	manifest.CreatedBy = "web"

	if err := h.deletions.SaveManifest(manifest); err != nil {
		slog.Error("failed to save manifest", "error", err)
		http.Error(w, "Failed to save manifest", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/deletions", http.StatusSeeOther)
}

// handleStageMessage stages a single message for deletion by its database ID.
func (h *Handler) handleStageMessage(w http.ResponseWriter, r *http.Request) {
	if h.deletions == nil {
		http.Error(w, "Deletion staging not available", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()

	msgIDStr := chi.URLParam(r, "id")
	msgID, err := strconv.ParseInt(msgIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid message ID", http.StatusBadRequest)
		return
	}

	msg, err := h.engine.GetMessage(ctx, msgID)
	if err != nil {
		slog.Error("failed to load message for deletion", "error", err, "id", msgID)
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	if msg.SourceMessageID == "" {
		http.Error(w, "Message has no Gmail ID", http.StatusBadRequest)
		return
	}

	description := fmt.Sprintf("Message: %s", msg.Subject)
	if description == "Message: " {
		description = "Message: (no subject)"
	}

	manifest := deletion.NewManifest(description, []string{msg.SourceMessageID})
	manifest.Filters = deletion.Filters{Account: msg.AccountEmail}
	manifest.CreatedBy = "web"

	if err := h.deletions.SaveManifest(manifest); err != nil {
		slog.Error("failed to save manifest", "error", err)
		http.Error(w, "Failed to save manifest", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/deletions", http.StatusSeeOther)
}

func (h *Handler) handleCancelDeletion(w http.ResponseWriter, r *http.Request) {
	if h.deletions == nil {
		http.Error(w, "Deletion staging not available", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	if !validManifestID.MatchString(id) {
		http.Error(w, "Invalid batch ID", http.StatusBadRequest)
		return
	}

	if err := h.deletions.CancelManifest(id); err != nil {
		slog.Error("failed to cancel manifest", "error", err, "id", id)
		http.Error(w, fmt.Sprintf("Failed to cancel: %v", err), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/deletions", http.StatusSeeOther)
}
