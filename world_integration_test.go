//go:build integration

// 単位 4（地形）を本物のサーバー（internal/testserver の Paper 1.21.4、平らなワールド）で確かめるテスト。
// 普段の go test では動かない:
//
//	sg docker -c 'go test -tags integration -run TestWorldIntegration -v -count=1 -timeout 110s .'
//
// テストは自分の区画（testserver.NewArea）の中だけでブロックを置く。
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
	"github.com/Satoru314/go-mcdriver/internal/rcon"
	"github.com/Satoru314/go-mcdriver/internal/testserver"
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

func rconMust(t *testing.T, rc *rcon.Client, cmd string) string {
	t.Helper()
	return testserver.Command(t, rc, cmd)
}

func connectBot(t *testing.T, addr, name string) *Client {
	t.Helper()
	var c *Client
	var err error
	for try := 0; try < 3; try++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		c, err = Connect(ctx, Config{Addr: addr, Username: name, ViewDistance: 4})
		cancel()
		// Paper は同じアドレスからの接続を 4 秒あけないと断る（connection-throttle）。
		// 断られた接続も「最後の接続」として数えられるので、4 秒より長く待ってからやり直す
		if err == nil || !strings.Contains(err.Error(), "throttled") {
			break
		}
		time.Sleep(5 * time.Second)
	}
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

// moveBot は RCON の tp で bot を (x, StandY, z) の地表に立たせ、そのまわりのチャンクが届くまで待つ。
func moveBot(t *testing.T, rc *rcon.Client, c *Client, x, z int) BlockPos {
	t.Helper()
	c.mu.Lock()
	before := c.teleports
	c.mu.Unlock()
	out := rconMust(t, rc, fmt.Sprintf("tp %s %d.5 %d %d.5", c.cfg.Username, x, testserver.StandY, z))
	if !strings.Contains(out, "Teleported") {
		t.Fatalf("tp: %q", out)
	}
	end := time.Now().Add(10 * time.Second)
	for {
		c.mu.Lock()
		n := c.teleports
		c.mu.Unlock()
		if n > before {
			break
		}
		if time.Now().After(end) {
			t.Fatal("tp の強制修正が届かない")
		}
		time.Sleep(20 * time.Millisecond)
	}
	p := botBlockPos(t, c)
	if p != (BlockPos{x, testserver.StandY, z}) {
		t.Fatalf("tp のあとの位置 %v", p)
	}
	waitLoaded(t, c, p, 32)
	return p
}

// ---- テスト ----

func TestWorldIntegration(t *testing.T) {
	ts := testserver.Start(t)
	rc := ts.RCON(t)
	area := ts.NewArea(t)
	area.ForceLoad(t, rc)
	defer func() {
		if t.Failed() {
			t.Logf("サーバーのログ（最後の 40 行）:\n%s", testserver.Logs(context.Background(), 40))
		}
	}()
	X0, Z0 := area.X0, area.Z0

	start := time.Now()
	c := connectBot(t, ts.GameAddr, area.BotName)
	cx, cz := area.Center()
	feet := moveBot(t, rc, c, cx, cz)
	t.Logf("bot の足元 %v、チャンク %d 個が %v で届いた。Dimension %+v、バイオームの registry %d 個",
		feet, c.LoadedChunks(), time.Since(start), c.Dimension(), len(c.reg.Entries("minecraft:worldgen/biome")))

	// 1. 地表: 平らなワールドは y=-64 が岩盤、-63〜-62 が土、-61 が草、その上は空気
	t.Run("地表", func(t *testing.T) {
		want := map[int]string{-64: "bedrock", -63: "dirt", -62: "dirt", -61: "grass_block[snowy=false]", -60: "air", -59: "air", 0: "air"}
		bad := 0
		for dx := -14; dx <= 14; dx += 2 {
			for dz := -14; dz <= 14; dz += 2 {
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
				if top != testserver.SurfaceY {
					t.Errorf("(%d, %d) の地表の高さ %d、期待 %d", p.X, p.Z, top, testserver.SurfaceY)
				}
			}
		}
	})

	// 2. チャンクのデータで届いた明るさ: 空の下は block 0 / sky 15、土の中は sky 0
	t.Run("明るさ", func(t *testing.T) {
		for _, p := range []BlockPos{feet, {X0 + 28, testserver.StandY, Z0 + 28}, {X0 + 3, testserver.StandY, Z0 + 3}} {
			if b, sky, ok := c.Light(p); !ok || sky != 15 || b != 0 {
				t.Errorf("空の下 %v: block %d sky %d ok %v、期待 0 15", p, b, sky, ok)
			}
			if _, sky, ok := c.Light(p.Offset(0, -2, 0)); !ok || sky != 0 {
				t.Errorf("土の中 %v: sky %d ok %v、期待 0", p.Offset(0, -2, 0), sky, ok)
			}
		}
		// ブロックの無い高いところ（サーバーが明るさの配列を送ってこないセクション）。公式のクライアントでは空の明るさ 15
		for _, y := range []int{-20, 10, 200} {
			p := feet.Offset(0, y-feet.Y, 0)
			if b, sky, ok := c.Light(p); !ok || sky != 15 || b != 0 {
				t.Errorf("高い空中 %v: block %d sky %d ok %v、期待 0 15", p, b, sky, ok)
			}
		}
	})

	// 3. 光る石を置いたあと、そのまわりの Light() は古い値を返さない（ok=false か、届いた正しい値）
	t.Run("光る石のあとの明るさ", func(t *testing.T) {
		// サーバーが明るさの配列を送ってくる、地表の近くのセクション。ほかの項目より先に行う（ブロックを変えると周りが古くなるため）
		lamp := BlockPos{X0 + 28, testserver.StandY + 3, Z0 + 12}
		next := lamp.Offset(1, 0, 0)
		if b, sky, ok := c.Light(next); !ok || b != 0 || sky != 15 {
			t.Fatalf("置く前の %v: block %d sky %d ok %v、期待 0 15", next, b, sky, ok)
		}
		rconMust(t, rc, fmt.Sprintf("setblock %d %d %d minecraft:glowstone", lamp.X, lamp.Y, lamp.Z))
		glow := fullState(t, "glowstone")
		if got, ok := waitBlock(c, lamp, glow.ID, 3*time.Second); !ok || got.ID != glow.ID {
			t.Fatalf("光る石が届かない: %v", got)
		}
		check := func(when string) {
			for _, q := range []struct {
				p    BlockPos
				want int
			}{{next, 14}, {lamp.Offset(3, 0, 0), 12}, {lamp.Offset(0, 0, 14), 1}} {
				b, sky, ok := c.Light(q.p)
				t.Logf("%s %v: block %d sky %d ok %v", when, q.p, b, sky, ok)
				if ok && b != q.want {
					t.Errorf("%s %v: 古い明るさ block %d を返した（ok=true）。正しくは %d", when, q.p, b, q.want)
				}
			}
		}
		check("直後")
		time.Sleep(2 * time.Second)
		check("2 秒後")
	})

	// 4. RCON の setblock / fill で置いたものと Block() が一致する。変えたら反映される
	base := BlockPos{X0 + 2, testserver.StandY, Z0 + 2}
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
		a, b := BlockPos{X0 + 2, testserver.StandY + 10, Z0 + 20}, BlockPos{X0 + 20, testserver.StandY + 20, Z0 + 26}
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

		// 変えたら反映される: 階段を石に、石を空気に
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
		// 区画の中にすっぽり入るチャンクの、セクション y=0〜15
		pcx, pcz := (X0+15)>>4, (Z0+15)>>4
		var placed []place2
		for i, st := range states {
			p := BlockPos{pcx*16 + i%16, i / 256, pcz*16 + (i/16)%16}
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
				t.Fatalf("%s: %v = %v（ok=%v）、置いたのは %v", area.BotName, pl.p, got, ok, pl.st)
			}
		}
		// 別の bot はチャンクのデータ（パレットなし）で受け取る
		name2 := area.BotName + "b"
		c2 := connectBot(t, ts.GameAddr, name2)
		moveBot(t, rc, c2, pcx*16+8, pcz*16+8)
		wrong := 0
		for _, pl := range placed {
			if got, ok := c2.Block(pl.p); !ok || got.ID != pl.st.ID {
				if wrong < 5 {
					t.Errorf("%s: %v = %v（ok=%v）、置いたのは %v", name2, pl.p, got, ok, pl.st)
				}
				wrong++
			}
		}
		stats := logPaletteStats(t, name2)
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
	})

	logPaletteStats(t, area.BotName)
	logPacketCounts(t, area.BotName)
	c.mu.Lock()
	tp := c.teleports
	c.mu.Unlock()
	t.Logf("強制修正 %d 回（ログインと tp の 2 回が期待）", tp)
	if tp != 2 {
		t.Errorf("強制修正 %d 回", tp)
	}
	select {
	case <-c.Done():
		t.Errorf("切断された: %v", c.Err())
	default:
	}
}

type place2 struct {
	p  BlockPos
	st data.BlockState
}
