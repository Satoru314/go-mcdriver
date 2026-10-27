// Package protocol は Minecraft Java Edition 1.21.4（protocol 769）の通信の基本を扱う。
//
// ここにあるのは、ゲームの中身を知らない下の層だけである。
//
//   - 基本の型の読み書き（VarInt, VarLong, String, Position, UUID, BitSet など）
//     読むときは Decoder（バイト列の上を進む）、書くときは Encoder（バイト列に足していく）を使う
//   - パケットの枠（長さの前置き、圧縮のありなし）の読み書き。ReadPacket / WritePacket
//
// 仕様は https://minecraft.wiki/w/Java_Edition_protocol を参照する。
// 壊れたデータや巨大な長さを受け取ってもパニックせず、エラーを返す。
package protocol

import "errors"

// 読み書きで返すエラー。呼び出し側は errors.Is で見分けられる。
var (
	// ErrVarIntTooBig は VarInt が 5 バイト（VarLong は 10 バイト）を超えたときのエラー
	ErrVarIntTooBig = errors.New("protocol: VarInt が長すぎる")
	// ErrVarLongTooBig は VarLong が 10 バイトを超えたときのエラー
	ErrVarLongTooBig = errors.New("protocol: VarLong が長すぎる")
	// ErrStringTooLong は文字列が最大長を超えたときのエラー
	ErrStringTooLong = errors.New("protocol: 文字列が最大長を超えている")
	// ErrNegativeLength は長さの前置きが負のときのエラー
	ErrNegativeLength = errors.New("protocol: 長さが負")
	// ErrTooLong は配列などの長さが上限や残りのデータ量を超えたときのエラー
	ErrTooLong = errors.New("protocol: 長さが上限を超えている")
	// ErrBadIdentifier は Identifier の書式が正しくないときのエラー
	ErrBadIdentifier = errors.New("protocol: Identifier の書式が正しくない")
	// ErrOutOfRange は値が型の表せる範囲の外にあるときのエラー（Position の書き込みなど）
	ErrOutOfRange = errors.New("protocol: 値が範囲の外にある")
	// ErrPacketTooLarge はパケットの長さが上限を超えたときのエラー
	ErrPacketTooLarge = errors.New("protocol: パケットが大きすぎる")
	// ErrEmptyPacket はパケットの長さが 0 以下のときのエラー
	ErrEmptyPacket = errors.New("protocol: パケットが空")
	// ErrBadCompression は圧縮されたパケットが正しくないときのエラー
	ErrBadCompression = errors.New("protocol: 圧縮されたパケットが正しくない")
)
