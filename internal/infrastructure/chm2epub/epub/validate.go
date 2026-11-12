package epub

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var (
	hrefRe   = regexp.MustCompile(`(?:href|src)\s*=\s*"([^"]+)"`)
	cssRefRe = regexp.MustCompile(`(?i)url\(\s*['"]?([^'")]+)['"]?\s*\)`)
)

// Validate 对生成的 EPUB 做结构自检，返回问题清单。
func Validate(filename string) []string {
	var problems []string

	zr, err := zip.OpenReader(filename)
	if err != nil {
		return []string{fmt.Sprintf("无法打开 EPUB: %v", err)}
	}
	defer zr.Close()

	if len(zr.File) == 0 {
		return []string{"EPUB 为空"}
	}

	first := zr.File[0]
	if first.Name != "mimetype" {
		problems = append(problems, "mimetype 不是第一个条目")
	} else if first.Method != zip.Store {
		problems = append(problems, "mimetype 被压缩（必须 STORED）")
	} else if rc, err := first.Open(); err != nil {
		problems = append(problems, fmt.Sprintf("读取 mimetype 失败: %v", err))
	} else {
		data, _ := io.ReadAll(rc)
		rc.Close()
		if string(data) != "application/epub+zip" {
			problems = append(problems, "mimetype 内容不正确")
		}
	}

	files := make(map[string]bool, len(zr.File))
	for _, f := range zr.File {
		files[f.Name] = true
	}
	for _, required := range []string{"META-INF/container.xml", "OEBPS/content.opf", "OEBPS/nav.xhtml", "OEBPS/toc.ncx"} {
		if !files[required] {
			problems = append(problems, "缺少必需文件: "+required)
		}
	}

	if opf, err := readZipFile(zr, "OEBPS/content.opf"); err != nil {
		problems = append(problems, "无法读取 content.opf")
		return problems
	} else {
		for _, m := range hrefRe.FindAllStringSubmatch(opf, -1) {
			if missing := checkRef("OEBPS", m[1], files); missing != "" {
				problems = append(problems, "manifest 指向不存在的文件: "+missing)
			}
		}
	}

	if nav, err := readZipFile(zr, "OEBPS/nav.xhtml"); err == nil {
		for _, m := range hrefRe.FindAllStringSubmatch(nav, -1) {
			if missing := checkRef("OEBPS", m[1], files); missing != "" {
				problems = append(problems, "nav 指向不存在的文件: "+missing)
			}
		}
	}

	// 逐页检查内部链接、资源引用与 title
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "OEBPS/") || !isXHTMLName(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			problems = append(problems, "无法读取 "+f.Name)
			continue
		}
		data, _ := io.ReadAll(rc)
		rc.Close()

		doc, err := html.Parse(strings.NewReader(string(data)))
		if err != nil {
			problems = append(problems, "XHTML 无法解析: "+f.Name)
			continue
		}
		dir := path.Dir(f.Name)
		problems = append(problems, checkDocLinks(doc, dir, files, f.Name)...)
		if name := docTitle(doc); name == "" {
			problems = append(problems, path.Base(f.Name)+" 缺少非空 <title>")
		}
	}

	// 样式表里的 url() 引用
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "OEBPS/") || !strings.EqualFold(path.Ext(f.Name), ".css") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(rc)
		rc.Close()

		dir := path.Dir(f.Name)
		for _, sub := range cssRefRe.FindAllStringSubmatch(string(data), -1) {
			if missing := checkRef(dir, sub[1], files); missing != "" {
				problems = append(problems, fmt.Sprintf("%s 引用了不存在的文件: %s", path.Base(f.Name), missing))
			}
		}
	}

	return problems
}

func checkDocLinks(doc *html.Node, dir string, files map[string]bool, name string) []string {
	var problems []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for _, a := range n.Attr {
				key := strings.ToLower(a.Key)
				if key != "href" && key != "src" {
					continue
				}
				if missing := checkRef(dir, a.Val, files); missing != "" {
					problems = append(problems, fmt.Sprintf("%s 引用了不存在的文件: %s", path.Base(name), missing))
					if len(problems) > 50 {
						return
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return problems
}

func docTitle(doc *html.Node) string {
	var title string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "title" && title == "" {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.TextNode {
					title += c.Data
				}
			}
			title = strings.TrimSpace(title)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return title
}

func isXHTMLName(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".htm", ".xhtml", ".xht":
		return true
	}
	return false
}

// checkRef 把相对引用解析为 zip 内路径并检查是否存在。
func checkRef(dir, ref string, files map[string]bool) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "#") {
		return ""
	}
	lower := strings.ToLower(ref)
	for _, p := range []string{"http://", "https://", "ftp://", "mailto:", "tel:", "data:", "javascript:", "file:", "ms-its:", "mk:@msitstore:", "//"} {
		if strings.HasPrefix(lower, p) {
			return ""
		}
	}
	// 跨 CHM 引用（other.chm::/page.html）在单本转换中无法解析，属预期情况
	if strings.Contains(ref, "::") {
		return ""
	}
	if i := strings.IndexByte(ref, '#'); i >= 0 {
		ref = ref[:i]
	}
	if ref == "" {
		return ""
	}
	ref = strings.ReplaceAll(ref, "%20", " ")
	ref = strings.ReplaceAll(ref, "\\", "/")
	if strings.HasPrefix(ref, "/") {
		return "" // 容器根之外的绝对路径不校验
	}
	target := path.Clean(path.Join(dir, ref))
	if files[target] {
		return ""
	}
	return ref
}

func readZipFile(zr *zip.ReadCloser, name string) (string, error) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()
			data, err := io.ReadAll(rc)
			if err != nil {
				return "", err
			}
			return string(data), nil
		}
	}
	return "", fmt.Errorf("not found: %s", name)
}
