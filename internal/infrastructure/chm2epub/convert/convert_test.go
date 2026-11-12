package convert

import "testing"

func TestSanitizePath(t *testing.T) {
	bad := []string{"../x.html", "a/../../b.html", "x:stream", ""}
	for _, in := range bad {
		if p, ok := sanitizePath(in); ok {
			t.Errorf("sanitizePath(%q) 应当被拒绝，返回 %q", in, p)
		}
	}

	good := map[string]string{
		"/dir/page.html":       "dir/page.html",
		"dir\\page.html":       "dir/page.html",
		"page.html;junk":       "page.html",
		"/a/b/c.css":           "a/b/c.css",
		"images/a b.png":       "images/a b.png",
		"bad:name.html":        "",
		"a/./b.html":           "a/b.html",
	}
	for in, want := range good {
		got, ok := sanitizePath(in)
		if want == "" {
			if ok {
				t.Errorf("sanitizePath(%q) 应当被拒绝，返回 %q", in, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("sanitizePath(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
}
