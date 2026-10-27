package protocol

import (
	"encoding/hex"
	"fmt"
	"unicode/utf8"
)

// 文字列の最大長（仕様の「String (n)」の n）。単位は UTF-16 のコード単位。
const (
	// MaxStringLen は最大長の指定がない文字列の上限（String (32767)）
	MaxStringLen = 32767
	// MaxIdentifierLen は Identifier の最大長
	MaxIdentifierLen = 32767
)

// utf16Len は s を UTF-16 にしたときのコード単位の数を返す。
// 仕様の文字列の最大長は Java の String.length()（UTF-16 のコード単位）で数える。
// U+10000 以上の文字はサロゲートペアで 2 と数える。正しくない UTF-8 のバイトは 1 と数える。
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 && r <= utf8.MaxRune {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// checkStringLen は文字列の長さが最大長 max 以内かを調べる。
// バイト数が max*3 を超えるか、UTF-16 のコード単位が max を超えたら ErrStringTooLong。
func checkStringLen(s string, max int) error {
	if len(s) > max*3 {
		return fmt.Errorf("%w: %d バイト（上限 %d バイト）", ErrStringTooLong, len(s), max*3)
	}
	if n := utf16Len(s); n > max {
		return fmt.Errorf("%w: %d 文字（上限 %d 文字）", ErrStringTooLong, n, max)
	}
	return nil
}

// ValidIdentifier は s が Identifier（名前空間:値）の書式として正しいかを返す。
// 名前空間は [a-z0-9._-]、値は [a-z0-9._-/] の文字だけからなる。
// 「:」がなければ名前空間 minecraft の値とみなす。名前空間と値は空でもよい（Java の実装に合わせる）。
func ValidIdentifier(s string) bool {
	ns, path := "minecraft", s
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			ns, path = s[:i], s[i+1:]
			break
		}
	}
	for i := 0; i < len(ns); i++ {
		c := ns[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	for i := 0; i < len(path); i++ {
		c := path[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '/') {
			return false
		}
	}
	return true
}

// UUID は 128 ビットの UUID。通信では上位 64 ビット、下位 64 ビットの順にビッグエンディアンで送る。
type UUID [16]byte

// String は UUID を「xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx」の形にする。
func (u UUID) String() string {
	var b [36]byte
	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:36], u[10:16])
	return string(b[:])
}

// Position はブロックの座標。通信では 64 ビットに詰めて送る。
// x は上位 26 ビット、z は次の 26 ビット、y は下位 12 ビット（どれも 2 の補数）。
type Position struct {
	X, Y, Z int
}

// Position の各成分が表せる範囲。
const (
	PositionMinXZ = -1 << 25  // -33554432
	PositionMaxXZ = 1<<25 - 1 // 33554431
	PositionMinY  = -1 << 11  // -2048
	PositionMaxY  = 1<<11 - 1 // 2047
)

// Pack は座標を 64 ビットに詰める。範囲の外の成分があれば ErrOutOfRange を返す。
func (p Position) Pack() (int64, error) {
	if p.X < PositionMinXZ || p.X > PositionMaxXZ ||
		p.Z < PositionMinXZ || p.Z > PositionMaxXZ ||
		p.Y < PositionMinY || p.Y > PositionMaxY {
		return 0, fmt.Errorf("%w: Position %v", ErrOutOfRange, p)
	}
	v := (uint64(int64(p.X))&0x3FFFFFF)<<38 |
		(uint64(int64(p.Z))&0x3FFFFFF)<<12 |
		uint64(int64(p.Y))&0xFFF
	return int64(v), nil
}

// UnpackPosition は 64 ビットに詰めた座標をほどく。負の座標は算術シフトで符号を広げて戻す。
func UnpackPosition(v int64) Position {
	return Position{
		X: int(v >> 38),
		Y: int(v << 52 >> 52),
		Z: int(v << 26 >> 38),
	}
}

// Angle は 1 回転を 256 等分した角度（1 バイト）。
type Angle uint8

// Degrees は角度を度（0 以上 360 未満）にする。
func (a Angle) Degrees() float32 {
	return float32(a) * 360 / 256
}

// AngleFromDegrees は度を Angle にする。360 度で一周して元に戻る（負の角度も扱える）。
func AngleFromDegrees(deg float32) Angle {
	// Java の (byte)(int)(deg * 256 / 360) に合わせて、0 の方向へ切り捨てた整数の下位 8 ビットを使う
	return Angle(uint8(int32(deg * 256 / 360)))
}

// BitSet は長さが可変のビットの並び。通信では long の個数（VarInt）と long の並びで送る。
// ビット i は i/64 番目の long の、下から i%64 番目のビット。
type BitSet []uint64

// Get はビット i が立っているかを返す。範囲の外は false。
func (b BitSet) Get(i int) bool {
	if i < 0 || i/64 >= len(b) {
		return false
	}
	return b[i/64]&(1<<(uint(i)%64)) != 0
}

// Set はビット i を立てる。足りなければ伸ばす。
func (b *BitSet) Set(i int) {
	if i < 0 {
		return
	}
	for i/64 >= len(*b) {
		*b = append(*b, 0)
	}
	(*b)[i/64] |= 1 << (uint(i) % 64)
}

// FixedBitSet は長さが決まったビットの並び。通信では ceil(n/8) バイトで送る（長さの前置きなし）。
// ビット i は i/8 番目のバイトの、下から i%8 番目のビット。
type FixedBitSet []byte

// NewFixedBitSet は n ビットの FixedBitSet を作る。
func NewFixedBitSet(n int) FixedBitSet {
	return make(FixedBitSet, (n+7)/8)
}

// Get はビット i が立っているかを返す。範囲の外は false。
func (b FixedBitSet) Get(i int) bool {
	if i < 0 || i/8 >= len(b) {
		return false
	}
	return b[i/8]&(1<<(uint(i)%8)) != 0
}

// Set はビット i を立てる。範囲の外なら何もしない。
func (b FixedBitSet) Set(i int) {
	if i < 0 || i/8 >= len(b) {
		return
	}
	b[i/8] |= 1 << (uint(i) % 8)
}
