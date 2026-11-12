// Package chm 实现 CHM（ITSF/ITSP）容器解析与 LZX 内容段解压。
//
// 格式参考 Microsoft ITSS/CHM 规范与 libmspack 的行为语义，
// 代码为独立实现，未复制任何 GPL/LGPL 源码。
package chm

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

const (
	headerSize       = 0x38 // ITSF 头
	sectionTableSize = 0x28 // 头部节表（v3）
	hs0Size          = 0x18 // 未压缩内容节头
	hs1Size          = 0x54 // ITSP 内容节头
	pmglHeaderSize   = 0x14 // PMGL 固定头
)

const (
	magicITSF = 0x46535449
	magicITSP = 0x50535449
	magicPMGL = 0x4C474D50
)

// CHM 内部的系统条目名（位于 DataSpace 命名空间中）。
const (
	nameContent = "::DataSpace/Storage/MSCompressed/Content"
	nameControl = "::DataSpace/Storage/MSCompressed/ControlData"
	nameSpan    = "::DataSpace/Storage/MSCompressed/SpanInfo"
	nameReset   = "::DataSpace/Storage/MSCompressed/Transform/{7FC28940-9D31-11D0-9B27-00A0C91E9C7C}/InstanceData/ResetTable"
)

// 安全上限，避免损坏或恶意文件导致资源耗尽。
const (
	maxChunkSize  = 1 << 16
	maxChunkCount = 100000
	maxSection1   = 1 << 31 // 2 GiB
	maxEntryName  = 1 << 12
)

// ErrNotCHM 表示文件不是合法的 CHM。
var ErrNotCHM = fmt.Errorf("不是有效的 CHM 文件")

// Entry 是 CHM 目录中的一条记录。Path 已去掉前导 '/'，统一使用 '/' 分隔。
type Entry struct {
	Path    string // 解码后的显示路径，例如 "index.html"、"images/a.png"
	RawName string // 原始字节串（含前导 '/'），用于系统条目精确匹配
	Section int    // 0=未压缩段，1=LZX 压缩段
	Offset  uint64
	Length  uint64
}

// IsSystem 判断是否为 DataSpace 等系统条目（以 "::" 开头）。
func (e *Entry) IsSystem() bool { return strings.HasPrefix(e.RawName, "/::") }

// Reader 是 CHM 文件的只读访问器。
type Reader struct {
	path string
	f    *os.File

	Version uint32
	LCID    uint32

	sec0      uint64 // 未压缩内容段起始偏移
	dirOff    uint64 // 目录块起始偏移
	chunkSize uint32
	numChunks uint32
	firstPMGL uint32
	lastPMGL  uint32
	indexRoot uint32
	density   uint32

	entries []*Entry
	byPath  map[string]*Entry // key: 小写 Path
	byRaw   map[string]*Entry // key: RawName

	sec1     []byte
	sec1Err  error
	sec1Load bool

	closed bool
}

// Open 打开并解析 CHM 头部与目录。
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r := &Reader{path: path, f: f}
	if err := r.readHeaders(); err != nil {
		_ = f.Close()
		return nil, err
	}
	r.readDirectory()
	return r, nil
}

// Close 释放文件句柄与缓存。
func (r *Reader) Close() error {
	if r == nil || r.closed {
		return nil
	}
	r.closed = true
	r.sec1 = nil
	return r.f.Close()
}

// Path 返回底层文件路径。
func (r *Reader) Path() string { return r.path }

// Entries 返回全部用户可见条目（不含 "::" 系统条目），按目录中的原始顺序。
func (r *Reader) Entries() []*Entry { return r.entries }

// Lookup 按路径查找条目，大小写不敏感。
func (r *Reader) Lookup(path string) *Entry {
	p := normalizePath(path)
	if e, ok := r.byPath[strings.ToLower(p)]; ok {
		return e
	}
	return nil
}

// Has 判断路径是否存在。
func (r *Reader) Has(path string) bool { return r.Lookup(path) != nil }

func (r *Reader) readHeaders() error {
	buf := make([]byte, headerSize)
	if _, err := io.ReadFull(r.f, buf); err != nil {
		return fmt.Errorf("读取 CHM 头部失败: %w", err)
	}
	if binary.LittleEndian.Uint32(buf[0:]) != magicITSF {
		return ErrNotCHM
	}
	r.Version = binary.LittleEndian.Uint32(buf[4:])
	r.LCID = binary.LittleEndian.Uint32(buf[0x14:])

	tbl := make([]byte, sectionTableSize)
	if _, err := io.ReadFull(r.f, tbl); err != nil {
		return fmt.Errorf("读取节表失败: %w", err)
	}
	offsetHS0 := binary.LittleEndian.Uint64(tbl[0x00:])
	offsetHS1 := binary.LittleEndian.Uint64(tbl[0x10:])
	offsetCS0 := binary.LittleEndian.Uint64(tbl[0x20:])

	hs1 := make([]byte, hs1Size)
	if _, err := r.f.ReadAt(hs1, int64(offsetHS1)); err != nil {
		return fmt.Errorf("读取 HS1 失败: %w", err)
	}
	if binary.LittleEndian.Uint32(hs1[0:]) != magicITSP {
		return fmt.Errorf("%w: ITSP 签名不匹配", ErrNotCHM)
	}
	r.chunkSize = binary.LittleEndian.Uint32(hs1[0x10:])
	r.density = binary.LittleEndian.Uint32(hs1[0x14:])
	r.indexRoot = binary.LittleEndian.Uint32(hs1[0x1C:])
	r.firstPMGL = binary.LittleEndian.Uint32(hs1[0x20:])
	r.lastPMGL = binary.LittleEndian.Uint32(hs1[0x24:])
	r.numChunks = binary.LittleEndian.Uint32(hs1[0x2C:])

	// HS0 只用于交叉校验文件长度，缺失时不影响主流程。
	hs0 := make([]byte, hs0Size)
	_, _ = r.f.ReadAt(hs0, int64(offsetHS0))

	r.dirOff = offsetHS1 + hs1Size
	if r.Version < 3 {
		r.sec0 = r.dirOff + uint64(r.chunkSize)*uint64(r.numChunks)
	} else {
		r.sec0 = offsetCS0
	}

	if r.chunkSize < pmglHeaderSize+2 || r.chunkSize > maxChunkSize {
		return fmt.Errorf("%w: 目录块大小异常 (%d)", ErrNotCHM, r.chunkSize)
	}
	if r.numChunks == 0 || r.numChunks > maxChunkCount {
		return fmt.Errorf("%w: 目录块数量异常 (%d)", ErrNotCHM, r.numChunks)
	}
	if r.firstPMGL > r.lastPMGL || r.lastPMGL >= r.numChunks {
		return fmt.Errorf("%w: PMGL 范围异常 (%d-%d/%d)", ErrNotCHM, r.firstPMGL, r.lastPMGL, r.numChunks)
	}
	return nil
}

func (r *Reader) readDirectory() {
	r.byPath = make(map[string]*Entry)
	r.byRaw = make(map[string]*Entry)
	chunk := make([]byte, r.chunkSize)

	for i := r.firstPMGL; i <= r.lastPMGL; i++ {
		off := int64(r.dirOff) + int64(i)*int64(r.chunkSize)
		n, err := r.f.ReadAt(chunk, off)
		if err != nil && err != io.EOF {
			continue
		}
		if n < pmglHeaderSize+2 {
			continue
		}
		if binary.LittleEndian.Uint32(chunk[0:]) != magicPMGL {
			continue
		}
		r.parsePMGL(chunk[:n])
	}
}

func (r *Reader) parsePMGL(chunk []byte) {
	end := len(chunk) - 2
	if end <= pmglHeaderSize {
		return
	}
	numEntries := int(binary.LittleEndian.Uint16(chunk[end:]))
	pos := pmglHeaderSize

	for k := 0; k < numEntries; k++ {
		nameLen, np, ok := readEncInt(chunk, pos, end)
		if !ok || nameLen <= 0 || nameLen > maxEntryName || np+nameLen > end {
			return
		}
		rawName := string(chunk[np : np+nameLen])
		pos = np + nameLen

		section, sp, ok := readEncInt(chunk, pos, end)
		if !ok {
			return
		}
		offset, op, ok := readEncInt(chunk, sp, end)
		if !ok {
			return
		}
		length, lp, ok := readEncInt(chunk, op, end)
		if !ok {
			return
		}
		pos = lp

		e := &Entry{
			Path:    normalizePath(decodeNameBytes([]byte(rawName))),
			RawName: rawName,
			Section: section,
			Offset:  uint64(offset),
			Length:  uint64(length),
		}
		if e.Section > 1 {
			continue
		}
		if e.Offset == 0 && e.Length == 0 && strings.HasSuffix(rawName, "/") {
			continue // 目录项
		}
		if e.Path == "" {
			continue
		}
		r.byRaw[rawName] = e
		if !e.IsSystem() {
			key := strings.ToLower(e.Path)
			if _, dup := r.byPath[key]; !dup {
				r.byPath[key] = e
				r.entries = append(r.entries, e)
			}
		}
	}
}

// ReadEntry 读取单个条目内容。
func (r *Reader) ReadEntry(e *Entry) ([]byte, error) {
	if e == nil {
		return nil, fmt.Errorf("条目为空")
	}
	if e.Section == 0 {
		if e.Length == 0 {
			return []byte{}, nil
		}
		if e.Length > maxSection1 {
			return nil, fmt.Errorf("条目过大: %s (%d 字节)", e.Path, e.Length)
		}
		buf := make([]byte, e.Length)
		if _, err := r.f.ReadAt(buf, int64(r.sec0+e.Offset)); err != nil {
			return nil, fmt.Errorf("读取 %s 失败: %w", e.Path, err)
		}
		return buf, nil
	}
	data, err := r.section1()
	if err != nil {
		return nil, err
	}
	if e.Offset+e.Length > uint64(len(data)) {
		return nil, fmt.Errorf("条目越界: %s (offset=%d len=%d, section=%d)", e.Path, e.Offset, e.Length, len(data))
	}
	return data[e.Offset : e.Offset+e.Length], nil
}

// ReadPath 按路径读取条目内容（大小写不敏感）。
func (r *Reader) ReadPath(path string) ([]byte, error) {
	e := r.Lookup(path)
	if e == nil {
		return nil, fmt.Errorf("CHM 中不存在条目 %q", path)
	}
	return r.ReadEntry(e)
}

// ReadSystem 读取 "::DataSpace/..." 系统条目，主要用于调试与内部解压。
func (r *Reader) ReadSystem(name string) ([]byte, error) {
	e := r.systemEntry(name)
	if e == nil {
		return nil, fmt.Errorf("CHM 中不存在系统条目 %q", name)
	}
	return r.ReadEntry(e)
}

// Section0Offset 返回未压缩内容段起始偏移。
func (r *Reader) Section0Offset() uint64 { return r.sec0 }

// systemEntry 按原始名（不带前导 '/'）查找系统条目。
func (r *Reader) systemEntry(name string) *Entry {
	if e, ok := r.byRaw["/"+name]; ok {
		return e
	}
	if e, ok := r.byRaw[name]; ok {
		return e
	}
	return nil
}

// readEncInt 读取 CHM 的变长整数：每字节低 7 位有效，最高位为续读标志。
func readEncInt(buf []byte, pos, end int) (val int, next int, ok bool) {
	var result uint64
	var c byte = 0x80
	i := 0
	for (c & 0x80) != 0 {
		if pos >= end || i >= 9 {
			return 0, pos, false
		}
		c = buf[pos]
		pos++
		result = (result << 7) | uint64(c&0x7F)
		i++
	}
	if result > 1<<62 {
		return 0, pos, false
	}
	return int(result), pos, true
}

// normalizePath 统一路径分隔符并去掉前导 '/'。
func normalizePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimLeft(p, "/")
	return p
}

// decodeNameBytes 尝试把可能不是 UTF-8 的条目名解码为字符串。
func decodeNameBytes(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	if s, ok := decodeWith(b, simplifiedchinese.GBK); ok {
		return s
	}
	if s, ok := decodeWith(b, traditionalchinese.Big5); ok {
		return s
	}
	// 兜底：按 Latin-1 映射，保证不丢字节。
	rs := make([]rune, len(b))
	for i, c := range b {
		rs[i] = rune(c)
	}
	return string(rs)
}

func decodeWith(b []byte, enc encoding.Encoding) (string, bool) {
	out, err := enc.NewDecoder().Bytes(b)
	if err != nil {
		return "", false
	}
	if !utf8.Valid(out) {
		return "", false
	}
	s := string(out)
	if strings.ContainsRune(s, '\uFFFD') {
		return "", false
	}
	return s, true
}
