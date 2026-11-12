package epub

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

const opfPath = "OEBPS/content.opf"

type manifestItem struct {
	ID        string
	Href      string
	MediaType string
	Props     string
}

// Write 把 Book 写成 EPUB 3 文件。
func Write(filename string, b *Book) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	// mimetype 必须是第一个条目且不压缩
	mh := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	mh.SetModTime(time.Unix(0, 0).UTC())
	w, err := zw.CreateHeader(mh)
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte("application/epub+zip")); err != nil {
		return err
	}

	if err := writeFile(zw, "META-INF/container.xml", []byte(containerXML), true); err != nil {
		return err
	}

	items, spine := buildManifest(b)
	opf := buildOPF(b, items, spine)
	if err := writeFile(zw, opfPath, []byte(opf), true); err != nil {
		return err
	}
	if err := writeFile(zw, "OEBPS/nav.xhtml", []byte(buildNav(b)), true); err != nil {
		return err
	}
	if err := writeFile(zw, "OEBPS/toc.ncx", []byte(buildNCX(b)), true); err != nil {
		return err
	}

	for _, ch := range b.Chapters {
		if err := writeFile(zw, "OEBPS/"+ch.Path, ch.Data, true); err != nil {
			return err
		}
	}
	resPaths := make(map[string]bool, len(b.Resources))
	for _, r := range b.Resources {
		resPaths[r.Path] = true
		if err := writeFile(zw, "OEBPS/"+r.Path, r.Data, isText(r.Path)); err != nil {
			return err
		}
	}
	if b.CoverPath != "" && len(b.CoverData) > 0 && !resPaths[b.CoverPath] {
		if err := writeFile(zw, "OEBPS/"+b.CoverPath, b.CoverData, false); err != nil {
			return err
		}
	}
	return zw.Close()
}

func isText(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".html", ".htm", ".xhtml", ".css", ".svg", ".xml", ".ncx", ".txt", ".js", ".opf":
		return true
	}
	return false
}

func writeFile(zw *zip.Writer, name string, data []byte, deflate bool) error {
	h := &zip.FileHeader{Name: name}
	if deflate {
		h.Method = zip.Deflate
	} else {
		h.Method = zip.Store
	}
	h.SetModTime(time.Unix(0, 0).UTC())
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

const containerXML = `<?xml version="1.0" encoding="utf-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`

func buildManifest(b *Book) ([]manifestItem, []manifestItem) {
	var items []manifestItem
	seen := map[string]bool{}

	items = append(items, manifestItem{ID: "nav", Href: "nav.xhtml", MediaType: "application/xhtml+xml", Props: "nav"})
	items = append(items, manifestItem{ID: "ncx", Href: "toc.ncx", MediaType: "application/x-dtbncx+xml"})
	seen["nav.xhtml"], seen["toc.ncx"] = true, true

	var spine []manifestItem
	for i, ch := range b.Chapters {
		if seen[ch.Path] {
			continue
		}
		id := fmt.Sprintf("chap%03d", i)
		item := manifestItem{ID: id, Href: escapeHref(ch.Path), MediaType: MediaType(ch.Path)}
		items = append(items, item)
		spine = append(spine, item)
		seen[ch.Path] = true
	}

	if b.CoverPath != "" && !seen[b.CoverPath] {
		items = append(items, manifestItem{
			ID: "cover-image", Href: escapeHref(b.CoverPath),
			MediaType: MediaType(b.CoverPath), Props: "cover-image",
		})
		seen[b.CoverPath] = true
	}

	for _, r := range b.Resources {
		if seen[r.Path] {
			continue
		}
		items = append(items, manifestItem{
			ID:       fmt.Sprintf("res%04d", len(items)),
			Href:     escapeHref(r.Path),
			MediaType: MediaType(r.Path),
		})
		seen[r.Path] = true
	}
	return items, spine
}

func buildOPF(b *Book, items, spine []manifestItem) string {
	var s strings.Builder
	s.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	s.WriteString(`<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid"`)
	if b.Language != "" {
		s.WriteString(` xml:lang="` + xmlEscape(b.Language) + `"`)
	}
	s.WriteString(">\n<metadata xmlns:dc=\"http://purl.org/dc/elements/1.1/\">\n")

	identifier := b.Identifier
	if identifier == "" {
		identifier = "urn:uuid:00000000-0000-4000-8000-000000000000"
	}
	s.WriteString("  <dc:identifier id=\"bookid\">" + xmlEscape(identifier) + "</dc:identifier>\n")
	s.WriteString("  <dc:title>" + xmlEscape(b.Title) + "</dc:title>\n")
	if b.Language != "" {
		s.WriteString("  <dc:language>" + xmlEscape(b.Language) + "</dc:language>\n")
	}
	for i, a := range b.Authors {
		fmt.Fprintf(&s, "  <dc:creator id=\"creator%d\">%s</dc:creator>\n", i, xmlEscape(a))
		fmt.Fprintf(&s, "  <meta refines=\"#creator%d\" property=\"role\" scheme=\"marc:relators\">aut</meta>\n", i)
	}
	if b.Publisher != "" {
		s.WriteString("  <dc:publisher>" + xmlEscape(b.Publisher) + "</dc:publisher>\n")
	}
	if b.Description != "" {
		s.WriteString("  <dc:description>" + xmlEscape(b.Description) + "</dc:description>\n")
	}
	if b.ISBN != "" {
		s.WriteString("  <dc:identifier>" + xmlEscape(b.ISBN) + "</dc:identifier>\n")
	}
	if b.Source != "" {
		s.WriteString("  <dc:source>" + xmlEscape(b.Source) + "</dc:source>\n")
	}
	s.WriteString(`  <meta property="dcterms:modified">` + time.Now().UTC().Format("2006-01-02T15:04:05Z") + "</meta>\n")
	if b.CoverPath != "" {
		s.WriteString(`  <meta name="cover" content="cover-image"/>` + "\n")
	}
	s.WriteString("</metadata>\n<manifest>\n")
	for _, it := range items {
		fmt.Fprintf(&s, "  <item id=\"%s\" href=\"%s\" media-type=\"%s\"", it.ID, it.Href, it.MediaType)
		if it.Props != "" {
			s.WriteString(" properties=\"" + it.Props + "\"")
		}
		s.WriteString("/>\n")
	}
	s.WriteString("</manifest>\n<spine toc=\"ncx\">\n")
	for _, it := range spine {
		fmt.Fprintf(&s, "  <itemref idref=\"%s\"/>\n", it.ID)
	}
	s.WriteString("</spine>\n</package>\n")
	return s.String()
}

func buildNav(b *Book) string {
	var s strings.Builder
	s.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n<!DOCTYPE html>\n")
	s.WriteString(`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"`)
	if b.Language != "" {
		s.WriteString(` xml:lang="` + xmlEscape(b.Language) + `" lang="` + xmlEscape(b.Language) + `"`)
	}
	s.WriteString(">\n<head><meta charset=\"utf-8\"/><title>")
	s.WriteString(xmlEscape(b.Title))
	heading := "目录"
	if b.Language != "" && !strings.HasPrefix(b.Language, "zh") {
		heading = "Contents"
	}
	s.WriteString("</title></head>\n<body>\n<nav epub:type=\"toc\" id=\"toc\">\n<h1>" + heading + "</h1>\n")
	writeNavList(&s, b.TOC)
	if len(b.TOC) == 0 {
		s.WriteString("<ol><li><a href=\"")
		if len(b.Chapters) > 0 {
			s.WriteString(escapeHref(b.Chapters[0].Path))
		}
		s.WriteString("\">开始</a></li></ol>\n")
	}
	s.WriteString("</nav>\n</body>\n</html>\n")
	return s.String()
}

func writeNavList(s *strings.Builder, items []*TOCItem) {
	if len(items) == 0 {
		return
	}
	s.WriteString("<ol>\n")
	for _, it := range items {
		s.WriteString("<li><a href=\"" + xmlEscape(escapeHref(it.Href)) + "\">" + xmlEscape(it.Title) + "</a>")
		if len(it.Children) > 0 {
			s.WriteString("\n")
			writeNavList(s, it.Children)
		}
		s.WriteString("</li>\n")
	}
	s.WriteString("</ol>\n")
}

func buildNCX(b *Book) string {
	var s strings.Builder
	uid := b.Identifier
	if uid == "" {
		uid = "urn:uuid:00000000-0000-4000-8000-000000000000"
	}
	s.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	s.WriteString(`<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">` + "\n<head>\n")
	s.WriteString(`  <meta name="dtb:uid" content="` + xmlEscape(uid) + `"/>` + "\n")
	s.WriteString(`  <meta name="dtb:depth" content="` + fmt.Sprint(maxDepth(b.TOC)) + `"/>` + "\n")
	s.WriteString(`  <meta name="dtb:totalPageCount" content="0"/>` + "\n")
	s.WriteString(`  <meta name="dtb:maxPageNumber" content="0"/>` + "\n</head>\n")
	s.WriteString("<docTitle><text>" + xmlEscape(b.Title) + "</text></docTitle>\n<navMap>\n")
	n := 0
	writeNavPoints(&s, b.TOC, &n)
	s.WriteString("</navMap>\n</ncx>\n")
	return s.String()
}

func writeNavPoints(s *strings.Builder, items []*TOCItem, counter *int) {
	for _, it := range items {
		*counter++
		fmt.Fprintf(s, `<navPoint id="np%d" playOrder="%d"><navLabel><text>%s</text></navLabel><content src="%s"/>`,
			*counter, *counter, xmlEscape(it.Title), xmlEscape(escapeHref(it.Href)))
		if len(it.Children) > 0 {
			s.WriteString("\n")
			writeNavPoints(s, it.Children, counter)
		}
		s.WriteString("</navPoint>\n")
	}
}

func maxDepth(items []*TOCItem) int {
	depth := 0
	for _, it := range items {
		if d := 1 + maxDepth(it.Children); d > depth {
			depth = d
		}
	}
	return depth
}

func xmlEscape(s string) string {
	var b bytes.Buffer
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
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// escapeHref 对路径做最小限度的 URL 编码（空格、中文等保留一个可读形式）。
func escapeHref(p string) string {
	if p == "" {
		return ""
	}
	frag := ""
	if i := strings.IndexByte(p, '#'); i >= 0 {
		p, frag = p[:i], p[i:]
	}
	var b strings.Builder
	for _, r := range p {
		switch {
		case r == ' ':
			b.WriteString("%20")
		case r == '%':
			b.WriteString("%25")
		case r == '#':
			b.WriteString("%23")
		case r == '?':
			b.WriteString("%3F")
		case r < 0x20:
			fmt.Fprintf(&b, "%%%02X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(frag)
	return b.String()
}

// SortResources 按路径排序，便于产物比对。
func SortResources(rs []Resource) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Path < rs[j].Path })
}
