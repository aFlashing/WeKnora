package htmlclean

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func parse(t *testing.T, src string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	return doc
}

func TestDecodeGBK(t *testing.T) {
	// "中文测试" 的 GBK 编码
	gbk := []byte{0xD6, 0xD0, 0xCE, 0xC4, 0xB2, 0xE2, 0xCA, 0xD4}
	got, enc := Decode(gbk, "gbk")
	if got != "中文测试" {
		t.Errorf("GBK 解码结果 = %q, want %q (enc=%s)", got, "中文测试", enc)
	}

	page := append([]byte(`<html><head><meta charset="gbk"></head><body>`), gbk...)
	page = append(page, []byte(`</body></html>`)...)
	got, _ = Decode(page, "windows-1252")
	if !strings.Contains(got, "中文测试") {
		t.Errorf("按 meta 声明解码失败: %q", got)
	}
}

// 声明为 UTF-8 的页面不能被 LCID 推断出的 cp1252 抢先解码。
func TestDecodeUTF8DeclarationBeatsFallback(t *testing.T) {
	page := []byte(`<html><head><meta http-equiv="Content-Type" content="text/html; charset=UTF-8"></head>` +
		`<body>Memminger Straße © 2015</body></html>`)
	got, enc := Decode(page, "windows-1252")
	if !strings.Contains(got, "Straße") || !strings.Contains(got, "©") {
		t.Errorf("UTF-8 声明页面被误解码: %q (enc=%s)", got, enc)
	}
	if enc != "utf-8" {
		t.Errorf("编码判定 = %q, want utf-8", enc)
	}
}

func TestCleanRemovesScriptAndNavTable(t *testing.T) {
	src := `<html><head><script>alert(1)</script></head><body>
	<table><tr><td><img alt="prev" src="p.gif"></td></tr></table>
	<br>
	<p onclick="x()">hello</p>
	</body></html>`
	src = strings.Replace(src, `<p onclick="x()">hello</p>`, `<p onclick="x()">hello<br>world</p>`, 1)
	doc := parse(t, src)
	Clean(doc, Options{})

	out := string(RenderXHTML(doc))
	if strings.Contains(out, "alert(1)") {
		t.Error("script 未被移除")
	}
	if strings.Contains(out, "p.gif") {
		t.Error("导航表未被移除")
	}
	if strings.Contains(out, "onclick") {
		t.Error("事件属性未被移除")
	}
	if strings.Contains(out, "<br>") || !strings.Contains(out, "<br/>") {
		t.Error("br 未按 XHTML 自闭合")
	}
	if !strings.Contains(out, `xmlns="http://www.w3.org/1999/xhtml"`) {
		t.Error("缺少 XHTML 命名空间")
	}
}

func TestUnwrapSingleCellTable(t *testing.T) {
	src := `<html><body><table><tr><td><p>内容</p></td></tr></table></body></html>`
	doc := parse(t, src)
	Clean(doc, Options{})
	out := string(RenderXHTML(doc))
	if strings.Contains(out, "<table") {
		t.Errorf("单表格壳未被拆掉: %s", out)
	}
	if !strings.Contains(out, "<p>内容</p>") {
		t.Errorf("内容丢失: %s", out)
	}
}

func TestFixRootRelative(t *testing.T) {
	src := `<html><head><link rel="stylesheet" disabled="disabled" href="/css/a.css"></head>` +
		`<body><img src="/img/x.png"></body></html>`
	doc := parse(t, src)
	Clean(doc, Options{FileDir: "sub"})
	out := string(RenderXHTML(doc))
	if !strings.Contains(out, `href="../css/a.css"`) {
		t.Errorf("根相对 href 未改写: %s", out)
	}
	if !strings.Contains(out, `src="../img/x.png"`) {
		t.Errorf("根相对 src 未改写: %s", out)
	}
	if strings.Contains(out, "disabled") {
		t.Errorf("link 的 disabled 未移除: %s", out)
	}
}

func TestRewriteCSS(t *testing.T) {
	in := `body{background:url(/img/bg.png)} @import "/css/base.css";`
	out := string(RewriteCSS([]byte(in), "docs"))
	if !strings.Contains(out, `url(../img/bg.png)`) {
		t.Errorf("CSS url 未改写: %s", out)
	}
	if !strings.Contains(out, `@import "../css/base.css"`) {
		t.Errorf("CSS @import 未改写: %s", out)
	}
}

func TestSetTitle(t *testing.T) {
	doc := parse(t, `<html><head></head><body>x</body></html>`)
	SetTitle(doc, "章节一")
	if got := Title(doc); got != "章节一" {
		t.Errorf("Title = %q", got)
	}
}
