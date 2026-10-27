package nbt

import (
	"fmt"
	"math"
	"sort"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// 書き出しはテストと、サーバーへ NBT を送る必要が出たときのためのもの。
// コンパウンドの名前は辞書順に書く（map には順番がないので、出力を決まったものにするため）。

// WriteNetwork は v をネットワーク形式（ルートに名前なし）で書く。v が nil なら TAG_End だけを書く。
// 途中でエラーになったら、書きかけは取り消さない（Encoder の中身はエラーのときは使わないこと）。
func WriteNetwork(e *protocol.Encoder, v any) error {
	if v == nil {
		e.WriteUByte(TagEnd)
		return nil
	}
	t, ok := TagOf(v)
	if !ok {
		return fmt.Errorf("%w: %T", ErrUnsupportedValue, v)
	}
	e.WriteUByte(t)
	return writePayload(e, v, 0)
}

// WriteNamed は v をファイル形式（ルートに名前 name あり）で書く。
func WriteNamed(e *protocol.Encoder, name string, v any) error {
	t, ok := TagOf(v)
	if !ok {
		return fmt.Errorf("%w: %T", ErrUnsupportedValue, v)
	}
	e.WriteUByte(t)
	if err := writeStr(e, name); err != nil {
		return err
	}
	return writePayload(e, v, 0)
}

func writeStr(e *protocol.Encoder, s string) error {
	n := mutf8Len(s)
	if n > math.MaxUint16 {
		return fmt.Errorf("%w: 文字列が %d バイト（上限 65535）", ErrUnsupportedValue, n)
	}
	e.WriteUShort(uint16(n))
	e.WriteBytes(appendMUTF8(make([]byte, 0, n), s))
	return nil
}

func writeLen(e *protocol.Encoder, n int) error {
	if n > maxArrayLen {
		return fmt.Errorf("%w: 長さ %d", ErrUnsupportedValue, n)
	}
	e.WriteInt(int32(n))
	return nil
}

func writePayload(e *protocol.Encoder, v any, depth int) error {
	switch x := v.(type) {
	case int8:
		e.WriteInt8(x)
	case int16:
		e.WriteShort(x)
	case int32:
		e.WriteInt(x)
	case int64:
		e.WriteLong(x)
	case float32:
		e.WriteFloat(x)
	case float64:
		e.WriteDouble(x)
	case []byte:
		if err := writeLen(e, len(x)); err != nil {
			return err
		}
		e.WriteBytes(x)
	case string:
		return writeStr(e, x)
	case []int32:
		if err := writeLen(e, len(x)); err != nil {
			return err
		}
		for _, n := range x {
			e.WriteInt(n)
		}
	case []int64:
		if err := writeLen(e, len(x)); err != nil {
			return err
		}
		for _, n := range x {
			e.WriteLong(n)
		}
	case []any:
		if depth+1 > DefaultLimits.MaxDepth {
			return fmt.Errorf("%w: 深さ %d", ErrTooDeep, depth+1)
		}
		et := TagEnd
		if len(x) > 0 {
			var ok bool
			if et, ok = TagOf(x[0]); !ok {
				return fmt.Errorf("%w: リストの要素 %T", ErrUnsupportedValue, x[0])
			}
		}
		e.WriteUByte(et)
		if err := writeLen(e, len(x)); err != nil {
			return err
		}
		for i, item := range x {
			if t, _ := TagOf(item); t != et {
				return fmt.Errorf("%w: リストの %d 番目の型 %T が先頭と違う", ErrUnsupportedValue, i, item)
			}
			if err := writePayload(e, item, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		if depth+1 > DefaultLimits.MaxDepth {
			return fmt.Errorf("%w: 深さ %d", ErrTooDeep, depth+1)
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t, ok := TagOf(x[k])
			if !ok {
				return fmt.Errorf("%w: %q の値 %T", ErrUnsupportedValue, k, x[k])
			}
			e.WriteUByte(t)
			if err := writeStr(e, k); err != nil {
				return err
			}
			if err := writePayload(e, x[k], depth+1); err != nil {
				return err
			}
		}
		e.WriteUByte(TagEnd)
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedValue, v)
	}
	return nil
}
