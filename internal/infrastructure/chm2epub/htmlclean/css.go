package htmlclean

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var (
	cssURLRe    = regexp.MustCompile(`(?i)url\(\s*(['"]?)([^'")]+)(['"]?)\s*\)`)
	cssImportRe = regexp.MustCompile(`(?i)@import\s+(['"])([^'"]+)(['"])`)
)

// RewriteCSS 把 CSS 中的根绝对路径改写成相对 dir 的路径。
// dir 是 CSS 文件所在目录（相对 OEBPS），内联样式传当前页面目录。
func RewriteCSS(css []byte, dir string) []byte {
	s := string(css)
	s = cssURLRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := cssURLRe.FindStringSubmatch(m)
		if len(sub) < 4 {
			return m
		}
		u := strings.TrimSpace(sub[2])
		if !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") {
			return m
		}
		return "url(" + sub[1] + relativeTo(dir, strings.TrimPrefix(u, "/")) + sub[3] + ")"
	})
	s = cssImportRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := cssImportRe.FindStringSubmatch(m)
		if len(sub) < 4 {
			return m
		}
		u := strings.TrimSpace(sub[2])
		if !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") {
			return m
		}
		return "@import " + sub[1] + relativeTo(dir, strings.TrimPrefix(u, "/")) + sub[3]
	})
	return []byte(s)
}

// CSSRefs 返回 CSS 中通过 url() 与 @import 引用的目标。
func CSSRefs(css []byte) []string {
	var out []string
	s := string(css)
	for _, m := range cssURLRe.FindAllStringSubmatch(s, -1) {
		if len(m) >= 3 {
			out = append(out, strings.TrimSpace(m[2]))
		}
	}
	for _, m := range cssImportRe.FindAllStringSubmatch(s, -1) {
		if len(m) >= 3 {
			out = append(out, strings.TrimSpace(m[2]))
		}
	}
	return out
}

// SetTitle 确保文档有非空 <title>，缺省时使用 fallback。
func SetTitle(doc *html.Node, fallback string) {
	head := findElement(doc, "head")
	if head == nil {
		return
	}
	title := findElement(head, "title")
	if title == nil {
		title = &html.Node{Type: html.ElementNode, Data: "title"}
		head.AppendChild(title)
	}
	if strings.TrimSpace(textContent(title)) != "" {
		return
	}
	if fallback == "" {
		fallback = "Untitled"
	}
	title.AppendChild(&html.Node{Type: html.TextNode, Data: fallback})
}

func textContent(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return b.String()
}
