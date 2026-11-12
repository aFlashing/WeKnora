// Package toc 负责解析 CHM 的 .hhc 目录文件并生成阅读顺序。
package toc

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// Node 是目录树中的一个节点。
type Node struct {
	Title    string
	Href     string
	Children []*Node
}

// Parse 解析 .hhc 内容。文件缺失或格式异常时返回空树。
func Parse(data []byte) []*Node {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	ul := firstElement(doc, "ul")
	if ul == nil {
		return nil
	}
	return parseUL(ul)
}

func firstElement(n *html.Node, name string) *html.Node {
	if n.Type == html.ElementNode && n.Data == name {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if r := firstElement(c, name); r != nil {
			return r
		}
	}
	return nil
}

func parseUL(ul *html.Node) []*Node {
	var out []*Node
	for li := ul.FirstChild; li != nil; li = li.NextSibling {
		if li.Type != html.ElementNode || li.Data != "li" {
			continue
		}
		node := &Node{}
		for c := li.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.Data {
			case "object":
				readObject(c, node)
			case "ul":
				node.Children = parseUL(c)
			case "span", "div", "p":
				walkObjects(c, node)
			}
		}
		if node.Title != "" || node.Href != "" {
			out = append(out, node)
		}
	}
	return out
}

func walkObjects(n *html.Node, node *Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.Data {
		case "object":
			readObject(c, node)
		case "ul":
			node.Children = append(node.Children, parseUL(c)...)
		default:
			walkObjects(c, node)
		}
	}
}

func readObject(obj *html.Node, node *Node) {
	typ := strings.ToLower(attr(obj, "type"))
	if typ != "" && !strings.Contains(typ, "sitemap") {
		return
	}
	for p := obj.FirstChild; p != nil; p = p.NextSibling {
		if p.Type != html.ElementNode || p.Data != "param" {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(attr(p, "name")))
		value := strings.TrimSpace(attr(p, "value"))
		switch name {
		case "name":
			if value != "" {
				node.Title = value
			}
		case "local":
			if value != "" {
				node.Href = value
			}
		}
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// Flatten 按文档顺序展开为线性列表。
func Flatten(nodes []*Node) []*Node {
	var out []*Node
	var visit func([]*Node)
	visit = func(list []*Node) {
		for _, n := range list {
			out = append(out, n)
			visit(n.Children)
		}
	}
	visit(nodes)
	return out
}

// Count 返回节点总数。
func Count(nodes []*Node) int { return len(Flatten(nodes)) }

// Prune 去掉既无 href 也无子节点的节点。
func Prune(nodes []*Node) []*Node {
	var out []*Node
	for _, n := range nodes {
		n.Children = Prune(n.Children)
		if n.Href == "" && len(n.Children) == 0 {
			continue
		}
		out = append(out, n)
	}
	return out
}
