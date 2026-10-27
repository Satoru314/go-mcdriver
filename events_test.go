package mcdriver

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/chunk"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
	"gonum.org/v1/gonum/spatial/r3"
)

// playClientCfg は playClient と同じだが、設定を変えられる（Username は "worldbot" にする）。
func playClientCfg(t *testing.T, cfg Config) (*fakeServer, *Client) {
	t.Helper()
	s := newFakeServer(t)
	cfg.Addr, cfg.Username = s.addr(), "worldbot"
	ch := connectAsync(context.Background(), cfg)
	s.login("worldbot", 256)
	s.configure(true)
	s.enterPlay()
	r := wait(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	return s, r.c
}

// nextEvent は次の出来事を 1 つ受け取る。型が T でなければ失敗にする。
func nextEvent[T Event](t *testing.T, c *Client) T {
	t.Helper()
	select {
	case ev, ok := <-c.Events():
		if !ok {
			t.Fatalf("%T を待っていたがチャネルが閉じた", *new(T))
		}
		got, ok := ev.(T)
		if !ok {
			t.Fatalf("%T を待っていたが %T %+v が来た", *new(T), ev, ev)
		}
		if got.Time().IsZero() {
			t.Errorf("%T の時刻が空", got)
		}
		return got
	case <-time.After(2 * time.Second):
		t.Fatalf("%T が来ない", *new(T))
	}
	panic("unreachable")
}

// noEvent は、いま出来事がたまっていないことを確かめる。
func noEvent(t *testing.T, c *Client) {
	t.Helper()
	select {
	case ev := <-c.Events():
		t.Fatalf("出来事は無いはずが %T %+v", ev, ev)
	default:
	}
}

func (s *fakeServer) sendBlockUpdate(p BlockPos, id data.StateID) {
	s.send(data.PlayClientboundBlockUpdate, func(e *protocol.Encoder) {
		must(s.t, e.WritePosition(protocol.Position{X: p.X, Y: p.Y, Z: p.Z}))
		e.WriteVarInt(int32(id))
	})
}

// flatColumn はチャンク (0, 0) の、y=-64〜-61 が石でほかが空気のもの。
func flatColumn(t *testing.T) testColumn {
	stone, air := stateOf(t, "stone"), stateOf(t, "air")
	return testColumn{cx: 0, cz: 0, minY: -64, height: 384, block: func(x, y, z int) int32 {
		if y <= -61 {
			return int32(stone)
		}
		return int32(air)
	}}
}

// あふれたら古いものから捨て、数を数える。切断の知らせは最後に必ず入り、チャネルが閉じる。
func TestEventsOverflow(t *testing.T) {
	cl := &Client{cfg: Config{EventBuffer: 4}}
	cl.initFeatures()
	for i := range 10 {
		cl.emit(DeathEvent{eventBase: evNow(), Message: fmt.Sprint(i)})
	}
	if n := cl.EventsDropped(); n != 6 {
		t.Errorf("EventsDropped = %d、期待 6", n)
	}
	for i := 6; i < 10; i++ {
		ev := (<-cl.Events()).(DeathEvent)
		if ev.Message != fmt.Sprint(i) {
			t.Errorf("%d 番目 %q、期待 %d", i, ev.Message, i)
		}
	}
	for i := range 4 {
		cl.emit(DeathEvent{eventBase: evNow(), Message: fmt.Sprint(i)})
	}
	cl.emitDisconnect(ErrClosed)
	cl.emit(DeathEvent{eventBase: evNow()}) // 閉じたあとは何もしない（panic しない）
	var got []Event
	for ev := range cl.Events() {
		got = append(got, ev)
	}
	if len(got) != 4 {
		t.Fatalf("閉じるまでに %d 個、期待 4", len(got))
	}
	if d, ok := got[3].(DisconnectEvent); !ok || d.Err != ErrClosed {
		t.Errorf("最後 %+v、期待 DisconnectEvent{ErrClosed}", got[3])
	}
	if n := cl.EventsDropped(); n != 7 {
		t.Errorf("EventsDropped = %d、期待 7", n)
	}
}

// 誰も読まなくても受信の goroutine は止まらない（たくさんのブロックの変化のあとも Ping に答える）。
func TestEventsDoNotBlockReader(t *testing.T) {
	s, c := playClientCfg(t, Config{EventBuffer: 8})
	s.sendChunks(flatColumn(t))
	stone, air := stateOf(t, "stone"), stateOf(t, "air")
	const n = 500
	for i := range n {
		id := stone
		if i%2 == 1 {
			id = air
		}
		s.sendBlockUpdate(BlockPos{X: 1, Y: 0, Z: 1}, id)
	}
	done := make(chan struct{})
	go func() { s.sync(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("受信が止まっている（Pong が来ない）")
	}
	if d := c.EventsDropped(); d != n-8 {
		t.Errorf("EventsDropped = %d、期待 %d", d, n-8)
	}
	// 残っているのは最後の 8 個（石→空気で終わる）
	var last BlockChangeEvent
	for range 8 {
		last = nextEvent[BlockChangeEvent](t, c)
	}
	if last.Old.ID != stone || last.New.ID != air {
		t.Errorf("最後の変化 %v → %v、期待 石 → 空気", last.Old, last.New)
	}
	noEvent(t, c)
}

// 偽のサーバーから送ったパケットが、それぞれの出来事になる。
func TestEventsFromFakeServer(t *testing.T) {
	s, c := playClient(t)

	// 位置の強制修正
	s.teleport(packets.PlayerPosition{TeleportID: 5, X: 1.5, Y: -60, Z: 2.5, Yaw: 90, Pitch: 10}, 1.5, -60, 2.5, true)
	tp := nextEvent[TeleportEvent](t, c)
	if tp.Pos != (r3.Vec{X: 1.5, Y: -60, Z: 2.5}) || tp.Yaw != 90 || tp.Pitch != 10 || tp.TeleportID != 5 {
		t.Errorf("TeleportEvent %+v", tp)
	}

	// ブロックの変化（チャンクを受け取っただけでは知らせない。同じ状態への更新も知らせない）
	s.sendChunks(flatColumn(t))
	stone, air, dirt := stateOf(t, "stone"), stateOf(t, "air"), stateOf(t, "dirt")
	s.sendBlockUpdate(BlockPos{X: 3, Y: 0, Z: 5}, stone)
	s.sendBlockUpdate(BlockPos{X: 3, Y: 0, Z: 5}, stone)
	s.send(data.PlayClientboundSectionBlocksUpdate, func(e *protocol.Encoder) {
		e.WriteLong(chunk.SectionPos{X: 0, Y: -4, Z: 0}.Pack())
		e.WriteVarInt(2)
		e.WriteVarLong(chunk.PackBlockChange(chunk.BlockChange{X: 1, Y: 2, Z: 3, State: int32(dirt)})) // y = -64+2 = -62 の石 → 土
		e.WriteVarLong(chunk.PackBlockChange(chunk.BlockChange{X: 4, Y: 15, Z: 4, State: int32(air)})) // y = -49 はもともと空気
	})
	s.sendBlockUpdate(BlockPos{X: 100, Y: 0, Z: 100}, stone) // 読み込んでいないチャンクは知らせない
	s.sync()
	bc := nextEvent[BlockChangeEvent](t, c)
	if bc.Pos != (BlockPos{3, 0, 5}) || bc.Old.ID != air || bc.New.ID != stone || bc.New.Name() != "stone" {
		t.Errorf("BlockChangeEvent %+v", bc)
	}
	bc = nextEvent[BlockChangeEvent](t, c)
	if bc.Pos != (BlockPos{1, -62, 3}) || bc.Old.ID != stone || bc.New.ID != dirt {
		t.Errorf("Update Section Blocks の BlockChangeEvent %+v", bc)
	}
	noEvent(t, c)

	// 生き物の出現と消滅
	zombie := entType(t, "zombie")
	s.send(data.PlayClientboundAddEntity, func(e *protocol.Encoder) {
		packets.WriteSpawnEntity(e, packets.SpawnEntity{EntityID: 11, Type: int32(zombie.ID), X: 5, Y: -60, Z: 6})
	})
	s.send(data.PlayClientboundRemoveEntities, func(e *protocol.Encoder) { packets.WriteRemoveEntities(e, []int32{11, 99}) })
	s.sync()
	sp := nextEvent[EntitySpawnEvent](t, c)
	if sp.Entity.ID != 11 || sp.Entity.Type.Name != "zombie" || sp.Entity.Pos != (r3.Vec{X: 5, Y: -60, Z: 6}) {
		t.Errorf("EntitySpawnEvent %+v", sp)
	}
	rm := nextEvent[EntityRemoveEvent](t, c)
	if rm.Entity.ID != 11 || rm.Entity.Type.Name != "zombie" {
		t.Errorf("EntityRemoveEvent %+v", rm)
	}
	noEvent(t, c) // 知らない 99 は知らせない

	// 容器: サーバーが開いて閉じる、開いて自分で閉じる
	menu, _ := data.RegistryByName("menu")
	chest, _ := menu.ID("generic_9x3")
	open := func(win int32) {
		s.send(data.PlayClientboundOpenScreen, func(e *protocol.Encoder) {
			must(t, packets.WriteOpenScreen(e, packets.OpenScreen{WindowID: win, Type: chest, Title: map[string]any{"text": "Box"}}))
		})
	}
	open(3)
	s.send(data.PlayClientboundContainerClose, func(e *protocol.Encoder) { e.WriteVarInt(3) })
	s.sync()
	if o := nextEvent[ContainerOpenEvent](t, c); o.WindowID != 3 || o.Type != "generic_9x3" || o.Title != "Box" {
		t.Errorf("ContainerOpenEvent %+v", o)
	}
	if cc := nextEvent[ContainerCloseEvent](t, c); cc.WindowID != 3 || !cc.ByServer {
		t.Errorf("ContainerCloseEvent %+v", cc)
	}
	open(4)
	s.sync()
	nextEvent[ContainerOpenEvent](t, c)
	must(t, c.CloseContainer())
	if cc := nextEvent[ContainerCloseEvent](t, c); cc.WindowID != 4 || cc.ByServer {
		t.Errorf("自分で閉じた ContainerCloseEvent %+v", cc)
	}
	s.expect(data.PlayServerboundContainerClose, tickPackets)

	// ダメージ: 種類（Damage Event）のあとに HP が減る（Set Health）と 1 つの知らせになる
	health := func(h float32) {
		s.send(data.PlayClientboundSetHealth, func(e *protocol.Encoder) {
			packets.WriteSetHealth(e, packets.SetHealth{Health: h, Food: 20, Saturation: 5})
		})
	}
	health(20)
	s.send(data.PlayClientboundDamageEvent, func(e *protocol.Encoder) {
		packets.WriteDamageEvent(e, packets.DamageEvent{EntityID: 99, SourceCauseID: -1, SourceDirectID: -1}) // ほかの人へ
	})
	s.send(data.PlayClientboundDamageEvent, func(e *protocol.Encoder) {
		packets.WriteDamageEvent(e, packets.DamageEvent{EntityID: testLoginPlay.EntityID, SourceCauseID: 11, SourceDirectID: 11})
	})
	health(14)
	health(13.5) // 種類の無い減り方
	health(20)   // 増えたのは知らせない
	s.sync()
	dm := nextEvent[DamageEvent](t, c)
	if dm.Amount != 6 || dm.Health != 14 || dm.CauseEntityID != 11 {
		t.Errorf("DamageEvent %+v、期待 Amount 6 Health 14 Cause 11", dm)
	}
	if dm = nextEvent[DamageEvent](t, c); dm.Amount != 0.5 || dm.Health != 13.5 || dm.Type != "" || dm.CauseEntityID != -1 {
		t.Errorf("種類の無い DamageEvent %+v", dm)
	}
	noEvent(t, c)

	// 死亡とリスポーン
	s.send(data.PlayClientboundPlayerCombatKill, func(e *protocol.Encoder) {
		must(t, packets.WritePlayerCombatKill(e, packets.PlayerCombatKill{PlayerID: testLoginPlay.EntityID, Message: map[string]any{"text": "worldbot fell"}}))
	})
	health(0)
	s.send(data.PlayClientboundRespawn, func(e *protocol.Encoder) {
		must(t, packets.WriteRespawn(e, packets.Respawn{DimensionName: "minecraft:overworld", GameMode: 0, PreviousGameMode: -1}))
	})
	health(20)
	s.sync()
	if d := nextEvent[DeathEvent](t, c); d.Message != "worldbot fell" {
		t.Errorf("DeathEvent %+v", d)
	}
	if dm = nextEvent[DamageEvent](t, c); dm.Amount != 20 || dm.Health != 0 {
		t.Errorf("死んだときの DamageEvent %+v", dm)
	}
	if r := nextEvent[RespawnEvent](t, c); !r.AfterDeath || r.Dimension != "minecraft:overworld" || r.GameMode != Survival {
		t.Errorf("RespawnEvent %+v", r)
	}
	noEvent(t, c)

	// 天気
	gameEvent := func(ev uint8, v float32) {
		s.send(data.PlayClientboundGameEvent, func(e *protocol.Encoder) { packets.WriteGameEvent(e, packets.GameEvent{Event: ev, Value: v}) })
	}
	gameEvent(packets.GameEventBeginRaining, 0)
	gameEvent(packets.GameEventRainLevel, 0.1)
	s.sync()
	noEvent(t, c) // 0.2 までは晴れ
	if w := c.Weather(); w.Kind != Clear || !w.Raining || w.RainLevel != 0.1 {
		t.Errorf("降り始めの Weather %+v", w)
	}
	gameEvent(packets.GameEventRainLevel, 0.5)
	gameEvent(packets.GameEventRainLevel, 1)
	gameEvent(packets.GameEventThunderLevel, 0.95)
	s.sync()
	if w := nextEvent[WeatherEvent](t, c).Weather; w.Kind != Rain {
		t.Errorf("WeatherEvent %+v、期待 雨", w)
	}
	if w := nextEvent[WeatherEvent](t, c).Weather; w.Kind != Thunder || w.ThunderLevel != 0.95 {
		t.Errorf("WeatherEvent %+v、期待 雷雨", w)
	}
	gameEvent(packets.GameEventEndRaining, 0)
	gameEvent(packets.GameEventRainLevel, 0)
	s.sync()
	if w := nextEvent[WeatherEvent](t, c).Weather; w.Kind != Clear || w.Raining {
		t.Errorf("WeatherEvent %+v、期待 晴れ", w)
	}
	if w := c.Weather(); w.Kind != Clear || w.ThunderLevel != 0.95 {
		t.Errorf("止んだあとの Weather %+v", w)
	}

	// 時刻（進まないとき）
	s.send(data.PlayClientboundSetTime, func(e *protocol.Encoder) {
		packets.WriteSetTime(e, packets.SetTime{WorldAge: 1000, DayTime: 3*TicksPerDay + 6000, Ticking: false})
	})
	s.sync()
	time.Sleep(120 * time.Millisecond)
	if day, tod := c.Time(); day != 3 || tod != 6000 {
		t.Errorf("Time = %d, %d、期待 3, 6000", day, tod)
	}
	if ti := c.TimeInfo(); !ti.Known || ti.WorldAge < 1002 || ti.Ticking {
		t.Errorf("TimeInfo %+v（WorldAge は補間で 1002 以上）", ti)
	}

	// 切断: 最後に DisconnectEvent が来て閉じる
	s.nc.Close()
	dc := nextEvent[DisconnectEvent](t, c)
	if dc.Err == nil || dc.Err != c.Err() {
		t.Errorf("DisconnectEvent.Err %v、Client.Err %v", dc.Err, c.Err())
	}
	if _, ok := <-c.Events(); ok {
		t.Error("DisconnectEvent のあとにチャネルが閉じていない")
	}
}

// 種類は届いたが HP が減らないダメージは、1 秒たってから Amount 0 で知らせる。
func TestDamageWithoutHealthDrop(t *testing.T) {
	s, c := playClient(t)
	s.send(data.PlayClientboundSetHealth, func(e *protocol.Encoder) {
		packets.WriteSetHealth(e, packets.SetHealth{Health: 20, Food: 20})
	})
	s.send(data.PlayClientboundDamageEvent, func(e *protocol.Encoder) {
		packets.WriteDamageEvent(e, packets.DamageEvent{EntityID: testLoginPlay.EntityID, SourceCauseID: -1, SourceDirectID: -1})
	})
	s.sync()
	noEvent(t, c)
	time.Sleep(damageFlushAfter + 50*time.Millisecond)
	s.send(data.PlayClientboundSetTime, func(e *protocol.Encoder) { packets.WriteSetTime(e, packets.SetTime{}) })
	s.sync()
	if dm := nextEvent[DamageEvent](t, c); dm.Amount != 0 || dm.Health != 20 {
		t.Errorf("DamageEvent %+v、期待 Amount 0 Health 20", dm)
	}
}

// 時刻の補間: 進んでいるときだけ、受け取ってからのティックの数だけ進める（上限あり）。日をまたぐ。
func TestTimeInterpolation(t *testing.T) {
	recv := time.Unix(1000, 0)
	at := func(ms int) time.Time { return recv.Add(time.Duration(ms) * time.Millisecond) }
	st := packets.SetTime{WorldAge: 500, DayTime: TicksPerDay - 10, Ticking: true}
	cases := []struct {
		t        packets.SetTime
		ms       int
		age, day int64
		tod      int64
	}{
		{st, 0, 500, 0, TicksPerDay - 10},
		{st, 49, 500, 0, TicksPerDay - 10},
		{st, 500, 510, 1, 0},                                             // 10 ティックで日をまたぐ
		{st, 60_000, 600, 1, 90},                                         // 上限 100 ティック
		{st, -1000, 500, 0, 23990},                                       // 時計が戻っても進めない
		{packets.SetTime{WorldAge: 7, DayTime: 1000}, 1000, 27, 0, 1000}, // 止まっていれば時刻は進まない（年齢は進む）
		{packets.SetTime{DayTime: -1}, 0, 0, -1, TicksPerDay - 1},
	}
	for i, tc := range cases {
		ti := timeInfoAt(tc.t, true, recv, at(tc.ms))
		if ti.WorldAge != tc.age || ti.Day != tc.day || ti.TimeOfDay != tc.tod || ti.DayTime != tc.day*TicksPerDay+tc.tod {
			t.Errorf("%d: %+v、期待 age %d day %d tod %d", i, ti, tc.age, tc.day, tc.tod)
		}
	}
	if ti := timeInfoAt(st, false, recv, recv); ti != (TimeInfo{}) {
		t.Errorf("受け取る前 %+v", ti)
	}
}

// 天気の種類の決まり（本家の isRaining / isThundering）。
func TestWeatherKind(t *testing.T) {
	cases := []struct {
		rain, thunder float32
		want          WeatherKind
	}{
		{0, 0, Clear}, {0.2, 0, Clear}, {0.21, 0, Rain}, {1, 0.9, Rain}, {1, 0.91, Thunder},
		{0.5, 1, Rain}, {0, 1, Clear}, {0.95, 0.96, Thunder},
	}
	for _, c := range cases {
		if got := (Weather{RainLevel: c.rain, ThunderLevel: c.thunder}).kind(); got != c.want {
			t.Errorf("雨 %v 雷 %v → %v、期待 %v", c.rain, c.thunder, got, c.want)
		}
	}
	if Thunder.String() != "thunder" || Clear.String() != "clear" || Rain.String() != "rain" {
		t.Error("WeatherKind.String")
	}
}
