package chunk

import (
	"testing"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// FuzzReadChunkPacket: どんなバイト列でもパニックしない。読めたなら、全部の座標が読める。
func FuzzReadChunkPacket(f *testing.F) {
	// 高さ 32（2 セクション）の小さな Format で、本物に近い形の種を入れる
	small := Format{MinY: 0, Height: 32, BlockStateBits: testBlockBits, BiomeBits: testBiomeBits}
	seed := func(secs func(e *protocol.Encoder)) []byte {
		e := protocol.NewEncoder()
		e.WriteInt(1)
		e.WriteInt(2)
		e.WriteUByte(0) // 高さマップなし（TAG_End）
		data := protocol.NewEncoder()
		secs(data)
		e.WritePrefixedBytes(data.Bytes())
		e.WriteVarInt(0)
		var m protocol.BitSet
		m.Set(1)
		e.WriteBitSet(m)
		for range 3 {
			e.WriteBitSet(nil)
		}
		e.WriteVarInt(1)
		e.WritePrefixedBytes(make([]byte, LightBytes))
		e.WriteVarInt(0)
		return e.Bytes()
	}
	f.Add(seed(func(e *protocol.Encoder) { airSection(e); airSection(e) }))
	f.Add(seed(func(e *protocol.Encoder) {
		vals := make([]int32, SectionBlocks)
		for i := range vals {
			vals[i] = int32(i % 7)
		}
		section(e, 1, indirect(4, 4, vals), indirect(2, 2, make([]int32, 64)))
		section(e, 1, direct(15, 15, vals), direct(7, 7, make([]int32, 64)))
	}))
	f.Fuzz(func(t *testing.T, raw []byte) {
		c, err := ReadChunkPacket(protocol.NewDecoder(raw), small)
		if err != nil {
			return
		}
		for y := -16; y < 48; y++ {
			for i := 0; i < 16; i++ {
				if y >= 0 && y < 32 {
					if _, err := c.Block(i, y, 15-i); err != nil {
						t.Fatal(err)
					}
					if _, err := c.Biome(i, y, i); err != nil {
						t.Fatal(err)
					}
				}
				if _, _, err := c.SkyLight(i, y, i); err != nil {
					t.Fatal(err)
				}
				if _, _, err := c.BlockLight(i, y, i); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}
