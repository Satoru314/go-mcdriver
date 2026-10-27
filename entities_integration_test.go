//go:build integration

// 単位 6（生き物と物）を本物のサーバー（internal/testserver の Docker の Paper 1.21.4）で確かめるテスト。
//
//	sg docker -c 'go test -tags integration -run TestEntitiesIntegration -v -count=1 -timeout 110s .'
//
// 確かめること:
//   - RCON で summon した、動かない（NoAI）ゾンビ・羊・豚の種類と位置（RCON の data get と一致）、当たり判定の箱
//   - damage で体力が減る、Fire で燃えている
//   - tp で位置が追従する、kill で消える
//   - summon item の落ちているアイテムの種類と個数（部品情報つきのものも）
//   - 2 体目の bot がプレイヤーとして名前つきで見える
//
// テストの終わりに、自分が出したエンティティ（タグ付き）を消す。
package mcdriver

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
	"github.com/Satoru314/go-mcdriver/internal/rcon"
	"github.com/Satoru314/go-mcdriver/internal/testserver"
	"gonum.org/v1/gonum/spatial/r3"
)

// entPacketCounts は bot ごとの、エンティティに関わるパケットの数（本物のサーバーがどれを使うかの記録）。
var entPacketCounts = struct {
	sync.Mutex
	m map[string]map[int32]int
}{m: map[string]map[int32]int{}}

func init() {
	for _, id := range []int32{
		data.PlayClientboundAddEntity, data.PlayClientboundRemoveEntities, data.PlayClientboundMoveEntityPos,
		data.PlayClientboundMoveEntityPosRot, data.PlayClientboundMoveEntityRot, data.PlayClientboundEntityPositionSync,
		data.PlayClientboundTeleportEntity, data.PlayClientboundSetEntityMotion, data.PlayClientboundRotateHead,
		data.PlayClientboundSetEntityData, data.PlayClientboundPlayerInfoUpdate, data.PlayClientboundPlayerInfoRemove,
	} {
		onPlay(id, func(cl *Client, d *protocol.Decoder) error {
			entPacketCounts.Lock()
			defer entPacketCounts.Unlock()
			m := entPacketCounts.m[cl.cfg.Username]
			if m == nil {
				m = map[int32]int{}
				entPacketCounts.m[cl.cfg.Username] = m
			}
			m[id]++
			return nil
		})
	}
}

func entLogPacketCounts(t *testing.T, name string) {
	entPacketCounts.Lock()
	defer entPacketCounts.Unlock()
	var b strings.Builder
	for id, n := range entPacketCounts.m[name] {
		pk, _ := data.PacketByID(data.StatePlay, "clientbound", id)
		fmt.Fprintf(&b, "  %s(0x%02X) %d\n", pk.Name, id, n)
	}
	t.Logf("%s が受け取ったエンティティのパケット:\n%s", name, b.String())
}

// entRCONData は data get entity の応答から「data: 」のあとを返す。
func entRCONData(t *testing.T, rc *rcon.Client, sel, path string) string {
	t.Helper()
	out := testserver.Command(t, rc, fmt.Sprintf("data get entity %s %s", sel, path))
	i := strings.Index(out, "entity data: ")
	if i < 0 {
		t.Fatalf("data get entity %s %s の応答: %q", sel, path, out)
	}
	return strings.TrimSpace(out[i+len("entity data: "):])
}

// entRCONUUID は RCON でエンティティの UUID（[I; a, b, c, d]）を読む。
func entRCONUUID(t *testing.T, rc *rcon.Client, sel string) UUID {
	t.Helper()
	s := entRCONData(t, rc, sel, "UUID")
	s = strings.TrimSuffix(strings.TrimPrefix(s, "[I;"), "]")
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		t.Fatalf("UUID の形: %q", s)
	}
	var u UUID
	for i, p := range parts {
		v, err := strconv.ParseInt(strings.TrimSpace(p), 10, 32)
		if err != nil {
			t.Fatalf("UUID の形: %q", s)
		}
		x := uint32(int32(v))
		u[i*4], u[i*4+1], u[i*4+2], u[i*4+3] = byte(x>>24), byte(x>>16), byte(x>>8), byte(x)
	}
	return u
}

// entRCONPos は RCON でエンティティの位置（[x d, y d, z d]）を読む。
func entRCONPos(t *testing.T, rc *rcon.Client, sel string) r3.Vec {
	t.Helper()
	s := entRCONData(t, rc, sel, "Pos")
	s = strings.Trim(s, "[]")
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		t.Fatalf("Pos の形: %q", s)
	}
	var v [3]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(p), "d"), 64)
		if err != nil {
			t.Fatalf("Pos の形: %q", s)
		}
		v[i] = f
	}
	return r3.Vec{X: v[0], Y: v[1], Z: v[2]}
}

// entWait は cond が true になるまで（最長 wait）待ち、最後に見たエンティティと結果を返す。
func entWait(cl *Client, wait time.Duration, cond func([]Entity) bool) ([]Entity, bool) {
	deadline := time.Now().Add(wait)
	for {
		es := cl.Entities()
		if cond(es) {
			return es, true
		}
		if time.Now().After(deadline) {
			return es, false
		}
		select {
		case <-cl.Done():
			return es, false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func entByUUID(es []Entity, u UUID) (Entity, bool) {
	for _, e := range es {
		if e.UUID == u {
			return e, true
		}
	}
	return Entity{}, false
}

func entNear(a, b r3.Vec, eps float64) bool {
	return math.Abs(a.X-b.X) <= eps && math.Abs(a.Y-b.Y) <= eps && math.Abs(a.Z-b.Z) <= eps
}

func TestEntitiesIntegration(t *testing.T) {
	ts := testserver.Start(t)
	rc := ts.RCON(t)
	area := ts.NewArea(t)
	area.ForceLoad(t, rc)
	name := area.BotName
	tag := fmt.Sprintf("mcd6_%d", area.ID)
	defer func() {
		if t.Failed() {
			t.Logf("サーバーのログ（最後の 30 行）:\n%s", testserver.Logs(context.Background(), 30))
		}
	}()
	// 終わりに、出したエンティティを消す
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := rc.Command(ctx, fmt.Sprintf("kill @e[tag=%s]", tag))
		t.Logf("後片付け: %q %v", out, err)
	})

	cx, cz := area.Center()
	y := testserver.StandY

	cl, err := entConnect(t, ts.GameAddr, name)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer cl.Close()
	defer entLogPacketCounts(t, name)
	testserver.Command(t, rc, fmt.Sprintf("tp %s %d.5 %d %d.5", name, cx, y, cz))

	// ---- 動かない生き物を出す ----
	// ゾンビは日に当たると燃えるので、頭の上に石を置く
	type mob struct {
		kind   string
		dx, dz int
	}
	mobs := []mob{{"zombie", 4, 0}, {"sheep", -4, 0}, {"pig", 0, 4}}
	testserver.Command(t, rc, fmt.Sprintf("setblock %d %d %d minecraft:stone", cx+4, y+2, cz))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rc.Command(ctx, fmt.Sprintf("setblock %d %d %d minecraft:air", cx+4, y+2, cz))
	})
	uuids := map[string]UUID{}
	for _, m := range mobs {
		mt := tag + "_" + m.kind
		out := testserver.Command(t, rc, fmt.Sprintf("summon minecraft:%s %d.5 %d %d.5 {NoAI:1b,Silent:1b,PersistenceRequired:1b,Tags:[%q,%q]}",
			m.kind, cx+m.dx, y, cz+m.dz, tag, mt))
		if !strings.Contains(out, "Summoned") {
			t.Fatalf("summon %s: %q", m.kind, out)
		}
		uuids[m.kind] = entRCONUUID(t, rc, fmt.Sprintf("@e[tag=%s,limit=1]", mt))
	}
	es, ok := entWait(cl, 5*time.Second, func(es []Entity) bool {
		for _, u := range uuids {
			if _, ok := entByUUID(es, u); !ok {
				return false
			}
		}
		return true
	})
	if !ok {
		t.Fatalf("summon した生き物が 5 秒で見えない。見えているもの: %+v", es)
	}
	for _, m := range mobs {
		e, _ := entByUUID(es, uuids[m.kind])
		want := entRCONPos(t, rc, fmt.Sprintf("@e[tag=%s_%s,limit=1]", tag, m.kind))
		et, _ := data.EntityTypeByName(m.kind)
		t.Logf("%s: ID %d 位置 %v（RCON %v）向き %v/%v 体力 %v(%v) 箱 %v", m.kind, e.ID, e.Pos, want, e.Yaw, e.Pitch, e.Health, e.HasHealth, e.Box)
		if e.Type.Name != m.kind {
			t.Errorf("%s の種類が %q", m.kind, e.Type.Name)
		}
		if !entNear(e.Pos, want, 1e-6) {
			t.Errorf("%s の位置 %v、RCON では %v", m.kind, e.Pos, want)
		}
		hw := float64(et.Width) / 2
		if !entNear(e.Box.Min, r3.Vec{X: want.X - hw, Y: want.Y, Z: want.Z - hw}, 1e-6) ||
			!entNear(e.Box.Max, r3.Vec{X: want.X + hw, Y: want.Y + float64(et.Height), Z: want.Z + hw}, 1e-6) {
			t.Errorf("%s の当たり判定 %v", m.kind, e.Box)
		}
		if !e.HasHealth {
			t.Errorf("%s の体力を受け取っていない", m.kind)
		}
	}

	// ---- 体力と火 ----
	pigSel := fmt.Sprintf("@e[tag=%s_pig,limit=1]", tag)
	before, _ := cl.Entity(mustFind(t, es, uuids["pig"]).ID)
	testserver.Command(t, rc, fmt.Sprintf("damage %s 3", pigSel))
	es, ok = entWait(cl, 3*time.Second, func(es []Entity) bool {
		e, ok := entByUUID(es, uuids["pig"])
		return ok && e.Health == before.Health-3
	})
	if !ok {
		e, _ := entByUUID(es, uuids["pig"])
		t.Errorf("damage 3 のあとの豚の体力 %v、期待 %v（RCON %s）", e.Health, before.Health-3, entRCONData(t, rc, pigSel, "Health"))
	}
	testserver.Command(t, rc, fmt.Sprintf("data merge entity %s {Fire:200s}", pigSel))
	if _, ok = entWait(cl, 3*time.Second, func(es []Entity) bool {
		e, ok := entByUUID(es, uuids["pig"])
		return ok && e.OnFire
	}); !ok {
		t.Errorf("Fire を付けた豚が燃えていない")
	}
	testserver.Command(t, rc, fmt.Sprintf("data merge entity %s {Fire:0s}", pigSel))
	if _, ok = entWait(cl, 3*time.Second, func(es []Entity) bool {
		e, ok := entByUUID(es, uuids["pig"])
		return ok && !e.OnFire
	}); !ok {
		t.Errorf("火を消した豚が燃えたまま")
	}

	// ---- tp で追従する（近い移動と遠い移動）----
	sheepSel := fmt.Sprintf("@e[tag=%s_sheep,limit=1]", tag)
	for _, d := range []struct {
		x, y, z float64
		yaw     float32
	}{
		{float64(cx) - 2.25, float64(y), float64(cz) + 3.75, 90},
		{float64(cx) - 9.5, float64(y) + 3, float64(cz) - 6.125, -45}, // 宙に浮かせる（NoAI でも落ちる）
		{float64(cx) - 3.5, float64(y), float64(cz) - 0.5, 0},
	} {
		testserver.Command(t, rc, fmt.Sprintf("tp %s %v %v %v %v 0", sheepSel, d.x, d.y, d.z, d.yaw))
		time.Sleep(1500 * time.Millisecond) // 落ちきるまで待つ
		want := entRCONPos(t, rc, sheepSel)
		es, ok = entWait(cl, 3*time.Second, func(es []Entity) bool {
			e, ok := entByUUID(es, uuids["sheep"])
			return ok && entNear(e.Pos, want, 1e-3)
		})
		e, _ := entByUUID(es, uuids["sheep"])
		t.Logf("羊の tp %v → 見えた位置 %v（RCON %v）yaw %v", d, e.Pos, want, e.Yaw)
		if !ok {
			t.Errorf("tp のあとの羊の位置 %v、RCON では %v", e.Pos, want)
		}
	}

	// ---- 落ちているアイテム ----
	type drop struct {
		key, item  string
		count      int
		components string
		dx, dz     int
	}
	drops := []drop{
		{"diamond", "diamond", 7, "", 2, -3},
		{"sword", "diamond_sword", 1, `,components:{"minecraft:custom_name":'"Blade"',"minecraft:enchantments":{levels:{"minecraft:sharpness":3}},"minecraft:damage":5}`, -2, -3},
		{"box", "shulker_box", 1, `,components:{"minecraft:container":[{slot:0,item:{id:"minecraft:apple",count:3}},{slot:1,item:{id:"minecraft:potion",count:1,components:{"minecraft:potion_contents":{potion:"minecraft:swiftness"}}}}]}`, 0, -5},
		{"potion", "potion", 1, `,components:{"minecraft:potion_contents":{potion:"minecraft:strong_healing",custom_color:255}}`, 3, 3},
	}
	dropUUIDs := map[string]UUID{}
	for _, d := range drops {
		dt := tag + "_" + d.key
		out := testserver.Command(t, rc, fmt.Sprintf(`summon minecraft:item %d.5 %d %d.5 {Item:{id:"minecraft:%s",count:%d%s},PickupDelay:32767,Tags:[%q,%q]}`,
			cx+d.dx, y, cz+d.dz, d.item, d.count, d.components, tag, dt))
		if !strings.Contains(out, "Summoned") {
			t.Fatalf("summon item %s: %q", d.key, out)
		}
		dropUUIDs[d.key] = entRCONUUID(t, rc, fmt.Sprintf("@e[tag=%s,limit=1]", dt))
	}
	es, ok = entWait(cl, 5*time.Second, func(es []Entity) bool {
		for _, u := range dropUUIDs {
			if e, ok := entByUUID(es, u); !ok || e.Item.Count == 0 {
				return false
			}
		}
		return true
	})
	if !ok {
		t.Errorf("落ちているアイテムが 5 秒で全部見えない")
	}
	for _, d := range drops {
		e, ok := entByUUID(es, dropUUIDs[d.key])
		t.Logf("アイテム %s: %+v 位置 %v", d.key, e.Item, e.Pos)
		if !ok || e.Type.Name != "item" || e.Item.Name != d.item || e.Item.Count != d.count {
			t.Errorf("アイテム %s: 見えた %v、種類 %q、中身 %+v（期待 %s ×%d）", d.key, ok, e.Type.Name, e.Item, d.item, d.count)
		}
	}
	if es := entityKey.of(cl); es.metaErrors != 0 {
		t.Errorf("読めなかったメタデータ %d 個: %v", es.metaErrors, es.lastMetaErr)
	}

	// ---- 2 体目の bot がプレイヤーとして見える ----
	name2 := name + "b"
	cl2, err := entConnect(t, ts.GameAddr, name2)
	if err != nil {
		t.Fatalf("2 体目の Connect: %v", err)
	}
	defer cl2.Close()
	testserver.Command(t, rc, fmt.Sprintf("tp %s %d.5 %d %d.5", name2, cx+1, y, cz+2))
	es, ok = entWait(cl, 5*time.Second, func(es []Entity) bool {
		for _, e := range es {
			if e.Type.Name == "player" && e.Name == name2 && entNear(e.Pos, r3.Vec{X: float64(cx) + 1.5, Y: float64(y), Z: float64(cz) + 2.5}, 1e-3) {
				return true
			}
		}
		return false
	})
	for _, e := range es {
		if e.Type.Name == "player" {
			t.Logf("プレイヤー: ID %d 名前 %q UUID %v 位置 %v", e.ID, e.Name, e.UUID, e.Pos)
		}
	}
	if !ok {
		t.Errorf("2 体目の bot %s がプレイヤーとして見えない（名前 %v）", name2, cl.PlayerNames())
	}
	// 2 体目からは 1 体目が見える
	if _, ok := entWait(cl2, 5*time.Second, func(es []Entity) bool {
		for _, e := range es {
			if e.Type.Name == "player" && e.Name == name {
				return true
			}
		}
		return false
	}); !ok {
		t.Errorf("2 体目から 1 体目 %s が見えない", name)
	}
	cl2.Close()
	if _, ok := entWait(cl, 5*time.Second, func(es []Entity) bool {
		for _, e := range es {
			if e.Type.Name == "player" {
				return false
			}
		}
		return true
	}); !ok {
		t.Errorf("2 体目が抜けたのにプレイヤーが残る")
	}
	if _, ok := cl.PlayerNames()[UUID{}]; ok {
		t.Errorf("空の UUID が一覧にある")
	}

	// ---- kill で消える ----
	testserver.Command(t, rc, fmt.Sprintf("kill @e[tag=%s]", tag))
	es, ok = entWait(cl, 5*time.Second, func(es []Entity) bool {
		for _, u := range uuids {
			if _, ok := entByUUID(es, u); ok {
				return false
			}
		}
		for _, u := range dropUUIDs {
			if _, ok := entByUUID(es, u); ok {
				return false
			}
		}
		return true
	})
	if !ok {
		t.Errorf("kill したのに消えない: %+v", es)
	}

	if err := cl.Err(); err != nil {
		t.Errorf("Err %v", err)
	}
}

func mustFind(t *testing.T, es []Entity, u UUID) Entity {
	t.Helper()
	e, ok := entByUUID(es, u)
	if !ok {
		t.Fatalf("UUID %v のエンティティが無い", u)
	}
	return e
}

// entConnect は Connect する。テスト用サーバーは同じ IP からの接続を 4 秒に 1 回に絞る（Paper の connection-throttle）ので、
// ほかのテストと重なって「Connection throttled」で断られたら、少し待ってやり直す。
func entConnect(t *testing.T, addr, name string) (*Client, error) {
	t.Helper()
	for i := 0; ; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cl, err := Connect(ctx, Config{Addr: addr, Username: name})
		cancel()
		if err == nil || i >= 5 || !strings.Contains(err.Error(), "throttled") {
			return cl, err
		}
		t.Logf("%s の接続が絞られた（%v）。5 秒後にやり直す", name, err)
		time.Sleep(5 * time.Second)
	}
}
