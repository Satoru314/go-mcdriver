//go:build integration

// 単位 7（物理と移動）を本物のサーバー（Docker の Paper 1.21.4、平らなワールド）で確かめるテスト。
//
//	sg docker -c 'go test -tags integration -run TestMoveIntegration -v -count=1 -timeout 5m .'
//
// シナリオごとに専用の区画と bot を使い、並列に動かす。合格の条件:
//   - 最後の位置が、RCON の data get entity <bot> Pos と一致する（許容誤差 moveTolerance）
//   - 位置の強制修正は、ログインの 1 回と、テストが RCON で tp した回数だけ（それ以外の修正が 0 回）
//   - サーバーのログに、その bot の "moved too quickly" / "moved wrongly" / "floating too long" が出ない
//   - 切断されない
package mcdriver

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/internal/physics"
	"github.com/Satoru314/go-mcdriver/internal/rcon"
	"github.com/Satoru314/go-mcdriver/internal/testserver"
	"gonum.org/v1/gonum/spatial/r3"
)

// moveTolerance はドライバの位置とサーバーの記録の差の許容（サーバーは受け取った double をそのまま記録するので、
// 一致するはず。表示の丸めの分だけ見る）。
const moveTolerance = 1e-6

// moveEnv はシナリオ 1 つの環境。
type moveEnv struct {
	t    *testing.T
	rc   *rcon.Client
	cl   *Client
	name string
	b    int // 区画の x の端（北西の角）
	z    int // 歩く z（ブロック）
	tps  int // RCON で tp した回数（ログインを含めた、期待する強制修正の数）
}

// moveLogin は並列のシナリオのログインの間をあける。サーバーの connection-throttle（同じ IP は 4 秒に 1 回）で
// 9 個の bot が一度に繋ぐと、testserver.Retry のやり直しの上限を超えるため。
var moveLogin struct {
	sync.Mutex
	last time.Time
}

func moveConnect(t *testing.T, addr, name string) *Client {
	moveLogin.Lock()
	defer moveLogin.Unlock()
	if d := time.Until(moveLogin.last.Add(4500 * time.Millisecond)); d > 0 {
		time.Sleep(d)
	}
	defer func() { moveLogin.last = time.Now() }()
	return connectBot(t, addr, name)
}

func newMoveEnv(t *testing.T) *moveEnv {
	ts := testserver.Start(t)
	rc := ts.RCON(t)
	area := ts.NewArea(t)
	area.ForceLoad(t, rc)
	e := &moveEnv{t: t, rc: rc, name: area.BotName, b: area.X0, z: area.Z0 + 16, tps: 1}
	// 区画の地表の上を空にし、地表を草ブロックに戻す
	S := testserver.SurfaceY
	rconMust(t, rc, fmt.Sprintf("fill %d %d %d %d %d %d minecraft:air", e.b, S+1, area.Z0, e.b+31, S+12, area.Z0+31))
	rconMust(t, rc, fmt.Sprintf("fill %d %d %d %d %d %d minecraft:grass_block", e.b, S, area.Z0, e.b+31, S, area.Z0+31))
	rconMust(t, rc, fmt.Sprintf("fill %d %d %d %d %d %d minecraft:dirt", e.b, S-2, area.Z0, e.b+31, S-1, area.Z0+31))
	e.cl = moveConnect(t, ts.GameAddr, e.name)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("サーバーのログ（最後の 30 行）:\n%s", testserver.Logs(context.Background(), 30))
		}
	})
	e.tp(float64(e.b)+4.5, float64(testserver.StandY), float64(e.z)+0.5, -90)
	waitLoaded(t, e.cl, BlockPos{X: e.b + 16, Y: testserver.StandY, Z: e.z}, 32)
	e.settle()
	return e
}

func (e *moveEnv) cmd(c string) string { return rconMust(e.t, e.rc, c) }

// tp は RCON で (x, y, z) に向き yaw（pitch 0）で tp し、届くまで待つ。
func (e *moveEnv) tp(x, y, z float64, yaw float32) {
	e.t.Helper()
	before := e.teleports()
	e.cmd(fmt.Sprintf("tp %s %v %v %v %v 0", e.name, x, y, z, yaw))
	e.tps++
	e.waitFor(5*time.Second, "tp が届く", func() bool { return e.teleports() > before })
}

func (e *moveEnv) teleports() int {
	e.cl.mu.Lock()
	defer e.cl.mu.Unlock()
	return e.cl.teleports
}

// pos はドライバの今の位置と接地を返す。
func (e *moveEnv) pos() (r3.Vec, bool) {
	e.cl.mu.Lock()
	defer e.cl.mu.Unlock()
	return r3.Vec{X: e.cl.x, Y: e.cl.y, Z: e.cl.z}, e.cl.onGround
}

func (e *moveEnv) alive() {
	e.t.Helper()
	select {
	case <-e.cl.Done():
		e.t.Fatalf("切断された: %v", e.cl.Err())
	default:
	}
}

func (e *moveEnv) waitFor(d time.Duration, desc string, cond func() bool) {
	e.t.Helper()
	end := time.Now().Add(d)
	for !cond() {
		e.alive()
		if time.Now().After(end) {
			p, g := e.pos()
			e.t.Fatalf("%v たっても %s にならない（位置 %v 接地 %v）", d, desc, p, g)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// settle は操作を離し、接地して 10 ティック動かなくなるまで待つ。
func (e *moveEnv) settle() r3.Vec {
	e.t.Helper()
	e.cl.SetControls(Controls{})
	var last r3.Vec
	still := time.Now()
	e.waitFor(15*time.Second, "止まる", func() bool {
		p, g := e.pos()
		if p != last || !g {
			last, still = p, time.Now()
			return false
		}
		return time.Since(still) >= 500*time.Millisecond
	})
	return last
}

// run は操作 c を、cond が真になるまで（最長 d）続ける。
func (e *moveEnv) run(c Controls, d time.Duration, desc string, cond func(p r3.Vec) bool) {
	e.t.Helper()
	e.cl.SetControls(c)
	e.waitFor(d, desc, func() bool { p, _ := e.pos(); return cond(p) })
}

var posRe = regexp.MustCompile(`\[(-?[0-9.E-]+)d, (-?[0-9.E-]+)d, (-?[0-9.E-]+)d\]`)

// serverPos は RCON でサーバーが記録している位置を読む。
func (e *moveEnv) serverPos() r3.Vec {
	e.t.Helper()
	out := e.cmd("data get entity " + e.name + " Pos")
	m := posRe.FindStringSubmatch(out)
	if m == nil {
		e.t.Fatalf("data get entity の応答が読めない: %q", out)
	}
	var v [3]float64
	for i := range v {
		f, err := strconv.ParseFloat(m[i+1], 64)
		if err != nil {
			e.t.Fatalf("data get entity の数: %q: %v", out, err)
		}
		v[i] = f
	}
	return r3.Vec{X: v[0], Y: v[1], Z: v[2]}
}

// check は止まるのを待ち、位置がサーバーの記録と一致し、強制修正が tp の分だけで、ログに警告が無いことを確かめる。
// 止まった位置を返す。
func (e *moveEnv) check(desc string) r3.Vec {
	e.t.Helper()
	p := e.settle()
	// 最後の位置が送られて処理されるまで少し待つ
	time.Sleep(200 * time.Millisecond)
	sp := e.serverPos()
	d := r3.Sub(p, sp)
	e.t.Logf("%s: ドライバ %v、サーバー %v、差 %v", desc, p, sp, d)
	if math.Abs(d.X) > moveTolerance || math.Abs(d.Y) > moveTolerance || math.Abs(d.Z) > moveTolerance {
		e.t.Errorf("%s: 位置がサーバーの記録と違う: ドライバ %v、サーバー %v", desc, p, sp)
	}
	if n := e.teleports(); n != e.tps {
		e.t.Errorf("%s: 強制修正の数 %d、期待は %d（ログインと tp）", desc, n, e.tps)
	}
	logs := testserver.Logs(context.Background(), 3000)
	for _, line := range strings.Split(logs, "\n") {
		if !strings.Contains(line, e.name) {
			continue
		}
		for _, w := range []string{"moved too quickly", "moved wrongly", "floating too long"} {
			if strings.Contains(line, w) {
				e.t.Errorf("%s: サーバーのログに %q: %s", desc, w, line)
			}
		}
	}
	return p
}

func (e *moveEnv) fill(x0, y0, z0, x1, y1, z1 int, block string) {
	e.cmd(fmt.Sprintf("fill %d %d %d %d %d %d %s", x0, y0, z0, x1, y1, z1, block))
}

func TestMoveIntegration(t *testing.T) {
	Y := testserver.StandY
	fy := float64(Y)

	t.Run("歩く", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		start := time.Now()
		e.run(Controls{Forward: true}, 10*time.Second, "東に 10 ブロック", func(p r3.Vec) bool { return p.X >= float64(e.b)+14.5 })
		t.Logf("10 ブロックに %v（本家の歩きは 約 2.32 秒）", time.Since(start).Round(time.Millisecond))
		p := e.check("歩く")
		if p.Y != fy || p.Z != float64(e.z)+0.5 {
			t.Errorf("歩いたあとの位置 %v", p)
		}
	})

	t.Run("走る", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		start := time.Now()
		sprinted := false
		e.run(Controls{Forward: true, Sprint: true}, 10*time.Second, "東に 10 ブロック", func(p r3.Vec) bool {
			sprinted = sprinted || e.cl.Sprinting()
			return p.X >= float64(e.b)+14.5
		})
		t.Logf("10 ブロックに %v（本家の走りは 約 1.78 秒）", time.Since(start).Round(time.Millisecond))
		if !sprinted {
			t.Error("走っていない")
		}
		p := e.check("走る")
		if p.Y != fy {
			t.Errorf("走ったあとの位置 %v", p)
		}
	})

	t.Run("ジャンプ", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		top := fy
		e.cl.SetControls(Controls{Jump: true})
		end := time.Now().Add(1500 * time.Millisecond)
		for time.Now().Before(end) {
			p, _ := e.pos()
			top = max(top, p.Y)
			time.Sleep(5 * time.Millisecond)
		}
		t.Logf("ジャンプの最高点 %.4f（本家 1.2522）", top-fy)
		if math.Abs(top-fy-1.2522) > 0.001 {
			t.Errorf("ジャンプの最高点 %.4f", top-fy)
		}
		p := e.check("ジャンプ")
		if p.Y != fy {
			t.Errorf("着地した位置 %v", p)
		}
	})

	t.Run("2段の段差をジャンプで上る", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		e.fill(e.b+8, Y, e.z-2, e.b+8, Y, e.z+2, "minecraft:stone")
		e.fill(e.b+9, Y, e.z-2, e.b+20, Y+1, e.z+2, "minecraft:stone")
		e.run(Controls{Forward: true, Jump: true}, 10*time.Second, "2 段の上", func(p r3.Vec) bool { return p.X >= float64(e.b)+11 && p.Y >= fy+2 })
		e.run(Controls{Forward: true}, 5*time.Second, "少し歩く", func(p r3.Vec) bool { return p.X >= float64(e.b)+13 })
		p := e.check("2 段の段差")
		if p.Y != fy+2 {
			t.Errorf("上ったあとの位置 %v、y は %v のはず", p, fy+2)
		}
	})

	t.Run("高さ5から落ちる", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		e.fill(e.b+2, Y, e.z-2, e.b+6, Y+4, e.z+2, "minecraft:stone")
		e.tp(float64(e.b)+4.5, fy+5, float64(e.z)+0.5, -90)
		e.check("台の上")
		e.run(Controls{Forward: true}, 10*time.Second, "台から下りる", func(p r3.Vec) bool { return p.X >= float64(e.b)+9 })
		p := e.check("高さ 5 から落ちる")
		if p.Y != fy {
			t.Errorf("落ちたあとの位置 %v", p)
		}
	})

	t.Run("空中にtpされて落ちる", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		e.tp(float64(e.b)+4.5, fy+10, float64(e.z)+0.5, -90)
		e.waitFor(10*time.Second, "着地", func() bool { p, g := e.pos(); return g && p.Y == fy })
		// 以前は空中に浮いたまま 4 秒で切られた。着地のあとも切られないこと
		time.Sleep(5 * time.Second)
		e.alive()
		p := e.check("空中への tp")
		if p.Y != fy {
			t.Errorf("着地した位置 %v", p)
		}
	})

	t.Run("階段を上り下りする", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		for i := range 3 {
			if i > 0 {
				e.fill(e.b+8+i, Y, e.z-2, e.b+8+i, Y+i-1, e.z+2, "minecraft:stone")
			}
			e.fill(e.b+8+i, Y+i, e.z-2, e.b+8+i, Y+i, e.z+2, "minecraft:oak_stairs[facing=east]")
		}
		e.fill(e.b+11, Y, e.z-2, e.b+18, Y+2, e.z+2, "minecraft:stone")
		e.run(Controls{Forward: true}, 10*time.Second, "階段の上", func(p r3.Vec) bool { return p.X >= float64(e.b)+13 })
		p := e.check("階段を上る")
		if p.Y != fy+3 {
			t.Errorf("上ったあとの位置 %v、y は %v のはず", p, fy+3)
		}
		e.cl.Look(90, 0)
		e.run(Controls{Forward: true}, 10*time.Second, "階段の下", func(p r3.Vec) bool { return p.X <= float64(e.b)+5 })
		p = e.check("階段を下りる")
		if p.Y != fy {
			t.Errorf("下りたあとの位置 %v", p)
		}
	})

	t.Run("水に入って出る", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		S := testserver.SurfaceY
		e.fill(e.b+8, S-1, e.z-2, e.b+12, S, e.z+2, "minecraft:water")
		inWater := false
		e.run(Controls{Forward: true}, 10*time.Second, "水に入る", func(p r3.Vec) bool {
			e.cl.mu.Lock()
			inWater = inWater || moveKey.of(e.cl).p.InWater
			e.cl.mu.Unlock()
			return p.X >= float64(e.b)+10
		})
		// 水の中で少し沈む
		e.cl.SetControls(Controls{})
		time.Sleep(time.Second)
		p, _ := e.pos()
		t.Logf("水の中: %v", p)
		if !inWater || p.Y >= fy {
			t.Errorf("水に入っていない: inWater=%v 位置 %v", inWater, p)
		}
		e.run(Controls{Forward: true, Jump: true}, 15*time.Second, "水から出る", func(p r3.Vec) bool { return p.X >= float64(e.b)+15 && p.Y >= fy })
		p = e.check("水から出る")
		if p.Y != fy {
			t.Errorf("水から出たあとの位置 %v", p)
		}
	})

	t.Run("壁にぶつかる", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		e.fill(e.b+10, Y, e.z-4, e.b+10, Y+2, e.z+4, "minecraft:stone")
		e.cl.SetControls(Controls{Forward: true, Sprint: true})
		time.Sleep(3 * time.Second)
		p := e.check("壁")
		want := float64(e.b) + 10 - physics.HalfWidth
		if p.X != want || p.Y != fy {
			t.Errorf("壁で止まった位置 %v、x は %v のはず", p, want)
		}
	})
}
