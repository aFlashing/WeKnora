// Package meta 从 CHM 首页提取标题、作者、出版社、ISBN、简介与封面。
package meta

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/Tencent/WeKnora/internal/infrastructure/chm2epub/chm"
)

// Info 是抽取出的元数据。
type Info struct {
	Title       string
	Authors     []string
	Publisher   string
	ISBN        string
	Description string
	CoverPath   string
	CoverData   []byte
}

var (
	byRe    = regexp.MustCompile(`(?i)^\s*(by|author|作者|编著)\s*:?\s+`)
	sepsRe  = regexp.MustCompile(`\s*(?:&|,|;|、|/|\band\b)\s*`)
	numRe   = regexp.MustCompile(`[0-9.]+`)
	labelRe = regexp.MustCompile(`^[^:：]+[:：]\s*`)
	urlRe   = regexp.MustCompile(`(?i)^\s*(https?|ftp|mailto|javascript|data|file):`)
)

// Extract 依据 calibre 的启发式从首页抽取元数据。homePath 为首页在 CHM 内的路径。
func Extract(home *html.Node, homePath string, r *chm.Reader) *Info {
	info := &Info{}

	if authors := extractAuthors(home); len(authors) > 0 {
		info.Authors = authors
	}
	info.Publisher = firstNonEmpty(
		textOf(findSpanByClass(home, "imprint")),
		tableValue(home, "publisher", "出版社"),
	)
	info.ISBN = firstNonEmpty(
		textOf(findSpanByClass(home, "isbn")),
		tableValue(home, "isbn", "书号"),
	)
	info.Description = buildDescription(home)

	if src := coverSource(home); src != "" {
		if data, full := readCover(r, homePath, src); data != nil {
			info.CoverPath = full
			info.CoverData = data
		}
	}
	return info
}

func extractAuthors(home *html.Node) []string {
	raw := firstNonEmpty(
		textOf(findSpanByClass(home, "author")),
		tableValue(home, "by", "author", "作者"),
	)
	raw = byRe.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range sepsRe.Split(raw, -1) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func buildDescription(home *html.Node) string {
	date := firstNonEmpty(
		textOf(findSpanByClass(home, "cwdate")),
		tableValue(home, "pub date", "出版日期", "日期"),
	)
	pages := firstNonEmpty(
		textOf(findSpanByClass(home, "pages")),
		tableValue(home, "pages", "页数"),
	)
	date = strings.TrimSpace(strings.ReplaceAll(date, "©", ""))
	if date == "" && pages == "" {
		return ""
	}
	if m := numRe.FindString(pages); m != "" {
		pages = m
	}
	switch {
	case date != "" && pages != "":
		return "Published " + date + ", " + pages + " pages."
	case date != "":
		return "Published " + date + "."
	default:
		return pages + " pages."
	}
}

// coverSource 优先取 alt 含 cover 的图片，否则按 |高宽比-1.25| 最接近的原则挑选。
func coverSource(home *html.Node) string {
	var imgs []*html.Node
	walk(home, func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			imgs = append(imgs, n)
		}
	})
	for _, img := range imgs {
		if strings.Contains(strings.ToLower(attr(img, "alt")), "cover") {
			if src := attr(img, "src"); src != "" {
				return src
			}
		}
	}

	best := ""
	bestScore := 1e9
	for _, img := range imgs {
		src := attr(img, "src")
		if src == "" {
			continue
		}
		h, ok1 := firstNumber(attr(img, "height"))
		w, ok2 := firstNumber(attr(img, "width"))
		if !ok1 || !ok2 || w == 0 {
			if src != "" && best == "" {
				best, bestScore = src, 0
			}
			continue
		}
		score := abs(h/w - 1.25)
		if score < bestScore {
			best, bestScore = src, score
		}
	}
	return best
}

func readCover(r *chm.Reader, homePath, src string) ([]byte, string) {
	candidates := []string{
		path.Join(path.Dir(homePath), src),
		src,
		strings.TrimPrefix(src, "/"),
	}
	for _, c := range candidates {
		c = path.Clean(strings.ReplaceAll(c, "\\", "/"))
		c = strings.TrimPrefix(c, "./")
		data, err := r.ReadPath(c)
		if err != nil || len(data) == 0 {
			continue
		}
		if !looksLikeImage(data) {
			continue
		}
		return data, c
	}
	return nil, ""
}

func looksLikeImage(data []byte) bool {
	if len(data) < 12 {
		return false
	}
	switch {
	case data[0] == 0xFF && data[1] == 0xD8: // JPEG
		return true
	case data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G':
		return true
	case data[0] == 'G' && data[1] == 'I' && data[2] == 'F':
		return true
	case data[0] == 'B' && data[1] == 'M':
		return true
	case len(data) > 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return true
	}
	return false
}

// ---- 通用 DOM 工具 ----

func walk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
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

func textOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			return
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func findSpanByClass(root *html.Node, class string) *html.Node {
	var found *html.Node
	walk(root, func(n *html.Node) {
		if found != nil || n.Type != html.ElementNode {
			return
		}
		if n.Data != "span" && n.Data != "div" {
			return
		}
		if strings.Contains(strings.ToLower(attr(n, "class")), class) {
			found = n
		}
	})
	return found
}

// tableValue 在表格中查找关键词所在行，取其值。
func tableValue(root *html.Node, keywords ...string) string {
	var cells []*html.Node
	walk(root, func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "td" || n.Data == "th") {
			cells = append(cells, n)
		}
	})

	for _, td := range cells {
		txt := strings.ToLower(textOf(td))
		hit := false
		for _, kw := range keywords {
			if strings.Contains(txt, strings.ToLower(kw)) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}

		tr := td.Parent
		if tr == nil {
			continue
		}
		// 情况一：单元格只放标签，值在下一个单元格
		if labelRe.MatchString(textOf(td) + ":") && len(strings.Fields(textOf(td))) <= 3 {
			if next := nextElement(td); next != nil {
				if v := strings.TrimSpace(textOf(next)); v != "" {
					return strings.TrimSpace(labelRe.ReplaceAllString(v, ""))
				}
			}
		}
		// 情况二：整行形如 "Publisher: xxx"
		rowTxt := strings.TrimSpace(textOf(tr))
		if m := labelRe.FindStringIndex(rowTxt); m != nil {
			if v := strings.TrimSpace(rowTxt[m[1]:]); v != "" {
				return v
			}
		}
		return ""
	}
	return ""
}

func nextElement(n *html.Node) *html.Node {
	for c := n.NextSibling; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			return c
		}
	}
	return nil
}

func firstNumber(s string) (float64, bool) {
	m := numRe.FindString(s)
	if m == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(m, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

var _ = urlRe
