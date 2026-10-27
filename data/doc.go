// Package data は Minecraft Java Edition 1.21.4 のブロック・アイテム・タグ・レシピ・パケット ID・registry の情報を持つ。
//
// *_gen.go は cmd/gendata が Mojang の公式サーバー jar のデータ出力（--reports と --server）から自動生成したもの。
// 手で書き換えず、作り直すときはリポジトリのルートで `go run ./cmd/gendata` を実行する。
//
// 名前はすべて "minecraft:" を外した形で持つ（例 "oak_stairs"）。名前で引く関数は "minecraft:" 付きでも受け付ける。
//
// 公式データに含まれないため、ここには無い情報: ブロックの硬さ、当たり判定の形、
// ブロックが「適した道具でないと何も落とさない」かどうか、明るさ（光の強さ）など。
package data

//go:generate go run ../cmd/gendata -work ../.gendata -out .

import "strings"

// trimNS は名前の先頭の "minecraft:" を外す。
func trimNS(name string) string {
	return strings.TrimPrefix(name, "minecraft:")
}
