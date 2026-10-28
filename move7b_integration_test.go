//go:build integration

// 単位 7b（水の流れ、泳ぎ、しゃがみ、満腹度、クモの巣）を本物のサーバー（Docker の Paper 1.21.4）で確かめるテスト。
//
//	sg docker -c 'go test -tags integration -run TestMove7bIntegration -v -count=1 -timeout 5m .'
//
// 合格の条件は move_integration_test.go と同じ（moveEnv.check）: 止まった位置が RCON の記録と一致し、
// 強制修正がログインと tp の分だけで、サーバーのログに "moved wrongly" などが出ず、切断されない。
package mcdriver

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/internal/physics"
	"github.com/Satoru314/go-mcdriver/internal/testserver"
	"gonum.org/v1/gonum/spatial/r3"
)

// phys は物理の状態の写しを返す。
func (e *moveEnv) phys() physics.Player {
	e.cl.mu.Lock()
	defer e.cl.mu.Unlock()
	return moveKey.of(e.cl).p
}

func TestMove7bIntegration(t *testing.T) {
	S := testserver.SurfaceY
	Y := testserver.StandY
	fy := float64(Y)

	// 幅 1 の溝（地表の高さ、x = b+6〜b+13）の西の端に水の源を置き、東へ流す（b+13 で level 7、b+14 は草の壁）。
	// 溝の上に立つと東へ押され、東の壁で止まる。
	t.Run("流れる水に押される", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		e.fill(e.b+6, S, e.z, e.b+13, S, e.z, "minecraft:air")
		e.cmd(fmt.Sprintf("setblock %d %d %d minecraft:water", e.b+6, S, e.z))
		time.Sleep(3 * time.Second) // 流れが広がるのを待つ（1 ブロック 5 ティック）
		s, _ := e.cl.Block(BlockPos{X: e.b + 10, Y: S, Z: e.z})
		t.Logf("x=b+10 の水: %v", s)
		e.tp(float64(e.b)+7.5, float64(S), float64(e.z)+0.5, -90)
		pushed := false
		e.waitFor(15*time.Second, "東の壁まで押される", func() bool {
			p, _ := e.pos()
			pushed = pushed || p.X > float64(e.b)+8
			return p.X >= float64(e.b)+14-physics.HalfWidth-1e-9
		})
		p := e.check("流れる水")
		if want := float64(e.b) + 14 - physics.HalfWidth; p.X != want || p.Y != float64(S) || p.Z != float64(e.z)+0.5 {
			t.Errorf("押されて止まった位置 %v、x は %v のはず", p, want)
		}
	})

	// 深さ 3 の溝（x = b+6〜b+13）。下の 2 段は水の源で埋め、一番上の段は西の端の源から東へ流す
	// （流れる水の下が流れる水だと下へ落ちて横に広がらないので、下は源にする）。
	// 底で目まで浸かってから、少し上を向いて西（上流）へ泳ぐ。水面の近くで当たり判定が流れる段に掛かり、東へ押される。
	t.Run("流れる水の中を上流へ泳ぐ", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		e.fill(e.b+6, S, e.z, e.b+13, S, e.z, "minecraft:air")
		e.fill(e.b+6, S-2, e.z, e.b+13, S-1, e.z, "minecraft:water")
		e.cmd(fmt.Sprintf("setblock %d %d %d minecraft:water", e.b+6, S, e.z))
		time.Sleep(3 * time.Second)
		e.tp(float64(e.b)+12.5, float64(S-2), float64(e.z)+0.5, 90)
		e.cl.Look(90, -15)
		time.Sleep(time.Second)
		ph := e.phys()
		t.Logf("溝の底: 水 %v 目が水の中 %v 押されて %v", ph.InWater, ph.EyeInWater, ph.Vel)
		x0, _ := e.pos()
		minX := x0.X
		swam := false
		e.cl.SetControls(Controls{Forward: true, Sprint: true})
		end := time.Now().Add(2500 * time.Millisecond)
		for time.Now().Before(end) {
			e.alive()
			p, _ := e.pos()
			minX = min(minX, p.X)
			swam = swam || e.cl.Swimming()
			time.Sleep(10 * time.Millisecond)
		}
		t.Logf("2.5 秒で x %.3f → %.3f（泳いだ %v）", x0.X, minX, swam)
		if !swam || minX > x0.X-3 {
			t.Errorf("上流へ泳げない: 泳いだ %v、x %.3f → %.3f", swam, x0.X, minX)
		}
		e.check("上流へ泳ぐ")
	})

	// 深さ 3 の池（一番上の水のブロックは地表の高さで、岸の上面と同じ高さ）の底から、ジャンプを押し続けて岸へ向かう。
	t.Run("深い池から岸に上がる", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		e.fill(e.b+6, S-2, e.z-2, e.b+10, S, e.z+2, "minecraft:water")
		e.tp(float64(e.b)+8.5, float64(S-2), float64(e.z)+0.5, -90)
		e.check("池の底")
		e.run(Controls{Forward: true, Jump: true}, 20*time.Second, "岸に上がる", func(p r3.Vec) bool {
			return p.X >= float64(e.b)+12 && p.Y >= fy
		})
		p := e.check("池から上がる")
		if p.Y != fy {
			t.Errorf("上がったあとの位置 %v", p)
		}
	})

	// 岸が一番上の水のブロックより 1 段高いと、本家の式ではジャンプでも上がれない（水面から 1.11 上）。
	// 壁で 0.3 の速さをもらえるのは水に触れているティックだけで、水面（足 S-1+8/9）から出る最後のティックに
	// 0.34 上がって 0.3 をもらい、空中で 0.3 + 0.2156 + 0.1329 + 0.0518 上がっても、足は fy-0.072 までしか届かない
	// （internal/physics の TestShoreClimbTrace と TestShoreOneBlockHigherBound）。
	// 壁に押しつけたまま浮き沈みし（足が岸の上面の 0.5 下より上まで上がる）、上がれず、強制修正も起きないこと。
	t.Run("1段高い岸には上がれない", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		e.fill(e.b+6, S, e.z-2, e.b+10, S, e.z+2, "minecraft:air")
		e.fill(e.b+6, S-2, e.z-2, e.b+10, S-1, e.z+2, "minecraft:water")
		e.tp(float64(e.b)+8.5, float64(S-2), float64(e.z)+0.5, -90)
		e.check("池の底")
		e.cl.SetControls(Controls{Forward: true, Jump: true})
		top := math.Inf(-1)
		wallX := float64(e.b) + 11 - physics.HalfWidth
		atWall := false
		end := time.Now().Add(6 * time.Second)
		for time.Now().Before(end) {
			e.alive()
			p, _ := e.pos()
			top = max(top, p.Y)
			atWall = atWall || p.X == wallX
			time.Sleep(5 * time.Millisecond)
		}
		t.Logf("一番高い足 %.4f（岸の上は %v、本家の式の上限は %.4f）", top, fy, fy-0.0718)
		if top >= fy {
			t.Errorf("岸に上がった（一番高い足 %.4f）", top)
		}
		if top < fy-0.5 || !atWall {
			t.Errorf("壁で浮き沈みしていない（一番高い足 %.4f、壁に着いた %v）", top, atWall)
		}
		e.check("池の底に戻る")
	})

	// しゃがんで高さ 4 の台の東の端へ歩いても落ちない。しゃがみをやめると落ちる。
	t.Run("しゃがんで崖の端から落ちない", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		e.fill(e.b+2, Y, e.z-2, e.b+6, Y+3, e.z+2, "minecraft:stone")
		e.tp(float64(e.b)+4.5, fy+4, float64(e.z)+0.5, -90)
		e.check("台の上")
		e.cl.SetControls(Controls{Forward: true, Sneak: true})
		time.Sleep(3 * time.Second)
		if q := e.cl.Pose(); q != PoseCrouching {
			t.Errorf("姿勢 %v、crouching のはず", q)
		}
		e.cl.SetControls(Controls{Sneak: true})
		time.Sleep(500 * time.Millisecond)
		p := e.check("しゃがんで端")
		edge := float64(e.b) + 7
		if p.Y != fy+4 || p.X <= edge || p.X >= edge+physics.HalfWidth {
			t.Errorf("端で止まった位置 %v（x は %v〜%v のはず）", p, edge, edge+physics.HalfWidth)
		}
		t.Logf("端から %.4f はみ出して止まった", p.X-edge)
		e.run(Controls{Forward: true}, 5*time.Second, "しゃがみをやめて落ちる", func(p r3.Vec) bool { return p.Y == fy })
		p = e.check("落ちる")
		if p.Y != fy {
			t.Errorf("落ちたあとの位置 %v", p)
		}
	})

	// 満腹度を 6 以下にすると走れない。満腹度が戻れば走れる。
	t.Run("満腹度が低いと走れない", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		e.cmd(fmt.Sprintf("effect give %s minecraft:hunger 60 255 true", e.name))
		e.waitFor(20*time.Second, "満腹度 6 以下", func() bool { return e.cl.Self().Food <= 6 })
		e.cmd(fmt.Sprintf("effect clear %s minecraft:hunger", e.name))
		t.Logf("満腹度 %d", e.cl.Self().Food)
		sprinted := false
		e.cl.SetControls(Controls{Forward: true, Sprint: true})
		end := time.Now().Add(2 * time.Second)
		for time.Now().Before(end) {
			e.alive()
			sprinted = sprinted || e.cl.Sprinting()
			time.Sleep(5 * time.Millisecond)
		}
		if sprinted {
			t.Error("満腹度 6 以下で走った")
		}
		e.check("満腹度が低い")
		e.cmd(fmt.Sprintf("effect give %s minecraft:saturation 1 255 true", e.name))
		e.waitFor(5*time.Second, "満腹度が戻る", func() bool { return e.cl.Self().Food > 6 })
		e.cl.Look(90, 0)
		e.waitFor(3*time.Second, "走る", func() bool {
			e.cl.SetControls(Controls{Forward: true, Sprint: true})
			return e.cl.Sprinting()
		})
		time.Sleep(500 * time.Millisecond)
		e.check("満腹度が戻って走る")
	})

	// クモの巣の中を歩くと遅い。
	t.Run("クモの巣で遅くなる", func(t *testing.T) {
		t.Parallel()
		e := newMoveEnv(t)
		e.fill(e.b+7, Y, e.z, e.b+11, Y+1, e.z, "minecraft:cobweb")
		e.run(Controls{Forward: true}, 5*time.Second, "クモの巣に入る", func(p r3.Vec) bool { return p.X >= float64(e.b)+7.5 })
		x0, _ := e.pos()
		time.Sleep(2 * time.Second)
		x1, _ := e.pos()
		v := (x1.X - x0.X) / 2
		t.Logf("クモの巣の中の歩き %.3f m/s（ふつうは 4.317）", v)
		if v <= 0 || v > 0.6 {
			t.Errorf("クモの巣の中の速さ %.3f m/s", v)
		}
		e.check("クモの巣")
	})
}
