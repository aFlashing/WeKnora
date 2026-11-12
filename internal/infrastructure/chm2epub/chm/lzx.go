package chm

import (
	"encoding/binary"
	"fmt"
)

const lzxFrameUnit = 0x8000

// controlData 是 MSCompressed/ControlData 的解析结果，字段单位已归一为字节。
type controlData struct {
	Version       uint32
	ResetInterval uint32
	WindowSize    uint32
}

func (r *Reader) readControl() (*controlData, error) {
	e := r.systemEntry(nameControl)
	if e == nil {
		return nil, fmt.Errorf("CHM 缺少 MSCompressed/ControlData")
	}
	data, err := r.ReadEntry(e)
	if err != nil {
		return nil, err
	}
	if len(data) < 0x14 {
		return nil, fmt.Errorf("ControlData 长度异常: %d", len(data))
	}
	if binary.LittleEndian.Uint32(data[4:]) != 0x43585A4C { // "LZXC"
		return nil, fmt.Errorf("ControlData 签名不匹配")
	}
	c := &controlData{Version: binary.LittleEndian.Uint32(data[8:])}
	switch c.Version {
	case 1:
		c.ResetInterval = binary.LittleEndian.Uint32(data[0x0C:])
		c.WindowSize = binary.LittleEndian.Uint32(data[0x10:])
	case 2:
		c.ResetInterval = binary.LittleEndian.Uint32(data[0x0C:]) * lzxFrameUnit
		c.WindowSize = binary.LittleEndian.Uint32(data[0x10:]) * lzxFrameUnit
	default:
		return nil, fmt.Errorf("不支持的 LZX 控制数据版本 %d", c.Version)
	}
	if c.WindowSize < 1<<15 || c.WindowSize > 1<<25 {
		return nil, fmt.Errorf("LZX 窗口大小异常: %d", c.WindowSize)
	}
	if c.ResetInterval == 0 {
		c.ResetInterval = c.WindowSize
	}
	return c, nil
}

// ContentHead 返回压缩流开头的若干字节，用于调试与自检。
func (r *Reader) ContentHead(n int) []byte {
	e := r.systemEntry(nameContent)
	if e == nil {
		return nil
	}
	if uint64(n) > e.Length {
		n = int(e.Length)
	}
	buf := make([]byte, n)
	if _, err := r.f.ReadAt(buf, int64(r.sec0+e.Offset)); err != nil {
		return buf
	}
	return buf
}

// ControlInfo 返回 LZX 窗口大小、重置间隔（字节）。
func (r *Reader) ControlInfo() (windowSize, resetInterval uint32, err error) {
	c, err := r.readControl()
	if err != nil {
		return 0, 0, err
	}
	return c.WindowSize, c.ResetInterval, nil
}

// section1 返回压缩段解压后的完整内容（惰性执行，结果缓存）。
func (r *Reader) section1() ([]byte, error) {
	if !r.sec1Load {
		r.sec1Load = true
		r.sec1, r.sec1Err = r.decompressSection1()
	}
	return r.sec1, r.sec1Err
}

func (r *Reader) decompressSection1() ([]byte, error) {
	var need uint64
	for _, e := range r.entries {
		if e.Section == 1 && e.Offset+e.Length > need {
			need = e.Offset + e.Length
		}
	}
	if need == 0 {
		return []byte{}, nil
	}
	if need > maxSection1 {
		return nil, fmt.Errorf("压缩段解压后过大: %d 字节", need)
	}

	content := r.systemEntry(nameContent)
	if content == nil {
		return nil, fmt.Errorf("CHM 缺少 MSCompressed/Content")
	}
	if content.Length == 0 {
		return nil, fmt.Errorf("CHM 压缩流为空")
	}
	ctl, err := r.readControl()
	if err != nil {
		return nil, err
	}

	target := need
	if span := r.spanSize(); span > 0 && span < target {
		target = span
	}

	compressed := make([]byte, content.Length)
	if _, err := r.f.ReadAt(compressed, int64(r.sec0+content.Offset)); err != nil {
		return nil, fmt.Errorf("读取压缩流失败: %w", err)
	}

	windowBits := 0
	for size := ctl.WindowSize; size > 1; size >>= 1 {
		windowBits++
	}
	return decompressLZX(compressed, int(target), windowBits, int(ctl.ResetInterval))
}

// spanSize 读取 SpanInfo 中记录的压缩段解压后长度。
func (r *Reader) spanSize() uint64 {
	e := r.systemEntry(nameSpan)
	if e == nil || e.Length < 8 {
		return 0
	}
	data, err := r.ReadEntry(&Entry{RawName: e.RawName, Section: e.Section, Offset: e.Offset, Length: 8})
	if err != nil || len(data) < 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(data)
}
