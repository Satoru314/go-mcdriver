package protocol

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"errors"
	"io"
	"testing"
)

// 例: 圧縮なしの Handshake のような小さなパケット
func TestFrameUncompressed(t *testing.T) {
	p := Packet{ID: 0x00, Data: []byte{0xf6, 0x05, 0x01, 'a'}}
	b, err := EncodePacket(p, NoCompression)
	if err != nil {
		t.Fatal(err)
	}
	// Length=5, ID=0, Data
	want := []byte{0x05, 0x00, 0xf6, 0x05, 0x01, 'a'}
	if !bytes.Equal(b, want) {
		t.Errorf("EncodePacket = % x, 期待 % x", b, want)
	}
	got, err := ReadPacket(bytes.NewReader(b), NoCompression)
	if err != nil || got.ID != p.ID || !bytes.Equal(got.Data, p.Data) {
		t.Errorf("往復 %v → %v, %v", p, got, err)
	}
}

func TestFrameCompressedBelowThreshold(t *testing.T) {
	// 閾値未満は Data Length 0 で圧縮しない
	p := Packet{ID: 0x1a, Data: []byte{1, 2, 3}}
	b, err := EncodePacket(p, 256)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x05, 0x00, 0x1a, 1, 2, 3}
	if !bytes.Equal(b, want) {
		t.Errorf("EncodePacket = % x, 期待 % x", b, want)
	}
	got, err := ReadPacket(bytes.NewReader(b), 256)
	if err != nil || got.ID != p.ID || !bytes.Equal(got.Data, p.Data) {
		t.Errorf("往復 %v → %v, %v", p, got, err)
	}
}

func TestFrameCompressedAboveThreshold(t *testing.T) {
	data := bytes.Repeat([]byte("minecraft:stone "), 100) // 1600 バイト、よく縮む
	p := Packet{ID: 0x27, Data: data}
	b, err := EncodePacket(p, 256)
	if err != nil {
		t.Fatal(err)
	}
	// 中身を手で確かめる: Packet Length, Data Length = 1 + 1600, zlib
	d := NewDecoder(b)
	plen, _ := d.ReadVarInt()
	if int(plen) != d.Remaining() {
		t.Errorf("Packet Length %d, 残り %d", plen, d.Remaining())
	}
	dlen, _ := d.ReadVarInt()
	if dlen != int32(1+len(data)) {
		t.Errorf("Data Length %d, 期待 %d", dlen, 1+len(data))
	}
	if len(b) >= len(data) {
		t.Errorf("圧縮したのに縮んでいない: %d バイト", len(b))
	}
	zr, err := zlib.NewReader(bytes.NewReader(d.ReadRest()))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil || raw[0] != 0x27 || !bytes.Equal(raw[1:], data) {
		t.Errorf("zlib の中身が違う: %v", err)
	}

	got, err := ReadPacket(bytes.NewReader(b), 256)
	if err != nil || got.ID != p.ID || !bytes.Equal(got.Data, p.Data) {
		t.Errorf("往復 ID %d, %d バイト, %v", got.ID, len(got.Data), err)
	}
}

func TestFrameThresholdExact(t *testing.T) {
	const threshold = 64
	// ID(1 バイト) + Data(63 バイト) = 64 = 閾値ちょうど → 圧縮する
	exact := Packet{ID: 0x01, Data: bytes.Repeat([]byte{7}, threshold-1)}
	b, err := EncodePacket(exact, threshold)
	if err != nil {
		t.Fatal(err)
	}
	d := NewDecoder(b)
	_, _ = d.ReadVarInt()
	if dlen, _ := d.ReadVarInt(); dlen != threshold {
		t.Errorf("閾値ちょうど: Data Length %d, 期待 %d（圧縮する）", dlen, threshold)
	}
	got, err := ReadPacket(bytes.NewReader(b), threshold)
	if err != nil || got.ID != exact.ID || !bytes.Equal(got.Data, exact.Data) {
		t.Errorf("閾値ちょうどの往復: %v", err)
	}

	// 1 バイト少ない → 圧縮しない（Data Length 0）
	below := Packet{ID: 0x01, Data: bytes.Repeat([]byte{7}, threshold-2)}
	b, err = EncodePacket(below, threshold)
	if err != nil {
		t.Fatal(err)
	}
	d = NewDecoder(b)
	_, _ = d.ReadVarInt()
	if dlen, _ := d.ReadVarInt(); dlen != 0 {
		t.Errorf("閾値-1: Data Length %d, 期待 0", dlen)
	}
	got, err = ReadPacket(bytes.NewReader(b), threshold)
	if err != nil || !bytes.Equal(got.Data, below.Data) {
		t.Errorf("閾値-1 の往復: %v", err)
	}

	// 閾値 0 はすべて圧縮する
	b, _ = EncodePacket(Packet{ID: 5}, 0)
	d = NewDecoder(b)
	_, _ = d.ReadVarInt()
	if dlen, _ := d.ReadVarInt(); dlen != 1 {
		t.Errorf("閾値 0: Data Length %d, 期待 1", dlen)
	}
	got, err = ReadPacket(bytes.NewReader(b), 0)
	if err != nil || got.ID != 5 || len(got.Data) != 0 {
		t.Errorf("閾値 0 の往復: %v, %v", got, err)
	}
}

func TestFrameStream(t *testing.T) {
	// 続けて書いた複数のパケットを順に読める（余分に読み込まない）
	var buf bytes.Buffer
	ps := []Packet{
		{ID: 1, Data: []byte("a")},
		{ID: 2, Data: bytes.Repeat([]byte("b"), 1000)},
		{ID: 3, Data: nil},
	}
	for _, p := range ps {
		if err := WritePacket(&buf, p, 100); err != nil {
			t.Fatal(err)
		}
	}
	r := bufio.NewReader(&buf)
	for _, p := range ps {
		got, err := ReadPacket(r, 100)
		if err != nil || got.ID != p.ID || !bytes.Equal(got.Data, p.Data) {
			t.Fatalf("ID %d を読めない: %v, %v", p.ID, got.ID, err)
		}
	}
	if _, err := ReadPacket(r, 100); err != io.EOF {
		t.Errorf("終わりで io.EOF にならない: %v", err)
	}
}

// compressFrame はテスト用に、圧縮ありの枠を自由な Data Length で手作りする。
func compressFrame(dataLen int32, body []byte) []byte {
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(body)
	zw.Close()
	inner := AppendVarInt(nil, dataLen)
	inner = append(inner, z.Bytes()...)
	return append(AppendVarInt(nil, int32(len(inner))), inner...)
}

func TestFrameBroken(t *testing.T) {
	good, _ := EncodePacket(Packet{ID: 1, Data: bytes.Repeat([]byte("x"), 500)}, 64)
	badChecksum := append([]byte(nil), good...)
	badChecksum[len(badChecksum)-1] ^= 0xff
	body := append([]byte{0x01}, bytes.Repeat([]byte("y"), 200)...)

	cases := []struct {
		name      string
		in        []byte
		threshold int
		want      error // nil なら何かのエラーであればよい
	}{
		{"空", nil, NoCompression, io.EOF},
		{"長さだけ", []byte{0x05}, NoCompression, io.ErrUnexpectedEOF},
		{"中身が足りない", []byte{0x05, 0x00, 0x01}, NoCompression, io.ErrUnexpectedEOF},
		{"長さ 0", []byte{0x00}, NoCompression, ErrEmptyPacket},
		{"長さが負", AppendVarInt(nil, -1), NoCompression, ErrEmptyPacket},
		{"長さが巨大", AppendVarInt(nil, 0x7fffffff), NoCompression, ErrPacketTooLarge},
		{"長さが上限+1", AppendVarInt(nil, MaxPacketLen+1), 256, ErrPacketTooLarge},
		{"長さの VarInt が長すぎる", []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0x01}, NoCompression, ErrVarIntTooBig},
		{"Packet ID が途中で終わる", []byte{0x01, 0x80}, NoCompression, io.ErrUnexpectedEOF},
		{"Packet ID が負", append([]byte{0x05}, AppendVarInt(nil, -1)...), NoCompression, nil},
		{"zlib でないデータ", []byte{0x04, 0x64, 0xde, 0xad, 0xbe}, 64, ErrBadCompression},
		{"チェックサム違い", badChecksum, 64, ErrBadCompression},
		{"Data Length が実際より大きい", compressFrame(int32(len(body)+10), body), 64, ErrBadCompression},
		{"Data Length が実際より小さい", compressFrame(int32(len(body)-10), body), 64, ErrBadCompression},
		{"Data Length が閾値未満なのに圧縮", compressFrame(int32(len(body)), body), 1000, ErrBadCompression},
		{"Data Length が上限超え", compressFrame(MaxUncompressedLen+1, body), 64, ErrPacketTooLarge},
		{"Data Length が負", compressFrame(-1, body), 64, ErrBadCompression},
		{"Data Length の VarInt が途中", []byte{0x01, 0x80}, 64, ErrBadCompression},
		{"Data Length 0 で中身なし", []byte{0x01, 0x00}, 64, ErrEmptyPacket},
	}
	for _, tc := range cases {
		_, err := ReadPacket(bytes.NewReader(tc.in), tc.threshold)
		if err == nil {
			t.Errorf("%s: エラーにならない", tc.name)
			continue
		}
		if tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, 期待 %v", tc.name, err, tc.want)
		}
	}
}

func TestFrameEncodeLimits(t *testing.T) {
	// 圧縮なしで枠の上限を超える
	if _, err := EncodePacket(Packet{ID: 0, Data: make([]byte, MaxPacketLen)}, NoCompression); !errors.Is(err, ErrPacketTooLarge) {
		t.Errorf("圧縮なしの上限超え: %v", err)
	}
	// 圧縮前の上限を超える
	if _, err := EncodePacket(Packet{ID: 0, Data: make([]byte, MaxUncompressedLen)}, 256); !errors.Is(err, ErrPacketTooLarge) {
		t.Errorf("圧縮前の上限超え: %v", err)
	}
	// よく縮むなら、枠の上限より大きくても圧縮して送れる
	big := Packet{ID: 0x27, Data: make([]byte, 4<<20)}
	b, err := EncodePacket(big, 256)
	if err != nil {
		t.Fatalf("4MiB のゼロ: %v", err)
	}
	got, err := ReadPacket(bytes.NewReader(b), 256)
	if err != nil || len(got.Data) != len(big.Data) {
		t.Errorf("4MiB の往復: %d, %v", len(got.Data), err)
	}
	if _, err := EncodePacket(Packet{ID: -1}, NoCompression); err == nil {
		t.Errorf("負の Packet ID がエラーにならない")
	}
}
