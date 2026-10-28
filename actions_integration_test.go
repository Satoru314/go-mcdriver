//go:build integration

// 単位 9（行動）を本物のサーバー（Docker の Paper 1.21.4、平らなワールド）で確かめるテスト。
//
//	sg docker -c 'go test -tags integration -run TestActionsIntegration -v -count=1 -timeout 110s .'
//
// 全部で 5 回ログインする（4.5 秒ずつあける）。ほかのテストが同じサーバーに続けてログインしていると
// connection-throttle で待たされて 110 秒を超えることがあるので、そのときは -run 'TestActionsIntegration/(dig|item)' の
// ように分けて回す。
//
// シナリオごとに専用の区画と bot を使い、並列に動かす（ログインどうしは moveConnect で 4.5 秒あける）。合格の条件:
//   - 掘る: 素手で土がおおよそ本家の時間（15 ティック = 0.75 秒）で壊れる。石は木のつるはしで掘れて丸石が落ち、
//     素手では何も落ちない。黒曜石はダイヤのつるはしで 188 ティック（9.4 秒）かかる。RCON で空気になっている
//   - 置く: 樫の階段を Yaw で 4 方向、Cursor の上下で上付き・下付きに置き分けられる。ハーフブロックの上下、
//     原木の向き（置く面で決まる）。RCON の execute if block で状態が指定どおり
//   - 使う: ドアを UseBlock で開け閉めできる。チェストを UseBlock で開くと Container() に出て、ClickSlot で
//     石を入れて出せる（RCON で中身を確かめる）
//   - 食べる: パンを食べると満腹度が 5 増える。弓を引いて放つと矢のエンティティが出る
//   - 攻撃: NoAI のゾンビを剣で攻撃して倒せる。溜めが全力のときの方がダメージが大きい
//   - どのシナリオでも、強制修正はログインと RCON の tp の分だけ。サーバーのログにその bot の警告が無い。切断されない
package mcdriver

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/internal/rcon"
	"github.com/Satoru314/go-mcdriver/internal/testserver"
	"gonum.org/v1/gonum/spatial/r3"
)

// actEnv はシナリオ 1 つの環境。bot は (bx+0.5, StandY, bz+0.5) に立つ。
type actEnv struct {
	t      *testing.T
	rc     *rcon.Client
	cl     *Client
	name   string
	bx, bz int
	tps    int // 期待する強制修正の数（ログインと tp）
}

func newActEnv(t *testing.T) *actEnv {
	ts := testserver.Start(t)
	rc := ts.RCON(t)
	area := ts.NewArea(t)
	area.ForceLoad(t, rc)
	x, z := area.Center()
	e := &actEnv{t: t, rc: rc, name: area.BotName, bx: x, bz: z, tps: 1}
	S := testserver.SurfaceY
	e.cmd(fmt.Sprintf("fill %d %d %d %d %d %d minecraft:air", area.X0, S+1, area.Z0, area.X0+31, S+12, area.Z0+31))
	e.cmd(fmt.Sprintf("fill %d %d %d %d %d %d minecraft:grass_block", area.X0, S, area.Z0, area.X0+31, S, area.Z0+31))
	e.cmd(fmt.Sprintf("fill %d %d %d %d %d %d minecraft:dirt", area.X0, S-2, area.Z0, area.X0+31, S-1, area.Z0+31))
	e.cmd(fmt.Sprintf("kill @e[type=!player,x=%d,y=%d,z=%d,dx=31,dy=20,dz=31]", area.X0, S-2, area.Z0))
	e.cl = moveConnect(t, ts.GameAddr, e.name)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("サーバーのログ（最後の 40 行）:\n%s", testserver.Logs(context.Background(), 40))
		}
	})
	waitSelf(t, e.cl, 5*time.Second, "位置を受け取る", func(s Self) bool { return s.Spawned })
	e.cmd("gamemode survival " + e.name)
	e.cmd("clear " + e.name)
	e.cmd("effect clear " + e.name)
	e.tp()
	waitLoaded(t, e.cl, BlockPos{X: e.bx, Y: testserver.StandY, Z: e.bz}, 16)
	waitSelf(t, e.cl, 5*time.Second, "サバイバルで地面に立つ", func(s Self) bool { return s.GameMode == Survival && s.OnGround })
	return e
}

func (e *actEnv) cmd(c string) string {
	e.t.Helper()
	out := rconMust(e.t, e.rc, c)
	e.t.Logf("RCON %s → %q", c, out)
	return out
}

// tp は bot を (bx+0.5, StandY, bz+0.5) に東向きで立たせ、届くまで待つ。
func (e *actEnv) tp() {
	e.t.Helper()
	before := e.teleports()
	e.cmd(fmt.Sprintf("tp %s %d.5 %d %d.5 -90 0", e.name, e.bx, testserver.StandY, e.bz))
	e.tps++
	e.waitFor(5*time.Second, "tp が届く", func() bool { return e.teleports() > before })
}

func (e *actEnv) teleports() int {
	e.cl.mu.Lock()
	defer e.cl.mu.Unlock()
	return e.cl.teleports
}

func (e *actEnv) waitFor(d time.Duration, desc string, cond func() bool) {
	e.t.Helper()
	end := time.Now().Add(d)
	for !cond() {
		select {
		case <-e.cl.Done():
			e.t.Fatalf("%s を待つあいだに切断された: %v", desc, e.cl.Err())
		default:
		}
		if time.Now().After(end) {
			e.t.Fatalf("%v たっても %s にならない", d, desc)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// at は bot の立つブロックからずらした位置。
func (e *actEnv) at(dx, dy, dz int) BlockPos {
	return BlockPos{X: e.bx + dx, Y: testserver.StandY + dy, Z: e.bz + dz}
}

func (e *actEnv) setblock(p BlockPos, block string) {
	e.t.Helper()
	e.cmd(fmt.Sprintf("setblock %d %d %d %s", p.X, p.Y, p.Z, block))
	e.waitFor(5*time.Second, fmt.Sprintf("%v が %s になる", p, block), func() bool {
		s, ok := e.cl.Block(p)
		name, _, _ := strings.Cut(strings.TrimPrefix(block, "minecraft:"), "[")
		return ok && s.Name() == name
	})
}

// ifBlock は RCON の execute if block で p が spec（例 "minecraft:oak_stairs[facing=south]"）かを確かめる。
func (e *actEnv) ifBlock(p BlockPos, spec string) bool {
	e.t.Helper()
	return strings.Contains(e.cmd(fmt.Sprintf("execute if block %d %d %d %s", p.X, p.Y, p.Z, spec)), "passed")
}

// hold は持ち物の item を手に持つ（ホットバーにあること）。
func (e *actEnv) hold(item string) {
	e.t.Helper()
	inv := waitInv(e.t, e.cl, 5*time.Second, item+" が持ち物に出る", func(inv Inventory) bool {
		i := inv.Find(item)
		return i >= SlotHotbar && i < SlotHotbar+9
	})
	if err := e.cl.SelectHotbar(inv.Find(item) - SlotHotbar); err != nil {
		e.t.Fatal(err)
	}
}

// check は強制修正の数と、サーバーのログの警告を確かめる。
func (e *actEnv) check() {
	e.t.Helper()
	select {
	case <-e.cl.Done():
		e.t.Fatalf("切断された: %v", e.cl.Err())
	default:
	}
	if n := e.teleports(); n != e.tps {
		e.t.Errorf("強制修正の数 %d、期待は %d（ログインと tp）", n, e.tps)
	}
	logs := testserver.Logs(context.Background(), 3000)
	for _, line := range strings.Split(logs, "\n") {
		bad := strings.Contains(line, e.name) && (strings.Contains(line, "WARN") || strings.Contains(line, "moved"))
		for _, w := range []string{"Mismatch in destroy block pos", "Rejecting UseItemOnPacket"} {
			bad = bad || strings.Contains(line, w)
		}
		if bad {
			e.t.Errorf("サーバーのログに警告: %s", line)
		}
	}
}

func TestActionsIntegration(t *testing.T) {
	t.Run("dig", func(t *testing.T) {
		t.Parallel()
		e := newActEnv(t)
		dirt, stone1, stone2, obsidian := e.at(1, -1, 0), e.at(-1, -1, 0), e.at(0, -1, 1), e.at(0, -1, -1)
		e.setblock(dirt, "minecraft:dirt")
		e.setblock(stone1, "minecraft:stone")
		e.setblock(stone2, "minecraft:stone")
		e.setblock(obsidian, "minecraft:obsidian")

		dig := func(p BlockPos, wantTicks int) time.Duration {
			t.Helper()
			n, ok := e.cl.DigTicks(p)
			if !ok || n != wantTicks {
				t.Errorf("DigTicks(%v) = %d %v、期待 %d", p, n, ok, wantTicks)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			t0 := time.Now()
			if err := e.cl.Dig(ctx, p, Up); err != nil {
				t.Fatalf("Dig(%v): %v", p, err)
			}
			d := time.Since(t0)
			t.Logf("%v を %d ティック（%v）の見込みで掘った: %v", p, n, time.Duration(n)*TickInterval, d)
			if !e.ifBlock(p, "minecraft:air") {
				t.Errorf("掘ったのにサーバーで %v が空気でない", p)
			}
			return d
		}
		// 素手で土: 15 ティック = 0.75 秒（＋確認が届くまで）
		if d := dig(dirt, 15); d < 650*time.Millisecond || d > 1200*time.Millisecond {
			t.Errorf("素手で土を掘る時間 %v、期待はおおよそ 0.75 秒", d)
		}
		// 素手で石: 151 ティック。何も落ちない
		dig(stone1, 151)
		time.Sleep(1500 * time.Millisecond)
		if n := e.cl.Inventory().Count("cobblestone"); n != 0 {
			t.Errorf("素手で石を掘ったのに丸石が %d 個", n)
		}
		for _, en := range e.cl.Entities() {
			if en.Type.Name == "item" && (en.Item.Name == "cobblestone" || en.Item.Name == "stone") {
				t.Errorf("素手で石を掘ったのに落ちているアイテム: %+v", en.Item)
			}
		}
		// 木のつるはしで石: 23 ティック。丸石が落ちて拾う
		e.cmd("give " + e.name + " minecraft:wooden_pickaxe")
		e.hold("wooden_pickaxe")
		time.Sleep(200 * time.Millisecond) // 手に持つ物が変わったことがサーバーに届き、属性が届くまで
		dig(stone2, 23)
		e.waitFor(5*time.Second, "丸石が落ちる", func() bool { return e.dropped("cobblestone", stone2) })
		// ダイヤのつるはしで黒曜石: 188 ティック = 9.4 秒
		e.cmd("give " + e.name + " minecraft:diamond_pickaxe")
		e.hold("diamond_pickaxe")
		time.Sleep(200 * time.Millisecond)
		if d := dig(obsidian, 188); d < 9200*time.Millisecond || d > 10500*time.Millisecond {
			t.Errorf("黒曜石を掘る時間 %v、期待はおおよそ 9.4 秒", d)
		}
		e.waitFor(5*time.Second, "黒曜石が落ちる", func() bool { return e.dropped("obsidian", obsidian) })
		e.check()
	})

	t.Run("place", func(t *testing.T) {
		t.Parallel()
		e := newActEnv(t)
		e.cmd("give " + e.name + " minecraft:oak_stairs 64")
		e.cmd("give " + e.name + " minecraft:oak_slab 64")
		e.cmd("give " + e.name + " minecraft:oak_log 64")
		// 西の支え: x=-2 の石（y=0、z=-2〜2）。その西の面に置く（置かれるのは x=-3）
		for dz := -2; dz <= 2; dz++ {
			e.setblock(e.at(-2, 0, dz), "minecraft:stone")
		}
		// 南の支え（原木を z 向きに置く）
		e.setblock(e.at(0, 0, 3), "minecraft:stone")

		place := func(against BlockPos, f Face, yaw *float64, cursor *r3.Vec, want BlockPos, spec string) {
			t.Helper()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			p, err := e.cl.Place(ctx, against, f, PlaceOpts{Yaw: yaw, Cursor: cursor})
			if err != nil {
				t.Errorf("Place(%v, %v) %s: %v", against, f, spec, err)
				return
			}
			t.Logf("Place(%v, %v) → %v %v", against, f, p.Pos, p.State)
			if p.Pos != want {
				t.Errorf("置かれた位置 %v、期待 %v", p.Pos, want)
			}
			if !e.ifBlock(want, spec) {
				t.Errorf("%v が %s でない（ドライバの見た状態 %v）", want, spec, p.State)
			}
		}
		yaw := func(v float64) *float64 { return &v }
		cur := func(x, y, z float64) *r3.Vec { return &r3.Vec{X: x, Y: y, Z: z} }

		// 階段: Yaw で 4 方向（地面の上の面に置くので下付き）
		e.hold("oak_stairs")
		for i, d := range []struct {
			yaw    float64
			facing string
		}{{0, "south"}, {90, "west"}, {180, "north"}, {-90, "east"}} {
			ground := e.at(2, -1, -2+i)
			place(ground, Up, yaw(d.yaw), nil, ground.Neighbor(Up),
				fmt.Sprintf("minecraft:oak_stairs[facing=%s,half=bottom]", d.facing))
		}
		// 階段: 横の面の上半分を指すと上付き、下半分なら下付き
		place(e.at(-2, 0, -1), West, yaw(90), cur(0, 0.75, 0.5), e.at(-3, 0, -1), "minecraft:oak_stairs[facing=west,half=top]")
		place(e.at(-2, 0, 1), West, yaw(90), cur(0, 0.25, 0.5), e.at(-3, 0, 1), "minecraft:oak_stairs[facing=west,half=bottom]")
		// ハーフブロック: 上半分 → 上付き、下半分 → 下付き
		e.hold("oak_slab")
		place(e.at(-2, 0, -2), West, nil, cur(0, 0.75, 0.5), e.at(-3, 0, -2), "minecraft:oak_slab[type=top]")
		place(e.at(-2, 0, 2), West, nil, cur(0, 0.25, 0.5), e.at(-3, 0, 2), "minecraft:oak_slab[type=bottom]")
		// 下付きのハーフブロックの上の面に重ねると 2 段（同じ位置）
		place(e.at(-3, 0, 2), Up, nil, nil, e.at(-3, 0, 2), "minecraft:oak_slab[type=double]")
		// 原木: 置く面の軸の向き
		e.hold("oak_log")
		place(e.at(-2, 0, 0), West, nil, nil, e.at(-3, 0, 0), "minecraft:oak_log[axis=x]")
		place(e.at(0, 0, 3), South, nil, nil, e.at(0, 0, 4), "minecraft:oak_log[axis=z]")
		place(e.at(2, -1, 2), Up, nil, nil, e.at(2, 0, 2), "minecraft:oak_log[axis=y]")
		e.check()
	})

	t.Run("use", func(t *testing.T) {
		t.Parallel()
		e := newActEnv(t)
		e.cmd("give " + e.name + " minecraft:oak_door 4")
		e.cmd("give " + e.name + " minecraft:chest 4")
		e.cmd("give " + e.name + " minecraft:stone 10")
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// ドア: 東の 2 ブロック先に、西を向いて置く
		e.hold("oak_door")
		y := 90.0
		door, err := e.cl.Place(ctx, e.at(2, -1, 0), Up, PlaceOpts{Yaw: &y})
		if err != nil {
			t.Fatalf("ドアを置く: %v", err)
		}
		if door.Pos != e.at(2, 0, 0) || !e.ifBlock(door.Pos, "minecraft:oak_door[half=lower,facing=west,open=false]") {
			t.Fatalf("ドア %+v", door)
		}
		for _, open := range []bool{true, false} {
			if err := e.cl.UseBlock(ctx, door.Pos, West); err != nil {
				t.Fatalf("ドアを使う: %v", err)
			}
			if !e.ifBlock(door.Pos, fmt.Sprintf("minecraft:oak_door[open=%v]", open)) {
				t.Errorf("ドアの open が %v でない", open)
			}
			s, _ := e.cl.Block(door.Pos)
			if v, _ := s.Get("open"); v != strconv.FormatBool(open) {
				t.Errorf("ドライバの見たドア %v、期待 open=%v", s, open)
			}
		}

		// チェスト: 西の 2 ブロック先
		e.hold("chest")
		chest, err := e.cl.Place(ctx, e.at(-2, -1, 0), Up, PlaceOpts{})
		if err != nil {
			t.Fatalf("チェストを置く: %v", err)
		}
		if err := e.cl.UseBlock(ctx, chest.Pos, Up); err != nil {
			t.Fatalf("チェストを開く: %v", err)
		}
		w, ok := e.cl.Container()
		if !ok || w.Type != "generic_9x3" || w.Size != 27 {
			t.Fatalf("Container %+v %v", w, ok)
		}
		e.waitFor(5*time.Second, "チェストの中身が届く", func() bool { w, _ := e.cl.Container(); return len(w.Slots) == 63 })
		w, _ = e.cl.Container()
		src := -1
		for i := w.Size; i < len(w.Slots); i++ {
			if w.Slots[i].Name() == "stone" {
				src = i
			}
		}
		if src < 0 {
			t.Fatalf("チェストの窓の持ち物の部分に石が無い: %+v", w.Slots)
		}
		// 石を拾ってチェストのスロット 0 に置く
		if err := e.cl.ClickSlot(ctx, src, 0, ClickPickup); err != nil {
			t.Fatalf("石を拾う: %v", err)
		}
		if err := e.cl.ClickSlot(ctx, 0, 0, ClickPickup); err != nil {
			t.Fatalf("石をチェストに置く: %v", err)
		}
		cp := chest.Pos
		if !e.ifItems(cp, 0, "minecraft:stone") || e.count("stone") != 0 {
			t.Errorf("チェストのスロット 0 に石が無い、または持ち物に残っている")
		}
		// シフトクリックで持ち物に戻す
		if err := e.cl.ClickSlot(ctx, 0, 0, ClickQuickMove); err != nil {
			t.Fatalf("石を持ち物に戻す: %v", err)
		}
		if e.ifItems(cp, 0, "minecraft:stone") || e.count("stone") != 10 {
			t.Errorf("石が持ち物に戻っていない")
		}
		if err := e.cl.CloseContainer(); err != nil {
			t.Fatal(err)
		}
		e.check()
	})

	t.Run("item", func(t *testing.T) {
		t.Parallel()
		e := newActEnv(t)
		// 空腹の効果で満腹度を減らす
		e.cmd("effect give " + e.name + " minecraft:hunger 3 255")
		waitSelf(t, e.cl, 10*time.Second, "満腹度が 15 以下", func(s Self) bool { return s.Food <= 15 })
		e.cmd("effect clear " + e.name)
		time.Sleep(500 * time.Millisecond)
		e.cmd("give " + e.name + " minecraft:bread 3")
		e.hold("bread")
		before := e.cl.Self().Food
		t0 := time.Now()
		if err := e.cl.StartUseItem(); err != nil {
			t.Fatal(err)
		}
		s := waitSelf(t, e.cl, 4*time.Second, "満腹度が増える", func(s Self) bool { return s.Food > before })
		d := time.Since(t0)
		t.Logf("パンを食べた: 満腹度 %d → %d、%v", before, s.Food, d)
		if s.Food != min(20, before+5) {
			t.Errorf("満腹度 %d → %d、期待 +5", before, s.Food)
		}
		if d < 1500*time.Millisecond || d > 2200*time.Millisecond {
			t.Errorf("食べ終わるまで %v、期待はおおよそ 1.6 秒", d)
		}
		waitInv(t, e.cl, 3*time.Second, "パンが 1 つ減る", func(inv Inventory) bool { return inv.Count("bread") == 2 })
		if err := e.cl.StopUseItem(); err != nil {
			t.Fatal(err)
		}

		// 弓: 東の下向き（地面に刺さる）に引いて放つ
		e.cmd("give " + e.name + " minecraft:bow")
		e.cmd("give " + e.name + " minecraft:arrow 8")
		e.hold("bow")
		waitInv(t, e.cl, 3*time.Second, "矢が届く", func(inv Inventory) bool { return inv.Count("arrow") == 8 })
		e.cl.Look(-90, 20)
		time.Sleep(200 * time.Millisecond)
		if err := e.cl.StartUseItem(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1200 * time.Millisecond) // 20 ティックで引き切る
		if err := e.cl.StopUseItem(); err != nil {
			t.Fatal(err)
		}
		e.waitFor(3*time.Second, "矢のエンティティが出る", func() bool {
			for _, en := range e.cl.Entities() {
				if en.Type.Name == "arrow" {
					return true
				}
			}
			return false
		})
		waitInv(t, e.cl, 3*time.Second, "矢が 1 本減る", func(inv Inventory) bool { return inv.Count("arrow") == 7 })
		if out := e.cmd(fmt.Sprintf("execute if entity @e[type=arrow,x=%d,y=%d,z=%d,distance=..20]", e.bx, testserver.StandY, e.bz)); !strings.Contains(out, "passed") {
			t.Errorf("サーバーに矢が無い: %q", out)
		}
		e.check()
	})

	t.Run("attack", func(t *testing.T) {
		t.Parallel()
		e := newActEnv(t)
		tag := "t9z" + e.name
		zx, zy, zz := float64(e.bx)+2.5, float64(testserver.StandY), float64(e.bz)+0.5
		e.cmd(fmt.Sprintf(`summon minecraft:zombie %v %v %v {NoAI:1b,Silent:1b,PersistenceRequired:1b,Tags:["%s"],active_effects:[{id:"minecraft:fire_resistance",duration:-1,amplifier:0b,show_particles:0b}]}`, zx, zy, zz, tag))
		sel := fmt.Sprintf("@e[type=zombie,tag=%s,limit=1]", tag)
		var zid int32
		e.waitFor(5*time.Second, "ゾンビが見える", func() bool {
			for _, en := range e.cl.Entities() {
				if en.Type.Name == "zombie" && math.Abs(en.Pos.X-zx) < 0.1 && math.Abs(en.Pos.Z-zz) < 0.1 {
					zid = en.ID
					return true
				}
			}
			return false
		})
		healthRe := regexp.MustCompile(`([0-9.]+)f`)
		health := func() float64 {
			t.Helper()
			out := e.cmd("data get entity " + sel + " Health")
			m := healthRe.FindStringSubmatch(out)
			if m == nil {
				return -1
			}
			v, _ := strconv.ParseFloat(m[1], 64)
			return v
		}
		attack := func() {
			t.Helper()
			// ノックバックで離れたら戻す
			if en, ok := e.cl.Entity(zid); ok && r3.Norm(r3.Sub(en.Pos, r3.Vec{X: zx, Y: zy, Z: zz})) > 0.5 {
				e.cmd(fmt.Sprintf("tp %s %v %v %v", sel, zx, zy, zz))
				time.Sleep(200 * time.Millisecond)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := e.cl.Attack(ctx, zid); err != nil {
				t.Fatalf("Attack: %v", err)
			}
		}
		e.cmd("give " + e.name + " minecraft:iron_sword")
		waitInv(t, e.cl, 5*time.Second, "剣", func(inv Inventory) bool { return inv.Count("iron_sword") == 1 })
		// 持ち替えた直後（溜めが少ない）の攻撃
		e.hold("iron_sword")
		time.Sleep(120 * time.Millisecond)
		weakCharge := e.cl.AttackCharge()
		h0 := health()
		attack()
		time.Sleep(300 * time.Millisecond)
		h1 := health()
		// 全力の攻撃
		e.waitFor(3*time.Second, "溜めが全力", func() bool { return e.cl.AttackCharge() >= 1 })
		attack()
		time.Sleep(300 * time.Millisecond)
		h2 := health()
		weak, full := h0-h1, h1-h2
		t.Logf("溜め %.2f の攻撃で %.2f、全力で %.2f のダメージ（HP %.1f → %.1f → %.1f）", weakCharge, weak, full, h0, h1, h2)
		if weakCharge > 0.4 {
			t.Errorf("持ち替えた直後の溜め %v", weakCharge)
		}
		if !(weak > 0 && full > weak*2) {
			t.Errorf("全力のダメージ %v が溜めの少ない攻撃 %v より十分大きくない", full, weak)
		}
		// 素手に持ち替えて全力まで待ち、剣に持ち替えた直後から全力まで待って攻撃すると、全力のダメージになる
		// （サーバーの Update Attributes を待たずに、手元で剣の攻撃速度を当てはめるので、素手の速さで全力とみなさない）
		empty := -1
		inv := e.cl.Inventory()
		for i, it := range inv.Slots[SlotHotbar : SlotHotbar+9] {
			if it.Empty() {
				empty = i
				break
			}
		}
		if empty < 0 {
			t.Fatal("ホットバーに空きが無い")
		}
		if err := e.cl.SelectHotbar(empty); err != nil {
			t.Fatal(err)
		}
		time.Sleep(120 * time.Millisecond)
		e.waitFor(3*time.Second, "素手の溜めが全力", func() bool { return e.cl.AttackCharge() >= 1 })
		e.hold("iron_sword")
		// 持ち替えてから全力になるまでの溜めを細かく読み、どれも剣の攻撃速度（本家の float の 1.6）で数えた値であることを確かめる
		// （素手の攻撃速度 4 のままなら 0.3, 0.5, ... が出る。サーバーの Update Attributes が届く前の数ティックに出やすい）
		swordDelay := float64(float32(1 / (4 + -2.4000000953674316) * 20))
		var seen []float64
		var switched time.Time
		end := time.Now().Add(3 * time.Second)
		for {
			ch := e.cl.AttackCharge()
			if switched.IsZero() {
				if ch < 0.5 {
					switched = time.Now() // 持ち替えに気づいて溜め直した
				}
			} else if ch >= 1 {
				break
			}
			if !switched.IsZero() && (len(seen) == 0 || seen[len(seen)-1] != ch) {
				seen = append(seen, ch)
			}
			if time.Now().After(end) {
				t.Fatalf("持ち替えてから溜めが全力にならない（%v）", seen)
			}
			time.Sleep(2 * time.Millisecond)
		}
		wait := time.Since(switched)
		for _, ch := range seen {
			x := ch*swordDelay - 0.5
			if math.Abs(x-math.Round(x)) > 1e-3 {
				t.Errorf("持ち替えたあとの溜め %v が剣の攻撃速度で数えた値でない（%v）", ch, seen)
				break
			}
		}
		attack()
		time.Sleep(300 * time.Millisecond)
		h3 := health()
		t.Logf("持ち替えてから %v 待って全力の攻撃で %.2f のダメージ（HP %.1f → %.1f）", wait, h2-h3, h2, h3)
		if math.Abs((h2-h3)-full) > 0.05 {
			t.Errorf("持ち替えた直後から全力まで待った攻撃のダメージ %v、全力のダメージ %v と違う", h2-h3, full)
		}
		if wait < 400*time.Millisecond {
			t.Errorf("剣で全力になるまで %v（本家は 13 ティック = 0.65 秒）", wait)
		}
		// 倒れるまで全力で攻撃する
		for i := 0; i < 8; i++ {
			if !strings.Contains(e.cmd("execute if entity "+sel), "passed") {
				break
			}
			e.waitFor(3*time.Second, "溜めが全力", func() bool { return e.cl.AttackCharge() >= 1 })
			if _, ok := e.cl.Entity(zid); !ok {
				break
			}
			attack()
			time.Sleep(200 * time.Millisecond)
		}
		e.waitFor(3*time.Second, "ゾンビが消える", func() bool { _, ok := e.cl.Entity(zid); return !ok })
		if out := e.cmd("execute if entity " + sel); !strings.Contains(out, "failed") {
			t.Errorf("ゾンビが倒れていない: %q", out)
		}
		e.check()
	})
}

// dropped は、item が p の近くに落ちているか、拾って持ち物にあるかを返す
// （掘った穴の底に落ちた物は、地表に立つ bot には届かず拾えないことがある）。
func (e *actEnv) dropped(item string, p BlockPos) bool {
	if e.cl.Inventory().Count(item) > 0 {
		return true
	}
	for _, en := range e.cl.Entities() {
		if en.Type.Name == "item" && en.Item.Name == item && r3.Norm(r3.Sub(en.Pos, p.Center())) < 3 {
			return true
		}
	}
	return false
}

// ifItems は RCON でブロック p の容器のスロット slot に item があるかを確かめる。
func (e *actEnv) ifItems(p BlockPos, slot int, item string) bool {
	e.t.Helper()
	return strings.Contains(e.cmd(fmt.Sprintf("execute if items block %d %d %d container.%d %s", p.X, p.Y, p.Z, slot, item)), "passed")
}

// count は bot が持っている item の数を RCON で数える。
func (e *actEnv) count(item string) int {
	e.t.Helper()
	m := foundRe.FindStringSubmatch(e.cmd(fmt.Sprintf("clear %s %s 0", e.name, item)))
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}
