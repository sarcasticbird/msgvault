# Email HTML Rendering Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render emails in the web UI as they appear in Gmail — full HTML, inline images via CID resolution, external images blocked by default — using a sandboxed iframe fed by two new endpoints.

**Architecture:** A new `GET /messages/{id}/html` endpoint serves a self-contained HTML document with rewritten CID references, stripped scripts, and blocked external images. A second `GET /messages/{id}/inline/{cid}` endpoint serves inline MIME parts by Content-ID. The message detail template replaces its `<pre>` body with a sandboxed iframe pointing at the HTML endpoint.

**Tech Stack:** Go stdlib, `golang.org/x/net/html` (HTML rewriting), `github.com/jhillyerd/enmime` (MIME parsing), `github.com/go-chi/chi/v5` (routing) — all already in go.mod.

---

### Task 1: Add `GetMessageRaw` to the Query Engine Interface

The web layer accesses data through `query.Engine`, but raw MIME retrieval isn't exposed there. We need to add it so the new handlers can decompress and parse MIME on the fly.

**Files:**
- Modify: `internal/query/engine.go`
- Modify: `internal/query/shared.go`
- Modify: `internal/query/sqlite.go`
- Modify: `internal/query/duckdb.go`
- Modify: `internal/query/querytest/mock_engine.go`
- Test: `internal/query/sqlite_crud_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/query/sqlite_crud_test.go`:

```go
func TestGetMessageRaw(t *testing.T) {
	ctx := context.Background()
	engine := setupTestEngine(t)

	// Insert a message with raw MIME data via the store
	rawMIME := []byte("From: test@example.com\r\nSubject: Test\r\n\r\nHello")
	msgID := insertTestMessageWithRaw(t, engine, rawMIME)

	got, err := engine.GetMessageRaw(ctx, msgID)
	if err != nil {
		t.Fatalf("GetMessageRaw: %v", err)
	}
	if !bytes.Equal(got, rawMIME) {
		t.Errorf("GetMessageRaw = %q, want %q", got, rawMIME)
	}
}

func TestGetMessageRaw_NotFound(t *testing.T) {
	ctx := context.Background()
	engine := setupTestEngine(t)

	got, err := engine.GetMessageRaw(ctx, 999999)
	if err != nil {
		t.Fatalf("GetMessageRaw unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("GetMessageRaw = %q, want nil", got)
	}
}
```

Note: `insertTestMessageWithRaw` is a test helper that inserts a message and its raw MIME data — check if something like this already exists in the test helpers, and create it if not. It should insert into both `messages` and `message_raw` tables.

- [ ] **Step 2: Run test to verify it fails**

Run: `flox activate -c 'go test ./internal/query/ -run TestGetMessageRaw -v'`
Expected: Compile error — `GetMessageRaw` not defined on engine.

- [ ] **Step 3: Add `GetMessageRaw` to the Engine interface**

In `internal/query/engine.go`, add to the `Engine` interface:

```go
// GetMessageRaw returns the decompressed raw MIME data for a message.
// Returns nil, nil if the message has no raw data stored.
GetMessageRaw(ctx context.Context, id int64) ([]byte, error)
```

- [ ] **Step 4: Add shared implementation**

In `internal/query/shared.go`, add a new function (reuse the decompression logic from `extractBodyFromRawShared`):

```go
func getMessageRawShared(ctx context.Context, db *sql.DB, tablePrefix string, messageID int64) ([]byte, error) {
	var compressed []byte
	var compression sql.NullString

	err := db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT raw_data, compression FROM %smessage_raw WHERE message_id = ?
	`, tablePrefix), messageID).Scan(&compressed, &compression)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if compression.Valid && compression.String == "zlib" {
		r, err := zlib.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return nil, fmt.Errorf("zlib reader: %w", err)
		}
		defer func() { _ = r.Close() }()
		return io.ReadAll(r)
	}

	return compressed, nil
}
```

- [ ] **Step 5: Implement in SQLiteEngine and DuckDBEngine**

In `internal/query/sqlite.go`:

```go
func (e *SQLiteEngine) GetMessageRaw(ctx context.Context, id int64) ([]byte, error) {
	return getMessageRawShared(ctx, e.db, "", id)
}
```

In `internal/query/duckdb.go`:

```go
func (e *DuckDBEngine) GetMessageRaw(ctx context.Context, id int64) ([]byte, error) {
	return getMessageRawShared(ctx, e.sqliteDB, "", id)
}
```

Note: DuckDB engine uses `e.sqliteDB` for direct SQLite queries (not DuckDB) — check which field holds the SQLite connection and use that.

- [ ] **Step 6: Add to MockEngine**

In `internal/query/querytest/mock_engine.go`, add:

```go
// Add field:
RawMessages map[int64][]byte
GetMessageRawFunc func(context.Context, int64) ([]byte, error)

// Add method:
func (m *MockEngine) GetMessageRaw(ctx context.Context, id int64) ([]byte, error) {
	if m.GetMessageRawFunc != nil {
		return m.GetMessageRawFunc(ctx, id)
	}
	if m.RawMessages != nil {
		if raw, ok := m.RawMessages[id]; ok {
			return raw, nil
		}
	}
	return nil, nil
}
```

- [ ] **Step 7: Run test to verify it passes**

Run: `flox activate -c 'go test ./internal/query/ -run TestGetMessageRaw -v'`
Expected: PASS

- [ ] **Step 8: Run full test suite**

Run: `flox activate -c 'go test ./internal/query/... -v -count=1'`
Expected: All tests pass (including compile check that MockEngine still satisfies Engine interface).

- [ ] **Step 9: Format and commit**

```bash
flox activate -c 'go fmt ./... && go vet ./...'
git add internal/query/engine.go internal/query/shared.go internal/query/sqlite.go internal/query/duckdb.go internal/query/querytest/mock_engine.go internal/query/sqlite_crud_test.go
git commit -m "feat: add GetMessageRaw to query.Engine interface"
```

---

### Task 2: HTML Rewriting Engine

The core rendering logic — parse HTML, rewrite CID references, strip scripts, block external images, and handle plain-text-to-HTML conversion. This is a pure function with no HTTP or database dependencies, so it's easy to test in isolation.

**Files:**
- Create: `internal/web/html_render.go`
- Create: `internal/web/html_render_test.go`

- [ ] **Step 1: Write tests for HTML rewriting**

Create `internal/web/html_render_test.go`:

```go
package web

import (
	"strings"
	"testing"
)

func TestRewriteHTML_StripScripts(t *testing.T) {
	input := `<html><body><script>alert('xss')</script><p>Hello</p></body></html>`
	got := rewriteEmailHTML(input, 1, nil)
	if strings.Contains(got, "<script") {
		t.Error("script tag not stripped")
	}
	if !strings.Contains(got, "<p>Hello</p>") {
		t.Error("body content missing")
	}
}

func TestRewriteHTML_RewriteCID(t *testing.T) {
	input := `<html><body><img src="cid:abc123"></body></html>`
	got := rewriteEmailHTML(input, 42, nil)
	if !strings.Contains(got, `/messages/42/inline/abc123`) {
		t.Errorf("CID not rewritten, got: %s", got)
	}
}

func TestRewriteHTML_BlockExternalImages(t *testing.T) {
	input := `<html><body><img src="https://tracker.example.com/pixel.gif"></body></html>`
	got := rewriteEmailHTML(input, 1, nil)
	if strings.Contains(got, `src="https://tracker.example.com`) {
		t.Error("external image not blocked")
	}
	if !strings.Contains(got, `data-original-src="https://tracker.example.com/pixel.gif"`) {
		t.Error("original src not preserved in data attribute")
	}
}

func TestRewriteHTML_ExternalImageBanner(t *testing.T) {
	input := `<html><body><img src="https://example.com/img.png"></body></html>`
	got := rewriteEmailHTML(input, 1, nil)
	if !strings.Contains(got, "ext-img-banner") {
		t.Error("external image banner not injected")
	}
}

func TestRewriteHTML_NoBannerWithoutExternalImages(t *testing.T) {
	input := `<html><body><p>No images here</p></body></html>`
	got := rewriteEmailHTML(input, 1, nil)
	if strings.Contains(got, "ext-img-banner") {
		t.Error("banner injected when no external images present")
	}
}

func TestRewriteHTML_LinkSafety(t *testing.T) {
	input := `<html><body><a href="https://example.com">link</a></body></html>`
	got := rewriteEmailHTML(input, 1, nil)
	if !strings.Contains(got, `target="_blank"`) {
		t.Error("missing target=_blank on link")
	}
	if !strings.Contains(got, `rel="noopener noreferrer"`) {
		t.Error("missing rel=noopener noreferrer on link")
	}
}

func TestPlainTextToHTML(t *testing.T) {
	input := "Hello world\n\nCheck https://example.com for details"
	got := plainTextToHTML(input)
	if !strings.Contains(got, `<a href="https://example.com"`) {
		t.Error("URL not linkified")
	}
	if !strings.Contains(got, `target="_blank"`) {
		t.Error("linkified URL missing target=_blank")
	}
	if !strings.Contains(got, "Hello world") {
		t.Error("text content missing")
	}
}

func TestPlainTextToHTML_EscapesHTML(t *testing.T) {
	input := `<script>alert('xss')</script> & "quotes"`
	got := plainTextToHTML(input)
	if strings.Contains(got, "<script>") {
		t.Error("HTML not escaped in plain text conversion")
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Error("angle brackets not escaped")
	}
}

func TestRenderNoContent(t *testing.T) {
	got := renderEmailHTML(1, "", "", nil)
	if !strings.Contains(got, "No message content") {
		t.Error("missing no-content message")
	}
}

func TestRenderHTMLBody(t *testing.T) {
	html := `<html><body><b>Bold</b></body></html>`
	got := renderEmailHTML(1, "", html, nil)
	if !strings.Contains(got, "<b>Bold</b>") {
		t.Error("HTML body not rendered")
	}
}

func TestRenderPlainTextFallback(t *testing.T) {
	got := renderEmailHTML(1, "Plain text body", "", nil)
	if !strings.Contains(got, "Plain text body") {
		t.Error("plain text body not rendered")
	}
	if !strings.Contains(got, "<pre") {
		t.Error("plain text not wrapped in pre tag")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `flox activate -c 'go test ./internal/web/ -run TestRewrite -v'`
Expected: Compile error — functions not defined.

- [ ] **Step 3: Implement `html_render.go`**

Create `internal/web/html_render.go`:

```go
package web

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"

	"golang.org/x/net/html/atom"
	nethtml "golang.org/x/net/html"
)

var urlPattern = regexp.MustCompile(`https?://[^\s<>"'\)\]]+`)

const transparentPixel = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"

const iframeCSS = `body {
  margin: 0;
  padding: 16px;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  font-size: 14px;
  line-height: 1.5;
  color: #333;
  background: #fff;
  word-wrap: break-word;
  overflow-wrap: break-word;
}
img { max-width: 100%; height: auto; }
a { color: #268bd2; }
pre { white-space: pre-wrap; word-wrap: break-word; font-family: inherit; }
table { border-collapse: collapse; max-width: 100%; }
td, th { padding: 4px 8px; }
`

const extImageBannerCSS = `#ext-img-banner {
  background: #fdf6e3;
  border-bottom: 1px solid #eee8d5;
  padding: 8px 16px;
  margin: -16px -16px 16px -16px;
  font-size: 13px;
  color: #657b83;
}
#ext-img-banner button {
  margin-left: 8px;
  cursor: pointer;
  background: none;
  border: 1px solid #93a1a1;
  border-radius: 3px;
  padding: 2px 8px;
  color: #268bd2;
}
`

const extImageBannerHTML = `<div id="ext-img-banner">External images are blocked. <button onclick="loadExternalImages()">Load external images</button></div>`

const extImageScript = `<script>
function loadExternalImages() {
  document.querySelectorAll('img[data-original-src]').forEach(function(img) {
    img.src = img.dataset.originalSrc;
  });
  document.getElementById('ext-img-banner').remove();
}
</script>`

const resizeScript = `<script>
new ResizeObserver(function() {
  window.parent.postMessage({ type: 'resize', height: document.body.scrollHeight }, '*');
}).observe(document.body);
</script>`

// renderEmailHTML produces a self-contained HTML document for iframe embedding.
// It chooses between HTML body rendering and plain-text fallback.
// cidParts maps Content-ID to MIME content type (used only to confirm CID exists).
func renderEmailHTML(messageID int64, bodyText, bodyHTML string, cidParts map[string]string) string {
	var body string
	hasExternalImages := false

	if bodyHTML != "" {
		body, hasExternalImages = rewriteEmailHTMLInner(bodyHTML, messageID)
	} else if bodyText != "" {
		body = plainTextToHTML(bodyText)
	} else {
		body = `<p style="color: #999; font-style: italic;">(No message content)</p>`
	}

	return wrapHTMLDocument(body, hasExternalImages)
}

// rewriteEmailHTML is the public entry point for testing.
// cidParts is unused currently but reserved for future validation.
func rewriteEmailHTML(htmlContent string, messageID int64, cidParts map[string]string) string {
	body, _ := rewriteEmailHTMLInner(htmlContent, messageID)
	return wrapHTMLDocument(body, false)
}

// rewriteEmailHTMLInner rewrites HTML content: strips scripts, rewrites CID
// references, blocks external images, and adds safety attributes to links.
// Returns the rewritten body HTML and whether external images were found.
func rewriteEmailHTMLInner(htmlContent string, messageID int64) (string, bool) {
	doc, err := nethtml.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return html.EscapeString(htmlContent), false
	}

	hasExternalImages := false
	var walk func(*nethtml.Node)
	walk = func(n *nethtml.Node) {
		if n.Type == nethtml.ElementNode && n.DataAtom == atom.Script {
			n.Parent.RemoveChild(n)
			return
		}

		if n.Type == nethtml.ElementNode && n.DataAtom == atom.Img {
			for i, attr := range n.Attr {
				if attr.Key == "src" {
					if strings.HasPrefix(attr.Val, "cid:") {
						cid := strings.TrimPrefix(attr.Val, "cid:")
						n.Attr[i].Val = fmt.Sprintf("/messages/%d/inline/%s", messageID, cid)
					} else if strings.HasPrefix(attr.Val, "http://") || strings.HasPrefix(attr.Val, "https://") {
						hasExternalImages = true
						n.Attr = append(n.Attr, nethtml.Attribute{Key: "data-original-src", Val: attr.Val})
						n.Attr[i].Val = transparentPixel
					}
				}
			}
		}

		if n.Type == nethtml.ElementNode && n.DataAtom == atom.A {
			hasTarget := false
			hasRel := false
			for i, attr := range n.Attr {
				if attr.Key == "target" {
					n.Attr[i].Val = "_blank"
					hasTarget = true
				}
				if attr.Key == "rel" {
					n.Attr[i].Val = "noopener noreferrer"
					hasRel = true
				}
			}
			if !hasTarget {
				n.Attr = append(n.Attr, nethtml.Attribute{Key: "target", Val: "_blank"})
			}
			if !hasRel {
				n.Attr = append(n.Attr, nethtml.Attribute{Key: "rel", Val: "noopener noreferrer"})
			}
		}

		// Walk children — collect first since RemoveChild modifies sibling pointers
		var children []*nethtml.Node
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			children = append(children, c)
		}
		for _, c := range children {
			walk(c)
		}
	}
	walk(doc)

	var buf bytes.Buffer
	if err := nethtml.Render(&buf, doc); err != nil {
		return html.EscapeString(htmlContent), false
	}

	// Extract just the body content from the full document render.
	// The parser wraps fragments in <html><head></head><body>...</body></html>
	rendered := buf.String()
	return extractBodyContent(rendered), hasExternalImages
}

// extractBodyContent pulls content between <body> and </body> tags.
// If not found, returns the full content.
func extractBodyContent(htmlDoc string) string {
	lower := strings.ToLower(htmlDoc)
	start := strings.Index(lower, "<body>")
	end := strings.LastIndex(lower, "</body>")
	if start >= 0 && end > start {
		return htmlDoc[start+6 : end]
	}
	bodyTag := strings.Index(lower, "<body")
	if bodyTag >= 0 {
		closeAngle := strings.Index(lower[bodyTag:], ">")
		if closeAngle >= 0 && end > bodyTag+closeAngle {
			return htmlDoc[bodyTag+closeAngle+1 : end]
		}
	}
	return htmlDoc
}

// plainTextToHTML converts plain text to minimal HTML with linkified URLs.
func plainTextToHTML(text string) string {
	escaped := html.EscapeString(text)
	linkified := urlPattern.ReplaceAllStringFunc(escaped, func(u string) string {
		return fmt.Sprintf(`<a href="%s" target="_blank" rel="noopener noreferrer">%s</a>`, u, u)
	})
	return fmt.Sprintf(`<pre style="white-space: pre-wrap; word-wrap: break-word;">%s</pre>`, linkified)
}

// wrapHTMLDocument wraps body content in a self-contained HTML document
// with the iframe stylesheet, optional external image banner, and resize script.
func wrapHTMLDocument(body string, hasExternalImages bool) string {
	var buf strings.Builder
	buf.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><style>")
	buf.WriteString(iframeCSS)
	if hasExternalImages {
		buf.WriteString(extImageBannerCSS)
	}
	buf.WriteString("</style></head><body>")
	if hasExternalImages {
		buf.WriteString(extImageBannerHTML)
		buf.WriteString(extImageScript)
	}
	buf.WriteString(body)
	buf.WriteString(resizeScript)
	buf.WriteString("</body></html>")
	return buf.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `flox activate -c 'go test ./internal/web/ -run "TestRewrite|TestPlainText|TestRender" -v'`
Expected: All PASS

- [ ] **Step 5: Format and commit**

```bash
flox activate -c 'go fmt ./... && go vet ./...'
git add internal/web/html_render.go internal/web/html_render_test.go
git commit -m "feat: add HTML rewriting engine for email rendering"
```

---

### Task 3: HTTP Handlers for HTML and Inline CID Endpoints

Wire up the two new endpoints that serve rendered HTML and inline MIME parts.

**Files:**
- Modify: `internal/web/handlers.go`
- Modify: `internal/web/server.go`

- [ ] **Step 1: Write the `handleMessageHTML` handler**

Add to `internal/web/handlers.go`:

```go
func (h *Handler) handleMessageHTML(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid message ID", http.StatusBadRequest)
		return
	}

	msg, err := h.engine.GetMessage(ctx, id)
	if err != nil {
		slog.Error("failed to get message for HTML render", "error", err, "id", id)
		http.Error(w, "Failed to load message", http.StatusInternalServerError)
		return
	}
	if msg == nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	// Build CID map by parsing raw MIME if HTML contains cid: references
	var cidParts map[string]string
	if msg.BodyHTML != "" && strings.Contains(msg.BodyHTML, "cid:") {
		raw, rawErr := h.engine.GetMessageRaw(ctx, id)
		if rawErr != nil {
			slog.Warn("failed to get raw MIME for CID resolution", "error", rawErr, "id", id)
		} else if raw != nil {
			parsed, parseErr := mime.Parse(raw)
			if parseErr != nil {
				slog.Warn("failed to parse MIME for CID resolution", "error", parseErr, "id", id)
			} else {
				cidParts = make(map[string]string)
				for _, att := range parsed.Attachments {
					if att.ContentID != "" {
						cidParts[att.ContentID] = att.ContentType
					}
				}
			}
		}
	}

	rendered := renderEmailHTML(id, msg.BodyText, msg.BodyHTML, cidParts)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(rendered))
}
```

Note: Add `mime "github.com/wesm/msgvault/internal/mime"` to the imports (aliased since `net/http` is already imported, and the package name collides with the stdlib `mime` package). Check what alias the codebase already uses for this import — it may be `mimeparser` or similar.

- [ ] **Step 2: Write the `handleMessageInline` handler**

Add to `internal/web/handlers.go`:

```go
func (h *Handler) handleMessageInline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid message ID", http.StatusBadRequest)
		return
	}

	cidParam := chi.URLParam(r, "cid")
	if cidParam == "" {
		http.Error(w, "Missing content ID", http.StatusBadRequest)
		return
	}

	raw, err := h.engine.GetMessageRaw(ctx, id)
	if err != nil {
		slog.Error("failed to get raw MIME for inline part", "error", err, "id", id)
		http.Error(w, "Failed to load message", http.StatusInternalServerError)
		return
	}
	if raw == nil {
		http.Error(w, "Message raw data not found", http.StatusNotFound)
		return
	}

	parsed, err := mime.Parse(raw)
	if err != nil {
		slog.Error("failed to parse MIME for inline part", "error", err, "id", id)
		http.Error(w, "Failed to parse message", http.StatusInternalServerError)
		return
	}

	for _, att := range parsed.Attachments {
		if att.ContentID == cidParam {
			contentType := att.ContentType
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = w.Write(att.Content)
			return
		}
	}

	http.Error(w, "Inline part not found", http.StatusNotFound)
}
```

- [ ] **Step 3: Register the routes**

In `internal/web/server.go`, add to the `Routes()` function after the existing `r.Get("/messages/{id}", h.handleMessageDetail)` line:

```go
r.Get("/messages/{id}/html", h.handleMessageHTML)
r.Get("/messages/{id}/inline/{cid}", h.handleMessageInline)
```

- [ ] **Step 4: Verify it compiles**

Run: `flox activate -c 'go build ./...'`
Expected: Compiles without error.

- [ ] **Step 5: Format and commit**

```bash
flox activate -c 'go fmt ./... && go vet ./...'
git add internal/web/handlers.go internal/web/server.go
git commit -m "feat: add /messages/{id}/html and /messages/{id}/inline/{cid} endpoints"
```

---

### Task 4: Update Message Detail Template to Use Iframe

Replace the `<pre>` body rendering with a sandboxed iframe.

**Files:**
- Modify: `internal/web/templates/message_detail.templ`

- [ ] **Step 1: Update `messageBody` in the templ file**

In `internal/web/templates/message_detail.templ`, replace the `messageBody` templ function (lines 134-144):

Old:
```
templ messageBody(msg *query.MessageDetail) {
	<div class="card">
		if msg.BodyText != "" {
			<pre class="msg-body">{ msg.BodyText }</pre>
		} else if msg.BodyHTML != "" {
			<pre class="msg-body">{ htmlToPlainText(msg.BodyHTML) }</pre>
		} else {
			<p style="color: var(--fg-muted); font-style: italic;">(No message content)</p>
		}
	</div>
}
```

New:
```
templ messageBody(msg *query.MessageDetail) {
	<div class="card">
		<iframe
			sandbox="allow-same-origin allow-scripts"
			src={ fmt.Sprintf("/messages/%d/html", msg.ID) }
			class="msg-body-frame"
			frameborder="0"
			scrolling="no"
			style="width: 100%; border: none; min-height: 200px;"
		></iframe>
	</div>
}
```

- [ ] **Step 2: Regenerate the templ Go code**

Run: `flox activate -c 'templ generate ./internal/web/templates/'`

If `templ` is not available, check the Makefile for a generate target. If the generated `_templ.go` file exists alongside the `.templ` file, it needs to be regenerated after template changes.

- [ ] **Step 3: Verify it compiles**

Run: `flox activate -c 'go build ./...'`
Expected: Compiles without error.

- [ ] **Step 4: Commit**

```bash
git add internal/web/templates/message_detail.templ internal/web/templates/message_detail_templ.go
git commit -m "feat: replace pre body with sandboxed iframe in message detail"
```

---

### Task 5: Add Iframe Resize Listener to keys.js

The iframe needs to communicate its content height to the parent page so it auto-sizes.

**Files:**
- Modify: `internal/web/static/keys.js`

- [ ] **Step 1: Add the resize listener**

At the bottom of `internal/web/static/keys.js`, just before the closing `})();`, add:

```js
  // Iframe auto-resize for message HTML rendering
  window.addEventListener('message', function (e) {
    if (e.data && e.data.type === 'resize') {
      var frame = document.querySelector('.msg-body-frame');
      if (frame) frame.style.height = e.data.height + 'px';
    }
  });
```

- [ ] **Step 2: Commit**

```bash
git add internal/web/static/keys.js
git commit -m "feat: add iframe auto-resize listener for email HTML rendering"
```

---

### Task 6: Integration Testing and Polish

Test the full flow end-to-end with real email data.

**Files:**
- No new files — testing existing code

- [ ] **Step 1: Build and run the server**

```bash
flox activate -c 'make build'
./msgvault serve
```

- [ ] **Step 2: Test HTML email rendering**

Open the web UI in a browser. Navigate to a message that has an HTML body. Verify:
- HTML renders with formatting (bold, tables, colors, etc.)
- No `<script>` content visible or executing
- External images show as blank with "External images are blocked" banner
- Clicking "Load external images" loads them
- Links open in new tabs

- [ ] **Step 3: Test CID inline images**

Find a message with inline images (embedded via CID). Verify:
- Images display inline in the email body
- Images load from the `/messages/{id}/inline/{cid}` endpoint
- Browser caches them (check network tab — second load should be 304 or cached)

- [ ] **Step 4: Test plain text fallback**

Navigate to a plain-text-only message. Verify:
- Text displays with preserved whitespace
- URLs are clickable links
- No HTML injection possible (test with a message containing `<script>` in subject/body)

- [ ] **Step 5: Test empty message**

If possible, find a message with no body content. Verify:
- "(No message content)" message displays

- [ ] **Step 6: Test iframe resizing**

Verify the iframe auto-sizes to fit content:
- Short emails don't have excessive whitespace below
- Long emails expand the iframe (no inner scrollbar)
- Resizing the browser window re-adjusts

- [ ] **Step 7: Run full test suite**

```bash
flox activate -c 'make test'
flox activate -c 'make lint'
```
Expected: All tests and linting pass.

- [ ] **Step 8: Final commit if any polish needed**

If any fixes were needed during integration testing, commit them:

```bash
flox activate -c 'go fmt ./... && go vet ./...'
git add -A
git commit -m "fix: polish email HTML rendering from integration testing"
```
