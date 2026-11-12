package chm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadEncInt(t *testing.T) {
	cases := []struct {
		in   []byte
		want int
	}{
		{[]byte{0x05}, 5},
		{[]byte{0x81, 0x00}, 128},
		{[]byte{0xFF, 0x7F}, (0x7F << 7) | 0x7F},
	}
	for _, c := range cases {
		got, next, ok := readEncInt(c.in, 0, len(c.in))
		if !ok {
			t.Fatalf("readEncInt(%v) 返回失败", c.in)
		}
		if got != c.want {
			t.Errorf("readEncInt(%v) = %d, want %d", c.in, got, c.want)
		}
		if next != len(c.in) {
			t.Errorf("readEncInt(%v) next = %d, want %d", c.in, next, len(c.in))
		}
	}
}

// findSample 在 CHM2EPUB_SAMPLES 指定的目录中查找样本，找不到则跳过。
func findSample(t *testing.T, match string) string {
	t.Helper()
	dir := os.Getenv("CHM2EPUB_SAMPLES")
	if dir == "" {
		t.Skip("未设置 CHM2EPUB_SAMPLES，跳过真实样本测试")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skip("找不到样本目录，设置 CHM2EPUB_SAMPLES 后重试")
	}
	for _, e := range entries {
		if !e.IsDir() && strings.Contains(strings.ToLower(e.Name()), strings.ToLower(match)) {
			return filepath.Join(dir, e.Name())
		}
	}
	t.Skipf("样本目录中没有匹配 %q 的文件", match)
	return ""
}

func TestOpenAndReadEntries(t *testing.T) {
	path := findSample(t, "core_EtherCAT_ProfibusMaster_Terminal_EL6731")
	r, err := Open(path)
	if err != nil {
		t.Fatalf("打开 CHM 失败: %v", err)
	}
	defer r.Close()

	if r.Version != 3 {
		t.Errorf("版本 = %d, want 3", r.Version)
	}
	if len(r.Entries()) == 0 {
		t.Fatal("目录条目为空")
	}

	si, err := r.System()
	if err != nil {
		t.Fatalf("解析 #SYSTEM 失败: %v", err)
	}
	if !strings.Contains(si.Title, "EtherCAT") {
		t.Errorf("标题异常: %q", si.Title)
	}
	if si.DefaultTopic == "" {
		t.Error("默认主题为空")
	}

	// 读取一个压缩段条目，验证 LZX 解码
	for _, e := range r.Entries() {
		if e.Section == 1 && strings.HasSuffix(strings.ToLower(e.Path), ".htm") {
			data, err := r.ReadEntry(e)
			if err != nil {
				t.Fatalf("读取 %s 失败: %v", e.Path, err)
			}
			if len(data) == 0 {
				t.Fatalf("%s 内容为空", e.Path)
			}
			return
		}
	}
	t.Error("没有找到压缩段中的 HTML 条目")
}
