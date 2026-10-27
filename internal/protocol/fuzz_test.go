package protocol

import (
	"bytes"
	"testing"
)

// FuzzReadPacket は、どんなバイト列を読んでもパニックせず、
// 読めたパケットは書き直して読み直すと同じになることを確かめる。
// 短時間で回すには: go test -run '^$' -fuzz FuzzReadPacket -fuzztime 10s ./internal/protocol
func FuzzReadPacket(f *testing.F) {
	for _, th := range []int{NoCompression, 0, 64} {
		for _, p := range []Packet{
			{ID: 0, Data: nil},
			{ID: 0x27, Data: bytes.Repeat([]byte("ab"), 100)},
			{ID: 300, Data: []byte{1, 2, 3}},
		} {
			b, _ := EncodePacket(p, th)
			f.Add(b, th)
		}
	}
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0x07}, 64)
	f.Add([]byte{0x04, 0x64, 0x78, 0x9c, 0x00}, 64)

	f.Fuzz(func(t *testing.T, in []byte, threshold int) {
		if threshold > 1<<16 {
			threshold = 1 << 16
		}
		p, err := ReadPacket(bytes.NewReader(in), threshold)
		if err != nil {
			return
		}
		b, err := EncodePacket(p, threshold)
		if err != nil {
			t.Fatalf("読めたパケットを書けない: %v", err)
		}
		q, err := ReadPacket(bytes.NewReader(b), threshold)
		if err != nil || q.ID != p.ID || !bytes.Equal(q.Data, p.Data) {
			t.Fatalf("書き直したパケットが戻らない: %v", err)
		}
	})
}

// FuzzDecoder は、どんなバイト列を基本の型として読んでもパニックしないことを確かめる。
func FuzzDecoder(f *testing.F) {
	f.Add([]byte{0x03, 'a', 'b', 'c', 0x01, 0x80, 0x80, 0x01})
	f.Add(AppendVarInt(nil, 0x7fffffff))
	f.Fuzz(func(t *testing.T, in []byte) {
		d := NewDecoder(in)
		for d.Remaining() > 0 {
			before := d.Offset()
			_, _ = d.ReadString(16)
			_, _ = d.ReadIdentifier()
			_, _ = d.ReadVarLong()
			_, _ = d.ReadBitSet()
			_, _ = d.ReadPosition()
			_, _ = ReadPrefixedArray(d, 1<<20, (*Decoder).ReadVarInt)
			_, _, _ = ReadPrefixedOptional(d, (*Decoder).ReadUUID)
			_, _ = d.ReadLongArray()
			_, _ = d.ReadFixedBitSet(12)
			if d.Offset() == before {
				_ = d.Skip(1)
			}
		}
	})
}

// FuzzVarIntRoundTrip は VarInt と VarLong が往復で値を戻すことを確かめる。
func FuzzVarIntRoundTrip(f *testing.F) {
	f.Add(int32(0), int64(0))
	f.Add(int32(-1), int64(-1))
	f.Add(int32(25565), int64(1<<62))
	f.Fuzz(func(t *testing.T, v int32, w int64) {
		b := AppendVarInt(nil, v)
		got, n, err := DecodeVarInt(b)
		if err != nil || got != v || n != len(b) || n != VarIntLen(v) {
			t.Fatalf("VarInt %d: % x → %d, %d, %v", v, b, got, n, err)
		}
		lb := AppendVarLong(nil, w)
		lgot, ln, err := DecodeVarLong(lb)
		if err != nil || lgot != w || ln != len(lb) {
			t.Fatalf("VarLong %d: % x → %d, %d, %v", w, lb, lgot, ln, err)
		}
	})
}
