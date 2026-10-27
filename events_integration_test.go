//go:build integration

// 単位 10（出来事、時刻と天気）を本物のサーバー（internal/testserver の Docker の Paper 1.21.4）で確かめるテスト。
//
//	sg docker -c 'go test -tags integration -run "TestEventsIntegration|TestTimeWeatherIntegration" -v -count=1 -timeout 110s .'
//
// 合格の条件:
//   - setblock でブロックの変化の知らせ（位置と前後の状態）が来る
//   - summon と kill で生き物の出現・消滅の知らせが来る
//   - damage で種類（minecraft:magic）と量のダメージの知らせが来る
//   - kill で死亡の知らせ（理由に bot の名前）、Respawn() でリスポーンの知らせ（AfterDeath）が来る
//   - tp で位置の強制修正の知らせが来る
//   - time set / time add の値が Time() に出る
//   - weather rain / thunder / clear で天気の知らせと Weather() が変わる
//   - 切断の知らせが最後に来てチャネルが閉じる
package mcdriver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/internal/testserver"
	"gonum.org/v1/gonum/spatial/r3"
)

// eventLog は Events() を読み続けて記録する。
type eventLog struct {
	mu     sync.Mutex
	evs    []Event
	closed bool
	notify chan struct{}
}

func collectEvents(cl *Client) *eventLog {
	l := &eventLog{notify: make(chan struct{})}
	go func() {
		for ev := range cl.Events() {
			l.mu.Lock()
			l.evs = append(l.evs, ev)
			close(l.notify)
			l.notify = make(chan struct{})
			l.mu.Unlock()
		}
		l.mu.Lock()
		l.closed = true
		close(l.notify)
		l.notify = make(chan struct{})
		l.mu.Unlock()
	}()
	return l
}

// waitEvent は from 番目より後で、型が T で cond を満たす出来事を待つ。見つけたものとその番号を返す。
func waitEvent[T Event](t *testing.T, l *eventLog, from int, timeout time.Duration, desc string, cond func(T) bool) (T, int) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		l.mu.Lock()
		for i := from; i < len(l.evs); i++ {
			if ev, ok := l.evs[i].(T); ok && cond(ev) {
				l.mu.Unlock()
				return ev, i
			}
		}
		from = len(l.evs)
		closed, notify := l.closed, l.notify
		l.mu.Unlock()
		if closed {
			t.Fatalf("%s を待つあいだにチャネルが閉じた", desc)
		}
		select {
		case <-notify:
		case <-deadline:
			t.Fatalf("%v 待っても %s が来ない", timeout, desc)
		}
	}
}

func (l *eventLog) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.evs)
}

func (l *eventLog) dump(t *testing.T) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var b strings.Builder
	for _, ev := range l.evs {
		fmt.Fprintf(&b, "\n  %s %T %+v", ev.Time().Format("15:04:05.000"), ev, ev)
	}
	t.Logf("届いた出来事 %d 個:%s", len(l.evs), b.String())
}

func TestEventsIntegration(t *testing.T) {
	t.Parallel()
	ts := testserver.Start(t)
	rc := ts.RCON(t)
	area := ts.NewArea(t)
	name := area.BotName
	area.ForceLoad(t, rc)
	cmd := func(c string) string {
		t.Helper()
		out := testserver.Command(t, rc, c)
		t.Logf("RCON %s → %q", c, out)
		return out
	}

	cl := connectBot(t, ts.GameAddr, name)
	l := collectEvents(cl)
	defer func() {
		if t.Failed() {
			l.dump(t)
			t.Logf("サーバーのログ（最後の 40 行）:\n%s", testserver.Logs(context.Background(), 40))
		}
	}()
	waitSelf(t, cl, 5*time.Second, "位置と HP を受け取る", func(s Self) bool { return s.Spawned && s.Health > 0 })

	// tp → 位置の強制修正
	x, z := area.Center()
	want := r3.Vec{X: float64(x) + 0.5, Y: testserver.StandY, Z: float64(z) + 0.5}
	cmd(fmt.Sprintf("tp %s %d.5 %d %d.5 0 0", name, x, testserver.StandY, z))
	waitEvent(t, l, 0, 5*time.Second, "tp の TeleportEvent", func(ev TeleportEvent) bool { return ev.Pos == want })
	waitSelf(t, cl, 5*time.Second, "チャンクを受け取る", func(Self) bool {
		_, ok := cl.Block(BlockPos{X: x, Y: testserver.SurfaceY, Z: z})
		return ok
	})

	// setblock → ブロックの変化
	bp := BlockPos{X: x + 3, Y: testserver.StandY, Z: z + 3}
	from := l.len()
	cmd(fmt.Sprintf("setblock %d %d %d minecraft:oak_stairs[facing=east]", bp.X, bp.Y, bp.Z))
	bc, _ := waitEvent(t, l, from, 5*time.Second, "setblock の BlockChangeEvent", func(ev BlockChangeEvent) bool { return ev.Pos == bp })
	if bc.Old.Name() != "air" || bc.New.Name() != "oak_stairs" {
		t.Errorf("BlockChangeEvent %v → %v、期待 air → oak_stairs", bc.Old, bc.New)
	} else if f, _ := bc.New.Get("facing"); f != "east" {
		t.Errorf("階段の向き %q", f)
	}
	cmd(fmt.Sprintf("setblock %d %d %d minecraft:air", bp.X, bp.Y, bp.Z))
	bc, _ = waitEvent(t, l, from, 5*time.Second, "空気に戻す BlockChangeEvent", func(ev BlockChangeEvent) bool { return ev.Pos == bp && ev.New.Name() == "air" })
	if bc.Old.Name() != "oak_stairs" {
		t.Errorf("戻すときの前の状態 %v", bc.Old)
	}

	// summon と kill → 生き物の出現・消滅
	tag := fmt.Sprintf("evtest%d", area.ID)
	from = l.len()
	cmd(fmt.Sprintf(`summon minecraft:zombie %d.5 %d %d.5 {NoAI:1b,Silent:1b,Tags:["%s"]}`, x+5, testserver.StandY, z, tag))
	sp, _ := waitEvent(t, l, from, 5*time.Second, "zombie の EntitySpawnEvent", func(ev EntitySpawnEvent) bool { return ev.Entity.Type.Name == "zombie" })
	if sp.Entity.Pos != (r3.Vec{X: float64(x+5) + 0.5, Y: testserver.StandY, Z: float64(z) + 0.5}) {
		t.Errorf("出現の位置 %v", sp.Entity.Pos)
	}
	cmd("kill @e[tag=" + tag + "]")
	rm, _ := waitEvent(t, l, from, 5*time.Second, "zombie の EntityRemoveEvent", func(ev EntityRemoveEvent) bool { return ev.Entity.ID == sp.Entity.ID })
	if rm.Entity.Type.Name != "zombie" {
		t.Errorf("EntityRemoveEvent の種類 %q", rm.Entity.Type.Name)
	}

	// damage → ダメージ（種類と量）
	cmd("gamemode survival " + name)
	waitSelf(t, cl, 5*time.Second, "サバイバル", func(s Self) bool { return s.GameMode == Survival && s.Health == 20 })
	from = l.len()
	cmd("damage " + name + " 3 minecraft:magic")
	dm, _ := waitEvent(t, l, from, 5*time.Second, "damage の DamageEvent", func(DamageEvent) bool { return true })
	if dm.Type != "minecraft:magic" || dm.Amount != 3 || dm.Health != 17 {
		t.Errorf("DamageEvent %+v、期待 minecraft:magic 3 → HP 17", dm)
	}

	// kill → 死亡、Respawn() → リスポーン、そのあと位置の強制修正
	from = l.len()
	cmd("kill " + name)
	d, _ := waitEvent(t, l, from, 5*time.Second, "kill の DeathEvent", func(DeathEvent) bool { return true })
	t.Logf("DeathEvent %+v", d)
	if !strings.Contains(d.Message, name) {
		t.Errorf("死んだ理由 %q に名前 %s が無い", d.Message, name)
	}
	waitSelf(t, cl, 5*time.Second, "死亡", func(s Self) bool { return s.Dead })
	if err := cl.Respawn(); err != nil {
		t.Fatalf("Respawn(): %v", err)
	}
	r, i := waitEvent(t, l, from, 5*time.Second, "RespawnEvent", func(RespawnEvent) bool { return true })
	if !r.AfterDeath || r.Dimension != "minecraft:overworld" || r.GameMode != Survival {
		t.Errorf("RespawnEvent %+v", r)
	}
	waitEvent(t, l, i, 5*time.Second, "リスポーンのあとの TeleportEvent", func(TeleportEvent) bool { return true })

	// Close → 最後に切断の知らせ
	cl.Close()
	dc, _ := waitEvent(t, l, from, 5*time.Second, "DisconnectEvent", func(DisconnectEvent) bool { return true })
	if dc.Err != ErrClosed {
		t.Errorf("DisconnectEvent.Err %v、期待 ErrClosed", dc.Err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		l.mu.Lock()
		closed, n := l.closed, len(l.evs)
		last := l.evs[n-1]
		l.mu.Unlock()
		if closed {
			if _, ok := last.(DisconnectEvent); !ok {
				t.Errorf("最後の出来事が %T", last)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("DisconnectEvent のあとチャネルが閉じない")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := cl.EventsDropped(); n != 0 {
		t.Errorf("EventsDropped = %d（読み続けているのに捨てた）", n)
	}
}

// 時刻と天気はワールド全体に効くので、並列にせず 1 つだけで動かす。終わったら元に戻す。
func TestTimeWeatherIntegration(t *testing.T) {
	ts := testserver.Start(t)
	rc := ts.RCON(t)
	area := ts.NewArea(t)
	name := area.BotName
	cmd := func(c string) string {
		t.Helper()
		out := testserver.Command(t, rc, c)
		t.Logf("RCON %s → %q", c, out)
		return out
	}

	cl := connectBot(t, ts.GameAddr, name)
	l := collectEvents(cl)
	defer func() {
		if t.Failed() {
			l.dump(t)
		}
	}()
	waitTime := func(desc string, cond func(TimeInfo) bool) TimeInfo {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			ti := cl.TimeInfo()
			if cond(ti) {
				return ti
			}
			if time.Now().After(deadline) {
				t.Fatalf("5 秒待っても %s にならない。TimeInfo %+v", desc, ti)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	// 時刻（テスト用サーバーは doDaylightCycle false なので進まない）
	ti := waitTime("時刻を受け取る", func(ti TimeInfo) bool { return ti.Known })
	t.Logf("はじめの時刻 %+v", ti)
	if ti.Ticking {
		t.Errorf("doDaylightCycle false なのに Ticking")
	}
	orig := ti.DayTime
	defer cmd(fmt.Sprintf("time set %d", orig))
	cmd("time set 1000")
	waitTime("time set 1000", func(ti TimeInfo) bool { return ti.DayTime == 1000 })
	cmd("time set 30000")
	waitTime("time set 30000", func(ti TimeInfo) bool { return ti.DayTime == 30000 })
	if day, tod := cl.Time(); day != 1 || tod != 6000 {
		t.Errorf("Time() = %d, %d、期待 1, 6000", day, tod)
	}
	cmd("time add 100")
	waitTime("time add 100", func(ti TimeInfo) bool { return ti.DayTime == 30100 })

	// 天気
	defer cmd("weather clear")
	if w := cl.Weather(); w.Kind != Clear {
		cmd("weather clear")
		waitEvent(t, l, 0, 10*time.Second, "晴れ", func(ev WeatherEvent) bool { return ev.Weather.Kind == Clear })
	}
	steps := []struct {
		c    string
		want WeatherKind
	}{{"weather rain", Rain}, {"weather thunder", Thunder}, {"weather clear", Clear}}
	for _, st := range steps {
		from := l.len()
		start := time.Now()
		cmd(st.c)
		ev, _ := waitEvent(t, l, from, 10*time.Second, st.c+" の WeatherEvent", func(ev WeatherEvent) bool { return ev.Weather.Kind == st.want })
		t.Logf("%s: %v で %+v", st.c, time.Since(start).Round(10*time.Millisecond), ev.Weather)
		if w := cl.Weather(); w.Kind != st.want {
			t.Errorf("%s のあとの Weather() %+v", st.c, w)
		}
		if st.want != Clear && !ev.Weather.Raining {
			t.Errorf("%s のあと Raining が false（雨の開始の番号が逆？）%+v", st.c, ev.Weather)
		}
	}
	if w := cl.Weather(); w.Raining {
		t.Errorf("weather clear のあと Raining が true（雨の終了の番号が逆？）%+v", w)
	}
}
