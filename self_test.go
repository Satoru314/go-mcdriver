package mcdriver

import (
	"errors"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
	"gonum.org/v1/gonum/spatial/r3"
)

// 偽のサーバーから自分の体のパケットを送り、Self() に出ること。
// 死亡 → Respawn() で Client Command が送られる → Respawn を受け取って生き返る → Player Loaded をもう一度送る、まで確かめる。
func TestSelfFromFakeServer(t *testing.T) {
	s, c := playClient(t)

	// Login (play) のエンティティ ID とゲームモード。位置はまだ無い
	st := c.Self()
	if st.EntityID != 1234 || st.GameMode != Creative || st.Spawned || st.Dead {
		t.Fatalf("ログイン直後の Self %+v", st)
	}

	// 位置の強制修正（速度つき）
	s.teleport(packets.PlayerPosition{TeleportID: 1, X: 10.5, Y: -60, Z: -3.25, VelX: 0.1, VelY: 0.2, VelZ: 0.3, Yaw: 45, Pitch: 30}, 10.5, -60, -3.25, true)
	st = c.Self()
	if !st.Spawned || st.Pos != (r3.Vec{X: 10.5, Y: -60, Z: -3.25}) || st.Yaw != 45 || st.Pitch != 30 || !st.OnGround {
		t.Errorf("位置 %+v", st)
	}
	if st.Vel != (r3.Vec{X: 0.1, Y: 0.2, Z: 0.3}) {
		t.Errorf("速度 %v、期待 (0.1, 0.2, 0.3)", st.Vel)
	}
	// 速度の y だけ相対
	s.teleport(packets.PlayerPosition{TeleportID: 2, X: 10.5, Y: -60, Z: -3.25, VelY: 0.5, Yaw: 45, Pitch: 30, Flags: packets.RelVelY}, 10.5, -60, -3.25, false)
	if v := c.Self().Vel; v != (r3.Vec{X: 0, Y: 0.7, Z: 0}) {
		t.Errorf("相対の速度 %v、期待 (0, 0.7, 0)", v)
	}

	// Set Health、Set Experience、Player Abilities
	s.send(data.PlayClientboundSetHealth, func(e *protocol.Encoder) {
		packets.WriteSetHealth(e, packets.SetHealth{Health: 15.5, Food: 18, Saturation: 3.25})
	})
	s.send(data.PlayClientboundSetExperience, func(e *protocol.Encoder) {
		packets.WriteSetExperience(e, packets.SetExperience{Bar: 0.5, Level: 3, Total: 31})
	})
	s.send(data.PlayClientboundPlayerAbilities, func(e *protocol.Encoder) {
		packets.WritePlayerAbilities(e, packets.PlayerAbilities{
			Flags: packets.AbilityInvulnerable | packets.AbilityAllowFlying | packets.AbilityInstantBuild, FlyingSpeed: 0.05, FOVModifier: 0.1,
		})
	})
	// Set Entity Motion: ほかのエンティティのものは無視、自分のものは速度にする
	s.send(data.PlayClientboundSetEntityMotion, func(e *protocol.Encoder) {
		e.WriteVarInt(99)
		e.WriteShort(8000)
		e.WriteShort(8000)
		e.WriteShort(8000)
	})
	s.send(data.PlayClientboundSetEntityMotion, func(e *protocol.Encoder) {
		e.WriteVarInt(1234)
		e.WriteShort(-4000)
		e.WriteShort(800)
		e.WriteShort(0)
	})
	s.sync()
	st = c.Self()
	if st.Health != 15.5 || st.Food != 18 || st.Saturation != 3.25 {
		t.Errorf("HP %v 満腹度 %v 隠し満腹度 %v", st.Health, st.Food, st.Saturation)
	}
	if st.Experience != (Experience{Bar: 0.5, Level: 3, Total: 31}) {
		t.Errorf("経験値 %+v", st.Experience)
	}
	if a := st.Abilities; !a.Invulnerable || a.Flying || !a.AllowFlying || !a.InstantBuild || a.FlyingSpeed != 0.05 || a.WalkingSpeed != 0.1 {
		t.Errorf("Abilities %+v", a)
	}
	if st.Vel != (r3.Vec{X: -0.5, Y: 0.1, Z: 0}) {
		t.Errorf("Set Entity Motion の速度 %v、期待 (-0.5, 0.1, 0)", st.Vel)
	}

	// Game Event: ゲームモードの変更、すぐリスポーン、関係ないもの（雨）
	s.send(data.PlayClientboundGameEvent, func(e *protocol.Encoder) {
		packets.WriteGameEvent(e, packets.GameEvent{Event: packets.GameEventChangeGameMode, Value: 0})
	})
	s.send(data.PlayClientboundGameEvent, func(e *protocol.Encoder) {
		packets.WriteGameEvent(e, packets.GameEvent{Event: packets.GameEventImmediateRespawn, Value: 1})
	})
	s.send(data.PlayClientboundGameEvent, func(e *protocol.Encoder) {
		packets.WriteGameEvent(e, packets.GameEvent{Event: packets.GameEventBeginRaining})
	})
	// Damage Event: ほかのエンティティへのものは無視、自分へのものは LastDamage に入る
	s.send(data.PlayClientboundDamageEvent, func(e *protocol.Encoder) {
		packets.WriteDamageEvent(e, packets.DamageEvent{EntityID: 77, SourceTypeID: 1, SourceCauseID: -1, SourceDirectID: -1})
	})
	s.sync()
	st = c.Self()
	if st.GameMode != Survival || !st.ImmediateRespawn {
		t.Errorf("ゲームモード %v、すぐリスポーン %v", st.GameMode, st.ImmediateRespawn)
	}
	if st.HasDamage {
		t.Errorf("ほかのエンティティへのダメージが入った %+v", st.LastDamage)
	}
	before := time.Now()
	s.send(data.PlayClientboundDamageEvent, func(e *protocol.Encoder) {
		packets.WriteDamageEvent(e, packets.DamageEvent{
			EntityID: 1234, SourceTypeID: 5, SourceCauseID: 42, SourceDirectID: -1,
			HasSourcePos: true, SourceX: 1, SourceY: 2, SourceZ: 3,
		})
	})
	s.sync()
	st = c.Self()
	if d := st.LastDamage; !st.HasDamage || d.TypeID != 5 || d.CauseEntityID != 42 || d.DirectEntityID != -1 ||
		!d.HasSourcePos || d.SourcePos != (r3.Vec{X: 1, Y: 2, Z: 3}) || d.At.Before(before) {
		t.Errorf("LastDamage %+v（HasDamage %v）", d, st.HasDamage)
	}

	// 生きているあいだの Respawn() はエラー
	if err := c.Respawn(); !errors.Is(err, ErrNotDead) {
		t.Errorf("生きているときの Respawn() = %v、期待 ErrNotDead", err)
	}

	// 死亡: HP 0 と Player Combat Kill
	s.send(data.PlayClientboundSetHealth, func(e *protocol.Encoder) {
		packets.WriteSetHealth(e, packets.SetHealth{Health: 0, Food: 18, Saturation: 0})
	})
	s.send(data.PlayClientboundPlayerCombatKill, func(e *protocol.Encoder) {
		must(t, packets.WritePlayerCombatKill(e, packets.PlayerCombatKill{
			PlayerID: 1234, Message: map[string]any{"translate": "death.attack.magic", "with": []any{"worldbot"}},
		}))
	})
	s.sync()
	st = c.Self()
	if !st.Dead || st.Health != 0 || st.DeathMessage == "" {
		t.Fatalf("死んだあと Dead %v HP %v DeathMessage %q", st.Dead, st.Health, st.DeathMessage)
	}
	t.Logf("DeathMessage: %q", st.DeathMessage)

	// Respawn() → Client Command（action 0）
	if err := c.Respawn(); err != nil {
		t.Fatalf("Respawn(): %v", err)
	}
	d := s.expect(data.PlayServerboundClientCommand, tickPackets)
	if a, err := d.ReadVarInt(); err != nil || a != 0 || d.Remaining() != 0 {
		t.Fatalf("Client Command の action %d %v（残り %d）", a, err, d.Remaining())
	}

	// サーバーが Respawn を送る → 生き返り、新しい位置が届くまで位置を送らない
	s.send(data.PlayClientboundRespawn, func(e *protocol.Encoder) {
		must(t, packets.WriteRespawn(e, packets.Respawn{
			DimensionType: 0, DimensionName: "minecraft:overworld", GameMode: 0, PreviousGameMode: -1, IsFlat: true,
			HasDeathLocation: true, DeathDimension: "minecraft:overworld", DeathLocation: protocol.Position{X: 10, Y: -60, Z: -4},
			SeaLevel: -63,
		}))
	})
	s.sync()
	st = c.Self()
	if st.Dead || st.Spawned || st.GameMode != Survival || st.Vel != (r3.Vec{}) {
		t.Errorf("Respawn のあと Dead %v Spawned %v GameMode %v Vel %v", st.Dead, st.Spawned, st.GameMode, st.Vel)
	}
	s.nc.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if p, err := protocol.ReadPacket(s.br, s.th); err == nil {
		t.Fatalf("リスポーンのあと、位置を受け取る前にパケット 0x%02x が来た", p.ID)
	}
	// 新しい位置 → 確認、位置、Player Loaded（もう一度）
	s.teleport(packets.PlayerPosition{TeleportID: 3, X: 0.5, Y: -60, Z: 0.5}, 0.5, -60, 0.5, true)
	s.send(data.PlayClientboundSetHealth, func(e *protocol.Encoder) {
		packets.WriteSetHealth(e, packets.SetHealth{Health: 20, Food: 20, Saturation: 5})
	})
	s.sync()
	st = c.Self()
	if st.Dead || !st.Spawned || st.Health != 20 || st.Pos != (r3.Vec{X: 0.5, Y: -60, Z: 0.5}) {
		t.Errorf("リスポーンのあと %+v", st)
	}
	// 生き返ったあとの Respawn() はエラー
	if err := c.Respawn(); !errors.Is(err, ErrNotDead) {
		t.Errorf("生き返ったあとの Respawn() = %v、期待 ErrNotDead", err)
	}
}

// 自分の体のパケットを書いて読むと元に戻ること。
func TestSelfPacketsRoundTrip(t *testing.T) {
	e := protocol.NewEncoder()
	want := packets.DamageEvent{EntityID: 300, SourceTypeID: 20, SourceCauseID: -1, SourceDirectID: 7}
	packets.WriteDamageEvent(e, want)
	// 原因なし（-1）は 0、直接 7 は 8 として書かれる
	if b := e.Bytes(); len(b) != 6 || b[3] != 0 || b[4] != 8 || b[5] != 0 {
		t.Errorf("Damage Event のバイト列 %x", b)
	}
	got, err := packets.ReadDamageEvent(protocol.NewDecoder(e.Bytes()))
	if err != nil || got != want {
		t.Errorf("Damage Event %+v %v", got, err)
	}

	e = protocol.NewEncoder()
	packets.WriteSetHealth(e, packets.SetHealth{Health: 20, Food: 20, Saturation: 5})
	if e.Len() != 9 { // Float 4 + VarInt 1 + Float 4
		t.Errorf("Set Health %d バイト、期待 9", e.Len())
	}
	e = protocol.NewEncoder()
	must(t, packets.WritePlayerCombatKill(e, packets.PlayerCombatKill{PlayerID: 5, Message: "died"}))
	pk, err := packets.ReadPlayerCombatKill(protocol.NewDecoder(e.Bytes()))
	if err != nil || pk.PlayerID != 5 || pk.Message != "died" {
		t.Errorf("Player Combat Kill %+v %v", pk, err)
	}
}

func TestGameModeString(t *testing.T) {
	for m, want := range map[GameMode]string{Survival: "survival", Creative: "creative", Adventure: "adventure", Spectator: "spectator", 9: "GameMode(9)"} {
		if m.String() != want {
			t.Errorf("%d: %q", m, m.String())
		}
	}
}
