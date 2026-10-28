//go:build soak

// 長時間の連続運転のテスト。テスト用サーバー（internal/testserver）に bot を数体つなぎ、
// 歩く・走る・ジャンプ・tp を繰り返しながら長い時間動かす。一定の間隔で、切断の回数、位置の強制修正の回数、
// goroutine の数、ヒープの量を記録し、最後に増え続けていないかを判定する。
//
//	sg docker -c 'go test -tags soak -run TestSoak -v -count=1 -timeout 30m . -soak.duration 10m'
//
// 引数:
//
//	-soak.duration  動かす時間（既定 10m）
//	-soak.bots      bot の数（既定 3）
//	-soak.interval  記録の間隔（既定 30s）
//	-soak.csv       記録を CSV にも書き出すファイル（空なら書かない）
//
// 合格の条件:
//   - 切断が 0 回（切られたら繋ぎ直して続け、数える）
//   - 位置の強制修正が 0 回（自分で打った tp と、ログイン直後・リスポーン直後のものは数えない）
//   - goroutine の数が増え続けていない（最後の値が、慣らしのあとの最初の値 + 2 以下）
//   - ヒープが増え続けていない（後ろ 3 分の 1 の平均が、前 3 分の 1 の平均の 1.5 倍 + 8 MiB 以下）
//   - 出来事を捨てていない（読み続けているので）
package mcdriver

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/internal/rcon"
	"github.com/Satoru314/go-mcdriver/internal/testserver"
	"gonum.org/v1/gonum/spatial/r3"
)

var (
	soakDuration = flag.Duration("soak.duration", 10*time.Minute, "長時間運転の時間")
	soakBots     = flag.Int("soak.bots", 3, "長時間運転の bot の数")
	soakInterval = flag.Duration("soak.interval", 30*time.Second, "記録の間隔")
	soakCSV      = flag.String("soak.csv", "", "記録を書き出す CSV ファイル")
)

// soakBot は長時間運転の bot 1 体。
type soakBot struct {
	t    *testing.T
	ts   *testserver.Server
	area testserver.Area
	rc   *rcon.Client
	rng  *rand.Rand

	mu       sync.Mutex
	cl       *Client
	expected []r3.Vec // 自分で打った tp の行き先（届いたら消す）
	skipNext bool     // ログイン直後・リスポーン直後の強制修正は数えない

	disconnects atomic.Int64
	corrections atomic.Int64
	teleports   atomic.Int64 // 自分で打った tp で届いたもの
	dropped     atomic.Uint64
	actions     atomic.Int64
	errs        atomic.Int64
}

// soakSample は 1 回の記録。
type soakSample struct {
	at          time.Duration
	goroutines  int
	heap        uint64
	disconnects int64
	corrections int64
	teleports   int64
	actions     int64
	dropped     uint64
}

func TestSoak(t *testing.T) {
	dur, nbots, interval := *soakDuration, *soakBots, *soakInterval
	if dl, ok := t.Deadline(); ok && time.Until(dl) < dur+2*time.Minute {
		t.Fatalf("go test の -timeout が短い（残り %v、必要 %v 以上）。-timeout 30m などを付ける", time.Until(dl).Round(time.Second), dur+2*time.Minute)
	}
	ts := testserver.Start(t)
	t.Logf("長時間運転: %v、bot %d 体、記録の間隔 %v", dur, nbots, interval)

	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()

	bots := make([]*soakBot, nbots)
	for i := range bots {
		b := &soakBot{t: t, ts: ts, area: ts.NewArea(t), rc: ts.RCON(t), rng: rand.New(rand.NewPCG(uint64(i), uint64(time.Now().UnixNano())))}
		b.area.ForceLoad(t, b.rc)
		if err := b.connect(); err != nil {
			t.Fatalf("%s: %v", b.area.BotName, err)
		}
		bots[i] = b
	}
	defer func() {
		for _, b := range bots {
			b.mu.Lock()
			if b.cl != nil {
				b.cl.Close()
			}
			b.mu.Unlock()
		}
	}()

	var wg sync.WaitGroup
	for _, b := range bots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.run(ctx)
		}()
	}

	start := time.Now()
	var samples []soakSample
	take := func() {
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		s := soakSample{at: time.Since(start).Round(time.Second), goroutines: runtime.NumGoroutine(), heap: ms.HeapAlloc}
		for _, b := range bots {
			s.disconnects += b.disconnects.Load()
			s.corrections += b.corrections.Load()
			s.teleports += b.teleports.Load()
			s.actions += b.actions.Load()
			s.dropped += b.dropped.Load() + b.client().EventsDropped()
		}
		samples = append(samples, s)
		t.Logf("記録 %v: goroutine %d、ヒープ %.1f MiB、切断 %d、強制修正 %d、tp %d、行動 %d、捨てた出来事 %d",
			s.at, s.goroutines, float64(s.heap)/(1<<20), s.disconnects, s.corrections, s.teleports, s.actions, s.dropped)
	}
	take()
	tick := time.NewTicker(interval)
	for loop := true; loop; {
		select {
		case <-ctx.Done():
			loop = false
		case <-tick.C:
			take()
		}
	}
	tick.Stop()
	wg.Wait()
	take()

	if *soakCSV != "" {
		var b strings.Builder
		b.WriteString("seconds,goroutines,heap_bytes,disconnects,corrections,teleports,actions,dropped\n")
		for _, s := range samples {
			fmt.Fprintf(&b, "%d,%d,%d,%d,%d,%d,%d,%d\n", int(s.at.Seconds()), s.goroutines, s.heap, s.disconnects, s.corrections, s.teleports, s.actions, s.dropped)
		}
		if err := os.WriteFile(*soakCSV, []byte(b.String()), 0o644); err != nil {
			t.Errorf("CSV を書けない: %v", err)
		}
	}

	// ---- 判定 ----
	last := samples[len(samples)-1]
	if last.disconnects != 0 {
		t.Errorf("切断 %d 回", last.disconnects)
	}
	if last.corrections != 0 {
		t.Errorf("位置の強制修正 %d 回", last.corrections)
	}
	if last.dropped != 0 {
		t.Errorf("捨てた出来事 %d 個", last.dropped)
	}
	if last.actions == 0 || last.teleports == 0 {
		t.Errorf("行動 %d、tp %d（動いていない）", last.actions, last.teleports)
	}
	var errs int64
	for _, b := range bots {
		errs += b.errs.Load()
	}
	if errs != 0 {
		t.Errorf("RCON などの失敗 %d 回（ログを見る）", errs)
	}
	if len(samples) < 4 {
		t.Logf("記録が %d 回しかないので、増え方は判定しない", len(samples))
		return
	}
	// 最初の記録は繋いだ直後なので、2 回目（慣らしのあと）から見る
	ss := samples[1:]
	if g0, g1 := ss[0].goroutines, ss[len(ss)-1].goroutines; g1 > g0+2 {
		t.Errorf("goroutine が増えている: %d → %d", g0, g1)
	}
	third := len(ss) / 3
	if third == 0 {
		third = 1
	}
	avg := func(xs []soakSample) float64 {
		var sum float64
		for _, x := range xs {
			sum += float64(x.heap)
		}
		return sum / float64(len(xs))
	}
	h0, h1 := avg(ss[:third]), avg(ss[len(ss)-third:])
	t.Logf("ヒープの平均: 前 3 分の 1 %.1f MiB、後ろ 3 分の 1 %.1f MiB", h0/(1<<20), h1/(1<<20))
	if h1 > h0*1.5+8*(1<<20) {
		t.Errorf("ヒープが増え続けている: %.1f MiB → %.1f MiB", h0/(1<<20), h1/(1<<20))
	}
}

func (b *soakBot) client() *Client {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cl
}

// connect は繋いで、出来事を読む goroutine を始め、区画の真ん中に tp する。
func (b *soakBot) connect() error {
	var cl *Client
	err := testserver.Retry(context.Background(), b.t, func() (err error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cl, err = Connect(ctx, Config{Addr: b.ts.GameAddr, Username: b.area.BotName, ViewDistance: 4})
		return err
	})
	if err != nil {
		return err
	}
	b.mu.Lock()
	if b.cl != nil {
		b.dropped.Add(b.cl.EventsDropped()) // 前の接続の分
	}
	b.cl = cl
	b.skipNext = true
	b.mu.Unlock()
	go b.watch(cl)
	return b.tpCenter()
}

// watch は出来事を読み、位置の強制修正を数える。
func (b *soakBot) watch(cl *Client) {
	for ev := range cl.Events() {
		switch ev := ev.(type) {
		case TeleportEvent:
			b.mu.Lock()
			matched := false
			for i, p := range b.expected {
				if p == ev.Pos {
					b.expected = append(b.expected[:i], b.expected[i+1:]...)
					matched = true
					break
				}
			}
			skip := b.skipNext
			b.skipNext = false
			b.mu.Unlock()
			switch {
			case matched:
				b.teleports.Add(1)
			case skip:
			default:
				b.corrections.Add(1)
				b.t.Logf("%s: 位置の強制修正 %+v（tp を待っている %d 個）", b.area.BotName, ev, len(b.expected))
			}
		case RespawnEvent:
			b.mu.Lock()
			b.skipNext = true
			b.mu.Unlock()
		case DeathEvent:
			b.t.Logf("%s: 死んだ %q", b.area.BotName, ev.Message)
			go func() {
				time.Sleep(500 * time.Millisecond)
				if err := cl.Respawn(); err != nil {
					b.t.Logf("%s: リスポーン: %v", b.area.BotName, err)
				}
			}()
		case DisconnectEvent:
			if ev.Err != ErrClosed {
				b.t.Logf("%s: 切断 %v", b.area.BotName, ev.Err)
			}
		}
	}
}

// tpCenter は区画の真ん中に RCON で tp する（行き先は「待っている tp」に入れる）。
func (b *soakBot) tpCenter() error {
	x, z := b.area.Center()
	want := r3.Vec{X: float64(x) + 0.5, Y: testserver.StandY, Z: float64(z) + 0.5}
	b.mu.Lock()
	b.expected = append(b.expected, want)
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := b.rc.Command(ctx, fmt.Sprintf("tp %s %d.5 %d %d.5", b.area.BotName, x, testserver.StandY, z))
	if err != nil {
		b.errs.Add(1)
		b.t.Logf("%s: tp: %v", b.area.BotName, err)
	}
	return err
}

// run は ctx が終わるまで、行動を 1 つずつ繰り返す。切られたら数えて繋ぎ直す。
func (b *soakBot) run(ctx context.Context) {
	step := 0
	for ctx.Err() == nil {
		cl := b.client()
		select {
		case <-cl.Done():
			b.disconnects.Add(1)
			b.t.Logf("%s: 切断された: %v。繋ぎ直す", b.area.BotName, cl.Err())
			if err := b.connect(); err != nil {
				b.errs.Add(1)
				b.t.Logf("%s: 繋ぎ直せない: %v", b.area.BotName, err)
				sleepCtx(ctx, 5*time.Second)
			}
			continue
		default:
		}
		step++
		if step%15 == 0 {
			// 時々区画の真ん中に戻す（区画から出ていかないように）
			cl.SetControls(Controls{})
			b.tpCenter()
			sleepCtx(ctx, time.Second)
			continue
		}
		cl.Look(float32(b.rng.IntN(360)-180), 0)
		var c Controls
		switch b.rng.IntN(5) {
		case 0:
			c = Controls{Forward: true}
		case 1:
			c = Controls{Forward: true, Sprint: true}
		case 2:
			c = Controls{Forward: true, Jump: true}
		case 3:
			c = Controls{Jump: true}
		case 4:
			c = Controls{Left: b.rng.IntN(2) == 0, Right: true, Back: b.rng.IntN(2) == 0}
		}
		cl.SetControls(c)
		b.actions.Add(1)
		sleepCtx(ctx, time.Duration(500+b.rng.IntN(1500))*time.Millisecond)
		cl.SetControls(Controls{})
		sleepCtx(ctx, time.Duration(b.rng.IntN(500))*time.Millisecond)
	}
	b.client().SetControls(Controls{})
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
