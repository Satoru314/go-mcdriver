package nbt

import (
	"fmt"
	"unicode/utf16"
)

// NBT の文字列は Java の modified UTF-8 で書かれている。普通の UTF-8 との違いは 2 つ。
//   - U+0000 は 2 バイト（0xC0 0x80）で書く
//   - U+10000 以上の文字は UTF-16 のサロゲートペアにして、それぞれを 3 バイトで書く
//
// 4 バイトの並び（0xF0 以上）は使わない。

// decodeMUTF8 は modified UTF-8 のバイト列を Go の文字列にする。
// 対になっていないサロゲートは U+FFFD にする。
func decodeMUTF8(b []byte) (string, error) {
	ascii := true
	for _, c := range b {
		if c == 0 || c >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return string(b), nil
	}
	units := make([]uint16, 0, len(b))
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0:
			// Java の readUTF は 0 をそのまま受け付ける
			units = append(units, 0)
			i++
		case c < 0x80:
			units = append(units, uint16(c))
			i++
		case c&0xE0 == 0xC0:
			if i+1 >= len(b) || b[i+1]&0xC0 != 0x80 {
				return "", fmt.Errorf("%w: %d バイト目", ErrBadString, i)
			}
			units = append(units, uint16(c&0x1F)<<6|uint16(b[i+1]&0x3F))
			i += 2
		case c&0xF0 == 0xE0:
			if i+2 >= len(b) || b[i+1]&0xC0 != 0x80 || b[i+2]&0xC0 != 0x80 {
				return "", fmt.Errorf("%w: %d バイト目", ErrBadString, i)
			}
			units = append(units, uint16(c&0x0F)<<12|uint16(b[i+1]&0x3F)<<6|uint16(b[i+2]&0x3F))
			i += 3
		default:
			return "", fmt.Errorf("%w: %d バイト目 0x%02x", ErrBadString, i, c)
		}
	}
	return string(utf16.Decode(units)), nil
}

// appendMUTF8 は s を modified UTF-8 にして b に足す。
func appendMUTF8(b []byte, s string) []byte {
	for _, r := range s {
		switch {
		case r >= 1 && r < 0x80:
			b = append(b, byte(r))
		case r < 0x800: // U+0000 もここ
			b = append(b, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
		case r < 0x10000:
			b = append3(b, uint16(r))
		default:
			hi, lo := utf16.EncodeRune(r)
			b = append3(b, uint16(hi))
			b = append3(b, uint16(lo))
		}
	}
	return b
}

func append3(b []byte, u uint16) []byte {
	return append(b, 0xE0|byte(u>>12), 0x80|byte(u>>6&0x3F), 0x80|byte(u&0x3F))
}

// mutf8Len は s を modified UTF-8 にしたときのバイト数を返す。
func mutf8Len(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case r >= 1 && r < 0x80:
			n++
		case r < 0x800:
			n += 2
		case r < 0x10000:
			n += 3
		default:
			n += 6
		}
	}
	return n
}
