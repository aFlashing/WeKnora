package toc

import "testing"

const sampleHHC = `<html><body>
<object type="text/site properties"><param name="Window Styles" value="0x800025"></object>
<ul>
  <li><object type="text/sitemap">
      <param name="Name" value="第一章">
      <param name="Local" value="ch1.html">
    </object>
    <ul>
      <li><object type="text/sitemap">
        <param name="Name" value="第一节">
        <param name="Local" value="ch1/sec1.html#top">
      </object></li>
      <li><object type="text/sitemap">
        <param name="Name" value="第二节">
        <param name="Local" value="ch1/sec2.html">
      </object></li>
    </ul>
  </li>
  <li><object type="text/sitemap">
    <param name="Name" value="第二章">
    <param name="Local" value="ch2.html">
  </object></li>
</ul>
</body></html>`

func TestParseHHC(t *testing.T) {
	nodes := Parse([]byte(sampleHHC))
	if len(nodes) != 2 {
		t.Fatalf("顶层节点 = %d, want 2", len(nodes))
	}
	if nodes[0].Title != "第一章" || nodes[0].Href != "ch1.html" {
		t.Errorf("第一个节点异常: %+v", nodes[0])
	}
	if len(nodes[0].Children) != 2 {
		t.Fatalf("子节点 = %d, want 2", len(nodes[0].Children))
	}
	if nodes[0].Children[0].Href != "ch1/sec1.html#top" {
		t.Errorf("锚点丢失: %q", nodes[0].Children[0].Href)
	}
	if Count(nodes) != 4 {
		t.Errorf("Count = %d, want 4", Count(nodes))
	}
}

func TestParseEmpty(t *testing.T) {
	if nodes := Parse([]byte("<html><body>无目录</body></html>")); len(nodes) != 0 {
		t.Errorf("空目录应返回 0 个节点，得到 %d", len(nodes))
	}
}

func TestPrune(t *testing.T) {
	nodes := []*Node{
		{Title: "有链接", Href: "a.html"},
		{Title: "空节点", Children: []*Node{{Title: "空子节点"}}},
	}
	got := Prune(nodes)
	if len(got) != 1 || got[0].Title != "有链接" {
		t.Errorf("Prune 结果异常: %+v", got)
	}
}
