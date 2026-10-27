package nbt

import (
	"bytes"
	"testing"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// FuzzReadNetwork: どんなバイト列でもパニックしない。読めたなら、読み飛ばしも同じだけ進み、
// 書き出し → 読み込み → 書き出しでバイト列が変わらない。
func FuzzReadNetwork(f *testing.F) {
	f.Add(testNBT[3+len("hello world"):]) // 名前を除いたもの（先頭の型は別に足す）
	f.Add(append([]byte{TagCompound}, bigtestBytes()[3+len("Level"):]...))
	f.Add([]byte{TagList, TagList, 0, 0, 0, 1, TagEnd, 0, 0, 0, 0})
	f.Add([]byte{TagString, 0, 2, 0xc0, 0x80})
	f.Fuzz(func(t *testing.T, raw []byte) {
		lim := Limits{MaxDepth: 64, MaxBytes: 1 << 16}
		d := protocol.NewDecoder(raw)
		v, err := lim.ReadNetwork(d)
		ds := protocol.NewDecoder(raw)
		serr := lim.SkipNetwork(ds)
		if err != nil {
			return
		}
		if serr != nil || ds.Offset() != d.Offset() {
			t.Fatalf("読み飛ばしが読み込みと違う: serr=%v %d != %d", serr, ds.Offset(), d.Offset())
		}
		e := protocol.NewEncoder()
		if err := WriteNetwork(e, v); err != nil {
			t.Fatalf("読めた値が書き出せない: %v", err)
		}
		first := append([]byte(nil), e.Bytes()...)
		v2, err := lim.ReadNetwork(protocol.NewDecoder(first))
		if err != nil {
			t.Fatalf("書き出したものが読めない: %v", err)
		}
		e.Reset()
		if err := WriteNetwork(e, v2); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, e.Bytes()) {
			t.Fatal("往復でバイト列が変わった")
		}
	})
}
