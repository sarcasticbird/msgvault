package web

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"

	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
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

func rewriteEmailHTML(htmlContent string, messageID int64, cidParts map[string]string) string {
	body, hasExternal := rewriteEmailHTMLInner(htmlContent, messageID)
	return wrapHTMLDocument(body, hasExternal)
}

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

	rendered := buf.String()
	return extractBodyContent(rendered), hasExternalImages
}

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

func plainTextToHTML(text string) string {
	escaped := html.EscapeString(text)
	linkified := urlPattern.ReplaceAllStringFunc(escaped, func(u string) string {
		return fmt.Sprintf(`<a href="%s" target="_blank" rel="noopener noreferrer">%s</a>`, u, u)
	})
	return fmt.Sprintf(`<pre style="white-space: pre-wrap; word-wrap: break-word;">%s</pre>`, linkified)
}

func wrapHTMLDocument(body string, hasExternalImages bool) string {
	var buf strings.Builder
	buf.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>`)
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
