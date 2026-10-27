package nbt

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"reflect"
	"testing"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// bb は仕様どおりのバイト列を手で組み立てるための小さな道具（書き出しの実装は使わない）。
type bb struct{ b []byte }

func (w *bb) u8(v byte) *bb    { w.b = append(w.b, v); return w }
func (w *bb) u16(v uint16) *bb { w.b = binary.BigEndian.AppendUint16(w.b, v); return w }
func (w *bb) i32(v int32) *bb  { w.b = binary.BigEndian.AppendUint32(w.b, uint32(v)); return w }
func (w *bb) i64(v int64) *bb  { w.b = binary.BigEndian.AppendUint64(w.b, uint64(v)); return w }
func (w *bb) f32(v float32) *bb {
	w.b = binary.BigEndian.AppendUint32(w.b, math.Float32bits(v))
	return w
}
func (w *bb) f64(v float64) *bb {
	w.b = binary.BigEndian.AppendUint64(w.b, math.Float64bits(v))
	return w
}

// str は長さ 2 バイトつきの文字列。ここで使う文字列は普通の UTF-8 と modified UTF-8 が同じもの。
func (w *bb) str(s string) *bb { w.u16(uint16(len(s))); w.b = append(w.b, s...); return w }

// tag はコンパウンドの中の名前つきタグの頭（型と名前）。
func (w *bb) tag(t byte, name string) *bb { return w.u8(t).str(name) }

// Wiki の test.nbt: TAG_Compound('hello world') { TAG_String('name'): 'Bananrama' }
var testNBT = []byte{
	0x0a, 0x00, 0x0b, 'h', 'e', 'l', 'l', 'o', ' ', 'w', 'o', 'r', 'l', 'd',
	0x08, 0x00, 0x04, 'n', 'a', 'm', 'e',
	0x00, 0x09, 'B', 'a', 'n', 'a', 'n', 'r', 'a', 'm', 'a',
	0x00,
}

func TestWikiTestNBT(t *testing.T) {
	name, v, err := DecodeFile(testNBT)
	if err != nil {
		t.Fatal(err)
	}
	if name != "hello world" {
		t.Errorf("名前 = %q, 期待 %q", name, "hello world")
	}
	want := map[string]any{"name": "Bananrama"}
	if !reflect.DeepEqual(v, want) {
		t.Errorf("値 = %#v, 期待 %#v", v, want)
	}
	// 書き出すと同じバイト列になる（要素が 1 つなので順番の問題はない）
	e := protocol.NewEncoder()
	if err := WriteNamed(e, name, v); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(e.Bytes(), testNBT) {
		t.Errorf("書き出し = % x\n期待      % x", e.Bytes(), testNBT)
	}
}

// bigtestBytes は Wiki の bigtest.nbt（展開後）と同じ構造を、Wiki に載っている順に組み立てる。
func bigtestBytes() []byte {
	w := &bb{}
	w.tag(TagCompound, "Level")
	w.tag(TagLong, "longTest").i64(9223372036854775807)
	w.tag(TagShort, "shortTest").u16(32767)
	w.tag(TagString, "stringTest").str("HELLO WORLD THIS IS A TEST STRING ÅÄÖ!")
	w.tag(TagFloat, "floatTest").f32(0.49823147)
	w.tag(TagInt, "intTest").i32(2147483647)
	w.tag(TagCompound, "nested compound test")
	w.tag(TagCompound, "ham")
	w.tag(TagString, "name").str("Hampus")
	w.tag(TagFloat, "value").f32(0.75)
	w.u8(TagEnd)
	w.tag(TagCompound, "egg")
	w.tag(TagString, "name").str("Eggbert")
	w.tag(TagFloat, "value").f32(0.5)
	w.u8(TagEnd)
	w.u8(TagEnd)
	w.tag(TagList, "listTest (long)").u8(TagLong).i32(5)
	for i := int64(11); i <= 15; i++ {
		w.i64(i)
	}
	w.tag(TagList, "listTest (compound)").u8(TagCompound).i32(2)
	for i := 0; i < 2; i++ {
		w.tag(TagString, "name").str("Compound tag #" + string(rune('0'+i)))
		w.tag(TagLong, "created-on").i64(1264099775885)
		w.u8(TagEnd)
	}
	w.tag(TagByte, "byteTest").u8(127)
	w.tag(TagByteArray, "byteArrayTest (the first 1000 values of (n*n*255+n*7)%100, starting with n=0 (0, 62, 34, 16, 8, ...))").i32(1000)
	w.b = append(w.b, bigtestArray()...)
	w.tag(TagDouble, "doubleTest").f64(0.4931287132182315)
	w.u8(TagEnd)
	return w.b
}

func bigtestArray() []byte {
	a := make([]byte, 1000)
	for n := range a {
		a[n] = byte((n*n*255 + n*7) % 100)
	}
	return a
}

func bigtestValue() map[string]any {
	return map[string]any{
		"longTest":   int64(9223372036854775807),
		"shortTest":  int16(32767),
		"stringTest": "HELLO WORLD THIS IS A TEST STRING ÅÄÖ!",
		"floatTest":  float32(0.49823147),
		"intTest":    int32(2147483647),
		"nested compound test": map[string]any{
			"ham": map[string]any{"name": "Hampus", "value": float32(0.75)},
			"egg": map[string]any{"name": "Eggbert", "value": float32(0.5)},
		},
		"listTest (long)": []any{int64(11), int64(12), int64(13), int64(14), int64(15)},
		"listTest (compound)": []any{
			map[string]any{"name": "Compound tag #0", "created-on": int64(1264099775885)},
			map[string]any{"name": "Compound tag #1", "created-on": int64(1264099775885)},
		},
		"byteTest": int8(127),
		"byteArrayTest (the first 1000 values of (n*n*255+n*7)%100, starting with n=0 (0, 62, 34, 16, 8, ...))": bigtestArray(),
		"doubleTest": float64(0.4931287132182315),
	}
}

func TestWikiBigtest(t *testing.T) {
	raw := bigtestBytes()
	if a := bigtestArray(); a[1] != 62 || a[2] != 34 || a[3] != 16 || a[4] != 8 {
		t.Fatalf("配列の先頭が Wiki と違う: %v", a[:5])
	}
	name, v, err := DecodeFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Level" {
		t.Errorf("名前 = %q", name)
	}
	if !reflect.DeepEqual(v, bigtestValue()) {
		t.Errorf("値が違う:\n%#v", v)
	}

	// gzip で圧縮したもの（本物の bigtest.nbt と同じ形）も読める
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(raw)
	zw.Close()
	_, v2, err := DecodeFile(gz.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v2, v) {
		t.Error("gzip から読んだ値が違う")
	}

	// 読み飛ばすと、ちょうど全部を読み進める
	d := protocol.NewDecoder(raw)
	if err := SkipNamed(d); err != nil {
		t.Fatal(err)
	}
	if d.Remaining() != 0 {
		t.Errorf("読み飛ばしたあとに %d バイト残っている", d.Remaining())
	}
}

// 往復: 読む → 書く → 読むで値が変わらない。書き出しは名前の辞書順なので、
// 2 回目の書き出しのバイト列は 1 回目と一致する。
func TestRoundTrip(t *testing.T) {
	_, v, err := DecodeFile(bigtestBytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, network := range []bool{false, true} {
		e := protocol.NewEncoder()
		if network {
			err = WriteNetwork(e, v)
		} else {
			err = WriteNamed(e, "Level", v)
		}
		if err != nil {
			t.Fatal(err)
		}
		first := append([]byte(nil), e.Bytes()...)
		if len(first) != len(bigtestBytes())-map[bool]int{false: 0, true: 2 + len("Level")}[network] {
			t.Errorf("network=%v: 長さ %d が元と合わない", network, len(first))
		}
		d := protocol.NewDecoder(first)
		var v2 any
		if network {
			v2, err = ReadNetwork(d)
		} else {
			_, v2, err = ReadNamed(d)
		}
		if err != nil {
			t.Fatal(err)
		}
		if d.Remaining() != 0 || !reflect.DeepEqual(v, v2) {
			t.Errorf("network=%v: 往復で値が変わった", network)
		}
		e.Reset()
		if network {
			WriteNetwork(e, v2)
		} else {
			WriteNamed(e, "Level", v2)
		}
		if !bytes.Equal(first, e.Bytes()) {
			t.Errorf("network=%v: 2 回目の書き出しのバイト列が違う", network)
		}
	}
}

// ネットワーク形式: ルートに名前がない。
func TestNetworkFormat(t *testing.T) {
	// {"a": 1b}
	raw := []byte{0x0a, 0x01, 0x00, 0x01, 'a', 0x01, 0x00}
	d := protocol.NewDecoder(append(raw, 0xff))
	v, err := ReadNetwork(d)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v, map[string]any{"a": int8(1)}) {
		t.Errorf("値 = %#v", v)
	}
	if d.Remaining() != 1 {
		t.Errorf("後ろのバイトまで読んだ: 残り %d", d.Remaining())
	}
	// ルートが TAG_End なら値なし
	d = protocol.NewDecoder([]byte{0x00})
	v, err = ReadNetwork(d)
	if err != nil || v != nil || d.Remaining() != 0 {
		t.Errorf("TAG_End: v=%v err=%v 残り %d", v, err, d.Remaining())
	}
	// 1.20.3 以降はルートがコンパウンド以外でもよい（チャットの文字列など）
	d = protocol.NewDecoder([]byte{0x08, 0x00, 0x02, 'h', 'i'})
	v, err = ReadNetwork(d)
	if err != nil || v != "hi" {
		t.Errorf("文字列のルート: v=%v err=%v", v, err)
	}
}

// すべてのタグの型（空のリスト、入れ子のリスト、IntArray、LongArray を含む）。
func TestAllTags(t *testing.T) {
	v := map[string]any{
		"b":    int8(-1),
		"s":    int16(-2),
		"i":    int32(-3),
		"l":    int64(-4),
		"f":    float32(1.5),
		"d":    float64(-2.25),
		"ba":   []byte{1, 2, 3},
		"str":  "あ\x00😀",
		"le":   []any{},
		"ll":   []any{[]any{int32(1)}, []any{"x", "y"}},
		"ia":   []int32{1, -1, math.MaxInt32},
		"la":   []int64{math.MinInt64, 0},
		"c":    map[string]any{},
		"iaE":  []int32{},
		"strE": "",
	}
	e := protocol.NewEncoder()
	if err := WriteNetwork(e, v); err != nil {
		t.Fatal(err)
	}
	got, err := ReadNetwork(protocol.NewDecoder(e.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, v) {
		t.Errorf("往復で値が変わった:\n%#v\n%#v", got, v)
	}
	d := protocol.NewDecoder(e.Bytes())
	if err := SkipNetwork(d); err != nil || d.Remaining() != 0 {
		t.Errorf("読み飛ばし: err=%v 残り %d", err, d.Remaining())
	}
}

// modified UTF-8: U+0000 は C0 80、U+10000 以上はサロゲートペアの 6 バイト。
func TestModifiedUTF8(t *testing.T) {
	raw := []byte{0x08, 0x00, 0x0b, 'a', 0xc0, 0x80, 0xed, 0xa0, 0xbd, 0xed, 0xb8, 0x80, 'b', 'c'}
	v, err := ReadNetwork(protocol.NewDecoder(raw))
	if err != nil {
		t.Fatal(err)
	}
	if v != "a\x00😀bc" {
		t.Errorf("値 = %q", v)
	}
	e := protocol.NewEncoder()
	if err := WriteNetwork(e, "a\x00😀bc"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(e.Bytes(), raw) {
		t.Errorf("書き出し = % x\n期待      % x", e.Bytes(), raw)
	}
}

// 壊れたデータはエラーになり、パニックしない。
func TestBroken(t *testing.T) {
	deep := func(n int) []byte {
		w := &bb{}
		w.u8(TagList)
		for i := 0; i < n-1; i++ {
			w.u8(TagList).i32(1)
		}
		w.u8(TagEnd).i32(0)
		return w.b
	}
	cases := []struct {
		name string
		raw  []byte
		want error
	}{
		{"空", []byte{}, io.ErrUnexpectedEOF},
		{"知らない型", []byte{13}, ErrBadTagType},
		{"コンパウンドの途中で終わる", []byte{0x0a, 0x01, 0x00, 0x01}, io.ErrUnexpectedEOF},
		{"TAG_End が来ない", []byte{0x0a, 0x01, 0x00, 0x01, 'a', 0x05}, io.ErrUnexpectedEOF},
		{"負の配列長", (&bb{}).u8(TagByteArray).i32(-1).b, ErrNegativeLength},
		{"巨大な配列長", (&bb{}).u8(TagLongArray).i32(math.MaxInt32).b, ErrTooBig},
		{"巨大なリスト", (&bb{}).u8(TagList).u8(TagCompound).i32(math.MaxInt32).b, ErrTooBig},
		{"残りより長い配列", (&bb{}).u8(TagIntArray).i32(1000).b, io.ErrUnexpectedEOF},
		{"TAG_End の要素を持つリスト", (&bb{}).u8(TagList).u8(TagEnd).i32(3).b, ErrBadList},
		{"リストの要素の型が知らない型", (&bb{}).u8(TagList).u8(20).i32(0).b, ErrBadTagType},
		{"文字列の長さが足りない", (&bb{}).u8(TagString).u16(10).b, io.ErrUnexpectedEOF},
		{"正しくない modified UTF-8", []byte{0x08, 0x00, 0x01, 0xff}, ErrBadString},
		{"途中で切れた 2 バイト文字", []byte{0x08, 0x00, 0x01, 0xc3}, ErrBadString},
		{"深すぎる", deep(513), ErrTooDeep},
	}
	for _, c := range cases {
		_, err := ReadNetwork(protocol.NewDecoder(c.raw))
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, 期待 %v", c.name, err, c.want)
		}
		// 読み飛ばしは文字列の中身（modified UTF-8 として正しいか）を確かめない
		if errors.Is(c.want, ErrBadString) {
			continue
		}
		if err := SkipNetwork(protocol.NewDecoder(c.raw)); err == nil {
			t.Errorf("%s: 読み飛ばしでエラーにならない", c.name)
		}
	}
	// 深さ 512 ちょうどは読める
	if _, err := ReadNetwork(protocol.NewDecoder(deep(512))); err != nil {
		t.Errorf("深さ 512: %v", err)
	}
	// 読む量の上限
	lim := Limits{MaxDepth: 512, MaxBytes: 100}
	big := (&bb{}).u8(TagByteArray).i32(200).b
	big = append(big, make([]byte, 200)...)
	if _, err := lim.ReadNetwork(protocol.NewDecoder(big)); !errors.Is(err, ErrTooBig) {
		t.Errorf("上限を超える: err = %v", err)
	}
	// 圧縮されたデータの展開後の大きさも上限で止める
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(make([]byte, 10000))
	zw.Close()
	if _, _, err := lim.DecodeFile(gz.Bytes()); !errors.Is(err, ErrTooBig) {
		t.Errorf("展開後に上限を超える: err = %v", err)
	}
	// ファイルの後ろの余り
	if _, _, err := DecodeFile(append(append([]byte(nil), testNBT...), 0)); err == nil {
		t.Error("後ろの余りでエラーにならない")
	}
}

func TestWriteErrors(t *testing.T) {
	bad := []any{
		map[string]any{"x": 1}, // int は書けない
		[]any{int32(1), int64(2)},
		struct{}{},
	}
	for _, v := range bad {
		if err := WriteNetwork(protocol.NewEncoder(), v); !errors.Is(err, ErrUnsupportedValue) {
			t.Errorf("%#v: err = %v", v, err)
		}
	}
}
