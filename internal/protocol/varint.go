package protocol

import (
	"io"
)

// VarInt と VarLong の符号化。
// 下位から 7 ビットずつ並べ、続きがあるバイトは最上位ビット（0x80）を立てる。
// 負の数は 2 の補数のまま符号なしとして扱うので、VarInt は常に 5 バイト、VarLong は 10 バイトになる。

const (
	// MaxVarIntLen は VarInt の最大バイト数
	MaxVarIntLen = 5
	// MaxVarLongLen は VarLong の最大バイト数
	MaxVarLongLen = 10
)

// AppendVarInt は v を VarInt にして b の後ろに足す。
func AppendVarInt(b []byte, v int32) []byte {
	u := uint32(v)
	for u >= 0x80 {
		b = append(b, byte(u)|0x80)
		u >>= 7
	}
	return append(b, byte(u))
}

// AppendVarLong は v を VarLong にして b の後ろに足す。
func AppendVarLong(b []byte, v int64) []byte {
	u := uint64(v)
	for u >= 0x80 {
		b = append(b, byte(u)|0x80)
		u >>= 7
	}
	return append(b, byte(u))
}

// VarIntLen は v を VarInt にしたときのバイト数を返す。
func VarIntLen(v int32) int {
	u := uint32(v)
	n := 1
	for u >= 0x80 {
		u >>= 7
		n++
	}
	return n
}

// DecodeVarInt は b の先頭から VarInt を読み、値と使ったバイト数を返す。
// データが途中で終わったら io.ErrUnexpectedEOF（1 バイトもなければ io.EOF）、
// 5 バイトを超えたら ErrVarIntTooBig を返す。
func DecodeVarInt(b []byte) (int32, int, error) {
	var u uint32
	for i := 0; i < MaxVarIntLen; i++ {
		if i >= len(b) {
			if i == 0 {
				return 0, 0, io.EOF
			}
			return 0, 0, io.ErrUnexpectedEOF
		}
		c := b[i]
		u |= uint32(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return int32(u), i + 1, nil
		}
	}
	return 0, 0, ErrVarIntTooBig
}

// DecodeVarLong は b の先頭から VarLong を読み、値と使ったバイト数を返す。
func DecodeVarLong(b []byte) (int64, int, error) {
	var u uint64
	for i := 0; i < MaxVarLongLen; i++ {
		if i >= len(b) {
			if i == 0 {
				return 0, 0, io.EOF
			}
			return 0, 0, io.ErrUnexpectedEOF
		}
		c := b[i]
		u |= uint64(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return int64(u), i + 1, nil
		}
	}
	return 0, 0, ErrVarLongTooBig
}

// ReadVarIntFrom は r から 1 バイトずつ VarInt を読む（パケットの長さの前置き用）。
// 最初のバイトの前で r が終わったら io.EOF、途中で終わったら io.ErrUnexpectedEOF を返す。
// 余分に読まないように 1 バイトずつ読むので、r は bufio.Reader などで包んでおくとよい。
func ReadVarIntFrom(r io.Reader) (int32, error) {
	var u uint32
	var one [1]byte
	br, isByteReader := r.(io.ByteReader)
	for i := 0; i < MaxVarIntLen; i++ {
		var c byte
		var err error
		if isByteReader {
			c, err = br.ReadByte()
		} else {
			_, err = io.ReadFull(r, one[:])
			c = one[0]
		}
		if err != nil {
			if err == io.EOF && i > 0 {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
		}
		u |= uint32(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return int32(u), nil
		}
	}
	return 0, ErrVarIntTooBig
}
