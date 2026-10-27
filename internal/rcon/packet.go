// Package rcon は Minecraft サーバーの RCON（Source RCON プロトコル）のクライアントです。
//
// パケットの形（数値はすべてリトルエンディアン）:
//
//	int32  長さ（この後ろのバイト数: ID + 種類 + 本文 + 終端の 0x00 0x00）
//	int32  リクエスト ID
//	int32  種類（3: 認証, 2: コマンド実行 / 認証の応答, 0: 応答の本文）
//	[]byte 本文（ASCII / UTF-8）
//	0x00 0x00
package rcon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// パケットの種類。
const (
	// TypeResponse はコマンドの応答の本文。
	TypeResponse int32 = 0
	// TypeCommand はコマンドの実行の依頼。認証の応答もこの値になる。
	TypeCommand int32 = 2
	// TypeAuthResponse は認証の応答（値は TypeCommand と同じ）。
	TypeAuthResponse int32 = 2
	// TypeAuth は認証の依頼。
	TypeAuth int32 = 3
)

const (
	// headerSize は長さのあとの ID と種類の大きさ。
	headerSize = 8
	// minLength は本文が空のときの「長さ」の値（ID + 種類 + 終端 2 バイト）。
	minLength = headerSize + 2
	// MaxCommandLen は送れるコマンドの最大の長さ（バイト）。
	// Minecraft のサーバーは 1460 バイトより大きいパケットを受け付けないため、
	// 長さの欄を除いた 1456 バイトから ID・種類・終端を引いた値にする。
	MaxCommandLen = 1446
	// maxResponseLength は受け取るパケットの「長さ」の上限。
	// Minecraft は応答を 4096 バイトごとに分けて送るので、余裕をもたせた値にする。
	maxResponseLength = 1 << 16
)

// Packet は RCON のパケット 1 つ。
type Packet struct {
	ID   int32
	Type int32
	Body string
}

// ErrPacketTooLarge は送る・受け取るパケットが大きすぎるときのエラー。
var ErrPacketTooLarge = errors.New("rcon: パケットが大きすぎる")

// Encode はパケットを送るためのバイト列にする。
func (p Packet) Encode() []byte {
	n := minLength + len(p.Body)
	buf := make([]byte, 4+n)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(n))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(p.ID))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(p.Type))
	copy(buf[12:], p.Body)
	// 最後の 2 バイトは make で 0 になっている
	return buf
}

// ReadPacket は r からパケットを 1 つ読む。
func ReadPacket(r io.Reader) (Packet, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return Packet{}, err
	}
	n := int32(binary.LittleEndian.Uint32(lenBuf[:]))
	if n < minLength {
		return Packet{}, fmt.Errorf("rcon: パケットの長さが短すぎる: %d", n)
	}
	if n > maxResponseLength {
		return Packet{}, fmt.Errorf("%w: 長さ %d", ErrPacketTooLarge, n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return Packet{}, err
	}
	p := Packet{
		ID:   int32(binary.LittleEndian.Uint32(buf[0:4])),
		Type: int32(binary.LittleEndian.Uint32(buf[4:8])),
	}
	body := buf[headerSize:]
	// 終端は 0x00 0x00 のはずだが、念のため最初の 0x00 までを本文とする
	if i := bytes.IndexByte(body, 0); i >= 0 {
		body = body[:i]
	}
	p.Body = string(body)
	return p, nil
}
