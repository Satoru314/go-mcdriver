// Package data は Minecraft Java Edition 1.21.4 のブロック・アイテム・タグ・レシピ・パケット ID・registry の情報を持つ。
//
// *_gen.go は cmd/gendata が Mojang の公式サーバー jar のデータ出力（--reports と --server）から自動生成したもの。
// 手で書き換えず、作り直すときはリポジトリのルートで `go run ./cmd/gendata` を実行する。
//
// 名前はすべて "minecraft:" を外した形で持つ（例 "oak_stairs"）。名前で引く関数は "minecraft:" 付きでも受け付ける。
//
// 公式データに含まれない情報（ブロックの硬さ・爆発耐性・壊せるか・落とすのに必要な道具・光の強さ・光の通しやすさ・
// 当たり判定の形、エンティティの幅と高さ）は、PrismarineJS の minecraft-data（JSON）の固定したコミットから取っている。
// 生成時にブロック名・状態 ID・プロパティ・エンティティ名を公式データと突き合わせ、合わなければ生成を止める。
// minecraft-data の値はブロックごと（光の強さが状態で変わるものの違いは入っていない）。
package data

//go:generate go run ../cmd/gendata -work ../.gendata -out .

import "strings"

// trimNS は名前の先頭の "minecraft:" を外す。
func trimNS(name string) string {
	return strings.TrimPrefix(name, "minecraft:")
}
