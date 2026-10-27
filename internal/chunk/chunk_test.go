package chunk

import (
	"errors"
	"io"
	"math/rand/v2"
	"testing"

	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// テストで使うビット数。1.21.4 のブロック状態の総数（約 2.8 万）は 15 ビット、バイオームは 7 ビットに入る。
// 実際の値は公式データ（単位 1）から渡すので、ここでは仮の値。
const (
	testBlockBits = 15
	testBiomeBits = 7
)

var testFormat = Overworld(testBlockBits, testBiomeBits)

// packLongs は仕様どおりに値を long に詰める: 1 つの long に 64/bits 個、下位ビットから順に入れ、
// 要素が long の境界をまたがないように余りのビットは 0 のままにする。
func packLongs(vals []int, bits int) []int64 {
	per := 64 / bits
	out := make([]int64, (len(vals)+per-1)/per)
	for i, v := range vals {
		out[i/per] |= int64(uint64(v) << ((i % per) * bits))
	}
	return out
}

// container は paletted container のバイト列を書く道具。
type container struct {
	bpe     byte    // 送る bits per entry
	single  int32   // bpe == 0 のときの値
	palette []int32 // パレット（nil ならパレットなし）
	direct  bool    // パレットなし
	data    []int64 // long の並び
}

func (c container) write(e *protocol.Encoder) {
	e.WriteUByte(c.bpe)
	switch {
	case c.bpe == 0:
		e.WriteVarInt(c.single)
	case !c.direct:
		e.WriteVarInt(int32(len(c.palette)))
		for _, p := range c.palette {
			e.WriteVarInt(p)
		}
	}
	e.WriteLongArray(c.data)
}

// indirect は値の並び vals（ID）から、パレットと bits ビットの並びを作る。
func indirect(bpe byte, bits int, vals []int32) container {
	idx := map[int32]int{}
	var pal []int32
	nums := make([]int, len(vals))
	for i, v := range vals {
		n, ok := idx[v]
		if !ok {
			n = len(pal)
			idx[v] = n
			pal = append(pal, v)
		}
		nums[i] = n
	}
	return container{bpe: bpe, palette: pal, data: packLongs(nums, bits)}
}

func direct(bpe byte, bits int, vals []int32) container {
	nums := make([]int, len(vals))
	for i, v := range vals {
		nums[i] = int(v)
	}
	return container{bpe: bpe, direct: true, data: packLongs(nums, bits)}
}

// randomVals は 0〜kinds-1 番目の ID（base から並ぶ）をばらばらに並べたもの。
func randomVals(r *rand.Rand, n, kinds int, maxID int32) []int32 {
	ids := make([]int32, kinds)
	for i := range ids {
		ids[i] = r.Int32N(maxID)
	}
	out := make([]int32, n)
	for i := range out {
		out[i] = ids[r.IntN(kinds)]
	}
	return out
}

// section は 1 つのセクションのバイト列を書く。
func section(e *protocol.Encoder, count int16, blocks, biomes container) {
	e.WriteShort(count)
	blocks.write(e)
	biomes.write(e)
}

func airSection(e *protocol.Encoder) {
	section(e, 0, container{bpe: 0, single: 0}, container{bpe: 0, single: 0})
}

// decodeOne は 1 セクションだけの Format（高さ 16）で読む。
func decodeOne(t *testing.T, raw []byte) (Section, error) {
	t.Helper()
	f := Format{MinY: 0, Height: 16, BlockStateBits: testBlockBits, BiomeBits: testBiomeBits}
	secs, err := DecodeSections(raw, f)
	if err != nil {
		return Section{}, err
	}
	return secs[0], nil
}

func checkSection(t *testing.T, name string, s Section, blocks, biomes []int32) {
	t.Helper()
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				if got, want := s.Block(x, y, z), blocks[BlockIndex(x, y, z)]; got != want {
					t.Fatalf("%s: ブロック (%d,%d,%d) = %d, 期待 %d", name, x, y, z, got, want)
				}
				if got, want := s.Biome(x, y, z), biomes[BiomeIndex(x>>2, y>>2, z>>2)]; got != want {
					t.Fatalf("%s: バイオーム (%d,%d,%d) = %d, 期待 %d", name, x, y, z, got, want)
				}
			}
		}
	}
}

func fill(n int, v int32) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// ブロック状態の paletted container: 値 1 つ、パレットあり（各 bits per entry）、パレットなし。
func TestBlockContainers(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	biomes := fill(SectionBiomes, 3)
	biomeC := container{bpe: 0, single: 3}

	// 値 1 つ
	e := protocol.NewEncoder()
	section(e, 4096, container{bpe: 0, single: 1}, biomeC)
	s, err := decodeOne(t, e.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	checkSection(t, "single", s, fill(SectionBlocks, 1), biomes)
	if v, ok := s.Blocks.Uniform(); !ok || v != 1 {
		t.Errorf("single: 並びを持たずに値 1 つで持つはず: %d %v", v, ok)
	}

	// パレットあり: 送る bits per entry が 1〜4 なら 4 ビットで詰めてある。5〜8 はそのまま
	for bpe := 1; bpe <= 8; bpe++ {
		bits := max(bpe, 4)
		kinds := 1 << bpe
		blocks := randomVals(r, SectionBlocks, kinds, 1<<testBlockBits)
		c := indirect(byte(bpe), bits, blocks)
		if len(c.data) != (4096+64/bits-1)/(64/bits) {
			t.Fatalf("テストの組み立てが違う: long %d 個", len(c.data))
		}
		e := protocol.NewEncoder()
		section(e, 100, c, biomeC)
		s, err := decodeOne(t, e.Bytes())
		if err != nil {
			t.Fatalf("bpe=%d: %v", bpe, err)
		}
		checkSection(t, "indirect", s, blocks, biomes)
		if s.BlockCount != 100 {
			t.Errorf("ブロック数 = %d", s.BlockCount)
		}
	}

	// パレットなし: 送る bits per entry が 9 以上なら、ビット数は Format の 15（送られた値は使わない）
	for _, bpe := range []byte{9, 15} {
		blocks := randomVals(r, SectionBlocks, 4096, 1<<testBlockBits)
		e := protocol.NewEncoder()
		section(e, 4096, direct(bpe, testBlockBits, blocks), biomeC)
		s, err := decodeOne(t, e.Bytes())
		if err != nil {
			t.Fatalf("direct bpe=%d: %v", bpe, err)
		}
		checkSection(t, "direct", s, blocks, biomes)
	}
}

// ビットの並びを手で書いた long で確かめる（packLongs の書き間違いでテストが通ってしまうのを防ぐ）。
func TestBitLayoutByHand(t *testing.T) {
	// 4 ビットのパレット [0, 1, 9]。long 0 の下位 4 ビットが 0 番、その上が 1 番。
	data := make([]int64, 256)
	data[0] = 0x21        // 0 番 → パレット 1、1 番 → パレット 2
	data[1] = 0x2 << 60   // 31 番（long 1 の最上位 4 ビット）→ パレット 2
	data[255] = 0x1 << 60 // 4095 番 → パレット 1
	e := protocol.NewEncoder()
	section(e, 3, container{bpe: 4, palette: []int32{0, 1, 9}, data: data}, container{bpe: 0, single: 0})
	s, err := decodeOne(t, e.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		x, y, z int
		want    int32
	}{
		{0, 0, 0, 1}, {1, 0, 0, 9}, {2, 0, 0, 0}, {15, 0, 1, 9}, {15, 15, 15, 1}, {14, 15, 15, 0},
	}
	for _, c := range cases {
		if got := s.Block(c.x, c.y, c.z); got != c.want {
			t.Errorf("4 ビット (%d,%d,%d) = %d, 期待 %d", c.x, c.y, c.z, got, c.want)
		}
	}

	// 15 ビットのパレットなし: 1 つの long に 4 個（60 ビット）、上位 4 ビットは使わない。
	// 4 番は long 1 の下位 15 ビットにあり、long 0 の 60〜63 ビットにはない。
	data = make([]int64, 1024)
	u := uint64(1 | 2<<15 | 3<<30 | 0x7FFF<<45 | 0xF<<60) // 上位 4 ビットのごみは無視される
	data[0] = int64(u)
	data[1] = 5
	e = protocol.NewEncoder()
	section(e, 5, container{bpe: 15, direct: true, data: data}, container{bpe: 0, single: 0})
	s, err = decodeOne(t, e.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int32{1, 2, 3, 0x7FFF, 5, 0} {
		if got := s.Block(i, 0, 0); got != want {
			t.Errorf("15 ビット %d 番 = %d, 期待 %d", i, got, want)
		}
	}
}

// バイオームの paletted container: 0 は値 1 つ、1〜3 はそのビット数のパレット、4 以上はパレットなし（7 ビット）。
func TestBiomeContainers(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	blocks := fill(SectionBlocks, 0)
	for bpe := 1; bpe <= 3; bpe++ {
		biomes := randomVals(r, SectionBiomes, 1<<bpe, 1<<testBiomeBits)
		e := protocol.NewEncoder()
		section(e, 0, container{bpe: 0, single: 0}, indirect(byte(bpe), bpe, biomes))
		s, err := decodeOne(t, e.Bytes())
		if err != nil {
			t.Fatalf("bpe=%d: %v", bpe, err)
		}
		checkSection(t, "biome indirect", s, blocks, biomes)
	}
	for _, bpe := range []byte{4, 7} {
		biomes := randomVals(r, SectionBiomes, 64, 1<<testBiomeBits)
		c := direct(bpe, testBiomeBits, biomes)
		if len(c.data) != 8 { // 64/7 = 9 個ずつで 64 個 → 8 個の long
			t.Fatalf("テストの組み立てが違う: long %d 個", len(c.data))
		}
		e := protocol.NewEncoder()
		section(e, 0, container{bpe: 0, single: 0}, c)
		s, err := decodeOne(t, e.Bytes())
		if err != nil {
			t.Fatalf("bpe=%d: %v", bpe, err)
		}
		checkSection(t, "biome direct", s, blocks, biomes)
	}
}

// 壊れたセクションはエラーになる。
func TestBrokenSections(t *testing.T) {
	biomeC := container{bpe: 0, single: 0}
	build := func(blocks container) []byte {
		e := protocol.NewEncoder()
		section(e, 0, blocks, biomeC)
		return e.Bytes()
	}
	vals := make([]int, 4096)
	cases := []struct {
		name string
		raw  []byte
		want error
	}{
		{"空", nil, io.ErrUnexpectedEOF},
		{"送られた bpe=1 を 1 ビットで詰めた（4 ビットのはず）", build(container{bpe: 1, palette: []int32{0, 1}, data: packLongs(vals, 1)}), ErrBadData},
		{"long が足りない", build(container{bpe: 4, palette: []int32{0}, data: make([]int64, 255)}), ErrBadData},
		{"パレットの外の番号", build(container{bpe: 4, palette: []int32{0}, data: []int64{2: 1, 255: 0}}), ErrBadData},
		{"パレットの値が大きすぎる", build(container{bpe: 4, palette: []int32{1 << 15}, data: make([]int64, 256)}), ErrBadData},
		{"値 1 つが負", build(container{bpe: 0, single: -1}), ErrBadData},
		{"途中で切れる", build(container{bpe: 15, direct: true, data: make([]int64, 1024)})[:100], protocol.ErrTooLong},
	}
	for _, c := range cases {
		_, err := decodeOne(t, c.raw)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, 期待 %v", c.name, err, c.want)
		}
	}
	// セクションが足りない（24 個のはずが 1 個）
	if _, err := DecodeSections(build(container{bpe: 0}), testFormat); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("セクションが足りない: err = %v", err)
	}
	for _, f := range []Format{{Height: 0, BlockStateBits: 15, BiomeBits: 7}, {Height: 384, MinY: -60, BlockStateBits: 15, BiomeBits: 7},
		{Height: 384, BlockStateBits: 17, BiomeBits: 7}, {Height: 384, BlockStateBits: 15}} {
		if _, err := DecodeSections(nil, f); !errors.Is(err, ErrBadFormat) {
			t.Errorf("Format %+v: err = %v", f, err)
		}
	}
}

func TestBitsFor(t *testing.T) {
	for _, c := range [][2]int{{1, 0}, {2, 1}, {3, 2}, {64, 6}, {65, 7}, {27946, 15}, {32768, 15}, {32769, 16}} {
		if got := BitsFor(c[0]); got != c[1] {
			t.Errorf("BitsFor(%d) = %d, 期待 %d", c[0], got, c[1])
		}
	}
}

// lightArray は (x,y,z) の明るさが f(x,y,z) の配列を仕様どおりに作る（偶数番が下位 4 ビット）。
func lightArray(f func(x, y, z int) int) []byte {
	b := make([]byte, LightBytes)
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				i := y*256 + z*16 + x
				b[i/2] |= byte(f(x, y, z)&15) << (4 * (i % 2))
			}
		}
	}
	return b
}

// buildChunkPacket は Chunk Data and Update Light パケットの中身を仕様どおりに組み立てる。
// セクション 4（y=0〜15）だけに石などを置き、ほかは空気。
func buildChunkPacket(t *testing.T, blocks []int32) []byte {
	e := protocol.NewEncoder()
	e.WriteInt(-3) // Chunk X
	e.WriteInt(7)  // Chunk Z
	hm := map[string]any{"MOTION_BLOCKING": make([]int64, 37), "WORLD_SURFACE": make([]int64, 37)}
	if err := nbt.WriteNetwork(e, hm); err != nil {
		t.Fatal(err)
	}
	data := protocol.NewEncoder()
	for i := 0; i < 24; i++ {
		if i == 4 {
			section(data, 1000, indirect(5, 5, blocks), indirect(1, 1, append(fill(32, 1), fill(32, 2)...)))
		} else {
			airSection(data)
		}
	}
	e.WritePrefixedBytes(data.Bytes())
	// ブロックエンティティ 1 つ: チャンクの中の (5, 3, 9)
	e.WriteVarInt(1)
	e.WriteUByte(5<<4 | 9)
	e.WriteShort(3)
	e.WriteVarInt(7)
	nbt.WriteNetwork(e, map[string]any{"id": "minecraft:chest"})
	// 明るさ: 26 セクション。sky はセクション 5（y=0〜15）に配列、0 番は空。block はセクション 5 と 25（一番上）
	var sky, blk, esky, eblk protocol.BitSet
	sky.Set(5)
	esky.Set(0)
	blk.Set(5)
	blk.Set(25)
	eblk.Set(1)
	e.WriteBitSet(sky)
	e.WriteBitSet(blk)
	e.WriteBitSet(esky)
	e.WriteBitSet(eblk)
	e.WriteVarInt(1)
	e.WritePrefixedBytes(lightArray(func(x, y, z int) int { return 15 - y }))
	e.WriteVarInt(2)
	e.WritePrefixedBytes(lightArray(func(x, y, z int) int { return (x + z) % 16 }))
	e.WritePrefixedBytes(lightArray(func(x, y, z int) int { return 7 }))
	return e.Bytes()
}

func TestChunkPacket(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	blocks := randomVals(r, SectionBlocks, 20, 1<<testBlockBits)
	raw := buildChunkPacket(t, blocks)
	d := protocol.NewDecoder(raw)
	c, err := ReadChunkPacket(d, testFormat)
	if err != nil {
		t.Fatal(err)
	}
	if d.Remaining() != 0 {
		t.Errorf("%d バイト残っている", d.Remaining())
	}
	if c.X != -3 || c.Z != 7 {
		t.Errorf("座標 = (%d, %d)", c.X, c.Z)
	}
	if m, ok := c.Heightmaps.(map[string]any); !ok || len(m["WORLD_SURFACE"].([]int64)) != 37 {
		t.Errorf("高さマップ = %#v", c.Heightmaps)
	}
	// y=0〜15 はセクション 4
	for _, p := range [][3]int{{0, 0, 0}, {15, 15, 15}, {3, 7, 11}, {8, 1, 0}} {
		got, err := c.Block(p[0], p[1], p[2])
		if want := blocks[BlockIndex(p[0], p[1], p[2])]; err != nil || got != want {
			t.Errorf("ブロック %v = %d (%v), 期待 %d", p, got, err, want)
		}
	}
	for _, y := range []int{-64, -1, 16, 319} {
		if got, err := c.Block(0, y, 0); err != nil || got != 0 {
			t.Errorf("y=%d は空気のはず: %d %v", y, got, err)
		}
	}
	for _, y := range []int{-65, 320} {
		if _, err := c.Block(0, y, 0); !errors.Is(err, ErrOutOfRange) {
			t.Errorf("y=%d は範囲の外のはず: %v", y, err)
		}
	}
	if b, _ := c.Biome(0, 0, 0); b != 1 {
		t.Errorf("バイオーム (0,0,0) = %d", b)
	}
	if b, _ := c.Biome(0, 15, 15); b != 2 { // (0, 3, 3) → 番号 60
		t.Errorf("バイオーム (0,15,15) = %d", b)
	}
	if len(c.BlockEntities) != 1 {
		t.Fatalf("ブロックエンティティ %d 個", len(c.BlockEntities))
	}
	be := c.BlockEntities[0]
	if be.X != 5 || be.Y != 3 || be.Z != 9 || be.Type != 7 || be.Data.(map[string]any)["id"] != "minecraft:chest" {
		t.Errorf("ブロックエンティティ = %+v", be)
	}

	// 明るさ
	type lc struct {
		sky     bool
		x, y, z int
		want    int
		ok      bool
	}
	for _, l := range []lc{
		{true, 0, 0, 0, 15, true}, {true, 4, 15, 2, 0, true}, {true, 1, 6, 1, 9, true},
		{true, 0, -80, 0, 0, true},  // 0 番（ワールドの下の 1 つ）は空 → 0
		{true, 0, -64, 0, 0, false}, // 受け取っていない
		{false, 3, 0, 4, 7, true}, {false, 15, 2, 15, 14, true},
		{false, 0, -60, 0, 0, true},  // 1 番（y=-64〜-49）は空
		{false, 0, -70, 0, 0, false}, // 0 番は受け取っていない
		{false, 9, 335, 9, 7, true},  // 25 番（ワールドの上の 1 つ）
		{false, 0, 100, 0, 0, false}, // 受け取っていない
	} {
		var got int
		var ok bool
		var err error
		if l.sky {
			got, ok, err = c.SkyLight(l.x, l.y, l.z)
		} else {
			got, ok, err = c.BlockLight(l.x, l.y, l.z)
		}
		if err != nil || got != l.want || ok != l.ok {
			t.Errorf("明るさ %+v: %d %v %v", l, got, ok, err)
		}
	}
	if _, _, err := c.SkyLight(0, 336, 0); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("y=336 は範囲の外のはず: %v", err)
	}
	if _, _, err := c.SkyLight(0, -81, 0); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("y=-81 は範囲の外のはず: %v", err)
	}
}

func TestUpdateLight(t *testing.T) {
	c, _ := NewColumn(0, 0, testFormat)
	e := protocol.NewEncoder()
	e.WriteVarInt(0)
	e.WriteVarInt(0)
	var sky, empty protocol.BitSet
	sky.Set(2)
	sky.Set(70) // 範囲の外のビットは使わない
	empty.Set(3)
	e.WriteBitSet(sky)
	e.WriteBitSet(nil)
	e.WriteBitSet(empty)
	e.WriteBitSet(nil)
	e.WriteVarInt(2) // 範囲の外のビットの分の配列は読み捨てる
	e.WritePrefixedBytes(lightArray(func(x, y, z int) int { return 12 }))
	e.WritePrefixedBytes(lightArray(func(x, y, z int) int { return 1 }))
	e.WriteVarInt(0)
	x, z, ld, err := ReadUpdateLight(protocol.NewDecoder(e.Bytes()))
	if err != nil || x != 0 || z != 0 {
		t.Fatal(x, z, err)
	}
	if err := c.ApplyLight(ld); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := c.SkyLight(1, -48, 1); v != 12 || !ok { // 2 番 = y -48〜-33
		t.Errorf("sky = %d %v", v, ok)
	}
	if v, ok, _ := c.SkyLight(1, -32, 1); v != 0 || !ok {
		t.Errorf("空のセクションの sky = %d %v", v, ok)
	}
	// 配列が足りない
	bad := &LightData{BlockMask: protocol.BitSet{0b11}, Block: [][]byte{make([]byte, LightBytes)}}
	if err := c.ApplyLight(bad); !errors.Is(err, ErrBadData) {
		t.Errorf("配列が足りない: err = %v", err)
	}
	// 配列の長さが 2048 でない
	e = protocol.NewEncoder()
	for range 4 {
		e.WriteBitSet(nil)
	}
	e.WriteVarInt(1)
	e.WritePrefixedBytes(make([]byte, 100))
	e.WriteVarInt(0)
	// 配列の個数の上限は残りのバイト数（1 つ 2050 バイト以上）から決まるので、後ろを埋めて長さの確認まで届かせる
	e.WriteBytes(make([]byte, LightBytes))
	if _, err := ReadLightData(protocol.NewDecoder(e.Bytes())); !errors.Is(err, ErrBadData) {
		t.Errorf("長さが違う配列: err = %v", err)
	}
}

// 1 ブロックの更新と、セクション単位の複数更新。
func TestUpdates(t *testing.T) {
	air := func(s int32) bool { return s == 0 }
	f := testFormat
	f.IsAir = air
	w, err := NewWorld(f)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := NewColumn(-1, 2, f)
	w.Put(c)

	// Block Update: (-5, 70, 40) に状態 1
	e := protocol.NewEncoder()
	e.WritePosition(protocol.Position{X: -5, Y: 70, Z: 40})
	e.WriteVarInt(1)
	p, id, err := ReadBlockUpdate(protocol.NewDecoder(e.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetBlock(p.X, p.Y, p.Z, id); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.Block(-5, 70, 40); got != 1 {
		t.Errorf("Block Update のあと = %d", got)
	}
	if got, _ := c.Block(11, 70, 8); got != 1 { // -5 & 15 = 11, 40 & 15 = 8
		t.Errorf("チャンクの中の座標で = %d", got)
	}
	if n := c.Sections[(70+64)/16].BlockCount; n != 1 {
		t.Errorf("ブロック数 = %d", n)
	}
	if _, err := w.Block(100, 0, 0); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("読み込まれていないチャンク: %v", err)
	}
	if err := w.SetBlock(-5, 400, 40, 1); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("高さの外: %v", err)
	}
	if err := w.SetBlock(-5, 0, 40, 1<<15); !errors.Is(err, ErrBadData) {
		t.Errorf("大きすぎる ID: %v", err)
	}

	// Update Section Blocks: セクション (-1, -4, 2)（y=-64〜-49）
	sp := SectionPos{X: -1, Y: -4, Z: 2}
	changes := []BlockChange{{X: 0, Y: 0, Z: 0, State: 5}, {X: 15, Y: 15, Z: 15, State: 32767}, {X: 3, Y: 4, Z: 5, State: 9}, {X: 3, Y: 4, Z: 5, State: 0}}
	e = protocol.NewEncoder()
	e.WriteLong(sp.Pack())
	e.WriteVarInt(int32(len(changes)))
	for _, ch := range changes {
		e.WriteVarLong(PackBlockChange(ch))
	}
	gp, gc, err := ReadSectionUpdate(protocol.NewDecoder(e.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if gp != sp {
		t.Errorf("セクションの座標 = %+v", gp)
	}
	if len(gc) != len(changes) || gc[1] != changes[1] {
		t.Errorf("変化 = %+v", gc)
	}
	if err := w.ApplySectionUpdate(gp, gc); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		x, y, z int
		want    int32
	}{
		{-16, -64, 32, 5}, {-1, -49, 47, 32767}, {-13, -60, 37, 0}, {-15, -64, 32, 0},
	} {
		if got, _ := w.Block(q.x, q.y, q.z); got != q.want {
			t.Errorf("(%d,%d,%d) = %d, 期待 %d", q.x, q.y, q.z, got, q.want)
		}
	}
	if n := c.Sections[0].BlockCount; n != 2 {
		t.Errorf("ブロック数 = %d（9 を置いて 0 に戻したので 2 のはず）", n)
	}
	// 正しくない変化があれば何も変えない
	if err := c.ApplySectionUpdate(-4, []BlockChange{{State: 7}, {X: 16, State: 1}}); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("範囲の外: %v", err)
	}
	if got, _ := c.Block(0, -64, 0); got != 5 {
		t.Errorf("エラーのときに変わった: %d", got)
	}
	if err := c.ApplySectionUpdate(20, nil); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("セクションの外: %v", err)
	}
	w.Unload(-1, 2)
	if w.Len() != 0 {
		t.Error("Unload で消えない")
	}
}

// セクションの座標の詰め方: x 22 ビット、z 22 ビット、y 20 ビット。
func TestSectionPos(t *testing.T) {
	// x=1, z=2, y=3 → 1<<42 | 2<<20 | 3
	if v := (SectionPos{X: 1, Y: 3, Z: 2}).Pack(); v != 1<<42|2<<20|3 {
		t.Errorf("Pack = %x", v)
	}
	for _, p := range []SectionPos{{0, 0, 0}, {-1, -1, -1}, {-2097152, -524288, 2097151}, {2097151, 524287, -2097152}, {-4, -4, 3}} {
		if got := UnpackSectionPos(p.Pack()); got != p {
			t.Errorf("往復 %+v → %+v", p, got)
		}
	}
	// 状態 ID << 12 | x << 8 | z << 4 | y
	if v := PackBlockChange(BlockChange{X: 1, Y: 3, Z: 2, State: 10}); v != 10<<12|0x123 {
		t.Errorf("PackBlockChange = %x", v)
	}
}
