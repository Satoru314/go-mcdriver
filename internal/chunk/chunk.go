// Package chunk はチャンクのデータを展開し、ブロック状態 ID と明るさとして持つ。
//
// 扱うもの（Minecraft Java Edition 1.21.4, protocol 769）:
//
//   - Chunk Data and Update Light パケットの中身（ReadChunkPacket）
//   - セクションの並び（ブロック数、ブロック状態とバイオームの paletted container）（DecodeSections）
//   - 明るさ（sky light / block light のマスクと配列）（ReadLightData, Column.ApplyLight）
//   - 1 ブロックの更新（Block Update）とセクション単位の複数更新（Update Section Blocks）
//
// ブロックは状態 ID（整数）のまま持つ。名前や性質への変換は公式データ（data/）の担当。
// パレットなし（direct）のときのビット数は状態 ID の総数から決まるので、Format で外から渡す。
//
// このパッケージの型は鍵をかけない。複数の goroutine から使うときは呼び出し側（state）で守る。
//
// 仕様は https://minecraft.wiki/w/Java_Edition_protocol/Chunk_format を参照する。
package chunk

import (
	"errors"
	"fmt"
)

// セクションの大きさ。
const (
	SectionBlocks = 16 * 16 * 16 // セクションのブロックの数
	SectionBiomes = 4 * 4 * 4    // セクションのバイオームの数（4×4×4 ブロックで 1 つ）
	LightBytes    = 2048         // 1 セクションの明るさの配列のバイト数（1 ブロック 4 ビット）
	// MaxChunkDataSize はチャンクのデータ部分の大きさの上限（Java 版と同じ 2 MiB）
	MaxChunkDataSize = 2 * 1024 * 1024
)

// 読み込みで返すエラー。
var (
	// ErrBadData はデータが仕様と合わない
	ErrBadData = errors.New("chunk: データが正しくない")
	// ErrOutOfRange は座標がチャンクやワールドの外
	ErrOutOfRange = errors.New("chunk: 座標が範囲の外")
	// ErrBadFormat は Format の値が正しくない
	ErrBadFormat = errors.New("chunk: Format が正しくない")
	// ErrNotLoaded はチャンクが読み込まれていない
	ErrNotLoaded = errors.New("chunk: チャンクが読み込まれていない")
)

// Format はチャンクを読むのに外から決める値。
type Format struct {
	// MinY はワールドの一番下の y（オーバーワールドは -64）。16 の倍数
	MinY int
	// Height はワールドの高さ（オーバーワールドは 384）。16 の倍数
	Height int
	// BlockStateBits はブロック状態がパレットなしのときの 1 つあたりのビット数（ceil(log2(状態 ID の総数))）。1〜16
	BlockStateBits int
	// BiomeBits はバイオームがパレットなしのときのビット数（ceil(log2(バイオームの総数))）。1〜16
	BiomeBits int
	// IsAir は状態 ID が空気（空気・洞窟の空気・奈落の空気）かを返す。
	// 指定すると、ブロックの更新のときにセクションのブロック数（空気でないブロックの数）を数え直す。
	// nil なら、ブロック数はサーバーから受け取った値のまま変えない
	IsAir func(state int32) bool
}

// Overworld は 1.21.4 のオーバーワールドの高さを入れた Format を返す。ビット数は公式データから渡す。
func Overworld(blockStateBits, biomeBits int) Format {
	return Format{MinY: -64, Height: 384, BlockStateBits: blockStateBits, BiomeBits: biomeBits}
}

// BitsFor は n 種類の値を表すのに要るビット数 ceil(log2(n)) を返す（Java の Mth.ceillog2）。
// 状態 ID の総数から Format.BlockStateBits を求めるのに使う。
func BitsFor(n int) int {
	b := 0
	for 1<<b < n {
		b++
	}
	return b
}

// Sections はセクションの数を返す。
func (f Format) Sections() int { return f.Height / 16 }

// LightSections は明るさのセクションの数を返す（ワールドの上下に 1 つずつ多い）。
func (f Format) LightSections() int { return f.Height/16 + 2 }

func (f Format) validate() error {
	switch {
	case f.Height <= 0 || f.Height%16 != 0 || f.Height > 4064:
		return fmt.Errorf("%w: Height %d", ErrBadFormat, f.Height)
	case f.MinY%16 != 0:
		return fmt.Errorf("%w: MinY %d", ErrBadFormat, f.MinY)
	case f.BlockStateBits < 1 || f.BlockStateBits > 16:
		return fmt.Errorf("%w: BlockStateBits %d", ErrBadFormat, f.BlockStateBits)
	case f.BiomeBits < 1 || f.BiomeBits > 16:
		return fmt.Errorf("%w: BiomeBits %d", ErrBadFormat, f.BiomeBits)
	}
	return nil
}

// Section は 16×16×16 ブロックのセクション 1 つ。
type Section struct {
	// BlockCount は空気でないブロックの数（サーバーから受け取った値。Format.IsAir があれば更新のたびに数え直す）
	BlockCount int16
	// Blocks はブロック状態 ID。番号は (y*16+z)*16+x（x, y, z はセクションの中の 0〜15）
	Blocks Container
	// Biomes はバイオーム ID。番号は (y*4+z)*4+x（x, y, z は 4 ブロックごとの 0〜3）
	Biomes Container
}

// BlockIndex はセクションの中の座標（0〜15）からブロックの番号を求める。
func BlockIndex(x, y, z int) int { return (y*16+z)*16 + x }

// BiomeIndex はセクションの中のバイオームの座標（0〜3）から番号を求める。
func BiomeIndex(x, y, z int) int { return (y*4+z)*4 + x }

// Block はセクションの中の座標（0〜15）のブロック状態 ID を返す。
func (s *Section) Block(x, y, z int) int32 {
	return int32(s.Blocks.Get(BlockIndex(x, y, z)))
}

// Biome はセクションの中のブロックの座標（0〜15）にあるバイオーム ID を返す。
func (s *Section) Biome(x, y, z int) int32 {
	return int32(s.Biomes.Get(BiomeIndex(x>>2, y>>2, z>>2)))
}
