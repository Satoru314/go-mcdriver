package nbt

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// DecodeFile はファイル形式の NBT（level.dat や Wiki の bigtest.nbt など）を読む。
// gzip（1f 8b で始まる）と zlib（78 で始まる）で圧縮されていれば展開してから読む。
// 展開後の大きさは l.MaxBytes までに限る。後ろに余ったバイトがあればエラーにする。
func (l Limits) DecodeFile(b []byte) (name string, v any, err error) {
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultLimits.MaxBytes
	}
	var zr io.Reader
	switch {
	case len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b:
		g, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return "", nil, fmt.Errorf("nbt: gzip: %w", err)
		}
		zr = g
	case len(b) >= 2 && b[0] == 0x78 && (uint16(b[0])<<8|uint16(b[1]))%31 == 0:
		z, err := zlib.NewReader(bytes.NewReader(b))
		if err != nil {
			return "", nil, fmt.Errorf("nbt: zlib: %w", err)
		}
		zr = z
	}
	if zr != nil {
		// 上限より 1 バイト多く読めたら大きすぎる
		raw, err := io.ReadAll(io.LimitReader(zr, int64(l.MaxBytes)+1))
		if err != nil {
			return "", nil, fmt.Errorf("nbt: 展開: %w", err)
		}
		if len(raw) > l.MaxBytes {
			return "", nil, fmt.Errorf("%w: 展開後に %d バイトを超える", ErrTooBig, l.MaxBytes)
		}
		b = raw
	}
	d := protocol.NewDecoder(b)
	name, v, err = l.ReadNamed(d)
	if err != nil {
		return "", nil, err
	}
	if d.Remaining() != 0 {
		return "", nil, fmt.Errorf("nbt: 後ろに %d バイト余っている", d.Remaining())
	}
	return name, v, nil
}

// DecodeFile は DefaultLimits でファイル形式の NBT を読む。
func DecodeFile(b []byte) (string, any, error) {
	return DefaultLimits.DecodeFile(b)
}
