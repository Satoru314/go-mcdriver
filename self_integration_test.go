//go:build integration

// 単位 5（自分の体）を本物のサーバー（internal/testserver の Docker の Paper 1.21.4）で確かめるテスト。
//
//	sg docker -c 'go test -tags integration -run TestSelfIntegration -v -count=1 -timeout 110s .'
//
// 合格の条件:
//   - tp したあとの位置が Self() に出る
//   - effect give instant_damage で HP が減り、最後に受けたダメージ（minecraft:magic）が入る
//   - experience add で経験値が増える
//   - gamemode creative / survival の変化が出る
//   - kill で死んでいる → Respawn() → 生き返って HP 20。リスポーンのあともダメージを受ける（Player Loaded を送り直している）
package mcdriver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/protocol"

	"github.com/Satoru314/go-mcdriver/internal/testserver"
	"gonum.org/v1/gonum/spatial/r3"
)

// selfTrace は bot ごとの、自分の体のパケットの記録（失敗したときに見るため）。
var selfTrace = struct {
	sync.Mutex
	m map[string][]string
}{m: map[string][]string{}}

func init() {
	for _, id := range []int32{
		data.PlayClientboundSetHealth, data.PlayClientboundDamageEvent, data.PlayClientboundPlayerCombatKill,
		data.PlayClientboundRespawn, data.PlayClientboundGameEvent, data.PlayClientboundPlayerAbilities,
		data.PlayClientboundSetExperience, data.PlayClientboundPlayerPosition,
	} {
		onPlay(id, func(cl *Client, d *protocol.Decoder) error {
			pk, _ := data.PacketByID(data.StatePlay, "clientbound", id)
			line := fmt.Sprintf("%s %s %x", time.Now().Format("15:04:05.000"), pk.Name, d.ReadRest())
			selfTrace.Lock()
			defer selfTrace.Unlock()
			selfTrace.m[cl.cfg.Username] = append(selfTrace.m[cl.cfg.Username], line)
			return nil
		})
	}
}

func logSelfTrace(t *testing.T, name string) {
	selfTrace.Lock()
	defer selfTrace.Unlock()
	t.Logf("%s が受け取った自分の体のパケット:\n  %s", name, strings.Join(selfTrace.m[name], "\n  "))
}

// waitSelf は cond が true になるまで Self() を見続ける。timeout を過ぎたら失敗にする。
func waitSelf(t *testing.T, cl *Client, timeout time.Duration, desc string, cond func(Self) bool) Self {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s := cl.Self()
		if cond(s) {
			return s
		}
		select {
		case <-cl.Done():
			t.Fatalf("%s を待つあいだに切断された: %v", desc, cl.Err())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("%v 待っても %s にならない。Self = %+v", timeout, desc, s)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSelfIntegration(t *testing.T) {
	ts := testserver.Start(t)
	rc := ts.RCON(t)
	area := ts.NewArea(t)
	name := area.BotName
	defer func() {
		if t.Failed() {
			logSelfTrace(t, name)
			t.Logf("サーバーのログ（最後の 40 行）:\n%s", testserver.Logs(context.Background(), 40))
		}
	}()
	area.ForceLoad(t, rc)
	cmd := func(c string) string {
		t.Helper()
		out := testserver.Command(t, rc, c)
		t.Logf("RCON %s → %q", c, out)
		return out
	}

	var cl *Client
	err := testserver.Retry(context.Background(), t, func() (err error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cl, err = Connect(ctx, Config{Addr: ts.GameAddr, Username: name})
		return err
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer cl.Close()

	s := waitSelf(t, cl, 5*time.Second, "位置と HP を受け取る", func(s Self) bool { return s.Spawned && s.Health > 0 })
	t.Logf("ログイン直後: %+v", s)
	if s.EntityID == 0 || s.Dead {
		t.Errorf("ログイン直後の EntityID %d Dead %v", s.EntityID, s.Dead)
	}

	// tp したあとの位置
	x, z := area.Center()
	cmd(fmt.Sprintf("tp %s %d.5 %d %d.5 90 0", name, x, testserver.StandY, z))
	want := r3.Vec{X: float64(x) + 0.5, Y: testserver.StandY, Z: float64(z) + 0.5}
	s = waitSelf(t, cl, 5*time.Second, "tp した位置", func(s Self) bool { return s.Pos == want })
	if s.Yaw != 90 || s.Pitch != 0 {
		t.Errorf("tp のあとの向き yaw %v pitch %v、期待 90 0", s.Yaw, s.Pitch)
	}

	// ゲームモード（サバイバルにしてから）
	cmd("gamemode survival " + name)
	s = waitSelf(t, cl, 5*time.Second, "サバイバル", func(s Self) bool { return s.GameMode == Survival && !s.Abilities.InstantBuild })
	cmd("gamemode creative " + name)
	s = waitSelf(t, cl, 5*time.Second, "クリエイティブ", func(s Self) bool {
		return s.GameMode == Creative && s.Abilities.InstantBuild && s.Abilities.AllowFlying && s.Abilities.Invulnerable
	})
	t.Logf("クリエイティブの Abilities %+v", s.Abilities)
	cmd("gamemode survival " + name)
	s = waitSelf(t, cl, 5*time.Second, "サバイバルに戻る", func(s Self) bool {
		return s.GameMode == Survival && !s.Abilities.InstantBuild && !s.Abilities.AllowFlying
	})

	// ダメージ。ログインしたときは HP 20（instant_health を同じティックに重ねると打ち消し合うので使わない）。
	// 隠し満腹度が残っていると 10 ティックごとに回復するので、最初に届いた値を見る
	if s.Health != 20 {
		t.Fatalf("ダメージの前の HP %v、期待 20", s.Health)
	}
	cmd("effect give " + name + " minecraft:instant_damage 1 0")
	s = waitSelf(t, cl, 5*time.Second, "HP が減る", func(s Self) bool { return s.Health < 20 })
	t.Logf("ダメージのあと HP %v、LastDamage %+v", s.Health, s.LastDamage)
	if s.Health < 14 || s.Health > 15 {
		t.Errorf("instant_damage I のあとの HP %v、期待 14（20 - 6。回復しても 15 まで）", s.Health)
	}
	s = waitSelf(t, cl, 2*time.Second, "LastDamage", func(s Self) bool { return s.HasDamage })
	if s.LastDamage.Type != "minecraft:magic" {
		t.Errorf("LastDamage.Type %q、期待 minecraft:magic（%+v）", s.LastDamage.Type, s.LastDamage)
	}

	// 経験値
	xp0 := s.Experience
	cmd("experience add " + name + " 100 points")
	s = waitSelf(t, cl, 5*time.Second, "経験値 +100", func(s Self) bool { return s.Experience.Total == xp0.Total+100 })
	t.Logf("経験値 %+v → %+v", xp0, s.Experience)
	if s.Experience.Level <= xp0.Level {
		t.Errorf("100 ポイントでレベルが上がらない %+v", s.Experience)
	}
	cmd("experience add " + name + " 2 levels")
	lv := s.Experience.Level
	s = waitSelf(t, cl, 5*time.Second, "レベル +2", func(s Self) bool { return s.Experience.Level == lv+2 })

	// 生きているときの Respawn() はエラー
	if err := cl.Respawn(); !errors.Is(err, ErrNotDead) {
		t.Errorf("生きているときの Respawn() = %v、期待 ErrNotDead", err)
	}

	// 倒す → 死んでいる → Respawn() → 生き返って HP 20
	cmd("kill " + name)
	s = waitSelf(t, cl, 5*time.Second, "死亡", func(s Self) bool { return s.Dead })
	t.Logf("死亡: HP %v、DeathMessage %q", s.Health, s.DeathMessage)
	if s.Health > 0 {
		t.Errorf("死んだのに HP %v", s.Health)
	}
	if err := cl.Respawn(); err != nil {
		t.Fatalf("Respawn(): %v", err)
	}
	s = waitSelf(t, cl, 5*time.Second, "生き返って HP 20", func(s Self) bool {
		return !s.Dead && s.Spawned && s.Health == 20
	})
	t.Logf("リスポーンのあと: 位置 %v HP %v 満腹度 %v 経験値 %+v ゲームモード %v", s.Pos, s.Health, s.Food, s.Experience, s.GameMode)
	if s.GameMode != Survival {
		t.Errorf("リスポーンのあとのゲームモード %v", s.GameMode)
	}
	if s.Experience.Total != 0 && s.Experience.Level != 0 {
		// keepInventory が false なら経験値は失われる
		t.Logf("リスポーンのあとも経験値が残っている %+v", s.Experience)
	}

	// 区画に戻り、リスポーンのあとでもダメージを受ける。何秒後から効くかを記録する
	// （Player Loaded を送り直していなければ、サーバーは 60 ティックのあいだダメージを与えない）
	cmd(fmt.Sprintf("tp %s %d.5 %d %d.5", name, x, testserver.StandY, z))
	waitSelf(t, cl, 5*time.Second, "tp した位置（リスポーンのあと）", func(s Self) bool { return s.Pos == want })
	respawned := time.Now()
	for {
		cmd("effect give " + name + " minecraft:instant_damage 1 0")
		time.Sleep(300 * time.Millisecond)
		if s = cl.Self(); s.Health < 20 {
			break
		}
		if time.Since(respawned) > 5*time.Second {
			t.Fatalf("リスポーンのあと 5 秒たってもダメージを受けない。Self = %+v", s)
		}
	}
	t.Logf("リスポーンのあとのダメージ: tp から %v で HP %v", time.Since(respawned).Round(10*time.Millisecond), s.Health)
	if el := time.Since(respawned); el > 2*time.Second {
		t.Errorf("リスポーンのあと、ダメージを受けるまで %v かかった（Player Loaded が効いていない？）", el)
	}

	// 最後まで切られていない
	select {
	case <-cl.Done():
		t.Fatalf("切断された: %v", cl.Err())
	default:
	}
}
