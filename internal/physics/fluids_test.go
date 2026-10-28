package physics

import (
	"math"
	"strconv"
	"testing"

	"gonum.org/v1/gonum/spatial/r3"
)

// 単位 7b（水の流れ、泳ぎ、しゃがみ、満腹度、クモの巣など）の部品ごとのテスト。

func newPlayer(x, y, z float64) *Player {
	return &Player{Food: 20, Pos: r3.Vec{X: x, Y: y, Z: z}}
}

// streamWorld は y=0 が石の床、y=1 に西（x=0）の源から東へ流れる水（x=0 が源、x=k が level k、x=1〜7）を置き、
// 南北（z=-1 と z=1）を石の壁で挟んだ地形。
func streamWorld(t *testing.T) testWorld {
	w := floorWorld(30, "stone")
	for x := 0; x <= 7; x++ {
		w[BlockPos{x, 1, 0}] = mustState(t, "water", map[string]string{"level": strconv.Itoa(x)})
		w[BlockPos{x, 1, -1}] = mustState(t, "stone", nil)
		w[BlockPos{x, 1, 1}] = mustState(t, "stone", nil)
		w[BlockPos{x, 2, -1}] = mustState(t, "stone", nil)
		w[BlockPos{x, 2, 1}] = mustState(t, "stone", nil)
	}
	return w
}

// 流れの向き（FlowingFluid.getFlow）: 高い（量の多い）ほうから低いほうへ、長さ 1。
func TestFlowDirection(t *testing.T) {
	w := streamWorld(t)
	for x := 0; x <= 6; x++ {
		s, _ := w.Block(x, 1, 0)
		f := flow(w, x, 1, 0, infoOf(s))
		// 源（x=0）: 東は 7/9、西は空気で下は石（液体なし、高さ 0）なので西には流れない。8/9-7/9 = 1/9 で東へ
		if !near(f.X, 1, 1e-9) || f.Y != 0 || f.Z != 0 {
			t.Errorf("x=%d の流れ %v、東 (1, 0, 0) のはず", x, f)
		}
	}
	// 流れの先が落ちる（隣が空気で、その下に水）ときも、その向き
	w2 := floorWorld(10, "stone")
	w2[BlockPos{0, 2, 0}] = mustState(t, "water", map[string]string{"level": "0"})
	w2[BlockPos{1, 2, 0}] = mustState(t, "stone", nil) // 床の代わり（x=0 の水の下は空気）
	delete(w2, BlockPos{0, 0, 0})
	w2[BlockPos{0, 1, 0}] = mustState(t, "air", nil)
	w2[BlockPos{-1, 1, 0}] = mustState(t, "water", map[string]string{"level": "1"})
	s, _ := w2.Block(0, 2, 0)
	f := flow(w2, 0, 2, 0, infoOf(s))
	// 西の隣（-1, 2, 0）は空気で、その下（-1, 1, 0）に量 7 の水: 8/9 - (7/9 - 0.8888889) > 0 なので西へ
	if f.X >= 0 {
		t.Errorf("滝に向かう流れ %v、西（x が負）のはず", f)
	}
}

// 落ちてくる水（level 8 以上）は、横に壁があると強く下を向く。
func TestFlowFallingNearWall(t *testing.T) {
	w := floorWorld(10, "stone")
	w[BlockPos{0, 3, 0}] = mustState(t, "water", map[string]string{"level": "8"})
	w[BlockPos{1, 3, 0}] = mustState(t, "stone", nil)
	s, _ := w.Block(0, 3, 0)
	f := flow(w, 0, 3, 0, infoOf(s))
	if !near(f.Y, -1, 1e-9) {
		t.Errorf("壁の横の落ちてくる水の流れ %v、真下のはず", f)
	}
	// 壁が無ければ下は向かない（横の高さの差も無い）
	w2 := floorWorld(10, "stone")
	w2[BlockPos{0, 3, 0}] = s
	if f := flow(w2, 0, 3, 0, infoOf(s)); f != (r3.Vec{}) {
		t.Errorf("周りに何も無い落ちてくる水の流れ %v、0 のはず", f)
	}
}

// 流れる水の上に立つと、流れの向き（東）に押される。押される速さは本家の式どおり。
func TestPushedByFlow(t *testing.T) {
	w := streamWorld(t)
	p := newPlayer(2.5, 1, 0.5)
	// 1 ティック目: x=2 の水（量 6、上は空気）の高さ 6/9。箱の下 1.001 から 0.6657 で 0.4 以上なので、流れ (1,0,0) をそのまま足す。
	// 速さが小さく、押す力 0.014 は 0.0045 以上なので、そのまま 0.014
	p.Tick(w, Input{})
	if !p.InWater {
		t.Fatal("InWater が偽")
	}
	t.Logf("1 ティック目の速さ %v 位置 %v", p.Vel, p.Pos)
	start := p.Pos.X
	for range 40 {
		p.Tick(w, Input{})
	}
	if p.Pos.X <= start+0.5 {
		t.Errorf("流れに押されない: x %v → %v", start, p.Pos.X)
	}
	if p.Pos.Z != 0.5 {
		t.Errorf("横（z）に動いた: %v", p.Pos)
	}
	t.Logf("2 秒で x %.3f → %.3f", start, p.Pos.X)
}

// 1 ティック目の押す力を、本家の式で手計算した値と比べる。
func TestPushFirstTickExact(t *testing.T) {
	w := streamWorld(t)
	p := newPlayer(2.5, 1, 0.5)
	p.updateFluids(w)
	// 流れ (1,0,0)、深さ 6/9 ≥ 0.4、1 ブロックだけ → 0.014
	if !near(p.Vel.X, 0.014, 1e-12) || p.Vel.Y != 0 || p.Vel.Z != 0 {
		t.Errorf("押す力 %v、(0.014, 0, 0) のはず", p.Vel)
	}
	if want := float64(float32(1)+float32(6)/9) - 1.001; !near(p.WaterHeight, want, 1e-12) {
		t.Errorf("深さ %v、%v のはず", p.WaterHeight, want)
	}
	// 浅い水（level 7、高さ 1/9 < 0.4）では、流れに深さを掛け、0.0045 より小さければ 0.0045 にそろえる
	p2 := newPlayer(7.5, 1, 0.5)
	p2.updateFluids(w)
	if !near(p2.Vel.X, 0.0045000000000000005, 1e-12) {
		t.Errorf("浅い水の押す力 %v、0.0045 のはず", p2.Vel)
	}
}

// deepPool は y=0 が床、y=1〜4 が水の源（深さ 4、一番上の水のブロックの上面は y=5、水面はその 8/9 の y=4.889）。
// x が 6 から先は岸。high なら岸は y=1〜5 の石（上に立つ高さ y=6。水のブロックより 1 段高い）、
// そうでなければ y=1〜4 の石（上に立つ高さ y=5。水のブロックの上面と同じ）。
func deepPool(t *testing.T, high bool) testWorld {
	w := floorWorld(30, "stone")
	ws := mustState(t, "water", nil)
	st := mustState(t, "stone", nil)
	top := 4
	if high {
		top = 5
	}
	for x := -6; x <= 12; x++ {
		for z := -6; z <= 6; z++ {
			for y := 1; y <= 5; y++ {
				switch {
				case x >= 6:
					if y <= top {
						w[BlockPos{x, y, z}] = st
					}
				case y <= 4:
					w[BlockPos{x, y, z}] = ws
				}
			}
		}
	}
	return w
}

// climbOut は深い池の水面に浮いてから、ジャンプを押し続けて東の岸へ向かう。岸に上がったティック数（上がれなければ -1）と、
// いちばん高く上がった足の高さを返す。
func climbOut(w World, in Input, standY float64) (int, float64) {
	p := newPlayer(0.5, 3, 0.5)
	p.Yaw = -90 // 東
	for range 60 {
		p.Tick(w, Input{Jump: true})
	}
	in.Jump, in.Forward = true, true
	top := 0.0
	for n := range 400 {
		p.Tick(w, in)
		top = max(top, p.Pos.Y)
		if p.Pos.X > 6.5 && p.OnGround && p.Pos.Y == standY {
			return n, top
		}
	}
	return -1, top
}

// 深い池の水面から、ジャンプを押し続けて岸へ向かうと、岸（水のブロックの上面と同じ高さ）に上がれる。
// 壁にぶつかると上向きに 0.3 の速さを得る（LivingEntity.travelInFluid の最後）ので上がれる。
func TestClimbOutOfDeepPool(t *testing.T) {
	n, _ := climbOut(deepPool(t, false), Input{}, 5)
	if n < 0 {
		t.Fatal("岸に上がれない")
	}
	t.Logf("%d ティックで岸に上がった", n)
}

// 岸が水のブロックより 1 段高い（水面から 1.11 上）と、本家の式ではジャンプでも泳いでも上がれない。
// 壁で得る 0.3 の速さは、水に触れている最後のティック（足が 4.888 以下）からしか得られず、
// そこから上がれる高さは 0.34 + 0.3 + 0.2156 + 0.1329 + 0.0518 で、足は 5.93 までしか届かない（6 が要る）。
func TestCannotClimbOneBlockAboveWater(t *testing.T) {
	w := deepPool(t, true)
	for _, in := range []Input{{}, {Sprint: true}} {
		n, top := climbOut(w, in, 6)
		t.Logf("走る %v: 一番高い足の高さ %.4f", in.Sprint, top)
		if n >= 0 || top >= 6 || top < 5.9 {
			t.Errorf("走る %v: %d ティックで上がった（一番高い足 %.4f）。本家の式では 5.93 までしか届かないはず", in.Sprint, n, top)
		}
	}
}

// 水の中で目まで浸かって走ると泳ぐ。姿勢は泳ぎ（高さ 0.6）になり、泳ぐ速さは水の中を歩くより速い。
func TestSwim(t *testing.T) {
	w := deepPool(t, false)
	p := newPlayer(-5.5, 1, 0.5)
	p.Yaw = -90
	for range 5 {
		p.Tick(w, Input{})
	}
	if !p.EyeInWater {
		t.Fatal("目が水の中にない")
	}
	in := Input{Forward: true, Sprint: true}
	for range 3 {
		p.Tick(w, in)
	}
	if !p.Sprinting || !p.Swimming || p.Pose != PoseSwimming {
		t.Fatalf("泳がない: sprint=%v swim=%v pose=%v", p.Sprinting, p.Swimming, p.Pose)
	}
	if h := p.Box().Max.Y - p.Box().Min.Y; !near(h, 0.6, 1e-6) {
		t.Errorf("泳ぐ姿勢の当たり判定の高さ %v", h)
	}
	for range 20 {
		p.Tick(w, in)
	}
	x0 := p.Pos.X
	for range 20 {
		p.Tick(w, in)
	}
	swim := p.Pos.X - x0
	// 1 ティックに動く量は (v + 0.02*0.98) で、移動のあと 0.9 倍になるので、上限は 0.0196 / (1 - 0.9) = 0.196 ブロック/ティック
	// = 3.92 m/s。まだ加速の途中なので、それより少し遅い
	t.Logf("泳ぐ速さ %.3f m/s、姿勢 %v、y=%.3f", swim, p.Pose, p.Pos.Y)
	if swim < 3.6 || swim > 3.92 {
		t.Errorf("泳ぐ速さ %.3f、本家は上限 3.92 に近づくはず", swim)
	}
	// 走るのをやめると泳ぎもやめ、立つ姿勢に戻る
	for range 3 {
		p.Tick(w, Input{Forward: true})
	}
	if p.Swimming || p.Sprinting || p.Pose != PoseStanding {
		t.Errorf("泳ぎをやめない: swim=%v sprint=%v pose=%v", p.Swimming, p.Sprinting, p.Pose)
	}
}

// 水面（目が水の外）では走れない（泳ぎ始めない）。
func TestNoSprintAtSurface(t *testing.T) {
	w := floorWorld(30, "stone")
	ws := mustState(t, "water", nil)
	for x := -10; x <= 10; x++ {
		for z := -3; z <= 3; z++ {
			w[BlockPos{x, 1, z}] = ws
		}
	}
	p := newPlayer(-5.5, 1, 0.5)
	p.Yaw = -90
	for range 20 {
		p.Tick(w, Input{Forward: true, Sprint: true})
		if p.Sprinting {
			t.Fatalf("浅い水の中で走った: %v", p.Pos)
		}
	}
}

// 流れる水の中を泳いで、流れに逆らって進める。
func TestSwimUpstream(t *testing.T) {
	w := floorWorld(40, "stone")
	// 深さ 3 の水路を東向きに流す: y=1〜2 は源、y=3 は東へ流れる（x=-7 が源）。流れを強くするため水面の層だけ流れにする
	for x := -7; x <= 7; x++ {
		lv := x + 7
		for z := -3; z <= 3; z++ {
			for y := 1; y <= 3; y++ {
				l := 0
				if y == 3 {
					l = min(lv, 7)
				}
				w[BlockPos{x, y, z}] = mustState(t, "water", map[string]string{"level": strconv.Itoa(l)})
			}
		}
	}
	p := newPlayer(5.5, 1, 0.5)
	p.Yaw = 90 // 西（上流）
	for range 5 {
		p.Tick(w, Input{})
	}
	x0 := p.Pos.X
	for range 60 {
		p.Tick(w, Input{Forward: true, Sprint: true})
	}
	t.Logf("3 秒で x %.3f → %.3f（泳ぎ %v）", x0, p.Pos.X, p.Swimming)
	if p.Pos.X > x0-3 {
		t.Errorf("上流へ進めない: x %.3f → %.3f", x0, p.Pos.X)
	}
}

// しゃがむと遅い（0.3 倍）。しゃがみは次のティックから効く（本家は前のティックのキーで決める）。
func TestSneakSpeed(t *testing.T) {
	sneak := steadySpeed(t, "stone", Input{Forward: true, Sneak: true}, -90)
	t.Logf("しゃがみ歩き %.4f m/s", sneak)
	// 本家の Wiki: 1.295 m/s（4.317 × 0.3）
	if !near(sneak, 4.317*0.3, 0.005) {
		t.Errorf("しゃがみ歩き %.4f、本家は約 1.295", sneak)
	}
	// しゃがみと走るは両立しない
	both := steadySpeed(t, "stone", Input{Forward: true, Sneak: true, Sprint: true}, -90)
	if !near(both, sneak, 1e-9) {
		t.Errorf("しゃがんで走った速さ %.4f", both)
	}
	w := floorWorld(5, "stone")
	p := newPlayer(0.5, 1, 0.5)
	p.Tick(w, Input{})
	p.Tick(w, Input{Sneak: true})
	if p.Crouching || p.Pose != PoseCrouching || !p.Sneaking {
		t.Errorf("1 ティック目: crouching=%v pose=%v sneaking=%v（姿勢だけ変わるはず）", p.Crouching, p.Pose, p.Sneaking)
	}
	p.Tick(w, Input{Sneak: true})
	if !p.Crouching || !near(p.Box().Max.Y-p.Box().Min.Y, 1.5, 1e-6) || p.EyeHeight() != 1.27 {
		t.Errorf("2 ティック目: crouching=%v 高さ %v", p.Crouching, p.Box().Max.Y-p.Box().Min.Y)
	}
}

// しゃがんでいれば、台の端から落ちない（どの向きに歩いても）。
func TestSneakEdge(t *testing.T) {
	w := testWorld{}
	st := mustState(t, "stone", nil)
	for x := -2; x <= 2; x++ {
		for z := -2; z <= 2; z++ {
			w[BlockPos{x, 4, z}] = st
		}
	}
	for _, yaw := range []float32{-90, 0, 90, 180, -45, 30} {
		p := newPlayer(0.5, 5, 0.5)
		p.Tick(w, Input{})
		p.Tick(w, Input{})
		p.Yaw = yaw
		for range 100 {
			p.Tick(w, Input{Forward: true, Sneak: true})
		}
		// 当たり判定（幅 0.6）がまだ台に 0 より多く掛かっている（中心が端から 0.3 未満の外）
		if p.Pos.Y != 5 || math.Abs(p.Pos.X-0.5) > 2.5+HalfWidth || math.Abs(p.Pos.Z-0.5) > 2.5+HalfWidth {
			t.Errorf("yaw %v: 端から落ちた: %v", yaw, p.Pos)
		}
		// しゃがみをやめて歩けば落ちる
		for range 40 {
			p.Tick(w, Input{Forward: true})
		}
		if p.Pos.Y >= 5 {
			t.Errorf("yaw %v: しゃがみをやめても落ちない: %v", yaw, p.Pos)
		}
	}
}

// 高さ 1.5 の隙間ではしゃがんだまま（キーを離しても立てない）で、這う（泳ぎの姿勢）には 0.6 の隙間。
func TestPoseInLowGap(t *testing.T) {
	w := floorWorld(10, "stone")
	// y=1 から 1.5 の高さに天井（y=2 の上半分のハーフブロック）
	for x := 2; x <= 6; x++ {
		for z := -2; z <= 2; z++ {
			w[BlockPos{x, 2, z}] = mustState(t, "stone_slab", map[string]string{"type": "top"})
		}
	}
	p := newPlayer(0.5, 1, 0.5)
	p.Yaw = -90
	for range 80 {
		p.Tick(w, Input{Forward: true, Sneak: true})
	}
	if p.Pos.X < 3 {
		t.Fatalf("しゃがんで隙間に入れない: %v", p.Pos)
	}
	for range 3 {
		p.Tick(w, Input{})
	}
	if p.Pose != PoseCrouching || !p.Crouching {
		t.Errorf("隙間の中で立った: pose=%v crouching=%v", p.Pose, p.Crouching)
	}
}

// 満腹度が 6 以下なら走り始められない。走っている途中で 6 以下になればやめる。
func TestSprintNeedsFood(t *testing.T) {
	w := floorWorld(150, "stone")
	p := newPlayer(-100.5, 1, 0.5)
	p.Yaw = -90
	p.Food = 6
	p.Tick(w, Input{})
	for range 20 {
		p.Tick(w, Input{Forward: true, Sprint: true})
		if p.Sprinting {
			t.Fatal("満腹度 6 で走った")
		}
	}
	p.Food = 7
	p.Tick(w, Input{Forward: true, Sprint: true})
	if !p.Sprinting {
		t.Fatal("満腹度 7 で走らない")
	}
	p.Food = 6
	p.Tick(w, Input{Forward: true, Sprint: true})
	if p.Sprinting {
		t.Error("満腹度が 6 になっても走り続けた")
	}
	p.MayFly = true
	p.Tick(w, Input{Forward: true, Sprint: true})
	if !p.Sprinting {
		t.Error("飛べるのに満腹度 6 で走らない")
	}
	// アイテムを使っている間は遅く、走らない
	use := steadySpeed(t, "stone", Input{Forward: true, Sprint: true, UsingItem: true}, -90)
	if !near(use, 4.317*0.2, 0.005) {
		t.Errorf("アイテムを使いながらの速さ %.4f", use)
	}
}

// slowWorld は床の上の x=2〜20、y=1〜2 に name のブロックを並べた地形。
func slowWorld(t *testing.T, name string, props map[string]string) testWorld {
	w := floorWorld(40, "stone")
	s := mustState(t, name, props)
	for x := 2; x <= 20; x++ {
		for z := -2; z <= 2; z++ {
			w[BlockPos{x, 1, z}] = s
			if name == "cobweb" || name == "powder_snow" {
				w[BlockPos{x, 2, z}] = s
			}
		}
	}
	return w
}

// クモの巣・甘い実の茂みの中は遅い（makeStuckInBlock）。
func TestStuckInBlocks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]string
		max   float64 // 1 秒あたりの速さの上限
	}{
		{"cobweb", nil, 0.5},
		{"sweet_berry_bush", map[string]string{"age": "1"}, 4.317 * 0.85},
	} {
		w := slowWorld(t, tc.name, tc.props)
		p := newPlayer(0.5, 1, 0.5)
		walkEast(w, p, 60, Input{})
		x0 := p.Pos.X
		walkEast(w, p, 20, Input{})
		v := p.Pos.X - x0
		t.Logf("%s の中の歩き %.3f m/s", tc.name, v)
		if p.Pos.X < 2 || v > tc.max || v <= 0 {
			t.Errorf("%s の中の速さ %.3f（上限 %.3f）、位置 %v", tc.name, v, tc.max, p.Pos)
		}
	}
	// クモの巣の中の落下は遅い（y の倍率 0.05）
	w := testWorld{}
	for y := 0; y <= 10; y++ {
		w[BlockPos{0, y, 0}] = mustState(t, "cobweb", nil)
	}
	w[BlockPos{0, -1, 0}] = mustState(t, "stone", nil)
	p := newPlayer(0.5, 10, 0.5)
	for range 20 {
		p.Tick(w, Input{})
	}
	if p.Pos.Y < 9 {
		t.Errorf("クモの巣の中を速く落ちた: y=%v", p.Pos.Y)
	}
}

// 粉雪の中は沈み、遅い。
func TestPowderSnow(t *testing.T) {
	w := slowWorld(t, "powder_snow", nil)
	p := newPlayer(3.5, 3, 0.5)
	for range 40 {
		p.Tick(w, Input{})
	}
	// 当たり判定が無いので床（y=1）まで沈む
	if p.Pos.Y != 1 {
		t.Errorf("粉雪に沈まない: %v", p.Pos)
	}
	x0 := p.Pos.X
	walkEast(w, p, 20, Input{})
	v := p.Pos.X - x0
	t.Logf("粉雪の中の歩き %.3f m/s", v)
	if v > 4.317*0.95 || v <= 0 {
		t.Errorf("粉雪の中の速さ %.3f", v)
	}
}

// 足場: 上に立てる。しゃがむと下りる。横から入れて、中ではジャンプで上る。
func TestScaffolding(t *testing.T) {
	w := floorWorld(10, "stone")
	for y := 1; y <= 4; y++ {
		w[BlockPos{0, y, 0}] = mustState(t, "scaffolding", map[string]string{"distance": "0", "bottom": "false"})
	}
	p := newPlayer(0.5, 6, 0.5)
	for range 20 {
		p.Tick(w, Input{})
	}
	if p.Pos.Y != 5 || !p.OnGround {
		t.Fatalf("足場の上に立てない: %v onGround=%v", p.Pos, p.OnGround)
	}
	for range 60 {
		p.Tick(w, Input{Sneak: true})
	}
	if p.Pos.Y != 1 {
		t.Errorf("しゃがんで足場を下りない: %v", p.Pos)
	}
	for range 60 {
		p.Tick(w, Input{Jump: true})
	}
	if p.Pos.Y < 4.5 {
		t.Errorf("足場の中でジャンプして上らない: %v", p.Pos)
	}
}

// 泡の柱（ソウルサンドの上）で浮き上がる。
func TestBubbleColumn(t *testing.T) {
	w := floorWorld(10, "soul_sand")
	for y := 1; y <= 8; y++ {
		w[BlockPos{0, y, 0}] = mustState(t, "bubble_column", map[string]string{"drag": "false"})
	}
	p := newPlayer(0.5, 1, 0.5)
	for range 40 {
		p.Tick(w, Input{})
	}
	if p.Pos.Y < 6 {
		t.Errorf("泡の柱で浮かない: %v", p.Pos)
	}
}

// 溶岩の流れにも押される（水よりずっと弱い）。
func TestLavaFlowPush(t *testing.T) {
	w := floorWorld(30, "stone")
	for x := 0; x <= 3; x++ {
		w[BlockPos{x, 1, 0}] = mustState(t, "lava", map[string]string{"level": strconv.Itoa(x * 2)})
	}
	p := newPlayer(1.5, 1, 0.5)
	p.updateFluids(w)
	if !p.InLava {
		t.Fatal("InLava が偽")
	}
	// 流れ (1,0,0) × 0.0023333 は 0.0045 より小さいので、止まっていれば 0.0045 にそろえる
	if !near(p.Vel.X, 0.0045000000000000005, 1e-12) || p.Vel.Y != 0 || p.Vel.Z != 0 {
		t.Errorf("溶岩の押す力 %v", p.Vel)
	}
	p2 := newPlayer(1.5, 1, 0.5)
	p2.UltraWarm = true
	p2.updateFluids(w)
	if !near(p2.Vel.X, LavaFlowScaleUltraWarm, 1e-12) {
		t.Errorf("ネザーの溶岩の押す力 %v、0.007 のはず", p2.Vel)
	}
}

// しゃがむと梯子で滑り落ちない。
func TestSneakOnLadder(t *testing.T) {
	w := floorWorld(10, "stone")
	for y := 1; y <= 8; y++ {
		w[BlockPos{2, y, 0}] = mustState(t, "stone", nil)
		w[BlockPos{1, y, 0}] = mustState(t, "ladder", map[string]string{"facing": "west"})
	}
	p := newPlayer(1.5, 1, 0.5)
	walkEast(w, p, 20, Input{})
	y := p.Pos.Y
	for range 20 {
		p.Tick(w, Input{Sneak: true})
	}
	if p.Pos.Y < y-0.2 {
		t.Errorf("しゃがんでも梯子を滑り落ちた: %v → %v", y, p.Pos.Y)
	}
}

// 面が埋まっているか（isFaceSturdy）。
func TestSturdyFaces(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]string
		want  [4]bool
	}{
		{"stone", nil, [4]bool{true, true, true, true}},
		{"oak_leaves", nil, [4]bool{}},
		{"oak_slab", map[string]string{"type": "bottom"}, [4]bool{}},
		{"oak_slab", map[string]string{"type": "double"}, [4]bool{true, true, true, true}},
		{"oak_stairs", map[string]string{"facing": "east", "half": "bottom"}, [4]bool{false, true, false, false}},
		{"soul_sand", nil, [4]bool{true, true, true, true}},
		{"glass", nil, [4]bool{true, true, true, true}},
	} {
		in := infoOf(mustState(t, tc.name, tc.props))
		if in.sturdy != tc.want {
			t.Errorf("%s %v: %v、期待 %v", tc.name, tc.props, in.sturdy, tc.want)
		}
	}
	// 固いか（blocksMotion）
	for name, want := range map[string]bool{"stone": true, "cobweb": false, "oak_sign": true, "air": false, "short_grass": false, "ladder": false, "oak_fence": true, "scaffolding": false} {
		if got := infoOf(mustState(t, name, nil)).blocksMotion; got != want {
			t.Errorf("%s の blocksMotion %v、期待 %v", name, got, want)
		}
	}
}
