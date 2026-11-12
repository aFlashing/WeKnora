package htmlclean

import (
	"path"
	"strings"

	"golang.org/x/net/html"
)

// Options 控制净化时依赖的外部信息。
type Options struct {
	// Exists 判断相对路径（以 HTML 所在目录为基准）的文件是否存在于 CHM 中。
	Exists func(p string) bool
	// Resolve 修正大小写等不匹配，返回真实路径。
	Resolve func(p string) (string, bool)
	// FileDir 是当前 HTML 所在目录（相对 CHM 根，可为空）。
	FileDir string
}

// Clean 按 calibre 的既有语义净化 HTML 树：
// 去脚本、修 body-in-head、删页首页尾导航表、修坏图、拆单表格壳。
func Clean(doc *html.Node, opt Options) {
	removeScripts(doc)
	removeEventAttrs(doc)
	removeBase(doc)
	fixBodyInHead(doc)
	removeNavTables(doc)
	removeLeadingBR(doc)
	fixImageSources(doc, opt)
	unwrapSingleCellTable(doc)
	fixRootRelative(doc, opt.FileDir)
	fixLinks(doc, opt)
	stripBrokenLinks(doc, opt)
	ensureHead(doc)
}

// fixRootRelative 把 "/x/y" 这类以站点根为基准的引用改为相对当前页面的路径。
// CHM 内部没有站点根的概念，这类引用在 EPUB 中会指向容器根之外。
func fixRootRelative(doc *html.Node, fileDir string) {
	rewrite := func(v string) (string, bool) {
		if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") {
			return v, false
		}
		target := strings.TrimPrefix(v, "/")
		return relativeTo(fileDir, target), true
	}

	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		switch n.Data {
		case "a", "area", "link":
			if v := attr(n, "href"); v != "" {
				if nv, ok := rewrite(v); ok {
					setAttr(n, "href", nv)
				}
			}
			if n.Data == "link" {
				// CHM 编译器常给样式表加 disabled，阅读器会因此忽略样式
				removeAttr(n, "disabled")
			}
		case "img", "source", "video", "audio", "iframe", "embed":
			if v := attr(n, "src"); v != "" {
				if nv, ok := rewrite(v); ok {
					setAttr(n, "src", nv)
				}
			}
		case "style":
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.TextNode {
					c.Data = string(RewriteCSS([]byte(c.Data), fileDir))
				}
			}
		}
	})
}

func walk(n *html.Node, fn func(*html.Node)) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		fn(c)
		walk(c, fn)
		c = next
	}
}

func removeNode(n *html.Node) {
	if n.Parent != nil {
		n.Parent.RemoveChild(n)
	}
}

func removeScripts(doc *html.Node) {
	var doomed []*html.Node
	walk(doc, func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "applet") {
			doomed = append(doomed, n)
		}
	})
	for _, n := range doomed {
		removeNode(n)
	}
}

func removeEventAttrs(doc *html.Node) {
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		kept := n.Attr[:0]
		for _, a := range n.Attr {
			name := strings.ToLower(a.Key)
			if strings.HasPrefix(name, "on") {
				continue
			}
			val := strings.TrimSpace(strings.ToLower(a.Val))
			if (name == "href" || name == "src" || name == "action") &&
				(strings.HasPrefix(val, "javascript:") || strings.HasPrefix(val, "vbscript:")) {
				continue
			}
			kept = append(kept, a)
		}
		n.Attr = kept
	})
}

func removeBase(doc *html.Node) {
	var doomed []*html.Node
	walk(doc, func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "base" {
			doomed = append(doomed, n)
		}
	})
	for _, n := range doomed {
		removeNode(n)
	}
}

// fixBodyInHead 把误嵌在 <head> 里的 <body> 提到 <html> 下。
func fixBodyInHead(doc *html.Node) {
	var body *html.Node
	walk(doc, func(n *html.Node) {
		if body == nil && n.Type == html.ElementNode && n.Data == "body" &&
			n.Parent != nil && n.Parent.Data == "head" {
			body = n
		}
	})
	if body == nil {
		return
	}
	htmlNode := findElement(doc, "html")
	if htmlNode == nil {
		htmlNode = doc
	}
	removeNode(body)
	htmlNode.AppendChild(body)
}

func findElement(n *html.Node, name string) *html.Node {
	if n.Type == html.ElementNode && n.Data == name {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if r := findElement(c, name); r != nil {
			return r
		}
	}
	return nil
}

func findBody(doc *html.Node) *html.Node { return findElement(doc, "body") }

// removeNavTables 删除带 prev/next/team 图标的页首与页尾导航表格。
func removeNavTables(doc *html.Node) {
	var tables []*html.Node
	walk(doc, func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "table" {
			tables = append(tables, n)
		}
	})
	if len(tables) == 0 {
		return
	}
	first, last := tables[0], tables[len(tables)-1]
	if isEdgeElement(first) && navAlt(first) {
		removeNode(first)
	}
	if last != first && isEdgeElement(last) && navAlt(last) {
		removeNode(last)
	}
}

func navAlt(table *html.Node) bool {
	var img *html.Node
	walk(table, func(n *html.Node) {
		if img == nil && n.Type == html.ElementNode && n.Data == "img" {
			img = n
		}
	})
	if img == nil {
		return false
	}
	alt := strings.ToLower(attr(img, "alt"))
	return strings.Contains(alt, "prev") || strings.Contains(alt, "next") || strings.Contains(alt, "team")
}

// isEdgeElement 判断节点是否是其父节点下第一个/最后一个元素子节点（忽略空白文本）。
func isEdgeElement(n *html.Node) bool {
	if n.Parent == nil {
		return false
	}
	for c := n.Parent.FirstChild; c != nil; c = c.NextSibling {
		if c == n {
			return true
		}
		if isIgnorable(c) {
			continue
		}
		return false
	}
	return false
}

func isLastElement(n *html.Node) bool {
	if n.Parent == nil {
		return false
	}
	for c := n.Parent.LastChild; c != nil; c = c.PrevSibling {
		if c == n {
			return true
		}
		if isIgnorable(c) {
			continue
		}
		return false
	}
	return false
}

func isIgnorable(n *html.Node) bool {
	return n.Type == html.CommentNode || (n.Type == html.TextNode && strings.TrimSpace(n.Data) == "")
}

// removeLeadingBR 删除页首孤立换行。
func removeLeadingBR(doc *html.Node) {
	body := findBody(doc)
	if body == nil {
		return
	}
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if isIgnorable(c) {
			continue
		}
		if c.Type == html.ElementNode && c.Data == "br" {
			removeNode(c)
		}
		return
	}
}

// fixImageSources 修复指向不存在文件的图片路径：先剥 ";junk"，再逐层剥前导 "../"。
func fixImageSources(doc *html.Node, opt Options) {
	if opt.Exists == nil {
		return
	}
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "img" {
			return
		}
		src := attr(n, "src")
		if src == "" {
			return
		}
		if opt.Exists(joinRel(opt.FileDir, src)) {
			return
		}
		fixed := stripSemi(src)
		if fixed != src && opt.Exists(joinRel(opt.FileDir, fixed)) {
			setAttr(n, "src", fixed)
			return
		}
		for strings.HasPrefix(fixed, "../") {
			fixed = fixed[3:]
			if opt.Exists(joinRel(opt.FileDir, fixed)) {
				setAttr(n, "src", fixed)
				return
			}
		}
		if fixed != src {
			setAttr(n, "src", fixed)
		}
	})
}

// unwrapSingleCellTable 把 body > table > tr > td 的单层壳拆掉。
func unwrapSingleCellTable(doc *html.Node) {
	body := findBody(doc)
	if body == nil {
		return
	}
	var table *html.Node
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if isIgnorable(c) {
			continue
		}
		if c.Type == html.ElementNode && c.Data == "table" {
			if table != nil {
				return
			}
			table = c
			continue
		}
		return
	}
	if table == nil {
		return
	}
	tr := onlyElementChild(table, "tr")
	if tr == nil {
		if tbody := onlyElementChild(table, "tbody"); tbody != nil {
			tr = onlyElementChild(tbody, "tr")
		}
	}
	if tr == nil {
		return
	}
	td := onlyElementChild(tr, "td")
	if td == nil {
		return
	}

	parent := table.Parent
	for c := td.FirstChild; c != nil; {
		next := c.NextSibling
		td.RemoveChild(c)
		parent.InsertBefore(c, table)
		c = next
	}
	removeNode(table)
}

func onlyElementChild(n *html.Node, name string) *html.Node {
	var found *html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if isIgnorable(c) {
			continue
		}
		if c.Type != html.ElementNode || c.Data != name {
			return nil
		}
		if found != nil {
			return nil
		}
		found = c
	}
	return found
}

// fixLinks 修正本地链接的大小写，跳过外链与锚点。
func fixLinks(doc *html.Node, opt Options) {
	if opt.Resolve == nil {
		return
	}
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		switch n.Data {
		case "a", "area", "link":
			href := attr(n, "href")
			if href == "" {
				return
			}
			if fixed, ok := resolveLocal(opt, href); ok && fixed != href {
				setAttr(n, "href", fixed)
			}
		case "img", "source", "video", "audio":
			src := attr(n, "src")
			if src == "" {
				return
			}
			if fixed, ok := resolveLocal(opt, src); ok && fixed != src {
				setAttr(n, "src", fixed)
			}
		}
	})
}

// stripBrokenLinks 把指向不存在文件的链接退化为纯文本，避免产物出现死链。
func stripBrokenLinks(doc *html.Node, opt Options) {
	if opt.Resolve == nil {
		return
	}
	var doomed []*html.Node
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "a" {
			return
		}
		href := attr(n, "href")
		if href == "" || isExternal(href) || strings.HasPrefix(href, "#") {
			return
		}
		if _, ok := resolveLocal(opt, href); !ok {
			doomed = append(doomed, n)
		}
	})
	for _, n := range doomed {
		unwrap(n)
	}
}

func unwrap(n *html.Node) {
	parent := n.Parent
	if parent == nil {
		return
	}
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		parent.InsertBefore(c, n)
		c = next
	}
	parent.RemoveChild(n)
}

func resolveLocal(opt Options, ref string) (string, bool) {
	if isExternal(ref) || strings.HasPrefix(ref, "#") {
		return ref, false
	}
	// "other.chm::/page.html" 形式：同包时可解析，跨包时保持原样
	if i := strings.Index(ref, "::"); i >= 0 {
		ref = strings.TrimPrefix(ref[i+2:], "/")
	}
	target, frag := splitFragment(ref)
	if target == "" {
		return ref, false
	}
	full := joinRel(opt.FileDir, target)
	if actual, ok := opt.Resolve(full); ok {
		rel := relativeTo(opt.FileDir, actual)
		if frag != "" {
			rel += "#" + frag
		}
		return rel, true
	}
	return ref, false
}

func isExternal(ref string) bool {
	lower := strings.ToLower(strings.TrimSpace(ref))
	for _, p := range []string{"http://", "https://", "ftp://", "mailto:", "tel:", "data:", "javascript:", "file:", "//"} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

func splitFragment(ref string) (string, string) {
	if i := strings.IndexByte(ref, '#'); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}

func stripSemi(s string) string {
	if i := strings.IndexByte(s, ';'); i >= 0 {
		return s[:i]
	}
	return s
}

// joinRel 以 dir 为基准拼接相对路径，并规范化。
func joinRel(dir, ref string) string {
	ref = strings.ReplaceAll(ref, "\\", "/")
	if strings.HasPrefix(ref, "/") {
		return path.Clean(strings.TrimPrefix(ref, "/"))
	}
	p := path.Clean(path.Join(dir, ref))
	return strings.TrimPrefix(p, "./")
}

func relativeTo(dir, target string) string {
	if dir == "" {
		return target
	}
	prefix := dir + "/"
	if strings.HasPrefix(target, prefix) {
		return target[len(prefix):]
	}
	return strings.Repeat("../", strings.Count(dir, "/")+1) + target
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func removeAttr(n *html.Node, key string) {
	kept := n.Attr[:0]
	for _, a := range n.Attr {
		if !strings.EqualFold(a.Key, key) {
			kept = append(kept, a)
		}
	}
	n.Attr = kept
}

func setAttr(n *html.Node, key, val string) {
	for i := range n.Attr {
		if strings.EqualFold(n.Attr[i].Key, key) {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

// ensureHead 保证存在 head/body 与 UTF-8 声明。
func ensureHead(doc *html.Node) {
	htmlNode := findElement(doc, "html")
	if htmlNode == nil {
		return
	}
	head := findElement(htmlNode, "head")
	if head == nil {
		head = &html.Node{Type: html.ElementNode, Data: "head"}
		htmlNode.InsertBefore(head, htmlNode.FirstChild)
	}

	// 删除旧的 charset 声明
	var doomed []*html.Node
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "meta" &&
			(attr(c, "charset") != "" || strings.Contains(strings.ToLower(attr(c, "http-equiv")), "content-type")) {
			doomed = append(doomed, c)
		}
	}
	for _, n := range doomed {
		removeNode(n)
	}

	meta := &html.Node{Type: html.ElementNode, Data: "meta",
		Attr: []html.Attribute{{Key: "charset", Val: "utf-8"}}}
	head.InsertBefore(meta, head.FirstChild)
}
