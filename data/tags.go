package data

import "sort"

// BlockTag はブロックのタグ（例 "mineable/pickaxe"）に含まれるブロックを ID の昇順で返す。
// ほかのタグへの参照は展開済み。"#" と "minecraft:" は付いていてもよい。返した中身を書き換えないこと。
func BlockTag(name string) ([]BlockID, bool) {
	t, ok := blockTags[trimNS(trimHash(name))]
	return t, ok
}

// BlockHasTag はブロックがタグに含まれるかを返す。
func BlockHasTag(b BlockID, tag string) bool {
	t, ok := BlockTag(tag)
	if !ok {
		return false
	}
	i := sort.Search(len(t), func(i int) bool { return t[i] >= b })
	return i < len(t) && t[i] == b
}

// BlockTagNames は全ブロックのタグの名前を返す（昇順）。
func BlockTagNames() []string { return sortedKeys(blockTags) }

// ItemTag はアイテムのタグ（例 "planks"）に含まれるアイテムを ID の昇順で返す。
func ItemTag(name string) ([]ItemID, bool) {
	t, ok := itemTags[trimNS(trimHash(name))]
	return t, ok
}

// ItemHasTag はアイテムがタグに含まれるかを返す。
func ItemHasTag(it ItemID, tag string) bool {
	t, ok := ItemTag(tag)
	if !ok {
		return false
	}
	i := sort.Search(len(t), func(i int) bool { return t[i] >= it })
	return i < len(t) && t[i] == it
}

// ItemTagNames は全アイテムのタグの名前を返す（昇順）。
func ItemTagNames() []string { return sortedKeys(itemTags) }

func trimHash(s string) string {
	if len(s) > 0 && s[0] == '#' {
		return s[1:]
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
