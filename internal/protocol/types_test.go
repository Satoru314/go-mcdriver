package protocol

import (
	"bytes"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
)

func TestPositionSpecExample(t *testing.T) {
	// 仕様書の例: x=18357644, y=831, z=-20882616 は
	// 01000110000001110110001100 10110000010101101101001000 001100111111
	const packed = int64(0b01000110000001110110001100_10110000010101101101001000_001100111111)
	p := Position{X: 18357644, Y: 831, Z: -20882616}
	got, err := p.Pack()
	if err != nil {
		t.Fatal(err)
	}
	if got != packed {
		t.Errorf("Pack(%v) = %#016x, 期待 %#016x", p, uint64(got), uint64(packed))
	}
	if back := UnpackPosition(packed); back != p {
		t.Errorf("UnpackPosition(%#016x) = %v, 期待 %v", uint64(packed), back, p)
	}
	// バイト列（ビッグエンディアン）としても一致する
	e := NewEncoder()
	if err := e.WritePosition(p); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x46, 0x07, 0x63, 0x2c, 0x15, 0xb4, 0x83, 0x3f}
	if !bytes.Equal(e.Bytes(), want) {
		t.Errorf("WritePosition = % x, 期待 % x", e.Bytes(), want)
	}
}

func TestPositionRoundTrip(t *testing.T) {
	cases := []Position{
		{0, 0, 0},
		{-1, -1, -1},
		{1, -64, -1},
		{-1, 319, 1},
		{-300, -60, 1234},
		{PositionMinXZ, PositionMinY, PositionMinXZ},
		{PositionMaxXZ, PositionMaxY, PositionMaxXZ},
		{PositionMinXZ, PositionMaxY, PositionMaxXZ},
		{-30000000, -2048, 29999999},
	}
	for _, p := range cases {
		e := NewEncoder()
		if err := e.WritePosition(p); err != nil {
			t.Errorf("WritePosition(%v): %v", p, err)
			continue
		}
		got, err := NewDecoder(e.Bytes()).ReadPosition()
		if err != nil || got != p {
			t.Errorf("Position 往復 %v → %v, %v", p, got, err)
		}
	}
	// -1, -1, -1 はすべてのビットが立つ
	v, _ := Position{-1, -1, -1}.Pack()
	if v != -1 {
		t.Errorf("Pack(-1,-1,-1) = %#x, 期待 -1", v)
	}
}

func TestPositionOutOfRange(t *testing.T) {
	cases := []Position{
		{PositionMaxXZ + 1, 0, 0},
		{PositionMinXZ - 1, 0, 0},
		{0, 0, PositionMaxXZ + 1},
		{0, PositionMaxY + 1, 0},
		{0, PositionMinY - 1, 0},
	}
	for _, p := range cases {
		e := NewEncoder()
		if err := e.WritePosition(p); !errors.Is(err, ErrOutOfRange) {
			t.Errorf("WritePosition(%v) = %v, 期待 ErrOutOfRange", p, err)
		}
		if e.Len() != 0 {
			t.Errorf("WritePosition(%v) がエラーなのに %d バイト書いた", p, e.Len())
		}
	}
}

func TestStringRoundTrip(t *testing.T) {
	cases := []string{"", "a", "minecraft:stone", "こんにちは", "😀絵文字", strings.Repeat("x", 300)}
	for _, s := range cases {
		e := NewEncoder()
		if err := e.WriteString(s, MaxStringLen); err != nil {
			t.Fatal(err)
		}
		// 長さの前置きはバイト数
		d := NewDecoder(e.Bytes())
		got, err := d.ReadString(MaxStringLen)
		if err != nil || got != s || d.Remaining() != 0 {
			t.Errorf("String 往復 %q → %q, %v, 残り %d", s, got, err, d.Remaining())
		}
	}
	// 仕様の形: 長さ VarInt のあとに UTF-8
	e := NewEncoder()
	_ = e.WriteString("あ", 16)
	if want := []byte{0x03, 0xe3, 0x81, 0x82}; !bytes.Equal(e.Bytes(), want) {
		t.Errorf("WriteString(あ) = % x, 期待 % x", e.Bytes(), want)
	}
}

func TestStringTooLong(t *testing.T) {
	// 書くとき: 文字数（UTF-16）で超過
	e := NewEncoder()
	if err := e.WriteString(strings.Repeat("a", 17), 16); !errors.Is(err, ErrStringTooLong) {
		t.Errorf("17 文字 / 上限 16: %v", err)
	}
	if e.Len() != 0 {
		t.Errorf("エラーなのに %d バイト書いた", e.Len())
	}
	if err := e.WriteString(strings.Repeat("a", 16), 16); err != nil {
		t.Errorf("16 文字 / 上限 16: %v", err)
	}
	// 3 バイト文字 16 個 = 48 バイトは上限 16 文字に収まる
	if err := NewEncoder().WriteString(strings.Repeat("あ", 16), 16); err != nil {
		t.Errorf("あ×16 / 上限 16: %v", err)
	}
	// サロゲートペアになる文字は 2 文字と数える
	if err := NewEncoder().WriteString(strings.Repeat("😀", 9), 17); !errors.Is(err, ErrStringTooLong) {
		t.Errorf("😀×9（UTF-16 で 18）/ 上限 17: %v", err)
	}

	// 読むとき: 文字数で超過
	e = NewEncoder()
	_ = e.WriteString(strings.Repeat("a", 17), 100)
	if _, err := NewDecoder(e.Bytes()).ReadString(16); !errors.Is(err, ErrStringTooLong) {
		t.Errorf("読み: 17 文字 / 上限 16: %v", err)
	}
	// 読むとき: 長さの前置きが max*3 を超える（中身を見る前に断る）
	b := AppendVarInt(nil, 49)
	if _, err := NewDecoder(b).ReadString(16); !errors.Is(err, ErrStringTooLong) {
		t.Errorf("読み: 49 バイト / 上限 16: %v", err)
	}
	// 巨大な長さの前置き
	b = AppendVarInt(nil, math.MaxInt32)
	if _, err := NewDecoder(b).ReadString(MaxStringLen); !errors.Is(err, ErrStringTooLong) {
		t.Errorf("読み: 巨大な長さ: %v", err)
	}
	// 負の長さ
	b = AppendVarInt(nil, -5)
	if _, err := NewDecoder(b).ReadString(MaxStringLen); !errors.Is(err, ErrNegativeLength) {
		t.Errorf("読み: 負の長さ: %v", err)
	}
	// 長さの割にデータが足りない
	b = append(AppendVarInt(nil, 10), 'a', 'b')
	if _, err := NewDecoder(b).ReadString(MaxStringLen); err != io.ErrUnexpectedEOF {
		t.Errorf("読み: 足りないデータ: %v", err)
	}
}

func TestIdentifier(t *testing.T) {
	good := []string{"minecraft:stone", "stone", "minecraft:textures/block/stone.png", "my_mod-1.0:a.b_c-d", "brand"}
	for _, s := range good {
		e := NewEncoder()
		if err := e.WriteIdentifier(s); err != nil {
			t.Errorf("WriteIdentifier(%q): %v", s, err)
			continue
		}
		got, err := NewDecoder(e.Bytes()).ReadIdentifier()
		if err != nil || got != s {
			t.Errorf("Identifier 往復 %q → %q, %v", s, got, err)
		}
	}
	bad := []string{"Minecraft:stone", "minecraft:Stone", "a/b:c", "minecraft:stone:x", "minecraft:st one"}
	for _, s := range bad {
		if err := NewEncoder().WriteIdentifier(s); !errors.Is(err, ErrBadIdentifier) {
			t.Errorf("WriteIdentifier(%q) = %v, 期待 ErrBadIdentifier", s, err)
		}
		e := NewEncoder()
		_ = e.WriteString(s, MaxStringLen)
		if _, err := NewDecoder(e.Bytes()).ReadIdentifier(); !errors.Is(err, ErrBadIdentifier) {
			t.Errorf("ReadIdentifier(%q) = %v, 期待 ErrBadIdentifier", s, err)
		}
	}
}

func TestFixedSizeTypes(t *testing.T) {
	e := NewEncoder()
	e.WriteBool(true)
	e.WriteBool(false)
	e.WriteInt8(-2)
	e.WriteUByte(200)
	e.WriteShort(-300)
	e.WriteUShort(65000)
	e.WriteInt(-123456789)
	e.WriteLong(math.MinInt64)
	e.WriteFloat(1.5)
	e.WriteDouble(-2.25)
	e.WriteAngle(AngleFromDegrees(90))
	u := UUID{0x06, 0x9a, 0x79, 0xf4, 0x44, 0xe9, 0x47, 0x26, 0xa5, 0xbe, 0xfc, 0xa9, 0x0e, 0x38, 0xaa, 0xf5}
	e.WriteUUID(u)

	want := []byte{
		0x01, 0x00, 0xfe, 0xc8,
		0xfe, 0xd4, // -300
		0xfd, 0xe8, // 65000
		0xf8, 0xa4, 0x32, 0xeb, // -123456789
		0x80, 0, 0, 0, 0, 0, 0, 0,
		0x3f, 0xc0, 0x00, 0x00, // 1.5
		0xc0, 0x02, 0, 0, 0, 0, 0, 0, // -2.25
		0x40, // 90 度 = 64
		0x06, 0x9a, 0x79, 0xf4, 0x44, 0xe9, 0x47, 0x26, 0xa5, 0xbe, 0xfc, 0xa9, 0x0e, 0x38, 0xaa, 0xf5,
	}
	if !bytes.Equal(e.Bytes(), want) {
		t.Fatalf("書いたバイト列\n% x\n期待\n% x", e.Bytes(), want)
	}

	d := NewDecoder(e.Bytes())
	check := func(name string, got, want any, err error) {
		t.Helper()
		if err != nil || got != want {
			t.Errorf("%s = %v, %v, 期待 %v", name, got, err, want)
		}
	}
	b1, err := d.ReadBool()
	check("Bool", b1, true, err)
	b2, err := d.ReadBool()
	check("Bool", b2, false, err)
	i8, err := d.ReadInt8()
	check("Byte", i8, int8(-2), err)
	u8, err := d.ReadUByte()
	check("UByte", u8, uint8(200), err)
	i16, err := d.ReadShort()
	check("Short", i16, int16(-300), err)
	u16, err := d.ReadUShort()
	check("UShort", u16, uint16(65000), err)
	i32, err := d.ReadInt()
	check("Int", i32, int32(-123456789), err)
	i64, err := d.ReadLong()
	check("Long", i64, int64(math.MinInt64), err)
	f32, err := d.ReadFloat()
	check("Float", f32, float32(1.5), err)
	f64, err := d.ReadDouble()
	check("Double", f64, -2.25, err)
	a, err := d.ReadAngle()
	check("Angle", a.Degrees(), float32(90), err)
	uu, err := d.ReadUUID()
	check("UUID", uu, u, err)
	if s := uu.String(); s != "069a79f4-44e9-4726-a5be-fca90e38aaf5" {
		t.Errorf("UUID.String() = %s", s)
	}
	if d.Remaining() != 0 {
		t.Errorf("読み残し %d", d.Remaining())
	}
	// 足りないときはエラー（パニックしない）
	if _, err := d.ReadLong(); err != io.ErrUnexpectedEOF {
		t.Errorf("空から ReadLong: %v", err)
	}
}

func TestAngle(t *testing.T) {
	cases := []struct {
		deg  float32
		want Angle
	}{{0, 0}, {90, 64}, {180, 128}, {270, 192}, {360, 0}, {-90, 192}, {450, 64}}
	for _, tc := range cases {
		if got := AngleFromDegrees(tc.deg); got != tc.want {
			t.Errorf("AngleFromDegrees(%v) = %d, 期待 %d", tc.deg, got, tc.want)
		}
	}
}

func TestBitSet(t *testing.T) {
	var bs BitSet
	bs.Set(0)
	bs.Set(65)
	bs.Set(130)
	e := NewEncoder()
	e.WriteBitSet(bs)
	want := []byte{0x03,
		0, 0, 0, 0, 0, 0, 0, 0x01,
		0, 0, 0, 0, 0, 0, 0, 0x02,
		0, 0, 0, 0, 0, 0, 0, 0x04}
	if !bytes.Equal(e.Bytes(), want) {
		t.Errorf("WriteBitSet = % x, 期待 % x", e.Bytes(), want)
	}
	got, err := NewDecoder(e.Bytes()).ReadBitSet()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if got.Get(i) != (i == 0 || i == 65 || i == 130) {
			t.Errorf("ビット %d = %v", i, got.Get(i))
		}
	}
	// 巨大な long の個数はエラー（確保しない）
	if _, err := NewDecoder(AppendVarInt(nil, math.MaxInt32)).ReadBitSet(); !errors.Is(err, ErrTooLong) {
		t.Errorf("巨大な BitSet: %v", err)
	}
}

func TestFixedBitSet(t *testing.T) {
	fb := NewFixedBitSet(20)
	if len(fb) != 3 {
		t.Fatalf("NewFixedBitSet(20) の長さ %d, 期待 3", len(fb))
	}
	fb.Set(0)
	fb.Set(9)
	fb.Set(19)
	e := NewEncoder()
	if err := e.WriteFixedBitSet(fb, 20); err != nil {
		t.Fatal(err)
	}
	if want := []byte{0x01, 0x02, 0x08}; !bytes.Equal(e.Bytes(), want) {
		t.Errorf("WriteFixedBitSet = % x, 期待 % x", e.Bytes(), want)
	}
	got, err := NewDecoder(e.Bytes()).ReadFixedBitSet(20)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 24; i++ {
		if got.Get(i) != (i == 0 || i == 9 || i == 19) {
			t.Errorf("ビット %d = %v", i, got.Get(i))
		}
	}
	if err := NewEncoder().WriteFixedBitSet(NewFixedBitSet(20), 8); !errors.Is(err, ErrTooLong) {
		t.Errorf("長すぎる FixedBitSet: %v", err)
	}
	if _, err := NewDecoder([]byte{1}).ReadFixedBitSet(20); err != io.ErrUnexpectedEOF {
		t.Errorf("足りない FixedBitSet: %v", err)
	}
}

func TestPrefixedArrayAndOptional(t *testing.T) {
	type prop struct {
		Name, Value string
		Sig         string
		HasSig      bool
	}
	writeProp := func(e *Encoder, p prop) error {
		if err := e.WriteString(p.Name, 64); err != nil {
			return err
		}
		if err := e.WriteString(p.Value, MaxStringLen); err != nil {
			return err
		}
		return WritePrefixedOptional(e, p.Sig, p.HasSig, func(e *Encoder, s string) error {
			return e.WriteString(s, 1024)
		})
	}
	readProp := func(d *Decoder) (prop, error) {
		var p prop
		var err error
		if p.Name, err = d.ReadString(64); err != nil {
			return p, err
		}
		if p.Value, err = d.ReadString(MaxStringLen); err != nil {
			return p, err
		}
		p.Sig, p.HasSig, err = ReadPrefixedOptional(d, func(d *Decoder) (string, error) { return d.ReadString(1024) })
		return p, err
	}
	in := []prop{{"textures", "abc", "", false}, {"x", "y", "sig", true}}
	e := NewEncoder()
	if err := WritePrefixedArray(e, in, writeProp); err != nil {
		t.Fatal(err)
	}
	d := NewDecoder(e.Bytes())
	out, err := ReadPrefixedArray(d, 16, readProp)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) || out[0] != in[0] || out[1] != in[1] || d.Remaining() != 0 {
		t.Errorf("Prefixed Array 往復 %v → %v（残り %d）", in, out, d.Remaining())
	}

	// 要素数の上限
	if _, err := ReadPrefixedArray(NewDecoder(e.Bytes()), 1, readProp); !errors.Is(err, ErrTooLong) {
		t.Errorf("上限 1 で 2 要素: %v", err)
	}
	// 巨大な要素数（残りのバイト数より多い）
	big := AppendVarInt(nil, math.MaxInt32)
	if _, err := ReadPrefixedArray(NewDecoder(big), math.MaxInt32, (*Decoder).ReadVarInt); !errors.Is(err, ErrTooLong) {
		t.Errorf("巨大な要素数: %v", err)
	}
	// 書きかけのエラーは取り消される
	e = NewEncoder()
	e.WriteUByte(0xaa)
	err = WritePrefixedArray(e, []prop{{"ok", "", "", false}, {strings.Repeat("n", 65), "", "", false}}, writeProp)
	if !errors.Is(err, ErrStringTooLong) || !bytes.Equal(e.Bytes(), []byte{0xaa}) {
		t.Errorf("書きかけの取り消し: %v, % x", err, e.Bytes())
	}
}

func TestLongArrayAndBytes(t *testing.T) {
	in := []int64{1, -1, math.MaxInt64}
	e := NewEncoder()
	if err := e.WriteLongArray(in); err != nil {
		t.Fatal(err)
	}
	if err := e.WritePrefixedBytes([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	e.WriteBytes([]byte{9, 8, 7})
	d := NewDecoder(e.Bytes())
	out, err := d.ReadLongArray()
	if err != nil || len(out) != 3 || out[0] != 1 || out[1] != -1 || out[2] != math.MaxInt64 {
		t.Errorf("LongArray 往復 %v → %v, %v", in, out, err)
	}
	pb, err := d.ReadPrefixedBytes(3)
	if err != nil || string(pb) != "abc" {
		t.Errorf("PrefixedBytes = %q, %v", pb, err)
	}
	if rest := d.ReadRest(); !bytes.Equal(rest, []byte{9, 8, 7}) {
		t.Errorf("ReadRest = % x", rest)
	}
	if _, err := NewDecoder(AppendVarInt(nil, 4)).ReadPrefixedBytes(3); !errors.Is(err, ErrTooLong) {
		t.Errorf("PrefixedBytes の上限超過: %v", err)
	}
	if _, err := NewDecoder(AppendVarInt(nil, 1<<20)).ReadLongArray(); !errors.Is(err, ErrTooLong) {
		t.Errorf("巨大な LongArray: %v", err)
	}
}
