package mcdriver

import (
	"context"
	"errors"
	"maps"
	"math"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
	"gonum.org/v1/gonum/spatial/r3"
)

// ---- 計算（本家と同じ式）----

func blockState(t *testing.T, s string) data.BlockState {
	t.Helper()
	st, _ := data.StateByID(stateOf(t, s))
	return st
}

func TestDestroyProgress(t *testing.T) {
	ground := digEnv{onGround: true, breakSpeed: 1, submerged: 0.2, haste: -1, fatigue: -1}
	with := func(f func(e *digEnv)) digEnv { e := ground; f(&e); return e }
	hand := ground
	wood := with(func(e *digEnv) { e.held = invItem(t, "wooden_pickaxe") })
	diamond := with(func(e *digEnv) { e.held = invItem(t, "diamond_pickaxe") })
	shovel := with(func(e *digEnv) { e.held = invItem(t, "iron_shovel") })

	for _, tc := range []struct {
		desc  string
		block string
		env   digEnv
		step  float32 // 1 ティックの進み（本家の式を手で計算したもの）
		ticks int
	}{
		// 土: 硬さ 0.5、素手 1、道具は要らない（÷30）→ 1/15。float32 で 15 回足すと 1 に届く
		{"土を素手で", "dirt", hand, float32(1) / 0.5 / 30, 15},
		// 土をシャベル（鉄 6）で
		{"土を鉄のシャベルで", "dirt", shovel, float32(6) / 0.5 / 30, 3},
		// 石: 硬さ 1.5、素手は道具が合わない（÷100）。float32 で足すので 150 回では 1 に届かず 151（本家も同じ）
		{"石を素手で", "stone", hand, float32(1) / 1.5 / 100, 151},
		// 石を木のつるはし（2）で（÷30）
		{"石を木のつるはしで", "stone", wood, float32(2) / 1.5 / 30, 23},
		// 黒曜石: 硬さ 50、ダイヤのつるはし（8）
		{"黒曜石をダイヤのつるはしで", "obsidian", diamond, float32(8) / 50 / 30, 188},
		// 黒曜石を木のつるはしで: 速さは 2 だが落とせない（÷100）
		{"黒曜石を木のつるはしで", "obsidian", wood, float32(2) / 50 / 100, 2500},
		// 効率強化 V（属性 mining_efficiency = 26）は道具の速さが 1 より大きいときだけ足す
		{"石をダイヤ+効率Vで", "stone", with(func(e *digEnv) { e.held = invItem(t, "diamond_pickaxe"); e.efficiency = 26 }), float32(8+26) / 1.5 / 30, 2},
		{"土を素手+効率で（足さない）", "dirt", with(func(e *digEnv) { e.efficiency = 26 }), float32(1) / 0.5 / 30, 15},
		// 空中は 1/5、水中は × 0.2
		{"土を空中で", "dirt", with(func(e *digEnv) { e.onGround = false }), float32(1) / 5 / 0.5 / 30, 76},
		{"土を水中の空中で", "dirt", with(func(e *digEnv) { e.onGround = false; e.eyeInWater = true }), float32(1) * 0.2 / 5 / 0.5 / 30, 0},
		// 採掘速度上昇 II（amplifier 1）は × 1.4、採掘速度低下 I は × 0.3
		{"土を素手+採掘速度上昇II", "dirt", with(func(e *digEnv) { e.haste = 1 }), float32(1) * 1.4 / 0.5 / 30, 0},
		{"土を素手+採掘速度低下I", "dirt", with(func(e *digEnv) { e.fatigue = 0 }), float32(1) * 0.3 / 0.5 / 30, 0},
		{"土を素手+採掘速度低下IV", "dirt", with(func(e *digEnv) { e.fatigue = 3 }), float32(1) * 8.1e-4 / 0.5 / 30, 0},
		// 硬さ 0 は一瞬
		{"短い草", "short_grass", hand, float32(math.Inf(1)), 1},
		// 剣はクモの巣に 15
		{"クモの巣を剣で", "cobweb", with(func(e *digEnv) { e.held = invItem(t, "iron_sword") }), float32(15) / 4 / 30, 0},
	} {
		got := destroyProgress(blockState(t, tc.block), tc.env)
		if math.Abs(float64(got-tc.step)) > 1e-6*math.Max(1, float64(tc.step)) && !(math.IsInf(float64(got), 1) && math.IsInf(float64(tc.step), 1)) {
			t.Errorf("%s: 進み %v、期待 %v", tc.desc, got, tc.step)
		}
		if tc.ticks > 0 {
			if n, ok := digTicksFor(got); !ok || n != tc.ticks {
				t.Errorf("%s: %d ティック（ok=%v）、期待 %d", tc.desc, n, ok, tc.ticks)
			}
		}
	}
	if got := destroyProgress(blockState(t, "bedrock"), ground); got != 0 {
		t.Errorf("岩盤の進み %v、期待 0", got)
	}
	if _, ok := digTicksFor(0); ok {
		t.Error("進み 0 なのに ok")
	}
}

func TestAttributeValue(t *testing.T) {
	a := &packets.Attribute{Base: 4, Modifiers: []packets.AttributeModifier{
		{ID: "minecraft:base_attack_speed", Amount: -2.4000000953674316, Operation: packets.ModifierAddValue},
	}}
	if v := attrAttackSpeed.value(a); math.Abs(v-1.6) > 1e-6 {
		t.Errorf("剣の攻撃速度 %v、期待 1.6", v)
	}
	// 足す → 元の値に掛けて足す → 掛ける の順
	a = &packets.Attribute{Base: 2, Modifiers: []packets.AttributeModifier{
		{Amount: 0.5, Operation: packets.ModifierAddMultipliedTotal},
		{Amount: 1, Operation: packets.ModifierAddValue},
		{Amount: 1, Operation: packets.ModifierAddMultipliedBase},
	}}
	if v := attrAttackSpeed.value(a); v != 9 { // (2+1) + 3×1 = 6、×1.5 = 9
		t.Errorf("修飾の計算 %v、期待 9", v)
	}
	if v := attrBlockRange.value(nil); v != 4.5 {
		t.Errorf("届く距離の既定値 %v", v)
	}
	if v := attrSubmergedMining.value(&packets.Attribute{Base: 100}); v != 20 {
		t.Errorf("範囲に収めない: %v", v)
	}
}

func TestAttackStrength(t *testing.T) {
	for _, tc := range []struct {
		ticks int
		speed float64
		want  float32
	}{
		{0, 4, 0.1},      // 素手: 5 ティックで全力。(0+0.5)/5
		{4, 4, 0.9},      // (4+0.5)/5
		{5, 4, 1},        // 全力
		{0, 1.6, 0.04},   // 剣: 12.5 ティック
		{12, 1.6, 1},     // (12+0.5)/12.5 = 1
		{11, 1.6, 0.92},  // 11.5/12.5
		{100, 1.6, 1},    // 1 で止まる
		{3, 0.8, 0.1400}, // 斧（0.8）: 25 ティック。3.5/25
	} {
		got := attackStrength(tc.ticks, tc.speed)
		if math.Abs(float64(got-tc.want)) > 1e-5 {
			t.Errorf("attackStrength(%d, %v) = %v、期待 %v", tc.ticks, tc.speed, got, tc.want)
		}
	}
}

func TestWaterHeight(t *testing.T) {
	air := blockState(t, "air")
	water := blockState(t, "water[level=0]")
	for _, tc := range []struct {
		s, above string
		h        float64
		ok       bool
	}{
		{"water[level=0]", "air", float64(float32(8) / 9), true},
		{"water[level=0]", "water[level=0]", 1, true},
		{"water[level=3]", "air", float64(float32(5) / 9), true},
		{"water[level=8]", "air", float64(float32(8) / 9), true},
		{"oak_stairs[waterlogged=true]", "air", float64(float32(8) / 9), true},
		{"kelp", "air", float64(float32(8) / 9), true},
		{"oak_stairs[waterlogged=false]", "air", 0, false},
		{"stone", "water[level=0]", 0, false},
	} {
		above := air
		if tc.above != "air" {
			above = water
		}
		h, ok := waterHeight(blockState(t, tc.s), above)
		if ok != tc.ok || math.Abs(h-tc.h) > 1e-9 {
			t.Errorf("waterHeight(%s, 上 %s) = %v %v、期待 %v %v", tc.s, tc.above, h, ok, tc.h, tc.ok)
		}
	}
}

func TestScreenTypes(t *testing.T) {
	for typ := range screenTypes {
		found := false
		for _, b := range data.Blocks() {
			if b.Type == typ {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("窓の開くブロックの種類 %q が公式データに無い", typ)
		}
	}
	for _, name := range []string{"chest", "barrel", "furnace", "crafting_table", "red_shulker_box", "damaged_anvil"} {
		b, _ := data.BlockByName(name)
		if !opensScreen(b) {
			t.Errorf("%s は窓が開くはず", name)
		}
	}
	for _, name := range []string{"oak_door", "lever", "red_bed", "stone"} {
		b, _ := data.BlockByName(name)
		if opensScreen(b) {
			t.Errorf("%s は窓が開かないはず", name)
		}
	}
}

// ---- 偽のサーバー ----

// actClient は偽のサーバーに繋ぎ、チャンク (0, 0)（y=-64〜-62 が石、-61 が土、(9, -61, 8) が樫の原木、
// (3, -60, 3) にチェスト）を送り、サバイバルにして、(8.5, -60, 8.5) に立たせた Client を返す。
func actClient(t *testing.T) (*fakeServer, *Client) {
	s, c := playClient(t)
	stone, dirt := stateOf(t, "stone"), stateOf(t, "dirt")
	logS := stateOf(t, "oak_log[axis=y]")
	chest := stateOf(t, "chest[facing=north,type=single,waterlogged=false]")
	air := stateOf(t, "air")
	s.sendChunks(testColumn{cx: 0, cz: 0, minY: -64, height: 384, block: func(x, y, z int) int32 {
		switch {
		case x == 9 && y == -61 && z == 8:
			return int32(logS)
		case x == 3 && y == -60 && z == 3:
			return int32(chest)
		case y <= -62:
			return int32(stone)
		case y == -61:
			return int32(dirt)
		}
		return int32(air)
	}})
	s.send(data.PlayClientboundGameEvent, func(e *protocol.Encoder) {
		packets.WriteGameEvent(e, packets.GameEvent{Event: packets.GameEventChangeGameMode, Value: 0})
	})
	s.teleport(packets.PlayerPosition{TeleportID: 1, X: 8.5, Y: -60, Z: 8.5}, 8.5, -60, 8.5, true)
	end := time.Now().Add(3 * time.Second)
	for !c.Self().OnGround || c.Self().GameMode != Survival {
		if time.Now().After(end) {
			t.Fatalf("地面に立たない: %+v", c.Self())
		}
		// 位置などを読み進める
		s.sync()
	}
	return s, c
}

// actPacket は偽のサーバーが受け取った行動のパケット 1 つ。
type actPacket struct {
	id    int32
	ticks int // それまでに数えた Client Tick End の数
	d     *protocol.Decoder
}

// readActs は、行動のパケット（tickPackets 以外）を n 個集める。Client Tick End を数え、
// 向きのパケットがあれば最後の向きを rot に入れる。
func (s *fakeServer) readActs(n int, rot *[2]float32) []actPacket {
	s.t.Helper()
	var out []actPacket
	ticks := 0
	for len(out) < n {
		p := s.read()
		switch p.ID {
		case data.PlayServerboundClientTickEnd:
			ticks++
		case data.PlayServerboundMovePlayerRot:
			d := protocol.NewDecoder(p.Data)
			y, _ := d.ReadFloat()
			x, _ := d.ReadFloat()
			if rot != nil {
				*rot = [2]float32{y, x}
			}
		case data.PlayServerboundMovePlayerPosRot:
			m, _ := packets.ReadMovePlayerPosRot(protocol.NewDecoder(p.Data))
			if rot != nil {
				*rot = [2]float32{m.Yaw, m.Pitch}
			}
		default:
			if tickPackets[p.ID] {
				continue
			}
			out = append(out, actPacket{id: p.ID, ticks: ticks, d: protocol.NewDecoder(p.Data)})
		}
	}
	return out
}

func (s *fakeServer) sendAck(seq int32) {
	s.send(data.PlayClientboundBlockChangedAck, func(e *protocol.Encoder) { e.WriteVarInt(seq) })
}

func (s *fakeServer) sendBlock(p BlockPos, id data.StateID) {
	s.send(data.PlayClientboundBlockUpdate, func(e *protocol.Encoder) {
		must(s.t, e.WritePosition(protocol.Position{X: p.X, Y: p.Y, Z: p.Z}))
		e.WriteVarInt(int32(id))
	})
}

func mustAction(t *testing.T, p actPacket, status int32, pos BlockPos, f Face) packets.PlayerAction {
	t.Helper()
	if p.id != data.PlayServerboundPlayerAction {
		t.Fatalf("Player Action を待っていたが 0x%02x", p.id)
	}
	a, err := packets.ReadPlayerAction(p.d)
	must(t, err)
	if a.Status != status || a.Pos != (protocol.Position{X: pos.X, Y: pos.Y, Z: pos.Z}) || (status != packets.ActionAbortDestroy && a.Face != int8(f)) {
		t.Fatalf("Player Action %+v、期待 status %d 位置 %v 面 %v", a, status, pos, f)
	}
	return a
}

func mustSwing(t *testing.T, p actPacket) {
	t.Helper()
	if p.id != data.PlayServerboundSwing {
		t.Fatalf("Swing Arm を待っていたが 0x%02x", p.id)
	}
	if h, err := p.d.ReadVarInt(); err != nil || h != packets.HandMain {
		t.Fatalf("Swing Arm の手 %d %v", h, err)
	}
}

// 素手で土を掘る: 掘り始め、毎ティック腕を振り、15 ティック目に掘り終わり。確認とブロックの変化で戻る。
func TestDigFromFakeServer(t *testing.T) {
	s, c := actClient(t)
	target := BlockPos{X: 8, Y: -61, Z: 8}
	if n, ok := c.DigTicks(target); !ok || n != 15 {
		t.Fatalf("DigTicks = %d %v、期待 15", n, ok)
	}
	errc := make(chan error, 1)
	go func() { errc <- c.Dig(context.Background(), target, Up) }()

	var rot [2]float32
	ps := s.readActs(2, &rot)
	start := mustAction(t, ps[0], packets.ActionStartDestroy, target, Up)
	mustSwing(t, ps[1])
	if rot[1] != 90 {
		t.Errorf("掘り始める前の向き %v、期待は真下（pitch 90）", rot)
	}
	if start.Sequence != 1 {
		t.Errorf("掘り始めのシーケンス番号 %d、期待 1", start.Sequence)
	}
	t0 := ps[0].ticks
	// 掘り始めのあと 14 回腕を振り、14 ティック目に掘り終わり（掘り始めのティックを含めて 15 ティック）
	ps = s.readActs(14, nil)
	for i := range 13 {
		mustSwing(t, ps[i])
	}
	mustSwing(t, ps[13])
	ps = s.readActs(1, nil)
	stop := mustAction(t, ps[0], packets.ActionStopDestroy, target, Up)
	if stop.Sequence != 2 {
		t.Errorf("掘り終わりのシーケンス番号 %d、期待 2", stop.Sequence)
	}
	// readActs の ticks は呼ぶたびに 0 から数えるので、間のティックは合計で見る
	_ = t0
	select {
	case err := <-errc:
		t.Fatalf("確認の前に戻った: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	s.sendBlock(target, stateOf(t, "air"))
	s.sendAck(2)
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Dig: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Dig が戻らない")
	}

	// サーバーが壊さなかった（確認だけ届く）なら ErrNotBroken
	logPos := BlockPos{X: 9, Y: -61, Z: 8}
	go func() { errc <- c.Dig(context.Background(), logPos, Up) }()
	var stopSeq int32
	for {
		ps := s.readActs(1, nil)
		if ps[0].id == data.PlayServerboundPlayerAction {
			a, _ := packets.ReadPlayerAction(ps[0].d)
			if a.Status == packets.ActionStopDestroy {
				stopSeq = a.Sequence
				break
			}
		}
	}
	s.sendAck(stopSeq)
	select {
	case err := <-errc:
		if !errors.Is(err, ErrNotBroken) {
			t.Fatalf("Dig = %v、期待 ErrNotBroken", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Dig が戻らない")
	}

	// ctx で止めると中止（面は下、番号 0）を送る
	ctx, cancel := context.WithCancel(context.Background())
	go func() { errc <- c.Dig(ctx, logPos, Up) }()
	ps = s.readActs(2, nil)
	mustAction(t, ps[0], packets.ActionStartDestroy, logPos, Up)
	cancel()
	for {
		ps := s.readActs(1, nil)
		if ps[0].id == data.PlayServerboundSwing {
			continue
		}
		a := mustAction(t, ps[0], packets.ActionAbortDestroy, logPos, Down)
		if a.Sequence != 0 || a.Face != int8(Down) {
			t.Errorf("中止 %+v", a)
		}
		break
	}
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Errorf("Dig = %v、期待 context.Canceled", err)
	}

	// 遠すぎる
	if err := c.Dig(context.Background(), BlockPos{X: 1, Y: -61, Z: 1}, Up); !errors.Is(err, ErrOutOfReach) {
		t.Errorf("遠いブロック: %v", err)
	}
	// 空気は掘れない
	if err := c.Dig(context.Background(), BlockPos{X: 8, Y: -59, Z: 8}, Up); !errors.Is(err, ErrNotDiggable) {
		t.Errorf("空気: %v", err)
	}
	// 属性で届く距離が 1 になると届かない
	rangeID := attributeIDs["block_interaction_range"]
	s.send(data.PlayClientboundUpdateAttributes, func(e *protocol.Encoder) {
		must(t, packets.WriteUpdateAttributes(e, packets.UpdateAttributes{EntityID: testLoginPlay.EntityID,
			Attributes: []packets.Attribute{{ID: rangeID, Base: 1}}}))
	})
	s.sync()
	if err := c.Dig(context.Background(), BlockPos{X: 8, Y: -61, Z: 9}, Up); !errors.Is(err, ErrOutOfReach) {
		t.Errorf("届く距離 1: %v", err)
	}
}

// 効果（採掘速度上昇・低下）と属性で掘る時間が変わる。
func TestDigEffectsFromFakeServer(t *testing.T) {
	s, c := actClient(t)
	target := BlockPos{X: 8, Y: -61, Z: 8}
	effect := func(name string, amp int32) {
		s.send(data.PlayClientboundUpdateMobEffect, func(e *protocol.Encoder) {
			packets.WriteMobEffect(e, packets.MobEffect{EntityID: testLoginPlay.EntityID, EffectID: mobEffectIDs[name], Amplifier: amp, Duration: 200})
		})
		s.sync()
	}
	remove := func(name string) {
		s.send(data.PlayClientboundRemoveMobEffect, func(e *protocol.Encoder) {
			e.WriteVarInt(testLoginPlay.EntityID)
			e.WriteVarInt(mobEffectIDs[name])
		})
		s.sync()
	}
	check := func(desc string, want int) {
		t.Helper()
		if n, ok := c.DigTicks(target); !ok || n != want {
			t.Errorf("%s: DigTicks = %d %v、期待 %d", desc, n, ok, want)
		}
	}
	check("素手", 15)
	effect("haste", 1) // ×1.4 → 1/0.5/30×1.4 = 0.0933 → 11 ティック
	check("採掘速度上昇 II", 11)
	remove("haste")
	effect("mining_fatigue", 0) // ×0.3 → 0.02。float32 で足すと 50 回では届かず 51
	check("採掘速度低下 I", 51)
	// 別のエンティティの効果は関係ない
	s.send(data.PlayClientboundRemoveMobEffect, func(e *protocol.Encoder) {
		e.WriteVarInt(99)
		e.WriteVarInt(mobEffectIDs["mining_fatigue"])
	})
	s.sync()
	check("ほかのエンティティの効果を消す", 51)
	remove("mining_fatigue")
	// block_break_speed ×2 → 8 ティック（1/15×2 を 8 回）
	s.send(data.PlayClientboundUpdateAttributes, func(e *protocol.Encoder) {
		must(t, packets.WriteUpdateAttributes(e, packets.UpdateAttributes{EntityID: testLoginPlay.EntityID,
			Attributes: []packets.Attribute{{ID: attributeIDs["block_break_speed"], Base: 2}}}))
	})
	s.sync()
	check("block_break_speed 2", 8)
}

// 置く: Yaw の向きを送ってから Use Item On（面、指す位置、シーケンス番号）と Swing Arm。
// 置かれた位置と状態を返す。容器を使うと Open Screen を待つ。
func TestPlaceFromFakeServer(t *testing.T) {
	s, c := actClient(t)
	against := BlockPos{X: 9, Y: -61, Z: 8} // 樫の原木（地面と同じ高さ）
	type res struct {
		p   Placed
		err error
	}
	rc := make(chan res, 1)
	yaw := 180.0
	cur := r3.Vec{X: 1, Y: 0.75, Z: 0.5}
	go func() {
		p, err := c.Place(context.Background(), against, East, PlaceOpts{Yaw: &yaw, Cursor: &cur})
		rc <- res{p, err}
	}()
	var rot [2]float32
	ps := s.readActs(2, &rot)
	if rot[0] != -180 {
		t.Errorf("置く前に送った向き %v、期待 yaw -180（180 を -180〜180 に直したもの）", rot)
	}
	if ps[0].id != data.PlayServerboundUseItemOn {
		t.Fatalf("Use Item On を待っていたが 0x%02x", ps[0].id)
	}
	u, err := packets.ReadUseItemOn(ps[0].d)
	must(t, err)
	want := packets.UseItemOn{Hand: packets.HandMain, Pos: protocol.Position{X: 9, Y: -61, Z: 8}, Face: int32(East),
		CursorX: 1, CursorY: 0.75, CursorZ: 0.5, Sequence: 1}
	if u != want {
		t.Errorf("Use Item On %+v、期待 %+v", u, want)
	}
	mustSwing(t, ps[1])
	stairs := stateOf(t, "oak_stairs[facing=north,half=top,shape=straight,waterlogged=false]")
	s.sendBlock(against, stateOf(t, "oak_log[axis=y]"))
	s.sendBlock(against.Neighbor(East), stairs)
	s.sendAck(1)
	r := <-rc
	if r.err != nil || r.p.Pos != against.Neighbor(East) || r.p.State.ID != stairs {
		t.Errorf("Place = %+v %v", r.p, r.err)
	}

	// 置けなかった（確認だけ）なら ErrNotPlaced
	go func() {
		p, err := c.Place(context.Background(), against, Up, PlaceOpts{})
		rc <- res{p, err}
	}()
	ps = s.readActs(2, nil)
	u, err = packets.ReadUseItemOn(ps[0].d)
	must(t, err)
	if u.Face != int32(Up) || u.CursorX != 0.5 || u.CursorY != 1 || u.CursorZ != 0.5 || u.Sequence != 2 {
		t.Errorf("Use Item On（上の面の真ん中）%+v", u)
	}
	s.sendAck(2)
	if r := <-rc; !errors.Is(r.err, ErrNotPlaced) {
		t.Errorf("Place = %v、期待 ErrNotPlaced", r.err)
	}

	// Cursor がブロックの外
	bad := r3.Vec{X: 1.5}
	if _, err := c.Place(context.Background(), against, Up, PlaceOpts{Cursor: &bad}); err == nil {
		t.Error("ブロックの外の Cursor でエラーにならない")
	}

	// チェストを使う: Open Screen が届くまで待つ
	chest := BlockPos{X: 3, Y: -60, Z: 3}
	// 遠すぎる（(8.5, -58.38, 8.5) から 5 以上）
	if err := c.UseBlock(context.Background(), chest, Up); !errors.Is(err, ErrOutOfReach) {
		t.Fatalf("遠いチェスト: %v", err)
	}
	s.teleport(packets.PlayerPosition{TeleportID: 2, X: 4.5, Y: -60, Z: 4.5}, 4.5, -60, 4.5, false)
	ec := make(chan error, 1)
	go func() { ec <- c.UseBlock(context.Background(), chest, Up) }()
	ps = s.readActs(2, nil)
	u, err = packets.ReadUseItemOn(ps[0].d)
	must(t, err)
	if u.Pos != (protocol.Position{X: 3, Y: -60, Z: 3}) || u.Sequence != 3 {
		t.Errorf("Use Item On（チェスト）%+v", u)
	}
	s.send(data.PlayClientboundOpenScreen, func(e *protocol.Encoder) {
		must(t, packets.WriteOpenScreen(e, packets.OpenScreen{WindowID: 4, Type: 2, Title: "Chest"}))
	})
	s.sendAck(3)
	if err := <-ec; err != nil {
		t.Fatalf("UseBlock: %v", err)
	}
	if w, ok := c.Container(); !ok || w.ID != 4 || w.Type != "generic_9x3" {
		t.Errorf("Container %+v %v", w, ok)
	}

	// 窓が届かなければ ErrNoScreen
	go func() { ec <- c.UseBlock(context.Background(), chest, Up) }()
	s.readActs(2, nil)
	s.sendAck(4)
	if err := <-ec; !errors.Is(err, ErrNoScreen) {
		t.Errorf("UseBlock = %v、期待 ErrNoScreen", err)
	}
}

// アイテムを使う・離す、攻撃と溜め。
func TestUseItemAndAttackFromFakeServer(t *testing.T) {
	s, c := actClient(t)
	c.Look(30, -10)
	must(t, c.StartUseItem())
	ps := s.readActs(1, nil)
	if ps[0].id != data.PlayServerboundUseItem {
		t.Fatalf("Use Item を待っていたが 0x%02x", ps[0].id)
	}
	u, err := packets.ReadUseItem(ps[0].d)
	must(t, err)
	if u != (packets.UseItem{Hand: packets.HandMain, Sequence: 1, Yaw: 30, Pitch: -10}) {
		t.Errorf("Use Item %+v", u)
	}
	must(t, c.StopUseItem())
	ps = s.readActs(1, nil)
	mustAction(t, ps[0], packets.ActionReleaseUseItem, BlockPos{}, Down)

	// ゾンビ（足元 (10.5, -60, 8.5)。目から箱まで約 1.7）
	zombie, _ := data.EntityTypeByName("zombie")
	s.send(data.PlayClientboundAddEntity, func(e *protocol.Encoder) {
		packets.WriteSpawnEntity(e, packets.SpawnEntity{EntityID: 50, UUID: protocol.UUID{5}, Type: int32(zombie.ID), X: 10.5, Y: -60, Z: 8.5})
	})
	s.send(data.PlayClientboundAddEntity, func(e *protocol.Encoder) {
		packets.WriteSpawnEntity(e, packets.SpawnEntity{EntityID: 51, UUID: protocol.UUID{6}, Type: int32(zombie.ID), X: 20.5, Y: -60, Z: 8.5})
	})
	s.sync()
	// 素手（攻撃速度 4）: 5 ティックで全力
	time.Sleep(400 * time.Millisecond)
	if ch := c.AttackCharge(); ch != 1 {
		t.Errorf("待ったあとの溜め %v、期待 1", ch)
	}
	ec := make(chan error, 1)
	go func() { ec <- c.Attack(context.Background(), 50) }()
	var rot [2]float32
	ps = s.readActs(2, &rot)
	if ps[0].id != data.PlayServerboundInteract {
		t.Fatalf("Interact を待っていたが 0x%02x", ps[0].id)
	}
	in, err := packets.ReadInteract(ps[0].d)
	must(t, err)
	if in != (packets.Interact{EntityID: 50, Type: packets.InteractAttack}) {
		t.Errorf("Interact %+v", in)
	}
	mustSwing(t, ps[1])
	if math.Abs(float64(rot[0])+90) > 1e-3 {
		t.Errorf("攻撃する前の向き %v、期待は東（yaw -90）", rot)
	}
	must(t, <-ec)
	if ch := c.AttackCharge(); ch > 0.5 {
		t.Errorf("攻撃した直後の溜め %v", ch)
	}
	if err := c.Attack(context.Background(), 51); !errors.Is(err, ErrOutOfReach) {
		t.Errorf("遠いゾンビ: %v", err)
	}
	if err := c.Attack(context.Background(), 999); !errors.Is(err, ErrNoEntity) {
		t.Errorf("知らないエンティティ: %v", err)
	}

	// 剣（攻撃速度 1.6 = 12.5 ティック）を持つと溜めが遅くなる。持ち替えでも溜め直し
	sword := packets.Slot{Count: 1, Item: invItem(t, "iron_sword").ID}
	startInventory(t, s, 1, map[int]packets.Slot{SlotHotbar + 1: sword})
	s.send(data.PlayClientboundUpdateAttributes, func(e *protocol.Encoder) {
		must(t, packets.WriteUpdateAttributes(e, packets.UpdateAttributes{EntityID: testLoginPlay.EntityID,
			Attributes: []packets.Attribute{{ID: attributeIDs["attack_speed"], Base: 4, Modifiers: []packets.AttributeModifier{
				{ID: "minecraft:base_attack_speed", Amount: -2.4, Operation: packets.ModifierAddValue}}}}}))
	})
	s.sync()
	time.Sleep(300 * time.Millisecond)
	must(t, c.SelectHotbar(1))
	time.Sleep(120 * time.Millisecond) // 次のティックで持ち替えに気づく
	if ch := c.AttackCharge(); ch > 0.3 {
		t.Errorf("持ち替えた直後の溜め %v（剣は 12.5 ティックで全力）", ch)
	}
	time.Sleep(250 * time.Millisecond)
	if ch := c.AttackCharge(); ch >= 1 || ch < 0.4 {
		t.Errorf("0.37 秒あとの溜め %v、期待は 0.4〜1 未満", ch)
	}
	time.Sleep(500 * time.Millisecond)
	if ch := c.AttackCharge(); ch != 1 {
		t.Errorf("0.87 秒あとの溜め %v、期待 1", ch)
	}
}

// Attack は位置を受け取る前（毎ティックの処理が止まっているあいだ）は送らずに待ち、ctx が終わったら ctx のエラーで戻る。
func TestAttackWaitsForCtx(t *testing.T) {
	s, c := playClient(t)
	zombie, _ := data.EntityTypeByName("zombie")
	s.send(data.PlayClientboundAddEntity, func(e *protocol.Encoder) {
		packets.WriteSpawnEntity(e, packets.SpawnEntity{EntityID: 50, UUID: protocol.UUID{5}, Type: int32(zombie.ID), X: 0.5, Y: 0, Z: 1.5})
	})
	s.sync()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := c.Attack(ctx, 50)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("位置を受け取る前の Attack = %v、期待 context.DeadlineExceeded", err)
	}
	if d := time.Since(start); d < 150*time.Millisecond || d > 2*time.Second {
		t.Errorf("Attack が戻るまで %v（ctx の 200ms で戻るはず）", d)
	}
	// 待つのをやめた攻撃は、あとで位置が届いても送らない
	// （Interact が来たら expect が失敗する）
	s.teleport(packets.PlayerPosition{TeleportID: 1, X: 0.5, Y: 0, Z: 0.5}, 0.5, 0, 0.5, false)
	time.Sleep(200 * time.Millisecond)
	skip := maps.Clone(tickPackets)
	skip[data.PlayServerboundPlayerLoaded] = true
	s.send(data.PlayClientboundPing, func(e *protocol.Encoder) { e.WriteInt(1) })
	s.expect(data.PlayServerboundPong, skip)
}

func TestWrapDegrees(t *testing.T) {
	for _, tc := range [][2]float32{{0, 0}, {180, -180}, {-180, -180}, {-225, 135}, {270, -90}, {540, -180}, {-90, -90}, {179.5, 179.5}} {
		if got := wrapDegrees(tc[0]); got != tc[1] {
			t.Errorf("wrapDegrees(%v) = %v、期待 %v", tc[0], got, tc[1])
		}
	}
}

// startsUsing: 本家のクライアントが Use Item で使い始める物か。
func TestStartsUsing(t *testing.T) {
	item := func(name string) *data.Item {
		it, ok := data.ItemByName(name)
		if !ok {
			t.Fatalf("アイテム %s が無い", name)
		}
		return it
	}
	hungry := Self{Food: 10}
	full := Self{Food: 20}
	creativeFull := Self{Food: 20, GameMode: Creative}
	for _, tc := range []struct {
		held     *data.Item
		self     Self
		hasArrow bool
		want     bool
	}{
		{nil, hungry, false, false},
		{item("stone"), hungry, false, false},
		{item("apple"), hungry, false, true},
		{item("apple"), full, false, false},        // 満腹では食べられない
		{item("apple"), creativeFull, false, true}, // クリエイティブは満腹でも
		{item("golden_apple"), full, false, true},  // いつでも食べられる
		{item("potion"), full, false, true},        // 飲み物
		{item("milk_bucket"), full, false, true},
		{item("bow"), hungry, false, false}, // 矢が無い
		{item("bow"), hungry, true, true},
		{item("bow"), Self{GameMode: Creative}, false, true},
		{item("shield"), hungry, false, true},
		{item("spyglass"), hungry, false, true},
		{item("crossbow"), hungry, true, false}, // 弾を込めたかが分からないので、サーバーの印に任せる
	} {
		if got := startsUsing(tc.held, tc.self, tc.hasArrow); got != tc.want {
			name := "素手"
			if tc.held != nil {
				name = tc.held.Name
			}
			t.Errorf("startsUsing(%s, 満腹度 %d, モード %v, 矢 %v) = %v、期待 %v", name, tc.self.Food, tc.self.GameMode, tc.hasArrow, got, tc.want)
		}
	}
}

// アイテムを使っているあいだは物理の Input.UsingItem が立ち、歩く速さが 0.2 倍になる。
// 使い始め: StartUseItem（使える物のとき）とサーバーの印。やめ: サーバーの印が消える、StopUseItem、
// 手に持つ物が変わる、サーバーの印が useConfirmTicks のあいだ届かない。
func TestUseItemSlowsMovement(t *testing.T) {
	s, c := actClient(t)
	startInventory(t, s, 1, map[int]packets.Slot{
		SlotHotbar:     plainSlot(t, "apple", 5),
		SlotHotbar + 1: plainSlot(t, "stone", 5),
	})
	// このテストで送るパケット（Use Item など）は読み飛ばして、Pong を待つ
	skip := map[int32]bool{data.PlayServerboundUseItem: true, data.PlayServerboundPlayerAction: true, data.PlayServerboundSetCarriedItem: true}
	for id := range tickPackets {
		skip[id] = true
	}
	sync := func() {
		t.Helper()
		s.send(data.PlayClientboundPing, func(e *protocol.Encoder) { e.WriteInt(1) })
		s.expect(data.PlayServerboundPong, skip)
	}
	using := func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.usingItemLocked()
	}
	// 自分の LivingEntity の印（番号 8、Byte）を送る
	sendUsing := func(on bool) {
		var v uint8
		if on {
			v = 0x01
		}
		s.send(data.PlayClientboundSetEntityData, func(e *protocol.Encoder) {
			e.WriteVarInt(testLoginPlay.EntityID)
			e.WriteUByte(metaIndexLivingFlags)
			e.WriteVarInt(packets.MetaByte)
			e.WriteUByte(v)
			packets.WriteMetadataEnd(e)
		})
		sync()
	}
	// 東へ歩いて、0.6 秒後の横の速さ（ブロック/ティック）を測る
	walk := func() float64 {
		c.Look(-90, 0)
		c.SetControls(Controls{Forward: true})
		time.Sleep(600 * time.Millisecond)
		sync()
		v := c.Self().Vel
		c.SetControls(Controls{})
		time.Sleep(300 * time.Millisecond)
		sync()
		return math.Hypot(v.X, v.Z)
	}

	// 満腹度が届いていない（20 とみなさない。Set Health の前は 0）のでリンゴは食べられる → 使い始める
	must(t, c.StartUseItem())
	if !using() {
		t.Fatal("StartUseItem のあと使っていない")
	}
	sendUsing(true)
	slow := walk()
	if !using() {
		t.Fatal("サーバーの印があるのに、歩いているあいだにやめた")
	}
	sendUsing(false) // 食べ終わり
	if using() {
		t.Fatal("サーバーの印が消えたのに使っている")
	}
	normal := walk()
	t.Logf("使っているとき %.4f、いないとき %.4f（ブロック/ティック）", slow, normal)
	// Vel は摩擦をかけたあとの値（歩いて約 0.118）
	if normal < 0.1 || math.Abs(slow/normal-0.2) > 0.03 {
		t.Errorf("使っているときの速さ %.4f が、いないとき %.4f の 0.2 倍でない", slow, normal)
	}

	// StopUseItem でやめる
	must(t, c.StartUseItem())
	sendUsing(true)
	must(t, c.StopUseItem())
	if using() {
		t.Error("StopUseItem のあと使っている")
	}
	sendUsing(false)

	// サーバーの印が届かなければ（サーバーが使わせなかった）、useConfirmTicks のあとやめる
	must(t, c.StartUseItem())
	time.Sleep(time.Duration(useConfirmTicks+6) * TickInterval)
	if using() {
		t.Error("サーバーの印が届かないのに使い続けている")
	}

	// 使えない物（石）では使い始めない
	must(t, c.SelectHotbar(1))
	sync()
	time.Sleep(3 * TickInterval)
	must(t, c.StartUseItem())
	if using() {
		t.Error("石で使い始めた")
	}

	// サーバーの印だけで使い始め、手に持つ物が変わったらやめる
	must(t, c.SelectHotbar(0))
	time.Sleep(3 * TickInterval)
	sendUsing(true)
	if !using() {
		t.Fatal("サーバーの印で使い始めない")
	}
	must(t, c.SelectHotbar(1))
	time.Sleep(3 * TickInterval)
	if using() {
		t.Error("手に持つ物が変わったのに使っている")
	}
}

// 行動の目の位置（届く距離と向き）は姿勢の目の高さを使うこと（しゃがむと 1.27、泳ぐと 0.4）。
func TestEyeFollowsPose(t *testing.T) {
	cl := &Client{}
	cl.initFeatures()
	cl.y = 10
	for _, tc := range []struct {
		pose Pose
		want float64
	}{{PoseStanding, 1.62}, {PoseCrouching, 1.27}, {PoseSwimming, 0.4}} {
		moveKey.of(cl).p.Pose = tc.pose
		if got := cl.eyeLocked().Y - 10; math.Abs(got-tc.want) > 1e-6 {
			t.Errorf("姿勢 %v の目の高さ %v、期待 %v", tc.pose, got, tc.want)
		}
	}
}
