package web

import (
	"strings"
	"testing"
)

func TestRewriteHTML_StripScripts(t *testing.T) {
	input := `<html><body><script>alert('xss')</script><p>Hello</p></body></html>`
	got := rewriteEmailHTML(input, 1, nil)
	if strings.Contains(got, "alert('xss')") {
		t.Error("email script content not stripped")
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
	if !strings.Contains(got, `data-original-src="https://tracker.example.com/pixel.gif"`) {
		t.Error("original src not preserved in data attribute")
	}
	if !strings.Contains(got, transparentPixel) {
		t.Error("src not replaced with transparent pixel")
	}
}

func TestRewriteHTML_ExternalImageBanner(t *testing.T) {
	input := `<html><body><img src="https://example.com/img.png"></body></html>`
	got := renderEmailHTML(1, "", input, nil)
	if !strings.Contains(got, "ext-img-banner") {
		t.Error("external image banner not injected")
	}
}

func TestRewriteHTML_NoBannerWithoutExternalImages(t *testing.T) {
	input := `<html><body><p>No images here</p></body></html>`
	got := renderEmailHTML(1, "", input, nil)
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
