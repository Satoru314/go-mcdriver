package chunk

import (
	"fmt"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// Container は paletted container を展開したもの。1 つのセクションのブロック状態（4096 個）か
// バイオーム（64 個）を、ID の並びとして持つ。
//
// 全部が同じ値のときは並びを持たずに値 1 つだけを持つ（空気だけのセクションが多いので、メモリを節約する）。
// 値は 16 ビットに収まる（Format の直接のビット数を 16 以下に限っている）。
type Container struct {
	n      int      // 要素の数（4096 または 64）
	single uint16   // vals が nil のときの、全部の値
	vals   []uint16 // 要素ごとの値。全部同じなら nil
}

// NewUniform は要素 n 個がすべて v の Container を作る。
func NewUniform(n int, v uint16) Container {
	return Container{n: n, single: v}
}

// Len は要素の数を返す。
func (c *Container) Len() int { return c.n }

// Get は i 番目の値を返す。i は範囲の中であること（範囲の外ならパニックする）。
func (c *Container) Get(i int) uint16 {
	if i < 0 || i >= c.n {
		panic(fmt.Sprintf("chunk: Container の範囲の外 %d（要素数 %d）", i, c.n))
	}
	if c.vals == nil {
		return c.single
	}
	return c.vals[i]
}

// Set は i 番目の値を v にし、前の値を返す。
func (c *Container) Set(i int, v uint16) uint16 {
	old := c.Get(i)
	if old == v {
		return old
	}
	if c.vals == nil {
		c.vals = make([]uint16, c.n)
		for j := range c.vals {
			c.vals[j] = c.single
		}
	}
	c.vals[i] = v
	return old
}

// Uniform は全部が同じ値として持っているなら、その値と true を返す。
// （並びを持っていて、たまたま全部同じ場合は false）
func (c *Container) Uniform() (uint16, bool) {
	return c.single, c.vals == nil
}

// Values は全部の値の写しを返す。
func (c *Container) Values() []uint16 {
	out := make([]uint16, c.n)
	for i := range out {
		out[i] = c.Get(i)
	}
	return out
}

// containerKind はパレットの種類。
type containerKind int

const (
	kindSingle   containerKind = iota // 値 1 つ（bits per entry が 0）
	kindIndirect                      // パレットあり。並びの値はパレットの番号
	kindDirect                        // パレットなし。並びの値が ID そのもの
)

// containerRule は bits per entry の決まり（1.21.4）。
type containerRule struct {
	size        int // 要素の数
	maxIndirect int // これ以下ならパレットあり
	minIndirect int // パレットありのときの最小のビット数（これより小さい値はこれに切り上げる）
	directBits  int // パレットなしのときのビット数（送られてきた bits per entry は使わない）
}

// blockRule はブロック状態の決まり: 0 は値 1 つ、1〜4 は 4 ビットのパレット、5〜8 はそのビット数のパレット、
// 9 以上はパレットなしで、ビット数は状態 ID の総数から決まる値（directBits）。
func blockRule(directBits int) containerRule {
	return containerRule{size: SectionBlocks, maxIndirect: 8, minIndirect: 4, directBits: directBits}
}

// biomeRule はバイオームの決まり: 0 は値 1 つ、1〜3 はそのビット数のパレット、
// 4 以上はパレットなしで、ビット数はバイオームの総数から決まる値。
func biomeRule(directBits int) containerRule {
	return containerRule{size: SectionBiomes, maxIndirect: 3, minIndirect: 1, directBits: directBits}
}

// longsFor は要素 n 個を 1 つ bits ビットで詰めたときの long の数を返す。
// 要素は long の境界をまたがない（1 つの long に 64/bits 個まで入れ、余りのビットは使わない）。
func longsFor(n, bits int) int {
	per := 64 / bits
	return (n + per - 1) / per
}

// readContainer は paletted container を読む（1.21.4 の形: bits per entry、パレット、long の数と long の並び）。
func readContainer(d *protocol.Decoder, r containerRule) (Container, error) {
	bpe, err := d.ReadUByte()
	if err != nil {
		return Container{}, fmt.Errorf("bits per entry: %w", err)
	}
	kind, bits := kindSingle, 0
	switch {
	case bpe == 0:
	case int(bpe) <= r.maxIndirect:
		kind, bits = kindIndirect, max(int(bpe), r.minIndirect)
	default:
		kind, bits = kindDirect, r.directBits
	}

	var palette []int32
	var single int32
	switch kind {
	case kindSingle:
		if single, err = d.ReadVarInt(); err != nil {
			return Container{}, fmt.Errorf("パレット（値 1 つ）: %w", err)
		}
		if err := checkID(single, r.directBits); err != nil {
			return Container{}, err
		}
	case kindIndirect:
		palette, err = protocol.ReadPrefixedArray(d, r.size, (*protocol.Decoder).ReadVarInt)
		if err != nil {
			return Container{}, fmt.Errorf("パレット: %w", err)
		}
		for _, id := range palette {
			if err := checkID(id, r.directBits); err != nil {
				return Container{}, err
			}
		}
	}

	n, err := d.ReadLength(d.Remaining() / 8)
	if err != nil {
		return Container{}, fmt.Errorf("long の数: %w", err)
	}
	if kind == kindSingle {
		// 値 1 つのときは並びを使わない（Java 版も読み捨てる）。ふつうは 0 個
		if err := d.Skip(n * 8); err != nil {
			return Container{}, err
		}
		return NewUniform(r.size, uint16(single)), nil
	}
	if want := longsFor(r.size, bits); n != want {
		return Container{}, fmt.Errorf("%w: long の数 %d（%d ビットなら %d）", ErrBadData, n, bits, want)
	}

	c := Container{n: r.size, vals: make([]uint16, r.size)}
	per := 64 / bits
	mask := uint64(1)<<bits - 1
	i := 0
	for range n {
		v, err := d.ReadLong()
		if err != nil {
			return Container{}, err
		}
		u := uint64(v)
		for j := 0; j < per && i < r.size; j++ {
			x := u >> (j * bits) & mask
			if kind == kindIndirect {
				if x >= uint64(len(palette)) {
					return Container{}, fmt.Errorf("%w: %d 番目がパレットの外（%d、パレットは %d 個）", ErrBadData, i, x, len(palette))
				}
				c.vals[i] = uint16(palette[x])
			} else {
				c.vals[i] = uint16(x) // mask で directBits（16 以下）ビットに収まっている
			}
			i++
		}
	}
	return c, nil
}

// checkID は ID が直接のビット数で表せる範囲にあるかを確かめる。
func checkID(id int32, directBits int) error {
	if id < 0 || int64(id) >= 1<<directBits {
		return fmt.Errorf("%w: ID %d が範囲の外（%d ビット）", ErrBadData, id, directBits)
	}
	return nil
}
