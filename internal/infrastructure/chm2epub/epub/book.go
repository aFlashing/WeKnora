// Package epub 负责构建并输出 EPUB 3 文件（同时附带 NCX 兼容旧阅读器）。
package epub

import (
	"mime"
	"path"
	"strings"
)

// Resource 是放入 OEBPS 的一个静态文件（图片、CSS、字体等）。
type Resource struct {
	Path string // 相对 OEBPS 的路径
	Data []byte
}

// Chapter 是正文中的一个 XHTML 文档。
type Chapter struct {
	Path  string // 相对 OEBPS 的路径
	Title string
	Data  []byte
}

// TOCItem 是导航目录项。
type TOCItem struct {
	Title    string
	Href     string // 相对 OEBPS，可含 #frag
	Children []*TOCItem
}

// Book 是待输出的电子书模型。
type Book struct {
	Title       string
	Authors     []string
	Language    string
	Publisher   string
	ISBN        string
	Description string
	Source      string
	Identifier  string

	CoverPath string // 相对 OEBPS
	CoverData []byte

	Chapters  []Chapter
	Resources []Resource
	TOC       []*TOCItem
}

// MediaType 依据扩展名返回 media type。
func MediaType(p string) string {
	ext := strings.ToLower(path.Ext(p))
	switch ext {
	case ".xhtml", ".html", ".htm":
		return "application/xhtml+xml"
	case ".css":
		return "text/css"
	case ".ncx":
		return "application/x-dtbncx+xml"
	case ".js":
		return "application/javascript"
	case ".svg":
		return "image/svg+xml"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	case ".webp":
		return "image/webp"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".eot":
		return "application/vnd.ms-fontobject"
	case ".mp3":
		return "audio/mpeg"
	case ".mp4":
		return "video/mp4"
	case ".xml":
		return "application/xml"
	case ".txt":
		return "text/plain"
	}
	if t := mime.TypeByExtension(ext); t != "" {
		if i := strings.IndexByte(t, ';'); i > 0 {
			t = t[:i]
		}
		return t
	}
	return "application/octet-stream"
}
