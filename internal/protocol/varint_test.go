package protocol

import (
	"bytes"
	"errors"
	"io"
	"math"
	"testing"
)

// 仕様書（Java Edition protocol の VarInt and VarLong）の例。
var varIntExamples = []struct {
	v    int32
	want []byte
}{
	{0, []byte{0x00}},
	{1, []byte{0x01}},
	{2, []byte{0x02}},
	{127, []byte{0x7f}},
	{128, []byte{0x80, 0x01}},
	{255, []byte{0xff, 0x01}},
	{25565, []byte{0xdd, 0xc7, 0x01}},
	{2097151, []byte{0xff, 0xff, 0x7f}},
	{2147483647, []byte{0xff, 0xff, 0xff, 0xff, 0x07}},
	{-1, []byte{0xff, 0xff, 0xff, 0xff, 0x0f}},
	{-2147483648, []byte{0x80, 0x80, 0x80, 0x80, 0x08}},
}

var varLongExamples = []struct {
	v    int64
	want []byte
}{
	{0, []byte{0x00}},
	{1, []byte{0x01}},
	{2, []byte{0x02}},
	{127, []byte{0x7f}},
	{128, []byte{0x80, 0x01}},
	{255, []byte{0xff, 0x01}},
	{2147483647, []byte{0xff, 0xff, 0xff, 0xff, 0x07}},
	{9223372036854775807, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}},
	{-1, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}},
	{-2147483648, []byte{0x80, 0x80, 0x80, 0x80, 0xf8, 0xff, 0xff, 0xff, 0xff, 0x01}},
	{-9223372036854775808, []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}},
}

func TestVarIntSpecExamples(t *testing.T) {
	for _, tc := range varIntExamples {
		got := AppendVarInt(nil, tc.v)
		if !bytes.Equal(got, tc.want) {
			t.Errorf("AppendVarInt(%d) = % x, 期待 % x", tc.v, got, tc.want)
		}
		if n := VarIntLen(tc.v); n != len(tc.want) {
			t.Errorf("VarIntLen(%d) = %d, 期待 %d", tc.v, n, len(tc.want))
		}
		v, n, err := DecodeVarInt(tc.want)
		if err != nil || v != tc.v || n != len(tc.want) {
			t.Errorf("DecodeVarInt(% x) = %d, %d, %v, 期待 %d, %d", tc.want, v, n, err, tc.v, len(tc.want))
		}
		v, err = ReadVarIntFrom(bytes.NewReader(tc.want))
		if err != nil || v != tc.v {
			t.Errorf("ReadVarIntFrom(% x) = %d, %v", tc.want, v, err)
		}
		// io.ByteReader でない Reader でも読める
		v, err = ReadVarIntFrom(io.MultiReader(bytes.NewReader(tc.want)))
		if err != nil || v != tc.v {
			t.Errorf("ReadVarIntFrom(MultiReader % x) = %d, %v", tc.want, v, err)
		}
	}
}

func TestVarLongSpecExamples(t *testing.T) {
	for _, tc := range varLongExamples {
		got := AppendVarLong(nil, tc.v)
		if !bytes.Equal(got, tc.want) {
			t.Errorf("AppendVarLong(%d) = % x, 期待 % x", tc.v, got, tc.want)
		}
		v, n, err := DecodeVarLong(tc.want)
		if err != nil || v != tc.v || n != len(tc.want) {
			t.Errorf("DecodeVarLong(% x) = %d, %d, %v, 期待 %d, %d", tc.want, v, n, err, tc.v, len(tc.want))
		}
	}
}

func TestVarIntRoundTrip(t *testing.T) {
	vals := []int32{0, 1, -1, 63, 64, 127, 128, 16383, 16384, 2097151, 2097152,
		268435455, 268435456, math.MaxInt32, math.MinInt32, -12345}
	for _, v := range vals {
		got, _, err := DecodeVarInt(AppendVarInt(nil, v))
		if err != nil || got != v {
			t.Errorf("VarInt 往復 %d → %d, %v", v, got, err)
		}
	}
	lvals := []int64{0, 1, -1, math.MaxInt64, math.MinInt64, math.MaxInt32, math.MinInt32, 1 << 56, -(1 << 40)}
	for _, v := range lvals {
		got, _, err := DecodeVarLong(AppendVarLong(nil, v))
		if err != nil || got != v {
			t.Errorf("VarLong 往復 %d → %d, %v", v, got, err)
		}
	}
}

func TestVarIntErrors(t *testing.T) {
	// 6 バイト目まで続きのビットが立っている
	if _, _, err := DecodeVarInt([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x01}); !errors.Is(err, ErrVarIntTooBig) {
		t.Errorf("長すぎる VarInt: %v", err)
	}
	if _, err := ReadVarIntFrom(bytes.NewReader([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0x01})); !errors.Is(err, ErrVarIntTooBig) {
		t.Errorf("長すぎる VarInt（Reader）: %v", err)
	}
	if _, _, err := DecodeVarLong(bytes.Repeat([]byte{0x80}, 11)); !errors.Is(err, ErrVarLongTooBig) {
		t.Errorf("長すぎる VarLong: %v", err)
	}
	// 途中で終わる
	if _, _, err := DecodeVarInt([]byte{0x80}); err != io.ErrUnexpectedEOF {
		t.Errorf("途中で終わる VarInt: %v", err)
	}
	if _, _, err := DecodeVarInt(nil); err != io.EOF {
		t.Errorf("空の VarInt: %v", err)
	}
	if _, err := ReadVarIntFrom(bytes.NewReader([]byte{0x80, 0x80})); err != io.ErrUnexpectedEOF {
		t.Errorf("途中で終わる VarInt（Reader）: %v", err)
	}
	if _, err := ReadVarIntFrom(bytes.NewReader(nil)); err != io.EOF {
		t.Errorf("空の VarInt（Reader）: %v", err)
	}
}
