package physics

import (
	"math"
	"testing"

	"github.com/Satoru314/go-mcdriver/data"
	"gonum.org/v1/gonum/spatial/r3"
)

// testWorld はテスト用の地形。置いていない場所は空気。|x|,|z| が 200 を超えると読み込まれていないことにする。
type testWorld map[BlockPos]data.BlockState

func (w testWorld) Block(x, y, z int) (data.BlockState, bool) {
	if x < -200 || x > 200 || z < -200 || z > 200 {
		return data.BlockState{}, false
	}
	if s, ok := w[BlockPos{x, y, z}]; ok {
		return s, true
	}
	return mustState(nil, "air", nil), true
}

func mustState(t *testing.T, name string, props map[string]string) data.BlockState {
	b, ok := data.BlockByName(name)
	if !ok {
		panic("ブロックが無い: " + name)
	}
	s, err := b.State(props)
	if err != nil {
		panic(err)
	}
	return s
}

// floorWorld は y=0 の面に石の床（-r〜r）を敷いた地形を返す。立つ高さは y=1。
func floorWorld(r int, name string) testWorld {
	w := testWorld{}
	s := mustState(nil, name, nil)
	for x := -r; x <= r; x++ {
		for z := -r; z <= r; z++ {
			w[BlockPos{x, 0, z}] = s
		}
	}
	return w
}

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

// 平らな床に落ちて、ちょうど床の上（y=1）で止まり、地面に立つ。
func TestFallAndLand(t *testing.T) {
	w := floorWorld(5, "stone")
	p := &Player{Pos: r3.Vec{X: 0.5, Y: 10, Z: 0.5}}
	ticks := 0
	for ; ticks < 100 && !p.OnGround; ticks++ {
		p.Tick(w, Input{})
	}
	if !p.OnGround || p.Pos.Y != 1 {
		t.Fatalf("着地しない: OnGround=%v y=%v（%d ティック）", p.OnGround, p.Pos.Y, ticks)
	}
	// 9 ブロックの落下は本家で 24 ティック前後（y の速さ: (v-0.08)*0.98）
	t.Logf("高さ 9 から %d ティックで着地", ticks)
	for range 40 {
		p.Tick(w, Input{})
	}
	if !p.OnGround || p.Pos.Y != 1 || p.Vel.Y != 0 && p.Vel.Y != -Gravity*float64(AirDrag) {
		t.Errorf("立っているあいだに動いた: y=%v vy=%v onGround=%v", p.Pos.Y, p.Vel.Y, p.OnGround)
	}
	if p.Pos.X != 0.5 || p.Pos.Z != 0.5 {
		t.Errorf("横に動いた: %v", p.Pos)
	}
}

// 立ってジャンプしたときの最高点は 約 1.2522（Minecraft Wiki の Jumping: 1.2522 ブロック）。
func TestJumpHeight(t *testing.T) {
	w := floorWorld(5, "stone")
	p := &Player{Pos: r3.Vec{X: 0.5, Y: 1, Z: 0.5}}
	p.Tick(w, Input{}) // 接地させる（速さ 0 からは 2 ティック目で下にぶつかる）
	p.Tick(w, Input{})
	if !p.OnGround {
		t.Fatal("接地していない")
	}
	top := p.Pos.Y
	p.Tick(w, Input{Jump: true})
	n := 1
	for ; n < 40 && !p.OnGround; n++ {
		top = max(top, p.Pos.Y)
		p.Tick(w, Input{})
	}
	h := top - 1
	t.Logf("最高点 %.6f、%d ティックで着地", h, n)
	if !near(h, 1.2522, 0.0005) {
		t.Errorf("最高点 = %.6f、本家は約 1.2522", h)
	}
	if p.Pos.Y != 1 {
		t.Errorf("着地した高さ = %v", p.Pos.Y)
	}
	// 本家の立ちジャンプは 12 ティックで着地する
	if n != 12 {
		t.Errorf("着地まで %d ティック、本家は 12", n)
	}
}

// steadySpeed は入力を続けて、速さが落ち着いたあとの 1 秒あたりの横の速さを測る。
func steadySpeed(t *testing.T, ground string, in Input, yaw float32) float64 {
	t.Helper()
	w := floorWorld(150, ground)
	p := &Player{Pos: r3.Vec{X: -100.5, Y: 1, Z: 0.5}, Yaw: yaw}
	p.Tick(w, Input{})
	for range 60 {
		p.Tick(w, in)
	}
	start := p.Pos
	for range 20 {
		p.Tick(w, in)
	}
	d := r3.Sub(p.Pos, start)
	return math.Hypot(d.X, d.Z)
}

func TestWalkAndSprintSpeed(t *testing.T) {
	// Minecraft Wiki（Sprinting / Walking）: 歩き 4.317 m/s、走り 5.612 m/s
	walk := steadySpeed(t, "stone", Input{Forward: true}, -90) // 東を向く
	sprint := steadySpeed(t, "stone", Input{Forward: true, Sprint: true}, -90)
	t.Logf("歩き %.4f m/s、走り %.4f m/s", walk, sprint)
	if !near(walk, 4.317, 0.005) {
		t.Errorf("歩く速さ = %.4f、本家は 4.317", walk)
	}
	if !near(sprint, 5.612, 0.005) {
		t.Errorf("走る速さ = %.4f、本家は 5.612", sprint)
	}
	// 斜めは入力 (0.98, 0.98) が moveRelative で長さ 1 にそろえられるので、前だけ（0.98）より 1/0.98 倍速い（本家 1.21.4 の動き）
	diag := steadySpeed(t, "stone", Input{Forward: true, Left: true}, -90)
	if !near(diag, 4.317/0.98, 0.005) {
		t.Errorf("斜めの速さ = %.4f", diag)
	}
	// 後ろ向きに走れない（前に押していないと走らない）
	back := steadySpeed(t, "stone", Input{Back: true, Sprint: true}, -90)
	if !near(back, 4.317, 0.005) {
		t.Errorf("後ろに走った速さ = %.4f、歩きと同じはず", back)
	}
	// 氷の上を歩く速さは加速が小さく減り方も小さいので、ほぼ同じ（0.1*0.216/0.98^3*0.98 / (1-0.98*0.91) = 約 4.16 m/s）。
	// ソウルサンドの上は遅い
	ice := steadySpeed(t, "ice", Input{Forward: true}, -90)
	soul := steadySpeed(t, "soul_sand", Input{Forward: true}, -90)
	t.Logf("氷 %.4f m/s、ソウルサンド %.4f m/s", ice, soul)
	if !near(ice, 4.156, 0.01) {
		t.Errorf("氷の上の速さ: %.4f", ice)
	}
	if soul >= walk*0.7 {
		t.Errorf("ソウルサンドの上が遅くない: %.4f", soul)
	}
}

// 走りジャンプは前に 0.2 足されるので、走るだけより速い（Minecraft Wiki: 約 7.1 m/s）。
func TestSprintJumpFaster(t *testing.T) {
	w := floorWorld(150, "stone")
	p := &Player{Pos: r3.Vec{X: -100.5, Y: 1, Z: 0.5}, Yaw: -90}
	p.Tick(w, Input{})
	for range 60 {
		p.Tick(w, Input{Forward: true, Sprint: true, Jump: true})
	}
	start := p.Pos
	for range 120 {
		p.Tick(w, Input{Forward: true, Sprint: true, Jump: true})
	}
	v := (p.Pos.X - start.X) / 6
	t.Logf("走りジャンプの平均 %.3f m/s", v)
	if v < 6.5 || v > 7.5 {
		t.Errorf("走りジャンプの速さ = %.3f、本家は約 7.1", v)
	}
}

// walkEast は東を向いて n ティック歩く。
func walkEast(w World, p *Player, n int, in Input) {
	p.Yaw = -90
	in.Forward = true
	for range n {
		p.Tick(w, in)
	}
}

func TestStepUp(t *testing.T) {
	// 高さ 0.5（下つきのハーフブロック）は自動で上れる
	w := floorWorld(10, "stone")
	for x := 3; x <= 9; x++ {
		for z := -3; z <= 3; z++ {
			w[BlockPos{x, 1, z}] = mustState(t, "oak_slab", map[string]string{"type": "bottom"})
		}
	}
	p := &Player{Pos: r3.Vec{X: 0.5, Y: 1, Z: 0.5}}
	walkEast(w, p, 25, Input{})
	if p.Pos.Y != 1.5 || p.Pos.X < 4 {
		t.Errorf("ハーフブロックに上れない: %v", p.Pos)
	}

	// 高さ 1 は上れず、壁の手前で止まる（x = 3 - 0.3）
	w = floorWorld(10, "stone")
	for x := 3; x <= 9; x++ {
		for z := -3; z <= 3; z++ {
			w[BlockPos{x, 1, z}] = mustState(t, "stone", nil)
		}
	}
	p = &Player{Pos: r3.Vec{X: 0.5, Y: 1, Z: 0.5}}
	walkEast(w, p, 40, Input{})
	if p.Pos.Y != 1 || !near(p.Pos.X, 3-HalfWidth, 1e-6) {
		t.Errorf("1 ブロックの段差: 位置 %v、期待は x=%.4f y=1", p.Pos, 3-HalfWidth)
	}
	if !p.HorizontalCollision {
		t.Error("壁にぶつかったのに HorizontalCollision が偽")
	}

	// 1 ブロックの段差はジャンプすれば上れる
	p = &Player{Pos: r3.Vec{X: 0.5, Y: 1, Z: 0.5}}
	walkEast(w, p, 30, Input{Jump: true})
	walkEast(w, p, 15, Input{})
	if p.Pos.Y != 2 || p.Pos.X < 4 {
		t.Errorf("ジャンプで 1 段上れない: %v", p.Pos)
	}
}

func TestWallStops(t *testing.T) {
	w := floorWorld(10, "stone")
	for y := 1; y <= 3; y++ {
		for z := -3; z <= 3; z++ {
			w[BlockPos{3, y, z}] = mustState(t, "stone", nil)
		}
	}
	p := &Player{Pos: r3.Vec{X: 0.5, Y: 1, Z: 0.5}}
	walkEast(w, p, 40, Input{Sprint: true, Jump: true})
	if !near(p.Pos.X, 3-HalfWidth, 1e-6) {
		t.Errorf("壁で止まらない: x=%v", p.Pos.X)
	}
	if p.Pos.Z != 0.5 {
		t.Errorf("z がずれた: %v", p.Pos.Z)
	}
	// 正面からぶつかると走るのをやめる
	if p.Sprinting {
		t.Error("壁にぶつかっても走ったまま")
	}
}

func TestStairs(t *testing.T) {
	// 東に上る階段（facing=east）を 3 段。上りきって y=4 に立つ
	// 上りきった先は x=6〜14 の高さ 3 の台
	w := floorWorld(30, "stone")
	for z := -3; z <= 3; z++ {
		for i := range 3 {
			for y := 1; y < 1+i; y++ {
				w[BlockPos{3 + i, y, z}] = mustState(t, "stone", nil)
			}
			w[BlockPos{3 + i, 1 + i, z}] = mustState(t, "oak_stairs", map[string]string{"facing": "east", "half": "bottom"})
		}
		for x := 6; x <= 14; x++ {
			for y := 1; y <= 3; y++ {
				w[BlockPos{x, y, z}] = mustState(t, "stone", nil)
			}
		}
	}
	p := &Player{Pos: r3.Vec{X: 0.5, Y: 1, Z: 0.5}}
	walkEast(w, p, 40, Input{})
	if p.Pos.Y != 4 || p.Pos.X < 7 || p.Pos.X > 14 {
		t.Errorf("階段を上りきれない: %v", p.Pos)
	}
	// 戻って下りる
	p.Yaw = 90
	for range 45 {
		p.Tick(w, Input{Forward: true})
	}
	if p.Pos.Y != 1 || p.Pos.X > 2.5 {
		t.Errorf("階段を下りきれない: %v", p.Pos)
	}
}

func TestSlabsWalkable(t *testing.T) {
	// ハーフブロックを並べた道の上を歩くと、高さ 0.5 のまま進む
	w := floorWorld(20, "stone")
	for x := 2; x <= 15; x++ {
		for z := -1; z <= 1; z++ {
			w[BlockPos{x, 1, z}] = mustState(t, "oak_slab", map[string]string{"type": "bottom"})
		}
	}
	p := &Player{Pos: r3.Vec{X: 0.5, Y: 1, Z: 0.5}}
	walkEast(w, p, 40, Input{})
	if p.Pos.Y != 1.5 || p.Pos.X < 6 || !p.OnGround {
		t.Errorf("ハーフブロックの上: %v onGround=%v", p.Pos, p.OnGround)
	}
}

// waterWorld は y=0 が床、y=1〜5 が水の源のプール（10×10）。
func waterWorld() testWorld {
	w := floorWorld(20, "stone")
	ws := mustState(nil, "water", nil)
	for x := -5; x <= 5; x++ {
		for z := -5; z <= 5; z++ {
			for y := 1; y <= 5; y++ {
				w[BlockPos{x, y, z}] = ws
			}
		}
	}
	return w
}

func TestWater(t *testing.T) {
	w := waterWorld()
	// 水面の近く（y=4）から、何もしなければゆっくり沈む
	p := &Player{Pos: r3.Vec{X: 0.5, Y: 4, Z: 0.5}}
	for range 20 {
		p.Tick(w, Input{})
	}
	if !p.InWater {
		t.Fatal("水の中なのに InWater が偽")
	}
	if p.Pos.Y >= 4 {
		t.Errorf("沈まない: y=%v", p.Pos.Y)
	}
	t.Logf("1 秒で y=4 → %.4f、速さ %.4f", p.Pos.Y, p.Vel.Y)
	// 水の中の落ち方は空気よりずっと遅い（本家の終端速度は約 -0.02 ブロック/ティック前後）
	if p.Vel.Y < -0.1 {
		t.Errorf("水の中で速く落ちすぎ: vy=%v", p.Vel.Y)
	}
	for range 300 {
		p.Tick(w, Input{})
	}
	if p.Pos.Y != 1 || !p.OnGround {
		t.Errorf("底に着かない: y=%v onGround=%v", p.Pos.Y, p.OnGround)
	}
	// ジャンプを押し続けると浮く
	for range 100 {
		p.Tick(w, Input{Jump: true})
	}
	if p.Pos.Y < 4.5 {
		t.Errorf("ジャンプで浮かない: y=%v", p.Pos.Y)
	}
	t.Logf("ジャンプを 5 秒押したあと y=%.4f", p.Pos.Y)
}

func TestWaterSlowerAndExit(t *testing.T) {
	// 浅い水（深さ 1）の中を歩くと遅く、水を抜ければふつうに歩く
	w := floorWorld(40, "stone")
	ws := mustState(nil, "water", nil)
	for x := 0; x <= 10; x++ {
		for z := -2; z <= 2; z++ {
			w[BlockPos{x, 1, z}] = ws
		}
	}
	p := &Player{Pos: r3.Vec{X: 2.5, Y: 1, Z: 0.5}}
	walkEast(w, p, 20, Input{})
	if !p.InWater {
		t.Fatal("InWater が偽")
	}
	x0 := p.Pos.X
	walkEast(w, p, 20, Input{})
	inWater := p.Pos.X - x0
	t.Logf("水の中の歩き %.3f m/s", inWater)
	if inWater > 2.5 {
		t.Errorf("水の中が速すぎる: %.3f", inWater)
	}
	walkEast(w, p, 100, Input{})
	if p.InWater || p.Pos.X < 11 || p.Pos.Y != 1 {
		t.Errorf("水から出られない: %v inWater=%v", p.Pos, p.InWater)
	}
}

func TestLadder(t *testing.T) {
	// 東の壁に梯子（facing=west）。東を向いて前に押すと上る
	w := floorWorld(10, "stone")
	for y := 1; y <= 8; y++ {
		w[BlockPos{2, y, 0}] = mustState(t, "stone", nil)
		w[BlockPos{1, y, 0}] = mustState(t, "ladder", map[string]string{"facing": "west"})
	}
	p := &Player{Pos: r3.Vec{X: 1.5, Y: 1, Z: 0.5}}
	walkEast(w, p, 20, Input{})
	if p.Pos.Y < 3 {
		t.Errorf("梯子を上らない: y=%v", p.Pos.Y)
	}
	// 上る速さは 0.2 ブロック/ティック前後（Minecraft Wiki: 2.35 m/s）
	y0 := p.Pos.Y
	walkEast(w, p, 10, Input{})
	v := (p.Pos.Y - y0) * 2
	t.Logf("梯子を上る速さ %.3f m/s", v)
	if !near(v, 2.35, 0.1) {
		t.Errorf("梯子を上る速さ = %.3f、本家は約 2.35", v)
	}
	// 離すとゆっくり下りる（1 ティックの下向きの動きは 0.15 まで）
	for range 10 {
		y := p.Pos.Y
		p.Tick(w, Input{})
		if y-p.Pos.Y > 0.15+1e-6 {
			t.Fatalf("梯子で速く落ちすぎ: %v", y-p.Pos.Y)
		}
	}
}

func TestNotLoadedDoesNotMove(t *testing.T) {
	w := floorWorld(5, "stone")
	p := &Player{Pos: r3.Vec{X: 300.5, Y: 50, Z: 0.5}}
	for range 10 {
		if p.Tick(w, Input{Forward: true}) {
			t.Fatal("読み込まれていない場所で Tick が真")
		}
	}
	if p.Pos != (r3.Vec{X: 300.5, Y: 50, Z: 0.5}) {
		t.Errorf("動いた: %v", p.Pos)
	}
}

func TestFenceNotJumpable(t *testing.T) {
	// フェンス（高さ 1.5）はジャンプでも越えられない
	w := floorWorld(10, "stone")
	for z := -3; z <= 3; z++ {
		w[BlockPos{3, 1, z}] = mustState(t, "oak_fence", map[string]string{"north": "true", "south": "true"})
	}
	p := &Player{Pos: r3.Vec{X: 0.5, Y: 1, Z: 0.5}}
	walkEast(w, p, 60, Input{Jump: true})
	// フェンスの柱は x が 0.375〜0.625 なので、x = 3.375 - 0.3 で止まる
	if p.Pos.X > 3.375-HalfWidth+1e-6 {
		t.Errorf("フェンスを越えた: %v", p.Pos)
	}
}

func TestSinTable(t *testing.T) {
	for _, d := range []float64{0, 30, 45, 90, -90, 180, 359} {
		r := d * math.Pi / 180
		if !near(float64(mthSin(r)), math.Sin(r), 1e-4) || !near(float64(mthCos(r)), math.Cos(r), 1e-4) {
			t.Errorf("sin/cos(%v) = %v %v", d, mthSin(r), mthCos(r))
		}
	}
}
