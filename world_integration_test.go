//go:build integration

// 単位 4（地形）を本物のサーバー（Docker の Paper 1.21.4、平らなワールド）で確かめるテスト。
// 普段の go test では動かない。テスト用サーバーを立てて繋ぐ:
//
//	MCDRIVER_ADDR=127.0.0.1:25765 MCDRIVER_RCON=127.0.0.1:25775 MCDRIVER_RCON_PASS=... \
//	  go test -tags integration -run TestWorldIntegration -v -count=1 -timeout 110s .
//
// RCON と道具（check3DialRCON, check3Env）は check3_integration_test.go のものを使う。
package mcdriver

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/chunk"
	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
	"gonum.org/v1/gonum/spatial/r3"
)

// ---- 本物のサーバーが送る paletted container の記録 ----

// paletteStats は bot ごとの、届いたセクションの bits per entry の数。
var paletteStats = struct {
	sync.Mutex
	m map[string]map[string]int // bot の名前 → "blocks bpe=4 palette" などの種類 → 数
}{m: map[string]map[string]int{}}

// packetCounts は bot ごとの、地形に関わるパケットの数。
var packetCounts = struct {
	sync.Mutex
	m map[string]map[int32]int
}{m: map[string]map[int32]int{}}

func init() {
	for _, id := range []int32{
		data.PlayClientboundLevelChunkWithLight, data.PlayClientboundForgetLevelChunk, data.PlayClientboundBlockUpdate,
		data.PlayClientboundSectionBlocksUpdate, data.PlayClientboundLightUpdate,
		data.PlayClientboundChunkBatchStart, data.PlayClientboundChunkBatchFinished,
	} {
		onPlay(id, func(cl *Client, d *protocol.Decoder) error {
			packetCounts.Lock()
			defer packetCounts.Unlock()
			m := packetCounts.m[cl.cfg.Username]
			if m == nil {
				m = map[int32]int{}
				packetCounts.m[cl.cfg.Username] = m
			}
			m[id]++
			return nil
		})
	}
}

func logPacketCounts(t *testing.T, name string) {
	packetCounts.Lock()
	defer packetCounts.Unlock()
	var b strings.Builder
	for id, n := range packetCounts.m[name] {
		pk, _ := data.PacketByID(data.StatePlay, "clientbound", id)
		fmt.Fprintf(&b, "  %s(0x%02X) %d\n", pk.Name, id, n)
	}
	t.Logf("%s が受け取った地形のパケット:\n%s", name, b.String())
}

func init() {
	// テストだけの処理: チャンクの中身を自分で読み、bits per entry とパレットの種類を数える
	onPlay(data.PlayClientboundLevelChunkWithLight, func(cl *Client, d *protocol.Decoder) error {
		kinds, err := scanChunkPalettes(d)
		paletteStats.Lock()
		defer paletteStats.Unlock()
		m := paletteStats.m[cl.cfg.Username]
		if m == nil {
			m = map[string]int{}
			paletteStats.m[cl.cfg.Username] = m
		}
		if err != nil {
			m["読めない: "+err.Error()]++
		}
		for _, k := range kinds {
			m[k]++
		}
		return nil
	})
}

// scanChunkPalettes はチャンクのデータのセクションごとに、ブロックとバイオームの container の種類を返す。
// 種類は "blocks bpe=N 値1つ / パレットK個 / パレットなし long L個" の形。
func scanChunkPalettes(d *protocol.Decoder) ([]string, error) {
	if _, err := d.ReadInt(); err != nil {
		return nil, err
	}
	if _, err := d.ReadInt(); err != nil {
		return nil, err
	}
	if _, err := nbt.ReadNetwork(d); err != nil {
		return nil, err
	}
	raw, err := d.ReadPrefixedBytes(chunk.MaxChunkDataSize)
	if err != nil {
		return nil, err
	}
	sd := protocol.NewDecoder(raw)
	var out []string
	for range 24 {
		if _, err := sd.ReadShort(); err != nil {
			return out, err
		}
		for _, kind := range []struct {
			name        string
			maxIndirect int
		}{{"blocks", 8}, {"biomes", 3}} {
			bpe, err := sd.ReadUByte()
			if err != nil {
				return out, err
			}
			desc := ""
			switch {
			case bpe == 0:
				if _, err := sd.ReadVarInt(); err != nil {
					return out, err
				}
				desc = "値1つ"
			case int(bpe) <= kind.maxIndirect:
				n, err := sd.ReadVarInt()
				if err != nil {
					return out, err
				}
				for range n {
					if _, err := sd.ReadVarInt(); err != nil {
						return out, err
					}
				}
				desc = "パレットあり"
			default:
				desc = "パレットなし"
			}
			longs, err := sd.ReadVarInt()
			if err != nil {
				return out, err
			}
			if err := sd.Skip(int(longs) * 8); err != nil {
				return out, err
			}
			out = append(out, fmt.Sprintf("%s bpe=%d %s long %d 個", kind.name, bpe, desc, longs))
		}
	}
	return out, nil
}

func logPaletteStats(t *testing.T, name string) map[string]int {
	paletteStats.Lock()
	defer paletteStats.Unlock()
	m := paletteStats.m[name]
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "  %-50s %d\n", k, m[k])
	}
	t.Logf("%s が受け取ったセクションの種類:\n%s", name, b.String())
	out := map[string]int{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ---- 道具 ----

// fullState は "oak_stairs[facing=east,half=top]" のような書き方から、書いていないプロパティを既定の値で埋めた状態を返す。
func fullState(t *testing.T, s string) data.BlockState {
	t.Helper()
	name, props, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(s, "minecraft:"), "]"), "[")
	b, ok := data.BlockByName(name)
	if !ok {
		t.Fatalf("ブロック %q が無い", name)
	}
	st := b.Default()
	if props != "" {
		for _, kv := range strings.Split(props, ",") {
			k, v, _ := strings.Cut(kv, "=")
			if st, ok = st.With(k, v); !ok {
				t.Fatalf("%s に %s=%s が無い", name, k, v)
			}
		}
	}
	return st
}

// waitBlock は p のブロックが want になるまで待つ。ならなければ最後の値を返す。
func waitBlock(c *Client, p BlockPos, want data.StateID, d time.Duration) (data.BlockState, bool) {
	end := time.Now().Add(d)
	for {
		got, ok := c.Block(p)
		if (ok && got.ID == want) || time.Now().After(end) {
			return got, ok
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func rconMust(t *testing.T, rc *check3RCON, cmd string) string {
	t.Helper()
	out, err := rc.command(cmd)
	if err != nil {
		t.Fatalf("RCON %q: %v", cmd, err)
	}
	return out
}

func connectBot(t *testing.T, addr, name string) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Connect(ctx, Config{Addr: addr, Username: name, ViewDistance: 4})
	if err != nil {
		t.Fatalf("Connect %s: %v", name, err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// botBlockPos は bot の足元のブロックの位置を、位置を受け取るまで待って返す。
func botBlockPos(t *testing.T, c *Client) BlockPos {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) {
		c.mu.Lock()
		has, x, y, z := c.hasPos, c.x, c.y, c.z
		c.mu.Unlock()
		if has {
			return BlockPosOf(r3.Vec{X: x, Y: y, Z: z})
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("位置が届かない")
	return BlockPos{}
}

// waitLoaded は p の周り（半径 r ブロック）のチャンクが全部届くまで待つ。
func waitLoaded(t *testing.T, c *Client, p BlockPos, r int) {
	t.Helper()
	end := time.Now().Add(20 * time.Second)
	for {
		missing := 0
		for dx := -r; dx <= r; dx += 16 {
			for dz := -r; dz <= r; dz += 16 {
				if _, ok := c.Block(p.Offset(dx, 0, dz)); !ok {
					missing++
				}
			}
		}
		if missing == 0 {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("20 秒たってもチャンクが届かない（足りない %d、届いた %d 個）", missing, c.LoadedChunks())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ---- テスト ----

func TestWorldIntegration(t *testing.T) {
	addr := check3Env(t, "MCDRIVER_ADDR")
	rconAddr := check3Env(t, "MCDRIVER_RCON")
	rconPass := check3Env(t, "MCDRIVER_RCON_PASS")
	rc, err := check3DialRCON(rconAddr, rconPass)
	if err != nil {
		t.Fatalf("RCON: %v", err)
	}
	defer rc.close()

	start := time.Now()
	c := connectBot(t, addr, "worldbot")
	feet := botBlockPos(t, c)
	waitLoaded(t, c, feet, 32)
	t.Logf("bot の足元 %v、チャンク %d 個が %v で届いた。Dimension %+v、バイオームの registry %d 個",
		feet, c.LoadedChunks(), time.Since(start), c.Dimension(), len(c.reg.Entries("minecraft:worldgen/biome")))

	// 1. 地表: 平らなワールドは y=-64 が岩盤、-63〜-62 が土、-61 が草、その上は空気
	t.Run("地表", func(t *testing.T) {
		want := map[int]string{-64: "bedrock", -63: "dirt", -62: "dirt", -61: "grass_block[snowy=false]", -60: "air", -59: "air", 0: "air"}
		bad := 0
		for dx := -24; dx <= 24; dx += 3 {
			for dz := -24; dz <= 24; dz += 3 {
				p := feet.Offset(dx, 0, dz)
				for y, w := range want {
					got, ok := c.Block(BlockPos{p.X, y, p.Z})
					if !ok || got.String() != w {
						if bad < 10 {
							t.Errorf("(%d, %d, %d) = %v（ok=%v）、期待 %s", p.X, y, p.Z, got, ok, w)
						}
						bad++
					}
				}
				// 一番上の空気でないブロック
				top := -65
				for y := 319; y >= -64; y-- {
					if st, ok := c.Block(BlockPos{p.X, y, p.Z}); ok && st.Name() != "air" {
						top = y
						break
					}
				}
				if top != -61 {
					t.Errorf("(%d, %d) の地表の高さ %d、期待 -61", p.X, p.Z, top)
				}
			}
		}
		if feet.Y != -60 {
			t.Errorf("bot の足元の y = %d、期待 -60", feet.Y)
		}
	})

	// 2. RCON の setblock / fill で置いたものと Block() が一致する
	base := feet.Offset(4, 0, 4) // y=-60（草の上の空気）
	t.Run("setblockとfill", func(t *testing.T) {
		type place struct {
			p     BlockPos
			state string
		}
		var places []place
		// 樫の階段: 4 方向 × 上下（離して置き、形がつながらないようにする）
		i := 0
		for _, facing := range []string{"north", "south", "west", "east"} {
			for _, half := range []string{"bottom", "top"} {
				places = append(places, place{base.Offset(i*2, 0, 0), fmt.Sprintf("oak_stairs[facing=%s,half=%s]", facing, half)})
				i++
			}
		}
		places = append(places,
			place{base.Offset(0, 0, 4), "stone_slab[type=top]"},
			place{base.Offset(2, 0, 4), "oak_log[axis=x]"},
			place{base.Offset(4, 0, 4), "glass"},
			place{base.Offset(6, 0, 4), "oak_door[facing=west,half=lower]"},
			place{base.Offset(6, 1, 4), "oak_door[facing=west,half=upper]"},
			place{base.Offset(0, -1, 6), "air"}, // 草を消す
		)
		// 水: 石で囲んだ中に置く（流れ出ないように）
		wp := base.Offset(10, 0, 6)
		rconMust(t, rc, fmt.Sprintf("fill %d %d %d %d %d %d minecraft:stone", wp.X-1, wp.Y, wp.Z-1, wp.X+1, wp.Y, wp.Z+1))
		places = append(places, place{wp, "water[level=0]"})

		for _, pl := range places {
			out := rconMust(t, rc, fmt.Sprintf("setblock %d %d %d minecraft:%s", pl.p.X, pl.p.Y, pl.p.Z, pl.state))
			if !strings.Contains(out, "Changed") {
				t.Errorf("setblock %s: %q", pl.state, out)
			}
		}
		for _, pl := range places {
			want := fullState(t, pl.state)
			got, ok := waitBlock(c, pl.p, want.ID, 3*time.Second)
			if !ok || got.ID != want.ID {
				t.Errorf("%v: Block() = %v（ok=%v）、置いたのは %v", pl.p, got, ok, want)
			}
			// サーバーも同じ状態と思っているか
			if out := rconMust(t, rc, fmt.Sprintf("execute if block %d %d %d minecraft:%s", pl.p.X, pl.p.Y, pl.p.Z, want.String())); !strings.Contains(out, "passed") {
				t.Errorf("%v: サーバーでは %s でない: %q", pl.p, want, out)
			}
		}

		// fill: チャンクの境目とセクションの境目をまたぐ範囲（Update Section Blocks がいくつも来る）
		a, b := base.Offset(-20, 10, -20), base.Offset(-2, 20, -14)
		out := rconMust(t, rc, fmt.Sprintf("fill %d %d %d %d %d %d minecraft:oak_planks", a.X, a.Y, a.Z, b.X, b.Y, b.Z))
		t.Logf("fill: %q", out)
		planks := fullState(t, "oak_planks")
		end := time.Now().Add(3 * time.Second)
		for {
			v, err := c.WorldView(a, b)
			if err != nil {
				t.Fatal(err)
			}
			wrong := 0
			var first BlockPos
			for x := a.X; x <= b.X; x++ {
				for y := a.Y; y <= b.Y; y++ {
					for z := a.Z; z <= b.Z; z++ {
						if id, ok := v.StateID(BlockPos{x, y, z}); !ok || id != planks.ID {
							if wrong == 0 {
								first = BlockPos{x, y, z}
							}
							wrong++
						}
					}
				}
			}
			if wrong == 0 {
				break
			}
			if time.Now().After(end) {
				got, ok := c.Block(first)
				t.Errorf("fill のあと %d ブロックが板でない（例 %v = %v ok=%v）", wrong, first, got, ok)
				break
			}
			time.Sleep(20 * time.Millisecond)
		}

		// 3. 変えたら反映される: 階段を石に、石を空気に
		p := places[0].p
		rconMust(t, rc, fmt.Sprintf("setblock %d %d %d minecraft:stone", p.X, p.Y, p.Z))
		if got, _ := waitBlock(c, p, fullState(t, "stone").ID, 3*time.Second); got.Name() != "stone" {
			t.Errorf("石にしたのに %v", got)
		}
		rconMust(t, rc, fmt.Sprintf("setblock %d %d %d minecraft:air", p.X, p.Y, p.Z))
		if got, _ := waitBlock(c, p, fullState(t, "air").ID, 3*time.Second); got.Name() != "air" {
			t.Errorf("空気にしたのに %v", got)
		}
		// fill で空気に戻す
		rconMust(t, rc, fmt.Sprintf("fill %d %d %d %d %d %d minecraft:air", a.X, a.Y, a.Z, b.X, b.Y, b.Z))
		if got, _ := waitBlock(c, a, fullState(t, "air").ID, 3*time.Second); got.Name() != "air" {
			t.Errorf("fill で空気にしたのに %v", got)
		}
	})

	// 4. 明るさ: 空の下は空の明るさ 15、光るブロックの隣はブロックの明るさ 14
	t.Run("明るさ", func(t *testing.T) {
		open := feet.Offset(-6, 0, 6)
		if b, sky, ok := c.Light(open); !ok || sky != 15 || b != 0 {
			t.Errorf("空の下 %v: block %d sky %d ok %v、期待 0 15", open, b, sky, ok)
		}
		if _, sky, ok := c.Light(open.Offset(0, -2, 0)); !ok || sky != 0 {
			t.Errorf("土の中 sky %d ok %v、期待 0", sky, ok)
		}
		lamp := feet.Offset(-6, 3, -6)
		rconMust(t, rc, fmt.Sprintf("setblock %d %d %d minecraft:glowstone", lamp.X, lamp.Y, lamp.Z))
		next := lamp.Offset(1, 0, 0)
		end := time.Now().Add(5 * time.Second)
		var b, sky int
		var ok bool
		for time.Now().Before(end) {
			if b, sky, ok = c.Light(next); ok && b == 14 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !ok || b != 14 {
			t.Errorf("光る石の隣 %v: block %d sky %d ok %v、期待 block 14", next, b, sky, ok)
		}
		if b2, _, _ := c.Light(lamp.Offset(3, 0, 0)); b2 != 12 {
			t.Errorf("光る石から 3 つ先 block %d、期待 12", b2)
		}
		rconMust(t, rc, fmt.Sprintf("setblock %d %d %d minecraft:air", lamp.X, lamp.Y, lamp.Z))
		end = time.Now().Add(5 * time.Second)
		for time.Now().Before(end) {
			if b, _, ok = c.Light(next); ok && b == 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if b != 0 {
			t.Errorf("光る石を消したあと block %d、期待 0", b)
		}
	})

	// 5. パレットなし: 1 つのセクションに 256 より多い種類を置き、別の bot で受け取り直す
	t.Run("パレットなし", func(t *testing.T) {
		var states []data.BlockState
		for _, b := range data.Blocks() {
			switch b.Type {
			case "block":
				states = append(states, b.Default())
			case "rotated_pillar":
				for _, ax := range []string{"x", "y", "z"} {
					if st, ok := b.Default().With("axis", ax); ok {
						states = append(states, st)
					}
				}
			}
		}
		// セクション（y=0〜15）の、bot の隣のチャンク
		cx, cz := (feet.X>>4)+1, feet.Z>>4
		var placed []place2
		for i, st := range states {
			p := BlockPos{cx*16 + i%16, 0 + i/256, cz*16 + (i/16)%16}
			out := rconMust(t, rc, fmt.Sprintf("setblock %d %d %d minecraft:%s", p.X, p.Y, p.Z, st.String()))
			if strings.Contains(out, "Changed") {
				placed = append(placed, place2{p, st})
			} else if len(placed) < 3 {
				t.Logf("置けない %s: %q", st, out)
			}
		}
		t.Logf("%d 種類のうち %d 種類を置いた", len(states), len(placed))
		if len(placed) <= 256 {
			t.Fatalf("置けた種類が %d（256 より多くないとパレットなしにならない）", len(placed))
		}
		// 今の bot には Update Section Blocks で届く
		for _, pl := range placed {
			if got, ok := waitBlock(c, pl.p, pl.st.ID, 3*time.Second); !ok || got.ID != pl.st.ID {
				t.Fatalf("worldbot: %v = %v（ok=%v）、置いたのは %v", pl.p, got, ok, pl.st)
			}
		}
		// 別の bot はチャンクのデータ（パレットなし）で受け取る
		c2 := connectBot(t, addr, "worldbot2")
		waitLoaded(t, c2, BlockPos{cx * 16, 0, cz * 16}, 0)
		wrong := 0
		for _, pl := range placed {
			if got, ok := c2.Block(pl.p); !ok || got.ID != pl.st.ID {
				if wrong < 5 {
					t.Errorf("worldbot2: %v = %v（ok=%v）、置いたのは %v", pl.p, got, ok, pl.st)
				}
				wrong++
			}
		}
		stats := logPaletteStats(t, "worldbot2")
		direct := 0
		for k, v := range stats {
			if strings.HasPrefix(k, "blocks") && strings.Contains(k, "パレットなし") {
				direct += v
				t.Logf("ブロックのパレットなし: %s（%d 回）。ドライバが使うビット数 %d", k, v, chunk.BitsFor(data.NumBlockStates))
			}
		}
		if direct == 0 {
			t.Error("パレットなしのセクションが届いていない")
		}
		// 片付け
		rconMust(t, rc, fmt.Sprintf("fill %d 0 %d %d 1 %d minecraft:air", cx*16, cz*16, cx*16+15, cz*16+15))
	})

	logPaletteStats(t, "worldbot")
	logPacketCounts(t, "worldbot")
	c.mu.Lock()
	tp := c.teleports
	c.mu.Unlock()
	t.Logf("強制修正 %d 回（ログインの 1 回だけが期待）、送った Chunk Batch Received の最後の値 %.1f",
		tp, worldKey.of(c).batch.desiredChunksPerTick())
	if tp != 1 {
		t.Errorf("強制修正 %d 回", tp)
	}
	select {
	case <-c.Done():
		t.Errorf("切断された: %v", c.Err())
	default:
	}
	// 置いたものを片付ける
	rconMust(t, rc, fmt.Sprintf("fill %d -61 %d %d -50 %d minecraft:air", base.X-22, base.Z-22, base.X+14, base.Z+8))
	rconMust(t, rc, fmt.Sprintf("fill %d -61 %d %d -61 %d minecraft:grass_block", base.X-22, base.Z-22, base.X+14, base.Z+8))
}

type place2 struct {
	p  BlockPos
	st data.BlockState
}
