# Email HTML Rendering in Web UI

## Goal

Render emails in the web UI as they would appear in Gmail — full HTML formatting, inline images, and proper layout — instead of the current plain-text-only `<pre>` display.

## Constraints

- Zero new dependencies (use `enmime`, `golang.org/x/net/html`, stdlib only)
- Zero schema migrations — all CID resolution done at render time from `message_raw`
- DB-agnostic — no new columns, no backfills; supports future UI/backend split
- No headless browsers, no server-side rendering engines

## Architecture

### Two New Endpoints

**`GET /messages/{id}/html`** — serves a self-contained HTML document for iframe embedding.

1. Fetch `BodyHTML` (and `BodyText` fallback) from query engine
2. If HTML contains `cid:` references:
   - Decompress `message_raw`, parse with `enmime`
   - Build `CID → MIME part` map
   - Rewrite `<img src="cid:xyz">` → `<img src="/messages/{id}/inline/{cid}">`
3. Strip all `<script>` tags (defense-in-depth)
4. Replace external `<img>` src with `data-original-src`, set `src` to transparent placeholder
5. Inject "Load external images" banner at top (hidden when no external images)
6. Inject inline stylesheet for sensible defaults (max-width images, font stack, white background)
7. Inject `ResizeObserver` snippet at bottom for iframe auto-height
8. Return `Content-Type: text/html`

For plain-text-only messages (no `BodyHTML`):
- Wrap in `<pre style="white-space: pre-wrap; word-wrap: break-word;">`
- Linkify URLs: `http(s)://...` → `<a href="..." target="_blank" rel="noopener noreferrer">`
- Same wrapper (stylesheet, resize observer)

For messages with neither body:
- Render "(No message content)" styled message

**`GET /messages/{id}/inline/{cid}`** — serves inline MIME parts by Content-ID.

1. Decompress `message_raw`, parse with `enmime`
2. URL-decode `{cid}` parameter, find MIME part where `ContentID` matches
3. Serve with correct `Content-Type` from the MIME part
4. Set aggressive cache headers (`Cache-Control: public, max-age=31536000, immutable`) since content is immutable
5. 404 if CID not found

### Fallback Behavior

- `message_raw` missing or corrupt → render `BodyText` as plain-text HTML
- `BodyText` also empty → render "(No message content)"
- CID reference with no matching MIME part → browser shows broken image icon

### HTML Rewriting (golang.org/x/net/html)

Parse the HTML into a node tree using `golang.org/x/net/html`, walk it, and apply transformations:

- **Script removal:** Delete all `<script>` nodes
- **CID rewriting:** On `<img>` nodes, if `src` starts with `cid:`, replace with `/messages/{id}/inline/{cid}`
- **External image blocking:** On `<img>` nodes, if `src` starts with `http://` or `https://`, move `src` to `data-original-src` and set `src` to a 1x1 transparent data URI
- **Link safety:** Add `target="_blank"` and `rel="noopener noreferrer"` to all `<a>` tags

Re-serialize the modified tree back to HTML.

### URL Linkification (Plain Text)

Simple regex: match `https?://[^\s<>"')\]]+` patterns, wrap in anchor tags. Run after HTML-escaping the plain text body to avoid injection.

## Frontend Changes

### message_detail.templ

Replace `messageBody()`:

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

### keys.js — Iframe Resize Listener

```js
window.addEventListener('message', (e) => {
  if (e.data?.type === 'resize') {
    const frame = document.querySelector('.msg-body-frame');
    if (frame) frame.style.height = e.data.height + 'px';
  }
});
```

### "Load External Images" (Inside Iframe)

Injected at the top of the iframe HTML document:

```html
<div id="ext-img-banner" style="...">
  External images are blocked.
  <button onclick="loadExternalImages()">Load external images</button>
</div>
<script>
function loadExternalImages() {
  document.querySelectorAll('img[data-original-src]').forEach(img => {
    img.src = img.dataset.originalSrc;
  });
  document.getElementById('ext-img-banner').remove();
}
</script>
```

This script runs inside the sandboxed iframe. `sandbox="allow-same-origin"` permits inline scripts within the iframe's own document by default — but to be explicit, the sandbox attribute should be `sandbox="allow-same-origin allow-scripts"` for the external image toggle to work. Scripts are still isolated to the iframe and cannot access the parent page.

Note: the `<script>` stripping in the rewriting step targets scripts from the *email's* HTML. The injected banner script is appended *after* the rewriting step, so it is not stripped.

### Iframe Stylesheet (Injected)

```css
body {
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
pre { white-space: pre-wrap; word-wrap: break-word; }
table { border-collapse: collapse; max-width: 100%; }
td, th { padding: 4px 8px; }
#ext-img-banner {
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
```

## Security Model

| Threat | Mitigation |
|--------|------------|
| Script execution from email HTML | `<script>` tags stripped server-side; iframe sandbox isolates any missed scripts |
| DOM escape into parent page | `sandbox` attribute prevents access to parent DOM |
| Tracking pixels / external image loading | External images blocked by default; user opt-in per message |
| Phishing links | Links open in `_blank` with `noopener noreferrer`; URL visible in browser status bar |
| CSS injection affecting parent | Iframe provides complete style isolation |
| Malformed HTML crashing parser | `golang.org/x/net/html` is fault-tolerant; malformed input produces best-effort tree |

## Files to Create/Modify

| File | Action | Purpose |
|------|--------|---------|
| `internal/web/html_render.go` | Create | HTML rewriting: CID replacement, script stripping, external image blocking, plain-text-to-HTML, URL linkification |
| `internal/web/handlers.go` | Modify | Add `handleMessageHTML` and `handleMessageInline` handlers |
| `internal/web/server.go` | Modify | Add `GET /messages/{id}/html` and `GET /messages/{id}/inline/{cid}` routes |
| `internal/web/templates/message_detail.templ` | Modify | Replace `<pre>` body with sandboxed iframe |
| `internal/web/static/keys.js` | Modify | Add iframe resize listener |

## Not In Scope

- TUI changes
- Schema migrations
- Attachment preview/thumbnails
- Quote-level styling or signature detection
- Dark mode inside iframe (`prefers-color-scheme` can be added later)
- Caching of parsed MIME (browser cache headers handle repeat image requests)
