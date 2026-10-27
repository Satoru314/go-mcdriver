package chunk

import (
	"fmt"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// ReadBlockUpdate は Block Update パケットの中身（Location (Position), Block ID (VarInt)）を読む。
func ReadBlockUpdate(d *protocol.Decoder) (protocol.Position, int32, error) {
	p, err := d.ReadPosition()
	if err != nil {
		return p, 0, err
	}
	id, err := d.ReadVarInt()
	return p, id, err
}

// SectionPos はセクションの座標（ブロックの座標を 16 で割って下に丸めたもの）。
type SectionPos struct {
	X, Y, Z int
}

// UnpackSectionPos は 64 ビットに詰めたセクションの座標をほどく。
// x は上位 22 ビット、z は次の 22 ビット、y は下位 20 ビット（どれも 2 の補数）。
func UnpackSectionPos(v int64) SectionPos {
	return SectionPos{
		X: int(v >> 42),
		Y: int(v << 44 >> 44),
		Z: int(v << 22 >> 42),
	}
}

// Pack はセクションの座標を 64 ビットに詰める（テスト用。範囲の確認はしない）。
func (p SectionPos) Pack() int64 {
	return int64(uint64(int64(p.X))&0x3FFFFF<<42 | uint64(int64(p.Z))&0x3FFFFF<<20 | uint64(int64(p.Y))&0xFFFFF)
}

// PackBlockChange は Update Section Blocks の 1 要素（VarLong）を作る: 状態 ID << 12 | x << 8 | z << 4 | y。
func PackBlockChange(c BlockChange) int64 {
	return int64(c.State)<<12 | int64(c.X&15)<<8 | int64(c.Z&15)<<4 | int64(c.Y&15)
}

// UnpackBlockChange は Update Section Blocks の 1 要素をほどく。
func UnpackBlockChange(v int64) BlockChange {
	return BlockChange{
		X:     int(v >> 8 & 15),
		Z:     int(v >> 4 & 15),
		Y:     int(v & 15),
		State: int32(v >> 12),
	}
}

// ReadSectionUpdate は Update Section Blocks パケットの中身を読む。
//
//	Chunk Section Position (Long), Blocks (Prefixed Array of VarLong)
func ReadSectionUpdate(d *protocol.Decoder) (SectionPos, []BlockChange, error) {
	v, err := d.ReadLong()
	if err != nil {
		return SectionPos{}, nil, err
	}
	// 1 セクションに 4096 ブロックしかないので、それより多い要素数はおかしい
	raw, err := protocol.ReadPrefixedArray(d, SectionBlocks, (*protocol.Decoder).ReadVarLong)
	if err != nil {
		return SectionPos{}, nil, err
	}
	changes := make([]BlockChange, len(raw))
	for i, r := range raw {
		changes[i] = UnpackBlockChange(r)
		if r>>12 != int64(changes[i].State) {
			return SectionPos{}, nil, fmt.Errorf("%w: 状態 ID %d が大きすぎる", ErrBadData, r>>12)
		}
	}
	return UnpackSectionPos(v), changes, nil
}

// World は読み込まれたチャンクの集まり。ブロックの座標（ワールドの座標）で読み書きする。
// 鍵はかけない（呼び出し側で守る）。
type World struct {
	Format Format
	cols   map[[2]int32]*Column
}

// NewWorld は空の World を作る。
func NewWorld(f Format) (*World, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	return &World{Format: f, cols: map[[2]int32]*Column{}}, nil
}

// Put はチャンクを入れる（同じ座標にあれば差し替える）。
func (w *World) Put(c *Column) { w.cols[[2]int32{c.X, c.Z}] = c }

// Unload はチャンクを捨てる（Unload Chunk パケット）。
func (w *World) Unload(x, z int32) { delete(w.cols, [2]int32{x, z}) }

// Column はチャンクの座標のチャンクを返す。なければ nil。
func (w *World) Column(x, z int32) *Column { return w.cols[[2]int32{x, z}] }

// Len は読み込まれているチャンクの数を返す。
func (w *World) Len() int { return len(w.cols) }

func (w *World) columnAt(x, z int) (*Column, error) {
	c := w.cols[[2]int32{int32(x >> 4), int32(z >> 4)}]
	if c == nil {
		return nil, fmt.Errorf("%w: ブロック (%d, %d)", ErrNotLoaded, x, z)
	}
	return c, nil
}

// Block はワールドの座標のブロック状態 ID を返す。
func (w *World) Block(x, y, z int) (int32, error) {
	c, err := w.columnAt(x, z)
	if err != nil {
		return 0, err
	}
	return c.Block(x&15, y, z&15)
}

// SetBlock はワールドの座標のブロック状態 ID を変える（Block Update の反映）。
// チャンクが読み込まれていなければ ErrNotLoaded（Java 版は無視する。呼び出し側で無視してよい）。
func (w *World) SetBlock(x, y, z int, state int32) error {
	c, err := w.columnAt(x, z)
	if err != nil {
		return err
	}
	return c.SetBlock(x&15, y, z&15, state)
}

// ApplySectionUpdate は Update Section Blocks を反映する。
func (w *World) ApplySectionUpdate(p SectionPos, changes []BlockChange) error {
	c := w.cols[[2]int32{int32(p.X), int32(p.Z)}]
	if c == nil {
		return fmt.Errorf("%w: チャンク (%d, %d)", ErrNotLoaded, p.X, p.Z)
	}
	return c.ApplySectionUpdate(p.Y, changes)
}
