// Package htmlclean 负责 CHM 内 HTML 的编码归一、结构净化与 XHTML 序列化。
package htmlclean

import (
	"bytes"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

var (
	metaCharsetRe = regexp.MustCompile(`(?i)<meta[^>]+charset\s*=\s*["']?\s*([a-zA-Z0-9_\-]+)`)
	xmlEncRe      = regexp.MustCompile(`(?is)^\s*<\?xml[^>]*encoding\s*=\s*["']([^"']+)["']`)
)

// Decode 把任意编码的 HTML 字节串转成 UTF-8 文本，并返回识别到的编码名。
func Decode(data []byte, fallback string) (string, string) {
	if len(data) == 0 {
		return "", "utf-8"
	}

	// BOM 优先
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		return string(data[3:]), "utf-8"
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		return decodeWith(data[2:], "utf-16le"), "utf-16le"
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		return decodeWith(data[2:], "utf-16be"), "utf-16be"
	}

	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	declared := ""
	if m := metaCharsetRe.FindSubmatch(head); m != nil {
		declared = strings.TrimSpace(strings.ToLower(string(m[1])))
	}
	if declared == "" {
		if m := xmlEncRe.FindSubmatch(head); m != nil {
			declared = strings.TrimSpace(strings.ToLower(string(m[1])))
		}
	}
	// 1) 页面自带声明优先
	if declared != "" {
		if isUTF8Name(declared) {
			if utf8.Valid(data) {
				return string(data), "utf-8"
			}
		} else if enc, err := encodingFor(declared); err == nil {
			if s, ok := tryDecode(data, enc); ok {
				return s, declared
			}
		}
	}

	// 2) 无有效声明：UTF-8 优先，再按 LCID 推断的兜底编码
	if utf8.Valid(data) {
		return string(data), "utf-8"
	}
	if fallback != "" && !isUTF8Name(fallback) {
		if enc, err := encodingFor(fallback); err == nil {
			if s, ok := tryDecode(data, enc); ok && hasNonASCII(s) {
				return s, fallback
			}
		}
	}

	// 3) 字节启发：GBK → Big5 → CP1252
	if s, ok := tryDecode(data, simplifiedchinese.GBK); ok && hasNonASCII(s) {
		return s, "gbk"
	}
	if s, ok := tryDecode(data, traditionalchinese.Big5); ok && hasNonASCII(s) {
		return s, "big5"
	}
	return decodeWith(data, "windows-1252"), "windows-1252"
}

func isUTF8Name(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "utf-8", "utf8", "unicode-1-1-utf-8":
		return true
	}
	return false
}

func encodingFor(name string) (encoding.Encoding, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	switch name {
	case "gb2312", "gbk", "gb18030", "x-gbk", "cp936":
		return simplifiedchinese.GBK, nil
	case "big5", "big-5", "cp950":
		return traditionalchinese.Big5, nil
	case "cp1252", "windows-1252", "iso-8859-1", "latin1", "latin-1":
		return charmap.Windows1252, nil
	case "cp1251", "windows-1251":
		return charmap.Windows1251, nil
	case "cp1250", "windows-1250":
		return charmap.Windows1250, nil
	}
	return htmlindex.Get(name)
}

func decodeWith(data []byte, name string) string {
	enc, err := encodingFor(name)
	if err != nil {
		return string(data)
	}
	if s, ok := tryDecode(data, enc); ok {
		return s
	}
	return string(data)
}

func tryDecode(data []byte, enc encoding.Encoding) (string, bool) {
	out, err := enc.NewDecoder().Bytes(data)
	if err != nil {
		return "", false
	}
	if !utf8.Valid(out) || bytes.ContainsRune(out, '\uFFFD') {
		return "", false
	}
	return string(out), true
}

func hasNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}
