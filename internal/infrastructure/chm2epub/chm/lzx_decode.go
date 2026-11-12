package chm

import (
	"errors"
	"fmt"
)

// 本文件是 LZX 解码器的独立实现，按 LZX 规范（Microsoft LZX Data Compression
// Format）与 CHM 的用法语义编写：
//   - 比特流按 16 位小端字组织，从高位到低位读取；
//   - 每 32768 字节为一帧，帧末对齐到 16 位；
//   - 每个 reset interval 起点重新读取 1 bit 的 Intel 头并重置 Huffman 状态。

const (
	lzxBlockVerbatim     = 1
	lzxBlockAligned      = 2
	lzxBlockUncompressed = 3

	lzxFrameSize = 32768

	lzxNumChars            = 256
	lzxNumPrimaryLengths   = 7
	lzxMinMatch            = 2
	lzxNumSecondaryLengths = 249
	lzxMaxMatch            = lzxNumPrimaryLengths + 249 + lzxMinMatch
)

var lzxPositionSlots = [11]int{30, 32, 34, 36, 38, 42, 50, 66, 98, 162, 290}

var lzxExtraBits = [36]int{
	0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8,
	9, 9, 10, 10, 11, 11, 12, 12, 13, 13, 14, 14, 15, 15, 16, 16,
}

var lzxPositionBase [290]int

func init() {
	for i := 1; i < len(lzxPositionBase); i++ {
		eb := 17
		if i-1 < len(lzxExtraBits) {
			eb = lzxExtraBits[i-1]
		}
		lzxPositionBase[i] = lzxPositionBase[i-1] + (1 << eb)
	}
}

var (
	errLZXBadBlock   = errors.New("LZX 块类型无效")
	errLZXHuffman    = errors.New("LZX Huffman 表无效")
	errLZXTruncated  = errors.New("LZX 数据不足")
	errLZXMatchRange = errors.New("LZX 匹配距离越界")
)

// lzxBits 是 MSB 优先、以 16 位小端字为单位的比特读取器。
type lzxBits struct {
	data []byte
	pos  int    // 下一个待加载字节
	n    uint   // 缓冲区中有效比特数
	acc  uint64 // 比特缓冲，低位对齐
}

func newLZXBits(data []byte) *lzxBits { return &lzxBits{data: data} }

func (b *lzxBits) ensure(k uint) {
	for b.n < k {
		if b.pos+1 < len(b.data) {
			w := uint64(b.data[b.pos]) | uint64(b.data[b.pos+1])<<8
			b.acc = b.acc<<16 | w
			b.n += 16
			b.pos += 2
			continue
		}
		if b.pos < len(b.data) {
			w := uint64(b.data[b.pos])
			b.acc = b.acc<<16 | w
			b.n += 16
			b.pos++
			continue
		}
		// 输入耗尽，补零，由上层按总长度判断是否真的出错。
		b.acc <<= 16
		b.n += 16
	}
}

func (b *lzxBits) read(k uint) uint32 {
	if k == 0 {
		return 0
	}
	b.ensure(k)
	shift := b.n - k
	v := uint32((b.acc >> shift) & (uint64(1)<<k - 1))
	b.n -= k
	if b.n == 0 {
		b.acc = 0
	} else {
		b.acc &= uint64(1)<<b.n - 1
	}
	return v
}

// align16 丢弃当前 16 位字内的剩余比特。
func (b *lzxBits) align16() {
	b.n -= b.n % 16
	if b.n == 0 {
		b.acc = 0
	} else {
		b.acc &= uint64(1)<<b.n - 1
	}
}

// discardToByte 丢弃全部缓冲，切换到原始字节读取（未压缩块）。
func (b *lzxBits) discardToByte() { b.n, b.acc = 0, 0 }

// raw 读取 n 个原始字节，前提是缓冲区已清空。
func (b *lzxBits) raw(n int) ([]byte, error) {
	if n < 0 {
		return nil, errLZXTruncated
	}
	if b.pos+n > len(b.data) {
		return nil, errLZXTruncated
	}
	out := b.data[b.pos : b.pos+n]
	b.pos += n
	return out, nil
}

// skip 跳过 n 个原始字节。
func (b *lzxBits) skip(n int) error {
	_, err := b.raw(n)
	return err
}

// huffTree 是规范 Huffman 表，按码长逐位解码。
type huffTree struct {
	count  [17]int
	first  [17]uint32
	offset [17]int
	syms   []uint16
	empty  bool
}

func buildHuff(lengths []uint8) (*huffTree, error) {
	t := &huffTree{syms: make([]uint16, len(lengths))}
	maxLen := uint8(0)
	for _, l := range lengths {
		if l > 16 {
			return nil, errLZXHuffman
		}
		if l > maxLen {
			maxLen = l
		}
		if l > 0 {
			t.count[l]++
		}
	}
	if maxLen == 0 {
		t.empty = true
		return t, nil
	}

	left := 1
	for l := 1; l <= 16; l++ {
		left <<= 1
		left -= t.count[l]
		if left < 0 {
			return nil, errLZXHuffman
		}
	}

	code := uint32(0)
	idx := 0
	for l := 1; l <= 16; l++ {
		t.first[l] = code
		t.offset[l] = idx
		code = (code + uint32(t.count[l])) << 1
		idx += t.count[l]
	}

	next := t.offset
	for sym, l := range lengths {
		if l != 0 {
			t.syms[next[l]] = uint16(sym)
			next[l]++
		}
	}
	return t, nil
}

func (t *huffTree) decode(b *lzxBits) (int, error) {
	if t.empty {
		return 0, errLZXHuffman
	}
	code := uint32(0)
	for l := 1; l <= 16; l++ {
		code = code<<1 | b.read(1)
		c := t.count[l]
		if c == 0 {
			continue
		}
		if idx := code - t.first[l]; idx < uint32(c) {
			return int(t.syms[t.offset[l]+int(idx)]), nil
		}
	}
	return 0, errLZXHuffman
}

// lzxDecoder 顺序解出整个压缩段。
type lzxDecoder struct {
	bits          *lzxBits
	windowBits    int
	numOffsets    int
	resetInterval int

	mainLens   []uint8
	lengthLens []uint8
	mainTree   *huffTree
	lengthTree *huffTree
	alignedTree *huffTree

	R0, R1, R2 int

	blockType      int
	blockRemaining int
	pendingOddSkip bool

	intelStarted bool
	intelSize    uint32

	out []byte
}

// decompressLZX 解压 CHM 压缩段。data 为原始压缩流，total 为需要的解压字节数。
func decompressLZX(data []byte, total int, windowBits, resetInterval int) ([]byte, error) {
	if total <= 0 {
		return []byte{}, nil
	}
	if windowBits < 15 || windowBits > 21 {
		return nil, fmt.Errorf("LZX 窗口位数非法: %d", windowBits)
	}
	if resetInterval <= 0 || resetInterval%lzxFrameSize != 0 {
		resetInterval = 1 << windowBits
		if resetInterval%lzxFrameSize != 0 {
			resetInterval = lzxFrameSize
		}
	}

	d := &lzxDecoder{
		bits:          newLZXBits(data),
		windowBits:    windowBits,
		resetInterval: resetInterval,
		mainLens:      make([]uint8, lzxNumChars+lzxPositionSlots[windowBits-15]*8),
		lengthLens:    make([]uint8, lzxNumSecondaryLengths),
		out:           make([]byte, 0, total),
	}
	d.numOffsets = lzxPositionSlots[windowBits-15] << 3

	if err := d.run(total); err != nil {
		return nil, err
	}
	return d.out, nil
}

func (d *lzxDecoder) resetState() {
	d.R0, d.R1, d.R2 = 1, 1, 1
	d.blockRemaining = 0
	d.blockType = 0
	d.mainTree, d.lengthTree, d.alignedTree = nil, nil, nil
	for i := range d.mainLens {
		d.mainLens[i] = 0
	}
	for i := range d.lengthLens {
		d.lengthLens[i] = 0
	}
}

func (d *lzxDecoder) run(total int) error {
	for len(d.out) < total {
		if len(d.out)%d.resetInterval == 0 {
			d.resetState()
			if d.bits.read(1) == 1 {
				hi := d.bits.read(16)
				lo := d.bits.read(16)
				d.intelSize = hi<<16 | lo
				d.intelStarted = true
			} else {
				d.intelSize = 0
				d.intelStarted = false
			}
		}

		frameEnd := (len(d.out)/lzxFrameSize + 1) * lzxFrameSize
		if frameEnd > total {
			frameEnd = total
		}

		for len(d.out) < frameEnd {
			if d.blockRemaining == 0 {
				if err := d.readBlock(); err != nil {
					return err
				}
			}
			if err := d.decodeChunk(frameEnd - len(d.out)); err != nil {
				return err
			}
		}

		if len(d.out)%lzxFrameSize == 0 {
			d.bits.align16()
		}
	}
	return nil
}

func (d *lzxDecoder) readBlock() error {
	if d.pendingOddSkip {
		d.pendingOddSkip = false
		if err := d.bits.skip(1); err != nil {
			return err
		}
	}

	blockType := int(d.bits.read(3))
	length := int(d.bits.read(16))<<8 | int(d.bits.read(8))
	if length < 0 {
		return errLZXBadBlock
	}

	switch blockType {
	case lzxBlockUncompressed:
		d.bits.align16()
		d.bits.discardToByte()
		raw, err := d.bits.raw(12)
		if err != nil {
			return err
		}
		r0 := int(uint32(raw[0]) | uint32(raw[1])<<8 | uint32(raw[2])<<16 | uint32(raw[3])<<24)
		r1 := int(uint32(raw[4]) | uint32(raw[5])<<8 | uint32(raw[6])<<16 | uint32(raw[7])<<24)
		r2 := int(uint32(raw[8]) | uint32(raw[9])<<8 | uint32(raw[10])<<16 | uint32(raw[11])<<24)
		limit := 1 << d.windowBits
		if r0 >= limit || r1 >= limit || r2 >= limit {
			return errLZXMatchRange
		}
		d.R0, d.R1, d.R2 = r0, r1, r2
		d.intelStarted = true
		if length%2 == 1 {
			d.pendingOddSkip = true
		}

	case lzxBlockAligned:
		var alen [8]uint8
		for i := range alen {
			alen[i] = uint8(d.bits.read(3))
		}
		tree, err := buildHuff(alen[:])
		if err != nil {
			return err
		}
		d.alignedTree = tree
		if err := d.readBlockTrees(); err != nil {
			return err
		}

	case lzxBlockVerbatim:
		if err := d.readBlockTrees(); err != nil {
			return err
		}

	default:
		return errLZXBadBlock
	}

	d.blockType = blockType
	d.blockRemaining = length
	return nil
}

func (d *lzxDecoder) readBlockTrees() error {
	if err := d.readLens(d.mainLens, 0, lzxNumChars); err != nil {
		return err
	}
	if err := d.readLens(d.mainLens, lzxNumChars, lzxNumChars+d.numOffsets); err != nil {
		return err
	}
	main, err := buildHuff(d.mainLens)
	if err != nil {
		return err
	}
	d.mainTree = main
	if d.mainLens[0xE8] != 0 {
		d.intelStarted = true
	}

	if err := d.readLens(d.lengthLens, 0, lzxNumSecondaryLengths); err != nil {
		return err
	}
	length, err := buildHuff(d.lengthLens)
	if err != nil {
		return err
	}
	d.lengthTree = length
	return nil
}

func (d *lzxDecoder) readLens(lens []uint8, first, last int) error {
	var pretreeLens [20]uint8
	for i := 0; i < 20; i++ {
		pretreeLens[i] = uint8(d.bits.read(4))
	}
	pre, err := buildHuff(pretreeLens[:])
	if err != nil {
		return err
	}
	if pre.empty {
		return errLZXHuffman
	}

	if last > len(lens) {
		last = len(lens)
	}
	for x := first; x < last; {
		z, err := pre.decode(d.bits)
		if err != nil {
			return err
		}
		switch z {
		case 17:
			y := int(d.bits.read(4)) + 4
			for ; y > 0 && x < last; y-- {
				lens[x] = 0
				x++
			}
		case 18:
			y := int(d.bits.read(5)) + 20
			for ; y > 0 && x < last; y-- {
				lens[x] = 0
				x++
			}
		case 19:
			y := int(d.bits.read(1)) + 4
			z2, err := pre.decode(d.bits)
			if err != nil {
				return err
			}
			v := int(lens[x]) - z2
			if v < 0 {
				v += 17
			}
			for ; y > 0 && x < last; y-- {
				lens[x] = uint8(v)
				x++
			}
		default:
			v := int(lens[x]) - z
			if v < 0 {
				v += 17
			}
			lens[x] = uint8(v)
			x++
		}
	}
	return nil
}

func (d *lzxDecoder) decodeChunk(need int) error {
	if need <= 0 {
		return nil
	}
	run := need
	if run > d.blockRemaining {
		run = d.blockRemaining
	}

	if d.blockType == lzxBlockUncompressed {
		raw, err := d.bits.raw(run)
		if err != nil {
			return err
		}
		d.out = append(d.out, raw...)
		d.blockRemaining -= run
		return nil
	}

	start := len(d.out)
	for len(d.out)-start < run {
		if err := d.decodeElement(); err != nil {
			return err
		}
	}
	d.blockRemaining -= len(d.out) - start
	if d.blockRemaining < 0 {
		return errLZXBadBlock
	}
	return nil
}

func (d *lzxDecoder) decodeElement() error {
	if d.mainTree == nil {
		return errLZXHuffman
	}
	main, err := d.mainTree.decode(d.bits)
	if err != nil {
		return err
	}

	if main < lzxNumChars {
		d.out = append(d.out, byte(main))
		return nil
	}

	main -= lzxNumChars
	matchLength := main & lzxNumPrimaryLengths
	if matchLength == lzxNumPrimaryLengths {
		if d.lengthTree == nil || d.lengthTree.empty {
			return errLZXHuffman
		}
		extra, err := d.lengthTree.decode(d.bits)
		if err != nil {
			return err
		}
		matchLength += extra
	}
	matchLength += lzxMinMatch

	slot := main >> 3
	var offset int
	switch slot {
	case 0:
		offset = d.R0
	case 1:
		offset = d.R1
		d.R1, d.R0 = d.R0, offset
	case 2:
		offset = d.R2
		d.R2, d.R0 = d.R0, offset
	default:
		extra := 17
		if slot < len(lzxExtraBits) {
			extra = lzxExtraBits[slot]
		}
		offset = lzxPositionBase[slot] - 2
		if extra >= 3 && d.blockType == lzxBlockAligned {
			if extra > 3 {
				offset += int(d.bits.read(uint(extra-3))) << 3
			}
			if d.alignedTree == nil {
				return errLZXHuffman
			}
			ab, err := d.alignedTree.decode(d.bits)
			if err != nil {
				return err
			}
			offset += ab
		} else if extra > 0 {
			offset += int(d.bits.read(uint(extra)))
		}
		d.R2, d.R1, d.R0 = d.R1, d.R0, offset
	}

	if offset <= 0 || offset > len(d.out) || offset > 1<<d.windowBits {
		return errLZXMatchRange
	}

	src := len(d.out) - offset
	for i := 0; i < matchLength; i++ {
		d.out = append(d.out, d.out[src+i])
	}
	return nil
}
