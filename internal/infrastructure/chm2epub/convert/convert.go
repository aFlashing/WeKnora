// Package convert 串联 CHM 解析、HTML 净化与 EPUB 打包。
package convert

import (
	"crypto/rand"
	"fmt"
	stdhtml "html"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/net/html"

	"github.com/Tencent/WeKnora/internal/infrastructure/chm2epub/chm"
	"github.com/Tencent/WeKnora/internal/infrastructure/chm2epub/epub"
	"github.com/Tencent/WeKnora/internal/infrastructure/chm2epub/htmlclean"
	"github.com/Tencent/WeKnora/internal/infrastructure/chm2epub/logx"
	"github.com/Tencent/WeKnora/internal/infrastructure/chm2epub/meta"
	"github.com/Tencent/WeKnora/internal/infrastructure/chm2epub/toc"
)

// Options 是单本转换的可选项。
type Options struct {
	OutPath   string
	Overwrite bool
	KeepTemp  bool
	Validate  bool
	Log       *logx.Logger
}

type fileItem struct {
	entry *chm.Entry
	path  string
}

// One 转换单个 CHM，返回生成的 EPUB 路径。
func One(chmPath string, opt Options) (string, error) {
	log := opt.Log
	if log == nil {
		log = logx.Default(logx.LevelInfo)
	}
	outPath := opt.OutPath
	if outPath == "" {
		base := strings.TrimSuffix(chmPath, filepath.Ext(chmPath))
		outPath = base + ".epub"
	} else if st, err := os.Stat(outPath); err == nil && st.IsDir() {
		name := strings.TrimSuffix(filepath.Base(chmPath), filepath.Ext(chmPath)) + ".epub"
		outPath = filepath.Join(outPath, name)
	}
	if !opt.Overwrite {
		if _, err := os.Stat(outPath); err == nil {
			return "", fmt.Errorf("输出文件已存在: %s（使用 --overwrite 覆盖）", outPath)
		}
	}

	r, err := chm.Open(chmPath)
	if err != nil {
		return "", err
	}
	defer r.Close()

	si, sysErr := r.System()
	if sysErr != nil {
		log.Debugf("%s: 读取 #SYSTEM 失败: %v", filepath.Base(chmPath), sysErr)
		si = &chm.SystemInfo{LCID: r.LCID}
	}
	lcid := si.LCID
	if lcid == 0 {
		lcid = r.LCID
	}
	fallbackEnc := chm.CodepageForLCID(lcid)
	log.Debugf("%s: CHM v%d, LCID=%#x, 编码猜测=%s", filepath.Base(chmPath), r.Version, lcid, fallbackEnc)

	paths := make(map[string]string) // 小写路径 -> 真实路径
	var htmlItems, resItems []*fileItem

	for _, e := range r.Entries() {
		p, ok := sanitizePath(e.Path)
		if !ok {
			continue
		}
		key := strings.ToLower(p)
		if _, dup := paths[key]; dup {
			continue
		}
		paths[key] = p
		item := &fileItem{entry: e, path: p}
		if isHTMLPath(p) {
			htmlItems = append(htmlItems, item)
		} else if isResourcePath(p) {
			resItems = append(resItems, item)
		}
	}
	if len(htmlItems) == 0 {
		return "", fmt.Errorf("CHM 中没有 HTML 页面")
	}

	resolve := func(p string) (string, bool) {
		p = strings.ReplaceAll(p, "\\", "/")
		p = strings.TrimPrefix(path.Clean("/"+p), "/")
		if actual, ok := paths[strings.ToLower(p)]; ok {
			return actual, true
		}
		return "", false
	}
	exists := func(p string) bool { _, ok := resolve(p); return ok }

	// 解析全部 HTML
	docs := make(map[string]*html.Node, len(htmlItems))
	for _, it := range htmlItems {
		raw, err := r.ReadEntry(it.entry)
		if err != nil {
			log.Warnf("读取 %s 失败，跳过: %v", it.path, err)
			continue
		}
		text, enc := htmlclean.Decode(raw, fallbackEnc)
		doc, err := html.Parse(strings.NewReader(text))
		if err != nil {
			log.Warnf("解析 %s 失败，跳过: %v", it.path, err)
			continue
		}
		log.Debugf("%s: %d 字节, 编码 %s", it.path, len(raw), enc)
		docs[it.path] = doc
	}
	if len(docs) == 0 {
		return "", fmt.Errorf("CHM 中没有可解析的 HTML 页面")
	}

	htmlSet := make(map[string]bool, len(docs))
	for p := range docs {
		htmlSet[p] = true
	}

	// 定位首页
	home := ""
	if p, ok := resolve(si.DefaultTopic); ok && htmlSet[p] {
		home = p
	}

	// 解析目录
	tocNodes := loadTOC(r, si, home, fallbackEnc, resolve, log)
	if home == "" {
		for _, n := range toc.Flatten(tocNodes) {
			if p, ok := resolveLocal(n.Href, htmlSet, resolve); ok {
				home = p
				break
			}
		}
	}
	if home == "" {
		names := make([]string, 0, len(docs))
		for p := range docs {
			names = append(names, p)
		}
		sort.Strings(names)
		home = names[0]
	}
	log.Debugf("首页: %s，目录节点: %d", home, toc.Count(tocNodes))

	// 净化
	for p, doc := range docs {
		dir := path.Dir(p)
		if dir == "." {
			dir = ""
		}
		htmlclean.Clean(doc, htmlclean.Options{
			Exists:  func(ref string) bool { return exists(joinRel(dir, ref)) },
			Resolve: func(ref string) (string, bool) { return resolve(joinRel(dir, ref)) },
			FileDir: dir,
		})
	}

	// 阅读顺序
	order := buildOrder(tocNodes, home, docs, htmlSet, resolve)
	log.Debugf("正文页数: %d", len(order))

	// 元数据
	info := &meta.Info{}
	if doc := docs[home]; doc != nil {
		info = meta.Extract(doc, home, r)
	}
	title := strings.TrimSpace(si.Title)
	if title == "" || strings.EqualFold(title, "easy chm") || strings.EqualFold(title, "chm") {
		title = ""
		if doc := docs[home]; doc != nil {
			title = htmlclean.Title(doc)
		}
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(chmPath), filepath.Ext(chmPath))
	}
	title = cleanText(title)

	book := &epub.Book{
		Title:       title,
		Authors:     info.Authors,
		Language:    chm.LanguageForLCID(lcid),
		Publisher:   cleanText(info.Publisher),
		ISBN:        strings.TrimSpace(info.ISBN),
		Description: cleanText(info.Description),
		Source:      filepath.Base(chmPath),
		Identifier:  newUUID(),
	}

	tocTitles := make(map[string]string)
	for _, n := range toc.Flatten(tocNodes) {
		if p, ok := resolveLocal(n.Href, htmlSet, resolve); ok {
			if _, exists := tocTitles[p]; !exists && strings.TrimSpace(n.Title) != "" {
				tocTitles[p] = cleanText(n.Title)
			}
		}
	}

	for _, p := range order {
		doc := docs[p]
		if doc == nil {
			continue
		}
		title := tocTitles[p]
		if title == "" {
			title = cleanText(htmlclean.Title(doc))
		}
		if title == "" {
			title = book.Title
		}
		htmlclean.SetTitle(doc, title)
		data := htmlclean.RenderXHTML(doc)
		book.Chapters = append(book.Chapters, epub.Chapter{Path: p, Title: title, Data: data})
		htmlSet[p] = true
	}

	// 封面
	if info.CoverPath != "" && len(info.CoverData) > 0 {
		book.CoverPath = info.CoverPath
		book.CoverData = info.CoverData
	}

	// 只保留被正文或 CSS 引用到的静态资源，避免把 CHM 里用不到的文件一并打包
	referenced := collectReferences(docs, resolve, func(p string) []byte {
		data, err := r.ReadPath(p)
		if err != nil {
			return nil
		}
		return data
	})
	if book.CoverPath != "" {
		referenced[strings.ToLower(book.CoverPath)] = true
	}

	for _, it := range resItems {
		if !referenced[strings.ToLower(it.path)] {
			continue
		}
		data, err := r.ReadEntry(it.entry)
		if err != nil {
			log.Warnf("读取资源 %s 失败，跳过: %v", it.path, err)
			continue
		}
		if strings.EqualFold(path.Ext(it.path), ".css") {
			dir := path.Dir(it.path)
			if dir == "." {
				dir = ""
			}
			data = htmlclean.RewriteCSS(data, dir)
		}
		book.Resources = append(book.Resources, epub.Resource{Path: it.path, Data: data})
	}

	book.TOC = buildBookTOC(tocNodes, resolve, htmlSet)

	if err := epub.Write(outPath, book); err != nil {
		return "", err
	}
	if opt.Validate {
		problems := epub.Validate(outPath)
		if len(problems) == 0 {
			log.Debugf("自检通过")
		} else {
			log.Warnf("自检发现 %d 个问题（多为源文档本身的失效链接）", len(problems))
			for i, p := range problems {
				if i >= 5 {
					log.Debugf("……另有 %d 个问题", len(problems)-5)
					break
				}
				log.Warnf("自检: %s", p)
			}
		}
	}
	return outPath, nil
}

func loadTOC(r *chm.Reader, si *chm.SystemInfo, home, fallbackEnc string,
	resolve func(string) (string, bool), log *logx.Logger) []*toc.Node {

	var candidate string
	if si.HHC != "" {
		if p, ok := resolve(si.HHC); ok {
			candidate = p
		}
	}
	if candidate == "" {
		var all []string
		for _, e := range r.Entries() {
			if strings.EqualFold(path.Ext(e.Path), ".hhc") {
				if p, ok := sanitizePath(e.Path); ok {
					all = append(all, p)
				}
			}
		}
		sort.Strings(all)
		base := strings.ToLower(strings.TrimSuffix(path.Base(home), path.Ext(home)))
		for _, p := range all {
			if strings.ToLower(strings.TrimSuffix(path.Base(p), path.Ext(p))) == base {
				candidate = p
				break
			}
		}
		if candidate == "" && len(all) > 0 {
			candidate = all[0]
		}
	}
	if candidate == "" {
		log.Debugf("CHM 中没有 .hhc 目录文件")
		return nil
	}
	raw, err := r.ReadPath(candidate)
	if err != nil {
		log.Warnf("读取目录文件 %s 失败: %v", candidate, err)
		return nil
	}
	text, _ := htmlclean.Decode(raw, fallbackEnc)
	nodes := toc.Parse([]byte(text))
	log.Debugf("目录文件: %s，节点 %d", candidate, toc.Count(nodes))
	return nodes
}

func buildOrder(tocNodes []*toc.Node, home string, docs map[string]*html.Node,
	htmlSet map[string]bool, resolve func(string) (string, bool)) []string {

	var order []string
	seen := make(map[string]bool)

	add := func(p string) bool {
		if p == "" || !htmlSet[p] || seen[p] {
			return false
		}
		seen[p] = true
		order = append(order, p)
		return true
	}

	for _, n := range toc.Flatten(tocNodes) {
		if p, ok := resolveLocal(n.Href, htmlSet, resolve); ok {
			add(p)
		}
	}

	// 从首页做广度优先遍历，最多 30 层
	type node struct {
		path  string
		depth int
	}
	visited := make(map[string]bool, len(htmlSet))
	queue := []node{{home, 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth > 30 || visited[cur.path] {
			continue
		}
		visited[cur.path] = true
		add(cur.path)
		doc := docs[cur.path]
		if doc == nil {
			continue
		}
		dir := path.Dir(cur.path)
		if dir == "." {
			dir = ""
		}
		for _, href := range htmlclean.Links(doc) {
			if p, ok := resolveLocal(href, htmlSet, func(x string) (string, bool) {
				return resolve(joinRel(dir, x))
			}); ok {
				if !seen[p] {
					queue = append(queue, node{p, cur.depth + 1})
				}
			}
		}
	}

	// 补齐未被引用到的页面，保证内容不丢
	rest := make([]string, 0, len(htmlSet))
	for p := range htmlSet {
		if !seen[p] {
			rest = append(rest, p)
		}
	}
	sort.Strings(rest)
	order = append(order, rest...)
	return order
}

func buildBookTOC(nodes []*toc.Node, resolve func(string) (string, bool), htmlSet map[string]bool) []*epub.TOCItem {
	var out []*epub.TOCItem
	for _, n := range nodes {
		item := &epub.TOCItem{Title: cleanText(n.Title)}
		if item.Title == "" {
			item.Title = "未命名"
		}
		if p, ok := resolveLocal(n.Href, htmlSet, resolve); ok {
			item.Href = p
			if frag := fragmentOf(n.Href); frag != "" {
				item.Href += "#" + frag
			}
		} else if n.Href != "" {
			continue // 指向不存在的页面，丢弃
		}
		item.Children = buildBookTOC(n.Children, resolve, htmlSet)
		if item.Href == "" && len(item.Children) == 0 {
			continue
		}
		out = append(out, item)
	}
	return out
}

func resolveLocal(ref string, htmlSet map[string]bool, resolve func(string) (string, bool)) (string, bool) {
	if ref == "" {
		return "", false
	}
	lower := strings.ToLower(strings.TrimSpace(ref))
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:") || strings.HasPrefix(lower, "javascript:") ||
		strings.HasPrefix(lower, "data:") || strings.HasPrefix(ref, "#") {
		return "", false
	}
	target := ref
	if i := strings.Index(target, "::"); i >= 0 {
		target = strings.TrimPrefix(target[i+2:], "/")
	}
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target = target[:i]
	}
	if target == "" {
		return "", false
	}
	target = strings.ReplaceAll(target, "%20", " ")
	p, ok := resolve(target)
	if !ok || !htmlSet[p] {
		return "", false
	}
	return p, true
}

func fragmentOf(ref string) string {
	if i := strings.IndexByte(ref, '#'); i >= 0 {
		return ref[i+1:]
	}
	return ""
}

func joinRel(dir, ref string) string {
	if strings.HasPrefix(ref, "/") {
		return strings.TrimPrefix(ref, "/")
	}
	if dir == "" {
		return ref
	}
	return dir + "/" + ref
}

func isHTMLPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".htm", ".html", ".xhtml", ".xht":
		return true
	}
	return false
}

func isResourcePath(p string) bool {
	base := path.Base(p)
	if strings.HasPrefix(base, "#") || strings.HasPrefix(base, "$") {
		return false
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".hhc", ".hhk", ".chm", ".chw", ".chg":
		return false
	case "":
		return false
	}
	return true
}

// sanitizePath 规范化 CHM 内部路径，拒绝越界与非法字符。
func sanitizePath(p string) (string, bool) {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "/")
	if i := strings.IndexByte(p, ';'); i >= 0 {
		p = p[:i]
	}
	if p == "" {
		return "", false
	}
	clean := path.Clean(p)
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", false
	}
	if strings.Contains(clean, ":") {
		return "", false
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return "", false
		}
	}
	// 替换在 Windows 上非法的字符
	var b strings.Builder
	for _, r := range clean {
		switch r {
		case '<', '>', '"', '|', '?', '*':
			b.WriteRune('_')
		default:
			if r < 0x20 {
				b.WriteRune('_')
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String(), true
}

func cleanText(s string) string {
	s = stdhtml.UnescapeString(s)
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return strings.Join(strings.Fields(s), " ")
}

// isExternalRef 判断引用是否为外部地址或跨 CHM 引用。
func isExternalRef(ref string) bool {
	l := strings.ToLower(strings.TrimSpace(ref))
	for _, p := range []string{
		"http://", "https://", "ftp://", "mailto:", "tel:", "data:",
		"javascript:", "file:", "ms-its:", "mk:@msitstore:", "//",
	} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// collectReferences 汇总正文（含 style 属性、内联样式）与 CSS 引用到的所有静态资源，
// 返回小写路径集合，用于裁剪未被使用的内容。
func collectReferences(docs map[string]*html.Node, resolve func(string) (string, bool),
	readFile func(string) []byte) map[string]bool {

	actual := make(map[string]string) // 小写路径 -> 真实路径

	add := func(dir, ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" || strings.HasPrefix(ref, "#") || isExternalRef(ref) {
			return
		}
		if i := strings.Index(ref, "::"); i >= 0 {
			ref = strings.TrimPrefix(ref[i+2:], "/")
		}
		if i := strings.IndexByte(ref, '#'); i >= 0 {
			ref = ref[:i]
		}
		ref = strings.ReplaceAll(ref, "%20", " ")
		if ref == "" {
			return
		}
		if full, ok := resolve(joinRel(dir, ref)); ok {
			actual[strings.ToLower(full)] = full
		}
	}

	refAttrs := map[string]bool{"src": true, "href": true, "data": true, "poster": true, "background": true}
	for p, doc := range docs {
		dir := path.Dir(p)
		if dir == "." {
			dir = ""
		}
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode {
				for _, a := range n.Attr {
					key := strings.ToLower(a.Key)
					if refAttrs[key] {
						add(dir, a.Val)
						continue
					}
					if key == "style" {
						for _, u := range htmlclean.CSSRefs([]byte(a.Val)) {
							add(dir, u)
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
	}

	// CSS 传递闭包：样式表里 url() 指向的图片、字体、@import 的其它样式表
	seen := map[string]bool{}
	for {
		next := ""
		for k, v := range actual {
			if !seen[k] && strings.EqualFold(path.Ext(v), ".css") {
				next = k
				break
			}
		}
		if next == "" {
			break
		}
		seen[next] = true
		p := actual[next]
		data := readFile(p)
		if data == nil {
			continue
		}
		dir := path.Dir(p)
		if dir == "." {
			dir = ""
		}
		for _, u := range htmlclean.CSSRefs(data) {
			add(dir, u)
		}
	}

	out := make(map[string]bool, len(actual))
	for k := range actual {
		out[k] = true
	}
	return out
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "urn:uuid:00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
