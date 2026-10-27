package conn

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// 文字の部品（Text Component）。
// 設定と Play の段階では NBT（ネットワーク形式）、ログインの段階では JSON の文字列で送られる。
// どちらも読んだ値は string / map[string]any / []any の組み合わせになるので、同じ関数で文字列にする。
// 翻訳の表は持たないので、translate はキーと引数をそのまま並べる（例 "multiplayer.disconnect.kicked"）。

// ReadTextNBT は NBT の文字の部品を読み、読める文字列にする。
func ReadTextNBT(d *protocol.Decoder) (string, error) {
	v, err := nbt.ReadNetwork(d)
	if err != nil {
		return "", fmt.Errorf("文字の部品（NBT）: %w", err)
	}
	return TextPlain(v), nil
}

// TextFromJSON は JSON の文字の部品を読める文字列にする。JSON として読めなければそのまま返す。
func TextFromJSON(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	return TextPlain(v)
}

// TextPlain は文字の部品（NBT か JSON を読んだ値）を、色などを捨てた文字列にする。
func TextPlain(v any) string {
	var b strings.Builder
	writeText(&b, v, 0)
	return b.String()
}

func writeText(b *strings.Builder, v any, depth int) {
	if depth > 64 { // 悪意のある深い入れ子で止まらないように
		return
	}
	switch t := v.(type) {
	case nil:
	case string:
		b.WriteString(t)
	case []any:
		for _, e := range t {
			writeText(b, e, depth+1)
		}
	case map[string]any:
		switch {
		case t["text"] != nil:
			writeText(b, t["text"], depth+1)
		case t[""] != nil:
			// NBT のリストで型をそろえるため、文字列が {"": "..."} で包まれることがある
			writeText(b, t[""], depth+1)
		case t["translate"] != nil:
			key := fmt.Sprint(t["translate"])
			if fb, ok := t["fallback"].(string); ok && fb != "" {
				key = fb
			}
			b.WriteString(key)
			if with, ok := t["with"].([]any); ok && len(with) > 0 {
				b.WriteString(" [")
				for i, a := range with {
					if i > 0 {
						b.WriteString(", ")
					}
					writeText(b, a, depth+1)
				}
				b.WriteString("]")
			}
		case t["keybind"] != nil:
			writeText(b, t["keybind"], depth+1)
		}
		if extra, ok := t["extra"].([]any); ok {
			for _, e := range extra {
				writeText(b, e, depth+1)
			}
		}
	default:
		// 数や真偽値（JSON の text が数のときなど）
		fmt.Fprint(b, t)
	}
}
