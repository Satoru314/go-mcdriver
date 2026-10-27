package nbt

import (
	"fmt"
	"io"
	"math"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// ReadNetwork はネットワーク形式（ルートに名前なし）の NBT を DefaultLimits で読む。
// ルートが TAG_End なら値なしとして nil を返す（エラーではない）。
func ReadNetwork(d *protocol.Decoder) (any, error) {
	return DefaultLimits.ReadNetwork(d)
}

// ReadNamed はファイル形式（ルートに名前あり）の NBT を DefaultLimits で読む。
func ReadNamed(d *protocol.Decoder) (name string, v any, err error) {
	return DefaultLimits.ReadNamed(d)
}

// SkipNetwork はネットワーク形式の NBT を、値を作らずに読み飛ばす。
func SkipNetwork(d *protocol.Decoder) error {
	return DefaultLimits.SkipNetwork(d)
}

// SkipNamed はファイル形式の NBT を、値を作らずに読み飛ばす。
func SkipNamed(d *protocol.Decoder) error {
	return DefaultLimits.SkipNamed(d)
}

// ReadNetwork はネットワーク形式の NBT を上限 l で読む。
func (l Limits) ReadNetwork(d *protocol.Decoder) (any, error) {
	r := l.newReader(d, false)
	t, err := r.tagType()
	if err != nil || t == TagEnd {
		return nil, err
	}
	return r.payload(t, 0)
}

// SkipNetwork はネットワーク形式の NBT を上限 l で読み飛ばす。
func (l Limits) SkipNetwork(d *protocol.Decoder) error {
	r := l.newReader(d, true)
	t, err := r.tagType()
	if err != nil || t == TagEnd {
		return err
	}
	_, err = r.payload(t, 0)
	return err
}

// ReadNamed はファイル形式の NBT を上限 l で読む。ルートが TAG_End なら名前も値もない。
func (l Limits) ReadNamed(d *protocol.Decoder) (string, any, error) {
	r := l.newReader(d, false)
	t, err := r.tagType()
	if err != nil || t == TagEnd {
		return "", nil, err
	}
	name, err := r.str()
	if err != nil {
		return "", nil, err
	}
	v, err := r.payload(t, 0)
	return name, v, err
}

// SkipNamed はファイル形式の NBT を上限 l で読み飛ばす。
func (l Limits) SkipNamed(d *protocol.Decoder) error {
	r := l.newReader(d, true)
	t, err := r.tagType()
	if err != nil || t == TagEnd {
		return err
	}
	if err := r.skipStr(); err != nil {
		return err
	}
	_, err = r.payload(t, 0)
	return err
}

// reader は 1 回の読み込みの状態（どこから読み始めたか、読み飛ばすだけか）を持つ。
type reader struct {
	d     *protocol.Decoder
	lim   Limits
	start int
	skip  bool
}

func (l Limits) newReader(d *protocol.Decoder, skip bool) *reader {
	if l.MaxDepth <= 0 {
		l.MaxDepth = DefaultLimits.MaxDepth
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultLimits.MaxBytes
	}
	return &reader{d: d, lim: l, start: d.Offset(), skip: skip}
}

// need は、これから n バイト読むことが上限の中に収まるかを確かめる。
func (r *reader) need(n int64) error {
	if n < 0 {
		return ErrNegativeLength
	}
	used := int64(r.d.Offset() - r.start)
	if used+n > int64(r.lim.MaxBytes) {
		return fmt.Errorf("%w: %d バイト（上限 %d バイト）", ErrTooBig, used+n, r.lim.MaxBytes)
	}
	if n > int64(r.d.Remaining()) {
		return fmt.Errorf("nbt: %d バイト必要だが残りは %d バイト: %w", n, r.d.Remaining(), io.ErrUnexpectedEOF)
	}
	return nil
}

func (r *reader) tagType() (byte, error) {
	if err := r.need(1); err != nil {
		return 0, err
	}
	t, err := r.d.ReadUByte()
	if err != nil {
		return 0, err
	}
	if t > TagLongArray {
		return 0, fmt.Errorf("%w: %d", ErrBadTagType, t)
	}
	return t, nil
}

// strBytes は長さ（符号なし 16 ビット）つきの文字列のバイト列を読む。
func (r *reader) strBytes() ([]byte, error) {
	if err := r.need(2); err != nil {
		return nil, err
	}
	n, err := r.d.ReadUShort()
	if err != nil {
		return nil, err
	}
	if err := r.need(int64(n)); err != nil {
		return nil, err
	}
	return r.d.ReadBytes(int(n))
}

func (r *reader) str() (string, error) {
	b, err := r.strBytes()
	if err != nil {
		return "", err
	}
	return decodeMUTF8(b)
}

func (r *reader) skipStr() error {
	if err := r.need(2); err != nil {
		return err
	}
	n, err := r.d.ReadUShort()
	if err != nil {
		return err
	}
	if err := r.need(int64(n)); err != nil {
		return err
	}
	return r.d.Skip(int(n))
}

// arrayLen は配列の長さ（Int）を読み、要素 size バイトの配列が残りに収まるかを確かめる。
func (r *reader) arrayLen(size int64) (int, error) {
	if err := r.need(4); err != nil {
		return 0, err
	}
	n, err := r.d.ReadInt()
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: %d", ErrNegativeLength, n)
	}
	if err := r.need(int64(n) * size); err != nil {
		return 0, err
	}
	return int(n), nil
}

// minSize は各タグの中身の最小のバイト数。リストの要素数が残りに収まるかの確認に使う。
var minSize = [...]int64{
	TagEnd: 0, TagByte: 1, TagShort: 2, TagInt: 4, TagLong: 8, TagFloat: 4, TagDouble: 8,
	TagByteArray: 4, TagString: 2, TagList: 5, TagCompound: 1, TagIntArray: 4, TagLongArray: 4,
}

// payload は型 t のタグの中身を読む。depth は外側の入れ子の深さ。
// 読み飛ばすだけのときは値を作らず nil を返す。
func (r *reader) payload(t byte, depth int) (any, error) {
	d := r.d
	switch t {
	case TagByte:
		if err := r.need(1); err != nil {
			return nil, err
		}
		return d.ReadInt8()
	case TagShort:
		if err := r.need(2); err != nil {
			return nil, err
		}
		return d.ReadShort()
	case TagInt:
		if err := r.need(4); err != nil {
			return nil, err
		}
		return d.ReadInt()
	case TagLong:
		if err := r.need(8); err != nil {
			return nil, err
		}
		return d.ReadLong()
	case TagFloat:
		if err := r.need(4); err != nil {
			return nil, err
		}
		return d.ReadFloat()
	case TagDouble:
		if err := r.need(8); err != nil {
			return nil, err
		}
		return d.ReadDouble()
	case TagByteArray:
		n, err := r.arrayLen(1)
		if err != nil {
			return nil, err
		}
		if r.skip {
			return nil, d.Skip(n)
		}
		return d.ReadBytes(n)
	case TagString:
		if r.skip {
			return nil, r.skipStr()
		}
		return r.str()
	case TagIntArray:
		n, err := r.arrayLen(4)
		if err != nil {
			return nil, err
		}
		if r.skip {
			return nil, d.Skip(n * 4)
		}
		a := make([]int32, n)
		for i := range a {
			if a[i], err = d.ReadInt(); err != nil {
				return nil, err
			}
		}
		return a, nil
	case TagLongArray:
		n, err := r.arrayLen(8)
		if err != nil {
			return nil, err
		}
		if r.skip {
			return nil, d.Skip(n * 8)
		}
		a := make([]int64, n)
		for i := range a {
			if a[i], err = d.ReadLong(); err != nil {
				return nil, err
			}
		}
		return a, nil
	case TagList:
		return r.list(depth + 1)
	case TagCompound:
		return r.compound(depth + 1)
	}
	return nil, fmt.Errorf("%w: %d", ErrBadTagType, t)
}

func (r *reader) checkDepth(depth int) error {
	if depth > r.lim.MaxDepth {
		return fmt.Errorf("%w: 深さ %d（上限 %d）", ErrTooDeep, depth, r.lim.MaxDepth)
	}
	return nil
}

func (r *reader) list(depth int) (any, error) {
	if err := r.checkDepth(depth); err != nil {
		return nil, err
	}
	et, err := r.tagType()
	if err != nil {
		return nil, err
	}
	n, err := r.arrayLen(minSize[et])
	if err != nil {
		return nil, err
	}
	if et == TagEnd && n > 0 {
		return nil, fmt.Errorf("%w: 要素の型が TAG_End で要素数が %d", ErrBadList, n)
	}
	var a []any
	if !r.skip {
		a = make([]any, 0, n)
	}
	for i := 0; i < n; i++ {
		v, err := r.payload(et, depth)
		if err != nil {
			return nil, fmt.Errorf("リストの %d 番目: %w", i, err)
		}
		if !r.skip {
			a = append(a, v)
		}
	}
	if r.skip {
		return nil, nil
	}
	return a, nil
}

func (r *reader) compound(depth int) (any, error) {
	if err := r.checkDepth(depth); err != nil {
		return nil, err
	}
	var m map[string]any
	if !r.skip {
		m = map[string]any{}
	}
	for {
		t, err := r.tagType()
		if err != nil {
			return nil, err
		}
		if t == TagEnd {
			break
		}
		if r.skip {
			if err := r.skipStr(); err != nil {
				return nil, err
			}
			if _, err := r.payload(t, depth); err != nil {
				return nil, err
			}
			continue
		}
		name, err := r.str()
		if err != nil {
			return nil, err
		}
		v, err := r.payload(t, depth)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", name, err)
		}
		m[name] = v // 同じ名前が 2 回出たら後のものを使う（Java の実装と同じ）
	}
	if r.skip {
		return nil, nil
	}
	return m, nil
}

// 長さの確認のための値。math.MaxInt32 より大きい長さは Int で表せない。
const maxArrayLen = math.MaxInt32
