// Package nbt は Minecraft の NBT（Named Binary Tag）を読む（書き出しもできる）。
//
// 形式は 2 つある。
//
//   - ネットワーク形式（1.20.2 以降）: ルートのタグの型のあとに名前がなく、すぐ中身が続く。
//     ルートの型が TAG_End（0）なら「値なし」を表す
//   - ファイル形式: ルートのタグの型、名前（長さ 2 バイトの modified UTF-8）、中身の順
//
// 読んだ値は Go の値になる。
//
//	TAG_Byte → int8, TAG_Short → int16, TAG_Int → int32, TAG_Long → int64,
//	TAG_Float → float32, TAG_Double → float64, TAG_Byte_Array → []byte,
//	TAG_String → string, TAG_List → []any, TAG_Compound → map[string]any,
//	TAG_Int_Array → []int32, TAG_Long_Array → []int64
//
// 壊れたデータや悪意のあるデータでもパニックせず、エラーを返す。
// 入れ子の深さと読む量には上限（Limits）があり、長さの前置きは残りのバイト数と比べてから確保する。
//
// 仕様は https://minecraft.wiki/w/NBT_format を参照する。
package nbt

import (
	"errors"
	"fmt"
)

// タグの型。
const (
	TagEnd       byte = 0
	TagByte      byte = 1
	TagShort     byte = 2
	TagInt       byte = 3
	TagLong      byte = 4
	TagFloat     byte = 5
	TagDouble    byte = 6
	TagByteArray byte = 7
	TagString    byte = 8
	TagList      byte = 9
	TagCompound  byte = 10
	TagIntArray  byte = 11
	TagLongArray byte = 12
)

// 読み書きで返すエラー。呼び出し側は errors.Is で見分けられる。
var (
	// ErrBadTagType は知らないタグの型（13 以上）
	ErrBadTagType = errors.New("nbt: 知らないタグの型")
	// ErrTooDeep は入れ子の深さが上限を超えた
	ErrTooDeep = errors.New("nbt: 入れ子が深すぎる")
	// ErrTooBig は読む量が上限を超えた
	ErrTooBig = errors.New("nbt: データが大きすぎる")
	// ErrNegativeLength は配列やリストの長さが負
	ErrNegativeLength = errors.New("nbt: 長さが負")
	// ErrBadString は modified UTF-8 として正しくない文字列
	ErrBadString = errors.New("nbt: 文字列が正しくない")
	// ErrBadList は要素の型が TAG_End なのに要素があるリスト
	ErrBadList = errors.New("nbt: リストの要素の型が正しくない")
	// ErrUnsupportedValue は書き出せない Go の値
	ErrUnsupportedValue = errors.New("nbt: 書き出せない値")
)

// Limits は読むときの上限。
type Limits struct {
	// MaxDepth は入れ子（リストとコンパウンド）の深さの上限。ルートのコンパウンドが深さ 1
	MaxDepth int
	// MaxBytes は 1 回の読み込みで読み進めてよいバイト数の上限
	MaxBytes int
}

// DefaultLimits は Java 版のサーバーに合わせた上限（深さ 512、2 MiB）。
var DefaultLimits = Limits{MaxDepth: 512, MaxBytes: 2 * 1024 * 1024}

// TagName はタグの型の名前を返す（エラーの表示用）。
func TagName(t byte) string {
	names := [...]string{"TAG_End", "TAG_Byte", "TAG_Short", "TAG_Int", "TAG_Long", "TAG_Float",
		"TAG_Double", "TAG_Byte_Array", "TAG_String", "TAG_List", "TAG_Compound", "TAG_Int_Array", "TAG_Long_Array"}
	if int(t) < len(names) {
		return names[t]
	}
	return fmt.Sprintf("TAG_?(%d)", t)
}

// TagOf は Go の値に対応するタグの型を返す。書き出せない値なら false。
func TagOf(v any) (byte, bool) {
	switch v.(type) {
	case int8:
		return TagByte, true
	case int16:
		return TagShort, true
	case int32:
		return TagInt, true
	case int64:
		return TagLong, true
	case float32:
		return TagFloat, true
	case float64:
		return TagDouble, true
	case []byte:
		return TagByteArray, true
	case string:
		return TagString, true
	case []any:
		return TagList, true
	case map[string]any:
		return TagCompound, true
	case []int32:
		return TagIntArray, true
	case []int64:
		return TagLongArray, true
	}
	return 0, false
}
