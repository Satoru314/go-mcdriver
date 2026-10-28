package mcdriver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/chunk"
	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
	"gonum.org/v1/gonum/spatial/r3"
)

// ---- 偽のサーバーが送るチャンクを組み立てる道具 ----

const testBlockBits = 15 // 27866 状態 → 15 ビット

func stateOf(t *testing.T, s string) data.StateID {
	t.Helper()
	name, props, _ := strings.Cut(strings.TrimSuffix(s, "]"), "[")
	b, ok := data.BlockByName(name)
	if !ok {
		t.Fatalf("ブロック %q が無い", name)
	}
	if props == "" {
		return b.DefaultState
	}
	m := map[string]string{}
	for _, kv := range strings.Split(props, ",") {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	st, err := b.State(m)
	if err != nil {
		t.Fatal(err)
	}
	return st.ID
}

// writeContainer は値の並びから paletted container を書く（全部同じなら値 1 つ、256 種類以下ならパレットあり、それより多ければパレットなし）。
func writeContainer(e *protocol.Encoder, vals []int32, minBits, maxIndirect, directBits int) {
	idx := map[int32]int{}
	var pal []int32
	for _, v := range vals {
		if _, ok := idx[v]; !ok {
			idx[v] = len(pal)
			pal = append(pal, v)
		}
	}
	if len(pal) == 1 {
		e.WriteUByte(0)
		e.WriteVarInt(pal[0])
		e.WriteVarInt(0)
		return
	}
	bits := max(chunk.BitsFor(len(pal)), minBits)
	direct := bits > maxIndirect
	if direct {
		bits = directBits
	}
	e.WriteUByte(byte(bits))
	if !direct {
		e.WriteVarInt(int32(len(pal)))
		for _, p := range pal {
			e.WriteVarInt(p)
		}
	}
	per := 64 / bits
	longs := make([]int64, (len(vals)+per-1)/per)
	for i, v := range vals {
		x := uint64(v)
		if !direct {
			x = uint64(idx[v])
		}
		longs[i/per] |= int64(x << ((i % per) * bits))
	}
	e.WriteLongArray(longs)
}

// lightOf は明るさのセクション 1 つ（2048 バイト）を作る。f はセクションの中の座標から値を返す。
func lightOf(f func(x, y, z int) int) []byte {
	b := make([]byte, chunk.LightBytes)
	for y := range 16 {
		for z := range 16 {
			for x := range 16 {
				i := chunk.BlockIndex(x, y, z)
				b[i/2] |= byte(f(x, y, z)&15) << (4 * (i % 2))
			}
		}
	}
	return b
}

// testColumn は偽のサーバーが送る 1 チャンク。
type testColumn struct {
	cx, cz int32
	minY   int // ディメンションの一番下（オーバーワールドは -64）
	height int
	// block はワールドの座標 (x, y, z) の状態 ID を返す
	block func(x, y, z int) int32
	// sky と blk は明るさを返す（ワールドの座標。nil なら送らない）
	sky, blk func(x, y, z int) int
	// lightSec は明るさを送るセクション（明るさのセクションの番号）を選ぶ。nil なら全部
	lightSec func(i int) bool
}

func (c testColumn) encode(t *testing.T, e *protocol.Encoder) {
	t.Helper()
	e.WriteInt(c.cx)
	e.WriteInt(c.cz)
	must(t, nbt.WriteNetwork(e, map[string]any{"MOTION_BLOCKING": make([]int64, 37)}))
	sec := protocol.NewEncoder()
	for s := range c.height / 16 {
		vals := make([]int32, chunk.SectionBlocks)
		count := int16(0)
		for y := range 16 {
			for z := range 16 {
				for x := range 16 {
					v := c.block(int(c.cx)*16+x, c.minY+s*16+y, int(c.cz)*16+z)
					vals[chunk.BlockIndex(x, y, z)] = v
					if !isAirState(v) {
						count++
					}
				}
			}
		}
		sec.WriteShort(count)
		writeContainer(sec, vals, 4, 8, testBlockBits)
		writeContainer(sec, make([]int32, chunk.SectionBiomes), 1, 3, 7)
	}
	e.WritePrefixedBytes(sec.Bytes())
	e.WriteVarInt(0) // ブロックエンティティなし
	encodeLight(e, c.minY, c.height, c.cx, c.cz, c.sky, c.blk, c.lightSec)
}

// encodeLight は明るさの部分を書く。f が nil の種類は何も送らない（マスクがすべて 0）。
func encodeLight(e *protocol.Encoder, minY, height int, cx, cz int32, sky, blk func(x, y, z int) int, sel func(i int) bool) {
	n := height/16 + 2
	layer := func(f func(x, y, z int) int) (protocol.BitSet, [][]byte) {
		var m protocol.BitSet
		var arrs [][]byte
		if f == nil {
			return m, nil
		}
		for i := range n {
			if sel != nil && !sel(i) {
				continue
			}
			m.Set(i)
			baseY := minY + (i-1)*16
			arrs = append(arrs, lightOf(func(x, y, z int) int { return f(int(cx)*16+x, baseY+y, int(cz)*16+z) }))
		}
		return m, arrs
	}
	sm, sa := layer(sky)
	bm, ba := layer(blk)
	e.WriteBitSet(sm)
	e.WriteBitSet(bm)
	e.WriteBitSet(nil)
	e.WriteBitSet(nil)
	for _, arrs := range [][][]byte{sa, ba} {
		e.WriteVarInt(int32(len(arrs)))
		for _, a := range arrs {
			e.WritePrefixedBytes(a)
		}
	}
}

// sendChunks は Chunk Batch Start、チャンク、Chunk Batch Finished を送り、Chunk Batch Received を待つ
// （受け取ったら、チャンクは処理し終わっている）。
func (s *fakeServer) sendChunks(cols ...testColumn) float32 {
	t := s.t
	t.Helper()
	s.send(data.PlayClientboundChunkBatchStart, nil)
	for _, c := range cols {
		s.send(data.PlayClientboundLevelChunkWithLight, func(e *protocol.Encoder) { c.encode(t, e) })
	}
	s.send(data.PlayClientboundChunkBatchFinished, func(e *protocol.Encoder) { e.WriteVarInt(int32(len(cols))) })
	d := s.expect(data.PlayServerboundChunkBatchReceived, tickPackets)
	v, err := d.ReadFloat()
	must(t, err)
	return v
}

// sync は Ping を送って Pong を待つ（それより前に送ったパケットは処理し終わっている）。
func (s *fakeServer) sync() {
	s.t.Helper()
	s.send(data.PlayClientboundPing, func(e *protocol.Encoder) { e.WriteInt(1) })
	s.expect(data.PlayServerboundPong, tickPackets)
}

// playClient は偽のサーバーに繋いで Play に入った Client を返す。
func playClient(t *testing.T) (*fakeServer, *Client) {
	t.Helper()
	s := newFakeServer(t)
	ch := connectAsync(context.Background(), Config{Addr: s.addr(), Username: "worldbot"})
	s.login("worldbot", 256)
	s.configure(true)
	s.enterPlay()
	r := wait(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	return s, r.c
}

func checkBlock(t *testing.T, c *Client, p BlockPos, want data.StateID) {
	t.Helper()
	got, ok := c.Block(p)
	if !ok || got.ID != want {
		w, _ := data.StateByID(want)
		t.Errorf("Block%v = %v（ok=%v）、期待 %v", p, got.ID, ok, w)
	}
}

// ---- テスト ----

// 偽のサーバーからチャンクを送り、Block / Light / WorldView で読めること。
// ブロックの更新、明るさの更新、チャンクを捨てる、ディメンションの切り替えも確かめる。
func TestWorldFromFakeServer(t *testing.T) {
	s, c := playClient(t)

	if d := c.Dimension(); d.Name != "minecraft:overworld" || d.MinY != -64 || d.Height != 384 || !d.HasSkylight {
		t.Fatalf("Dimension %+v", d)
	}

	stone := stateOf(t, "stone")
	grass := stateOf(t, "grass_block[snowy=false]")
	stairs := stateOf(t, "oak_stairs[facing=east,half=top,shape=straight,waterlogged=false]")
	water := stateOf(t, "water[level=0]")
	air := stateOf(t, "air")

	// チャンク (0, 0): y=-64〜-62 が石、-61 が草、(3, 0, 5) に階段、(4, 0, 5) に水。
	// チャンク (-1, 0): y=10 の段だけ、x+z で 300 種類のブロック（パレットなしになる）。
	col0 := testColumn{cx: 0, cz: 0, minY: -64, height: 384,
		block: func(x, y, z int) int32 {
			switch {
			case y <= -62:
				return int32(stone)
			case y == -61:
				return int32(grass)
			case x == 3 && y == 0 && z == 5:
				return int32(stairs)
			case x == 4 && y == 0 && z == 5:
				return int32(water)
			}
			return int32(air)
		},
		sky: func(x, y, z int) int {
			if y < -60 {
				return 0
			}
			return 15
		},
		blk: func(x, y, z int) int { return (x + z) % 16 },
	}
	many := func(x, z int) int32 { return int32(1 + (x&15)*16 + (z & 15)) } // 1〜256 + 下で 44 種類足す
	colM := testColumn{cx: -1, cz: 0, minY: -64, height: 384,
		block: func(x, y, z int) int32 {
			switch {
			case y == 10:
				return many(x, z)
			case y == 11 && x&15 < 11 && z&15 < 4:
				return int32(1000 + (x&15)*4 + (z & 15)) // 44 種類
			}
			return int32(air)
		},
	}
	if v := s.sendChunks(col0, colM); v <= 0 {
		t.Errorf("Chunk Batch Received の値 %v", v)
	}
	if n := c.LoadedChunks(); n != 2 {
		t.Errorf("LoadedChunks = %d", n)
	}

	checkBlock(t, c, BlockPos{0, -64, 0}, stone)
	checkBlock(t, c, BlockPos{15, -61, 15}, grass)
	checkBlock(t, c, BlockPos{3, 0, 5}, stairs)
	checkBlock(t, c, BlockPos{4, 0, 5}, water)
	checkBlock(t, c, BlockPos{5, 319, 5}, air)
	checkBlock(t, c, BlockPos{-16, 10, 0}, data.StateID(many(-16, 0)))
	checkBlock(t, c, BlockPos{-1, 10, 15}, data.StateID(many(-1, 15)))
	checkBlock(t, c, BlockPos{-6, 11, 3}, data.StateID(1000+10*4+3))
	if st, _ := c.Block(BlockPos{3, 0, 5}); st.Name() != "oak_stairs" {
		t.Errorf("階段の名前 %q", st.Name())
	} else if f, _ := st.Get("facing"); f != "east" {
		t.Errorf("階段の向き %q", f)
	} else if h, _ := st.Get("half"); h != "top" {
		t.Errorf("階段の上下 %q", h)
	}
	// 受け取っていない場所、高さの外
	for _, p := range []BlockPos{{16, 0, 0}, {0, 0, -1}, {0, -65, 0}, {0, 320, 0}} {
		if _, ok := c.Block(p); ok {
			t.Errorf("Block%v が ok", p)
		}
	}

	// 明るさ
	if b, sk, ok := c.Light(BlockPos{3, 0, 5}); !ok || b != 8 || sk != 15 {
		t.Errorf("Light(3,0,5) = %d %d %v、期待 8 15", b, sk, ok)
	}
	if b, sk, ok := c.Light(BlockPos{1, -64, 1}); !ok || b != 2 || sk != 0 {
		t.Errorf("Light(1,-64,1) = %d %d %v、期待 2 0", b, sk, ok)
	}
	// 明るさを 1 つも送っていないチャンク: 公式のクライアントと同じく block 0、sky 15
	if b, sk, ok := c.Light(BlockPos{-1, 0, 0}); !ok || b != 0 || sk != 15 {
		t.Errorf("明るさの無いチャンクの Light = %d %d %v、期待 0 15", b, sk, ok)
	}
	// 一部のセクション（明るさのセクション 10 と 12、y=80〜95 と 112〜127）だけ空の明るさを送ったチャンク (1, 0)
	skyF := func(x, y, z int) int { return (x + z + y/16) % 16 }
	colL := testColumn{cx: 1, cz: 0, minY: -64, height: 384,
		block:    func(x, y, z int) int32 { return int32(air) },
		sky:      skyF,
		lightSec: func(i int) bool { return i == 10 || i == 12 },
	}
	s.sendChunks(colL)
	for _, q := range []struct {
		y, want int
	}{
		{85, skyF(17, 85, 5)},   // データのあるセクション
		{0, skyF(17, 80, 5)},    // 上へたどってセクション 10 の一番下の層
		{-70, skyF(17, 80, 5)},  // ワールドの下の明るさのセクション
		{100, skyF(17, 112, 5)}, // セクション 12 の一番下の層
		{200, 15},               // 上に何も無い
		{335, 15},               // 一番上の明るさのセクション
	} {
		if b, sk, ok := c.Light(BlockPos{17, q.y, 5}); !ok || b != 0 || sk != q.want {
			t.Errorf("Light(17,%d,5) = %d %d %v、期待 0 %d", q.y, b, sk, ok, q.want)
		}
	}
	for _, y := range []int{-81, 336} {
		if _, _, ok := c.Light(BlockPos{17, y, 5}); ok {
			t.Errorf("明るさの範囲の外 y=%d が ok", y)
		}
	}
	// 明るさの更新（Update Light）: チャンク (0,0) の y=0〜15 の空を 4 にする
	s.send(data.PlayClientboundLightUpdate, func(e *protocol.Encoder) {
		e.WriteVarInt(0)
		e.WriteVarInt(0)
		var m protocol.BitSet
		m.Set(5) // 明るさのセクション 5 = y 0〜15
		e.WriteBitSet(m)
		e.WriteBitSet(nil)
		e.WriteBitSet(nil)
		e.WriteBitSet(nil)
		e.WriteVarInt(1)
		e.WritePrefixedBytes(lightOf(func(x, y, z int) int { return 4 }))
		e.WriteVarInt(0)
	})
	s.sync()
	if b, sk, ok := c.Light(BlockPos{3, 0, 5}); !ok || b != 8 || sk != 4 {
		t.Errorf("Update Light のあと Light(3,0,5) = %d %d %v、期待 8 4", b, sk, ok)
	}

	// Block Update と Update Section Blocks
	s.send(data.PlayClientboundBlockUpdate, func(e *protocol.Encoder) {
		must(t, e.WritePosition(protocol.Position{X: 3, Y: 0, Z: 5}))
		e.WriteVarInt(int32(air))
	})
	// 読み込んでいないチャンクへの更新は無視される
	s.send(data.PlayClientboundBlockUpdate, func(e *protocol.Encoder) {
		must(t, e.WritePosition(protocol.Position{X: 100, Y: 0, Z: 100}))
		e.WriteVarInt(int32(stone))
	})
	s.send(data.PlayClientboundSectionBlocksUpdate, func(e *protocol.Encoder) {
		e.WriteLong(chunk.SectionPos{X: 0, Y: -4, Z: 0}.Pack()) // y=-64〜-49
		e.WriteVarInt(2)
		e.WriteVarLong(chunk.PackBlockChange(chunk.BlockChange{X: 1, Y: 3, Z: 2, State: int32(stairs)})) // (1,-61,2)
		e.WriteVarLong(chunk.PackBlockChange(chunk.BlockChange{X: 0, Y: 0, Z: 0, State: int32(air)}))    // (0,-64,0)
	})
	s.sync()
	checkBlock(t, c, BlockPos{3, 0, 5}, air)
	checkBlock(t, c, BlockPos{1, -61, 2}, stairs)
	checkBlock(t, c, BlockPos{0, -64, 0}, air)
	checkBlock(t, c, BlockPos{1, -64, 0}, stone)

	// ブロックが変わったセクションと、その周り 26 個のセクションの明るさは古いので ok=false
	lightOK := func(p BlockPos) bool { _, _, ok := c.Light(p); return ok }
	// 下のセクションは全部（空の明るさの影）。{3, -40, 5} は 3 つ下、{19, -70, 5} は隣のチャンクの一番下
	for _, p := range []BlockPos{{3, 0, 5}, {3, 17, 5}, {3, -1, 5}, {-1, 0, 0}, {1, -64, 0}, {1, -50, 0}, {1, -80, 0}, {3, -40, 5}, {19, -70, 5}} {
		if lightOK(p) {
			t.Errorf("ブロックが変わったあと Light%v が ok", p)
		}
	}
	if !lightOK(BlockPos{3, 40, 5}) {
		t.Error("離れたセクションの Light が ok でない")
	}
	// そのセクションの明るさが Update Light で届けば印が消える（ほかのセクションは古いまま）
	s.send(data.PlayClientboundLightUpdate, func(e *protocol.Encoder) {
		e.WriteVarInt(0)
		e.WriteVarInt(0)
		var m protocol.BitSet
		m.Set(5)
		e.WriteBitSet(nil)
		e.WriteBitSet(m) // block light だけ
		e.WriteBitSet(nil)
		e.WriteBitSet(nil)
		e.WriteVarInt(0)
		e.WriteVarInt(1)
		e.WritePrefixedBytes(lightOf(func(x, y, z int) int { return 9 }))
	})
	s.sync()
	if b, sk, ok := c.Light(BlockPos{3, 0, 5}); !ok || b != 9 || sk != 4 {
		t.Errorf("明るさが届いたあと Light(3,0,5) = %d %d %v、期待 9 4", b, sk, ok)
	}
	if lightOK(BlockPos{3, 17, 5}) {
		t.Error("明るさが届いていないセクションが ok")
	}
	// チャンクのデータが届けば、そのチャンクの印は全部消える
	s.sendChunks(col0)
	for _, p := range []BlockPos{{3, 17, 5}, {1, -64, 0}} {
		if !lightOK(p) {
			t.Errorf("チャンクを受け取り直したのに Light%v が ok でない", p)
		}
	}
	checkBlock(t, c, BlockPos{3, 0, 5}, stairs) // 受け取り直した中身に戻る

	// WorldView: 2 つのチャンクと、受け取っていないチャンク (1, 0) と高さの外にまたがる範囲
	v, err := c.WorldView(BlockPos{20, -70, 3}, BlockPos{-20, 12, 6})
	if err != nil {
		t.Fatal(err)
	}
	if v.Min() != (BlockPos{-20, -70, 3}) || v.Max() != (BlockPos{20, 12, 6}) {
		t.Errorf("WorldView の範囲 %v %v", v.Min(), v.Max())
	}
	for x := -20; x <= 20; x++ {
		for y := -70; y <= 12; y++ {
			for z := 3; z <= 6; z++ {
				p := BlockPos{x, y, z}
				want, wantOK := c.Block(p)
				got, ok := v.Block(p)
				if ok != wantOK || got.ID != want.ID {
					t.Fatalf("WorldView.Block%v = %v %v、Block は %v %v", p, got.ID, ok, want.ID, wantOK)
				}
			}
		}
	}
	if _, ok := v.Block(BlockPos{0, 13, 3}); ok {
		t.Error("WorldView の範囲の外が ok")
	}
	if _, err := c.WorldView(BlockPos{0, 0, 0}, BlockPos{1 << 12, 1 << 12, 1 << 12}); !errors.Is(err, ErrWorldViewTooBig) {
		t.Errorf("大きすぎる WorldView: %v", err)
	}
	// 写しなので、あとの変化は入らない
	s.send(data.PlayClientboundBlockUpdate, func(e *protocol.Encoder) {
		must(t, e.WritePosition(protocol.Position{X: 2, Y: 0, Z: 4}))
		e.WriteVarInt(int32(stone))
	})
	s.sync()
	checkBlock(t, c, BlockPos{2, 0, 4}, stone)
	if b, _ := v.Block(BlockPos{2, 0, 4}); b.ID != air {
		t.Errorf("WorldView が書き換わった: %v", b)
	}

	// Unload Chunk（Z, X の順）
	s.send(data.PlayClientboundForgetLevelChunk, func(e *protocol.Encoder) {
		e.WriteInt(0)  // Z
		e.WriteInt(-1) // X
	})
	s.sync()
	if _, ok := c.Block(BlockPos{-16, 10, 0}); ok {
		t.Error("捨てたチャンクの Block が ok")
	}
	checkBlock(t, c, BlockPos{1, -64, 0}, stone)

	// 同じディメンションでの Respawn ではチャンクを捨てない
	rp := packets.Respawn{DimensionType: 0, DimensionName: "minecraft:overworld", PreviousGameMode: -1, SeaLevel: 63}
	s.send(data.PlayClientboundRespawn, func(e *protocol.Encoder) { must(t, packets.WriteRespawn(e, rp)) })
	s.sync()
	checkBlock(t, c, BlockPos{1, -64, 0}, stone)

	// ネザーへ（registry に中身が無いので、公式の値 0〜255 を使う）。チャンクは捨てる
	rp.DimensionType, rp.DimensionName = 1, "minecraft:the_nether"
	s.send(data.PlayClientboundRespawn, func(e *protocol.Encoder) { must(t, packets.WriteRespawn(e, rp)) })
	s.sync()
	if d := c.Dimension(); d.Name != "minecraft:the_nether" || d.MinY != 0 || d.Height != 256 || d.HasSkylight {
		t.Errorf("ネザーの Dimension %+v", d)
	}
	if c.LoadedChunks() != 0 {
		t.Errorf("ディメンションを変えたのにチャンクが %d 個残る", c.LoadedChunks())
	}
	nether := testColumn{cx: 0, cz: 0, minY: 0, height: 256,
		block: func(x, y, z int) int32 {
			if y == 0 {
				return int32(stateOf(t, "bedrock"))
			}
			return int32(air)
		},
		blk: func(x, y, z int) int { return 11 },
	}
	s.sendChunks(nether)
	checkBlock(t, c, BlockPos{0, 0, 0}, stateOf(t, "bedrock"))
	if b, sk, ok := c.Light(BlockPos{0, 5, 0}); !ok || b != 11 || sk != 0 {
		t.Errorf("ネザーの Light = %d %d %v、期待 11 0", b, sk, ok)
	}

	select {
	case <-c.Done():
		t.Fatalf("切れた: %v", c.Err())
	default:
	}
}

// 壊れたチャンクを受け取ったら、理由つきで切断する。
func TestWorldBadChunk(t *testing.T) {
	s, c := playClient(t)
	s.send(data.PlayClientboundLevelChunkWithLight, func(e *protocol.Encoder) {
		e.WriteInt(0)
		e.WriteInt(0)
		must(t, nbt.WriteNetwork(e, map[string]any{}))
		e.WritePrefixedBytes([]byte{0, 0, 0}) // セクションが足りない
	})
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("切れない")
	}
	if err := c.Err(); err == nil || !strings.Contains(err.Error(), "Chunk Data") {
		t.Errorf("Err = %v", err)
	}
}

// Login (play) の前（ブロックを何も受け取っていない Client）でも読み出しが安全に失敗すること。
func TestWorldBeforeLogin(t *testing.T) {
	c := &Client{reg: nil}
	c.initFeatures()
	if _, ok := c.Block(BlockPos{}); ok {
		t.Error("Block が ok")
	}
	if _, _, ok := c.Light(BlockPos{}); ok {
		t.Error("Light が ok")
	}
	v, err := c.WorldView(BlockPos{}, BlockPos{1, 1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Block(BlockPos{}); ok {
		t.Error("WorldView.Block が ok")
	}
}

// チャンクを受け取る速さの見積もりが、公式のクライアントと同じ計算になること。
func TestChunkBatchRate(t *testing.T) {
	b := newChunkBatchRate()
	if v := b.desiredChunksPerTick(); v != 3.5 {
		t.Errorf("はじめの値 %v、期待 3.5", v)
	}
	t0 := time.Unix(0, 0)
	// 10 チャンクに 1 ms → 1 チャンク 0.1 ms。ただし今の平均 2 ms の 1/3 に収め、(2 + 2/3) / 2 = 1.333 ms
	b.start(t0)
	b.finish(t0.Add(time.Millisecond), 10)
	if want := float32(7_000_000 / ((2_000_000 + 2_000_000/3.0) / 2)); b.desiredChunksPerTick() != want {
		t.Errorf("1 回目のあと %v、期待 %v", b.desiredChunksPerTick(), want)
	}
	// Start の無い Finish と 0 個のバッチは数えない
	before := b.desiredChunksPerTick()
	b.finish(t0.Add(time.Second), 5)
	b.start(t0)
	b.finish(t0.Add(time.Second), 0)
	if b.desiredChunksPerTick() != before {
		t.Errorf("数えないはずのバッチで変わった")
	}
}

func TestGeom(t *testing.T) {
	if p := BlockPosOf(r3.Vec{X: -0.5, Y: 64.99, Z: 3}); p != (BlockPos{-1, 64, 3}) {
		t.Errorf("BlockPosOf = %v", p)
	}
	if p := BlockPosOf(r3.Vec{X: -16, Y: -0.0001, Z: 15.9999}); p != (BlockPos{-16, -1, 15}) {
		t.Errorf("BlockPosOf = %v", p)
	}
	p := BlockPos{1, 2, 3}
	want := map[Face]BlockPos{Down: {1, 1, 3}, Up: {1, 3, 3}, North: {1, 2, 2}, South: {1, 2, 4}, West: {0, 2, 3}, East: {2, 2, 3}}
	for _, f := range Faces {
		if got := p.Neighbor(f); got != want[f] {
			t.Errorf("Neighbor(%v) = %v", f, got)
		}
		if got := p.Neighbor(f).Neighbor(f.Opposite()); got != p {
			t.Errorf("%v と %v で戻らない", f, f.Opposite())
		}
		n := f.Normal()
		if BlockPosOf(r3.Add(p.Center(), n)) != p.Neighbor(f) {
			t.Errorf("Normal(%v) = %v", f, n)
		}
	}
	// 名前は階段の facing の値と同じ
	b, _ := data.BlockByName("oak_stairs")
	for _, f := range []Face{North, South, West, East} {
		if _, err := b.State(map[string]string{"facing": f.String()}); err != nil {
			t.Errorf("facing=%s: %v", f, err)
		}
	}
	if p.Offset(-1, 0, 2) != (BlockPos{0, 2, 5}) || p.Center() != (r3.Vec{X: 1.5, Y: 2.5, Z: 3.5}) {
		t.Error("Offset / Center")
	}
}

// チャンクの端のブロックが変わっても、読み込んでいない隣のチャンクには明るさの「古い」の印を付けないこと
// （付けると、読み込まれないまま離れたときに印が残り、長く動かすと増え続ける）。
func TestStaleLightOnlyLoadedColumns(t *testing.T) {
	s, c := playClientCfg(t, Config{})
	s.sendChunks(flatColumn(t))
	// チャンク (0, 0) の北西の角。隣の (-1, *) と (*, -1) は読み込んでいない
	s.sendBlockUpdate(BlockPos{X: 0, Y: -60, Z: 0}, stateOf(t, "stone"))
	for end := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if st, ok := c.Block(BlockPos{X: 0, Y: -60, Z: 0}); ok && st.Block.Name == "stone" {
			break
		}
		if time.Now().After(end) {
			t.Fatal("ブロックの変化が届かない")
		}
	}
	ws := worldKey.of(c)
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	if len(ws.staleLight) == 0 {
		t.Fatal("印が 1 つも無い")
	}
	for sp := range ws.staleLight {
		if sp.X != 0 || sp.Z != 0 {
			t.Errorf("読み込んでいないチャンクのセクション %+v に印がある", sp)
		}
	}
}
