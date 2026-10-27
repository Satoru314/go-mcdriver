package protocol

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
)

// パケットの枠。
//
// 圧縮なし（threshold < 0）:
//
//	Length (VarInt)  … 以降の長さ（Packet ID と Data）
//	Packet ID (VarInt)
//	Data
//
// 圧縮あり（threshold >= 0）:
//
//	Packet Length (VarInt) … 以降の長さ（Data Length と、その後ろ）
//	Data Length (VarInt)   … 圧縮前の（Packet ID と Data）の長さ。圧縮していなければ 0
//	Packet ID と Data      … Data Length が 0 でなければ zlib で圧縮したもの
//
// 圧縮前の長さが threshold 以上なら圧縮し、未満なら Data Length 0 で圧縮せずに送る。

const (
	// MaxPacketLen は枠の長さ（Length / Packet Length）の上限。VarInt 3 バイトで表せる最大値（2^21 - 1）。
	MaxPacketLen = 1<<21 - 1
	// MaxUncompressedLen は圧縮前の長さ（Data Length）の上限（2^23）。Java の実装に合わせる。
	MaxUncompressedLen = 1 << 23
	// NoCompression は圧縮を使わないことを表す閾値。
	NoCompression = -1
)

// Packet は 1 つのパケット。Data は Packet ID の後ろの中身。
type Packet struct {
	ID   int32
	Data []byte
}

// payload は Packet ID と Data をつなげたバイト列を作る。
func (p Packet) payload() []byte {
	b := make([]byte, 0, MaxVarIntLen+len(p.Data))
	b = AppendVarInt(b, p.ID)
	return append(b, p.Data...)
}

// parsePayload は Packet ID と Data をつなげたバイト列を Packet に分ける。Data は b を指す。
func parsePayload(b []byte) (Packet, error) {
	id, n, err := DecodeVarInt(b)
	if err == io.EOF {
		return Packet{}, ErrEmptyPacket
	}
	if err != nil {
		return Packet{}, fmt.Errorf("Packet ID: %w", err)
	}
	if id < 0 {
		return Packet{}, fmt.Errorf("Packet ID が負: %d", id)
	}
	return Packet{ID: id, Data: b[n:]}, nil
}

// EncodePacket は p を枠に入れたバイト列を作る。threshold は圧縮の閾値（負なら圧縮なし）。
func EncodePacket(p Packet, threshold int) ([]byte, error) {
	if p.ID < 0 {
		return nil, fmt.Errorf("Packet ID が負: %d", p.ID)
	}
	body := p.payload()
	if len(body) > MaxUncompressedLen {
		return nil, fmt.Errorf("%w: 圧縮前 %d バイト（上限 %d）", ErrPacketTooLarge, len(body), MaxUncompressedLen)
	}

	var inner []byte // 枠の長さの後ろに続くもの
	if threshold < 0 {
		inner = body
	} else if len(body) < threshold {
		inner = make([]byte, 0, 1+len(body))
		inner = AppendVarInt(inner, 0)
		inner = append(inner, body...)
	} else {
		var buf bytes.Buffer
		buf.Write(AppendVarInt(nil, int32(len(body))))
		zw := zlib.NewWriter(&buf)
		if _, err := zw.Write(body); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		inner = buf.Bytes()
	}
	if len(inner) > MaxPacketLen {
		return nil, fmt.Errorf("%w: %d バイト（上限 %d）", ErrPacketTooLarge, len(inner), MaxPacketLen)
	}
	out := make([]byte, 0, MaxVarIntLen+len(inner))
	out = AppendVarInt(out, int32(len(inner)))
	return append(out, inner...), nil
}

// WritePacket は p を枠に入れて w に書く。1 回の Write で書く。
func WritePacket(w io.Writer, p Packet, threshold int) error {
	b, err := EncodePacket(p, threshold)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// ReadPacket は r から枠を 1 つ読み、Packet を返す。threshold は圧縮の閾値（負なら圧縮なし）。
//
// 最初のバイトの前で r が終わったら io.EOF を返す。途中で終わったら io.ErrUnexpectedEOF。
// 長さが上限を超えていたら ErrPacketTooLarge、圧縮が正しくなければ ErrBadCompression を返す。
// 余分に読まないので、r は bufio.Reader で包んでおくと速い。
func ReadPacket(r io.Reader, threshold int) (Packet, error) {
	length, err := ReadVarIntFrom(r)
	if err != nil {
		return Packet{}, err
	}
	if length <= 0 {
		return Packet{}, fmt.Errorf("%w: 長さ %d", ErrEmptyPacket, length)
	}
	if length > MaxPacketLen {
		return Packet{}, fmt.Errorf("%w: %d バイト（上限 %d）", ErrPacketTooLarge, length, MaxPacketLen)
	}
	frame := make([]byte, length)
	if _, err := io.ReadFull(r, frame); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return Packet{}, err
	}
	return DecodeFrame(frame, threshold)
}

// DecodeFrame は枠の長さを取り除いた中身（ReadPacket が読んだ Length の後ろ）を Packet にする。
func DecodeFrame(frame []byte, threshold int) (Packet, error) {
	if threshold < 0 {
		return parsePayload(frame)
	}
	dataLen, n, err := DecodeVarInt(frame)
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return Packet{}, fmt.Errorf("%w: Data Length: %w", ErrBadCompression, err)
	}
	rest := frame[n:]
	if dataLen == 0 {
		// 閾値未満なので圧縮していない
		return parsePayload(rest)
	}
	if dataLen < 0 {
		return Packet{}, fmt.Errorf("%w: Data Length が負: %d", ErrBadCompression, dataLen)
	}
	if int(dataLen) < threshold {
		return Packet{}, fmt.Errorf("%w: Data Length %d が閾値 %d より小さいのに圧縮されている", ErrBadCompression, dataLen, threshold)
	}
	if dataLen > MaxUncompressedLen {
		return Packet{}, fmt.Errorf("%w: 圧縮前 %d バイト（上限 %d）", ErrPacketTooLarge, dataLen, MaxUncompressedLen)
	}
	zr, err := zlib.NewReader(bytes.NewReader(rest))
	if err != nil {
		return Packet{}, fmt.Errorf("%w: %w", ErrBadCompression, err)
	}
	defer zr.Close()
	body := make([]byte, dataLen)
	if _, err := io.ReadFull(zr, body); err != nil {
		return Packet{}, fmt.Errorf("%w: 展開: %w", ErrBadCompression, err)
	}
	// ちょうど Data Length で終わっていること（チェックサムもここで確かめられる）
	var extra [1]byte
	if m, err := io.ReadFull(zr, extra[:]); m > 0 {
		return Packet{}, fmt.Errorf("%w: 展開した長さが Data Length %d より長い", ErrBadCompression, dataLen)
	} else if err != nil && !errors.Is(err, io.EOF) {
		return Packet{}, fmt.Errorf("%w: 展開の終わり: %w", ErrBadCompression, err)
	}
	return parsePayload(body)
}
