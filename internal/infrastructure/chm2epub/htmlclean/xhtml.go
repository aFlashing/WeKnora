package htmlclean

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

var booleanAttrs = map[string]bool{
	"checked": true, "compact": true, "declare": true, "defer": true,
	"disabled": true, "ismap": true, "multiple": true, "nohref": true,
	"noresize": true, "noshade": true, "nowrap": true, "readonly": true,
	"selected": true, "autofocus": true, "required": true, "hidden": true,
}

// RenderXHTML 把 DOM 序列化为合法 XHTML（EPUB3 内容文档）。
func RenderXHTML(doc *html.Node) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	b.WriteString("<!DOCTYPE html>\n")

	root := findElement(doc, "html")
	if root == nil {
		root = doc
	}
	renderNode(&b, root, true)
	b.WriteByte('\n')
	return b.Bytes()
}

func renderNode(b *bytes.Buffer, n *html.Node, root bool) {
	switch n.Type {
	case html.DocumentNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			renderNode(b, c, root)
		}
	case html.ElementNode:
		name := strings.ToLower(n.Data)
		b.WriteByte('<')
		b.WriteString(name)
		if root {
			b.WriteString(` xmlns="http://www.w3.org/1999/xhtml"`)
		}
		for _, a := range n.Attr {
			key := strings.ToLower(a.Key)
			if strings.HasPrefix(key, "xmlns") {
				continue
			}
			val := a.Val
			if val == "" && booleanAttrs[key] {
				val = key
			}
			b.WriteByte(' ')
			b.WriteString(key)
			b.WriteString(`="`)
			escapeAttr(b, val)
			b.WriteByte('"')
		}
		if voidElements[name] {
			b.WriteString("/>")
			return
		}
		b.WriteByte('>')

		if name == "style" {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.TextNode {
					b.WriteString(c.Data)
				}
			}
			b.WriteString("</style>")
			return
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			renderNode(b, c, false)
		}
		b.WriteString("</")
		b.WriteString(name)
		b.WriteByte('>')
	case html.TextNode:
		escapeText(b, n.Data)
	case html.CommentNode:
		// 丢弃注释，避免 EPUB 校验器对非法 "--" 报错
	}
}

func escapeText(b *bytes.Buffer, s string) {
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
}

func escapeAttr(b *bytes.Buffer, s string) {
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\n':
			b.WriteString("&#10;")
		case '\r':
			b.WriteString("&#13;")
		case '\t':
			b.WriteString("&#9;")
		default:
			b.WriteRune(r)
		}
	}
}

// Links 返回文档中所有本地链接目标（a/area 的 href）。
func Links(doc *html.Node) []string {
	var out []string
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if n.Data == "a" || n.Data == "area" {
			if href := attr(n, "href"); href != "" {
				out = append(out, href)
			}
		}
	})
	return out
}

// Title 返回文档标题文本。
func Title(doc *html.Node) string {
	t := findElement(doc, "title")
	if t == nil {
		return ""
	}
	var b strings.Builder
	for c := t.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return strings.TrimSpace(b.String())
}

// ImageSrcs 返回文档中所有 img 的 src。
func ImageSrcs(doc *html.Node) []string {
	var out []string
	walk(doc, func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			if src := attr(n, "src"); src != "" {
				out = append(out, src)
			}
		}
	})
	return out
}
