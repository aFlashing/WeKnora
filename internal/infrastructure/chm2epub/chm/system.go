package chm

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// SystemInfo 是 /#SYSTEM 中本工具关心的字段。
type SystemInfo struct {
	Title        string // code 3：标题
	DefaultTopic string // code 2：默认主题
	HHC          string // code 0：目录文件名
	HHK          string // code 1：索引文件名
	LCID         uint32 // code 4
	Compiler     string // code 9：编译器版本
}

// System 解析 /#SYSTEM。
func (r *Reader) System() (*SystemInfo, error) {
	e := r.systemEntry("#SYSTEM")
	if e == nil {
		return nil, fmt.Errorf("CHM 缺少 /#SYSTEM")
	}
	data, err := r.ReadEntry(e)
	if err != nil {
		return nil, err
	}
	si := &SystemInfo{LCID: r.LCID}
	pos := 4
	for pos+4 <= len(data) {
		code := binary.LittleEndian.Uint16(data[pos:])
		length := int(binary.LittleEndian.Uint16(data[pos+2:]))
		pos += 4
		if length < 0 || pos+length > len(data) {
			break
		}
		val := strings.TrimRight(string(data[pos:pos+length]), "\x00")
		pos += length

		switch code {
		case 0:
			si.HHC = normalizePath(val)
		case 1:
			si.HHK = normalizePath(val)
		case 2:
			si.DefaultTopic = normalizePath(val)
		case 3:
			si.Title = val
		case 4:
			if length >= 4 {
				si.LCID = binary.LittleEndian.Uint32(data[pos-length:])
			}
		case 9:
			si.Compiler = val
		}
	}
	if si.LCID == 0 {
		si.LCID = r.LCID
	}
	return si, nil
}

// Title 返回标题，缺失时返回空串。
func (r *Reader) Title() string {
	if si, err := r.System(); err == nil {
		return si.Title
	}
	return ""
}

// codepageTable 把 Windows LCID 映射到 HTML 常用的编码名。
var codepageTable = map[uint32]string{
	0x0804: "gbk",        // 简体中文
	0x1004: "gbk",        // 新加坡简体
	0x0404: "big5",       // 繁体中文（台湾）
	0x0C04: "big5",       // 繁体中文（香港）
	0x0411: "shift_jis",  // 日语
	0x0412: "euc-kr",     // 韩语
	0x0419: "windows-1251", // 俄语
	0x0405: "windows-1250", // 捷克语
	0x0415: "windows-1250", // 波兰语
	0x040E: "windows-1250", // 匈牙利语
	0x041A: "windows-1250", // 克罗地亚语
	0x041B: "windows-1250", // 斯洛伐克语
	0x0424: "windows-1250", // 斯洛文尼亚语
	0x041F: "windows-1254", // 土耳其语
	0x0408: "windows-1253", // 希腊语
	0x040D: "windows-1255", // 希伯来语
	0x0401: "windows-1256", // 阿拉伯语
	0x0425: "windows-1257", // 爱沙尼亚语
	0x0427: "windows-1257", // 立陶宛语
	0x0426: "windows-1257", // 拉脱维亚语
}

// CodepageForLCID 依据 LCID 推断默认编码，未识别时返回 windows-1252。
func CodepageForLCID(lcid uint32) string {
	if cp, ok := codepageTable[lcid]; ok {
		return cp
	}
	return "windows-1252"
}

// LanguageForLCID 依据 LCID 推断 BCP-47 语言标签，用于 EPUB 的 dc:language。
func LanguageForLCID(lcid uint32) string {
	primary := lcid & 0x3FF
	sub := (lcid >> 10) & 0x3F
	switch primary {
	case 0x04:
		switch sub {
		case 0x01:
			return "zh-Hans"
		case 0x02:
			return "zh-Hant"
		case 0x03:
			return "zh-Hant"
		default:
			return "zh"
		}
	case 0x09:
		return "en"
	case 0x11:
		return "ja"
	case 0x12:
		return "ko"
	case 0x19:
		return "ru"
	case 0x07:
		return "de"
	case 0x0C:
		return "fr"
	case 0x0A:
		return "es"
	case 0x10:
		return "it"
	case 0x15:
		return "pl"
	case 0x05:
		return "cs"
	case 0x0E:
		return "hu"
	case 0x1F:
		return "tr"
	case 0x08:
		return "el"
	case 0x0D:
		return "he"
	case 0x01:
		return "ar"
	case 0x16:
		return "pt"
	case 0x13:
		return "nl"
	case 0x1D:
		return "sv"
	case 0x06:
		return "da"
	case 0x0B:
		return "fi"
	case 0x14:
		return "no"
	}
	return "und"
}
