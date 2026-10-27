//go:build integration

// 単位 8（持ち物）を本物のサーバー（internal/testserver の Docker の Paper 1.21.4）で確かめるテスト。
//
//	sg docker -c 'go test -tags integration -run TestInventoryIntegration -v -count=1 -timeout 110s .'
//
// 合格の条件:
//   - give で渡したもの（石 64、エンチャントつきダイヤの剣、名前つきのリンゴ、耐久の減ったつるはし、ポーション）が
//     正しいスロットと個数で出る
//   - 部品情報の多いアイテム（本、花火、染めた革、シュルカーボックスの中身、旗の模様、防具の装飾など）を渡しても
//     切断されず、読めなかったパケットが 0
//   - SelectHotbar がサーバーに反映される（data get entity <bot> SelectedItemSlot）
//   - ClickSlot（拾う・置く・数字キーの入れ替え）がサーバーに受け入れられ、RCON で見たスロットと合う。
//     部品つきの剣を動かしても受け入れられる（予測につけた部品情報がサーバーのものと一致する）
//   - Drop で捨てたものが減る（RCON の clear で数える）
//
// 容器を開くのは単位 9（UseBlock）なので、ここでは持ち物の窓だけを試す。
package mcdriver

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
	"github.com/Satoru314/go-mcdriver/internal/testserver"
)

// invTrace は bot ごとの、持ち物のパケットの記録（失敗したときに見るため）。
var invTrace = struct {
	sync.Mutex
	m map[string][]string
}{m: map[string][]string{}}

func init() {
	for _, id := range []int32{
		data.PlayClientboundContainerSetContent, data.PlayClientboundContainerSetSlot, data.PlayClientboundSetPlayerInventory,
		data.PlayClientboundSetCursorItem, data.PlayClientboundSetHeldSlot, data.PlayClientboundOpenScreen, data.PlayClientboundContainerClose,
	} {
		onPlay(id, func(cl *Client, d *protocol.Decoder) error {
			pk, _ := data.PacketByID(data.StatePlay, "clientbound", id)
			raw := d.ReadRest()
			if len(raw) > 300 {
				raw = raw[:300]
			}
			line := fmt.Sprintf("%s %s %x", time.Now().Format("15:04:05.000"), pk.Name, raw)
			invTrace.Lock()
			defer invTrace.Unlock()
			invTrace.m[cl.cfg.Username] = append(invTrace.m[cl.cfg.Username], line)
			return nil
		})
	}
}

func logInvTrace(t *testing.T, name string) {
	invTrace.Lock()
	defer invTrace.Unlock()
	t.Logf("%s が受け取った持ち物のパケット:\n  %s", name, strings.Join(invTrace.m[name], "\n  "))
}

// waitInv は cond が true になるまで Inventory() を見続ける。
func waitInv(t *testing.T, cl *Client, timeout time.Duration, desc string, cond func(Inventory) bool) Inventory {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		inv := cl.Inventory()
		if cond(inv) {
			return inv
		}
		select {
		case <-cl.Done():
			t.Fatalf("%s を待つあいだに切断された: %v", desc, cl.Err())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("%v 待っても %s にならない。持ち物 = %v", timeout, desc, describeInv(inv))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func describeInv(inv Inventory) string {
	var b strings.Builder
	for i, s := range inv.Slots {
		if !s.Empty() {
			fmt.Fprintf(&b, "%d:%v ", i, s)
		}
	}
	fmt.Fprintf(&b, "カーソル:%v 選択:%d Stale:%v", inv.Cursor, inv.Selected, inv.Stale)
	return b.String()
}

var foundRe = regexp.MustCompile(`Found (\d+) matching item`)

func TestInventoryIntegration(t *testing.T) {
	ts := testserver.Start(t)
	rc := ts.RCON(t)
	area := ts.NewArea(t)
	name := area.BotName
	defer func() {
		if t.Failed() {
			logInvTrace(t, name)
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
	// count は bot が持っている item の数を RCON の clear（0 個消す = 数えるだけ）で数える
	count := func(item string) int {
		t.Helper()
		out := cmd(fmt.Sprintf("clear %s %s 0", name, item))
		m := foundRe.FindStringSubmatch(out)
		if m == nil {
			return 0
		}
		var n int
		fmt.Sscan(m[1], &n)
		return n
	}
	// has は持ち物の番号 idx（container.N）に item があるかを RCON で確かめる
	has := func(idx int, item string) bool {
		t.Helper()
		return strings.Contains(cmd(fmt.Sprintf("execute if items entity %s container.%d %s", name, idx, item)), "passed")
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
	waitSelf(t, cl, 5*time.Second, "位置を受け取る", func(s Self) bool { return s.Spawned })
	x, z := area.Center()
	cmd(fmt.Sprintf("tp %s %d.5 %d %d.5", name, x, testserver.StandY, z))
	cmd("gamemode survival " + name)
	cmd("clear " + name)
	waitInv(t, cl, 5*time.Second, "空の持ち物", func(inv Inventory) bool { return inv.Count("stone") == 0 })

	// ---- give で渡したものが正しいスロットに出る（ホットバー 0 から順に入る） ----
	gives := []struct {
		item string
		n    int
		want string
	}{
		{"stone", 64, "stone"},
		{`diamond_sword[enchantments={levels:{"minecraft:sharpness":5,"minecraft:unbreaking":3}}]`, 1, "diamond_sword"},
		{`apple[custom_name='{"text":"Ringo","color":"red"}']`, 5, "apple"},
		{"diamond_pickaxe[damage=100]", 1, "diamond_pickaxe"},
		{`potion[potion_contents={potion:"minecraft:swiftness"}]`, 1, "potion"},
	}
	for _, g := range gives {
		cmd(fmt.Sprintf("give %s %s %d", name, g.item, g.n))
	}
	inv := waitInv(t, cl, 5*time.Second, "give したもの", func(inv Inventory) bool { return inv.Hotbar(4).Name() == "potion" })
	for i, g := range gives {
		if s := inv.Hotbar(i); s.Name() != g.want || s.Count != g.n {
			t.Errorf("ホットバー %d（窓の %d）= %v、期待 %s×%d", i, SlotHotbar+i, s, g.want, g.n)
		}
	}

	// ---- 部品情報の多いアイテム ----
	exotic := []string{
		`written_book[written_book_content={title:"T",author:"A",pages:['"p1"','{"text":"p2","bold":true}']}]`,
		`writable_book[writable_book_content={pages:["hello","world"]}]`,
		`firework_rocket[fireworks={flight_duration:2,explosions:[{shape:"star",colors:[I;16711680],fade_colors:[I;255],has_trail:true}]}]`,
		`firework_star[firework_explosion={shape:"creeper",colors:[I;65280]}]`,
		`leather_chestplate[dyed_color={rgb:16711680}]`,
		`shulker_box[container=[{slot:0,item:{id:"minecraft:diamond_sword",count:1,components:{"minecraft:damage":5,"minecraft:enchantments":{levels:{"minecraft:sharpness":1}}}}},{slot:3,item:{id:"minecraft:stone",count:10}}]]`,
		`crossbow[charged_projectiles=[{id:"minecraft:arrow",count:1}]]`,
		`tipped_arrow[potion_contents={potion:"minecraft:poison"}]`,
		`potion[potion_contents={custom_color:16711680,custom_effects:[{id:"minecraft:speed",amplifier:1,duration:200}]}]`,
		`suspicious_stew[suspicious_stew_effects=[{id:"minecraft:night_vision",duration:100}]]`,
		`goat_horn[instrument="minecraft:ponder_goat_horn"]`,
		`white_banner[banner_patterns=[{pattern:"minecraft:creeper",color:"red"},{pattern:"minecraft:stripe_top",color:"blue"}]]`,
		`diamond_chestplate[trim={material:"minecraft:gold",pattern:"minecraft:coast"}]`,
		`bundle[bundle_contents=[{id:"minecraft:apple",count:2}]]`,
		`compass[lodestone_tracker={target:{pos:[I;1,2,3],dimension:"minecraft:overworld"},tracked:false}]`,
		`iron_pickaxe[can_break={blocks:"minecraft:stone"},can_place_on={blocks:["minecraft:dirt","minecraft:grass_block"]}]`,
		`iron_sword[attribute_modifiers={modifiers:[{type:"minecraft:attack_damage",id:"test:dmg",amount:5,operation:"add_value",slot:"mainhand"}]}]`,
		`stick[tool={rules:[{blocks:"#minecraft:mineable/pickaxe",speed:8,correct_for_drops:true}]}]`,
		`stick[food={nutrition:2,saturation:1},consumable={consume_seconds:2,on_consume_effects:[{type:"minecraft:apply_effects",effects:[{id:"minecraft:speed",duration:100}]}]}]`,
		`stick[lore=['"a"','"b"'],rarity="epic",custom_model_data={floats:[1.0],strings:["x"]},enchantment_glint_override=true,max_stack_size=5]`,
		`enchanted_book[stored_enchantments={levels:{"minecraft:mending":1}}]`,
		`stick[equippable={slot:"head"},repair_cost=3,unbreakable={},custom_data={a:1}]`,
		`stick[death_protection={death_effects:[{type:"minecraft:clear_all_effects"}]},use_cooldown={seconds:1},damage_resistant={types:"#minecraft:is_fire"}]`,
		`stick[use_remainder={id:"minecraft:bowl",count:1}]`,
		`player_head[profile={name:"Steve"}]`,
		`beehive[block_state={honey_level:"3"}]`,
		`stone[!minecraft:max_stack_size]`,
	}
	given := 0
	for _, it := range exotic {
		out := cmd(fmt.Sprintf("give %s %s 1", name, it))
		if strings.Contains(out, "Gave") {
			given++
		} else {
			t.Logf("give できなかった（書き方の誤り。テストの対象外）: %s → %q", it, out)
		}
	}
	inv = waitInv(t, cl, 5*time.Second, "部品の多いものを受け取る", func(inv Inventory) bool {
		n := 0
		for _, s := range inv.Slots {
			if !s.Empty() {
				n++
			}
		}
		return n >= len(gives)+given
	})
	t.Logf("give できた部品の多いもの %d / %d。持ち物: %s", given, len(exotic), describeInv(inv))
	if n, last := cl.InventoryReadErrors(); n != 0 || inv.Stale {
		t.Errorf("読めなかったパケット %d（最後 %v）、Stale %v", n, last, inv.Stale)
	}
	if given < len(exotic)-3 {
		t.Errorf("give できたものが少ない: %d / %d", given, len(exotic))
	}

	// ---- SelectHotbar ----
	if err := cl.SelectHotbar(3); err != nil {
		t.Fatal(err)
	}
	var out string
	for range 20 {
		out = cmd(fmt.Sprintf("data get entity %s SelectedItemSlot", name))
		if strings.HasSuffix(strings.TrimSpace(out), ": 3") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), ": 3") {
		t.Errorf("SelectedItemSlot: %q、期待 3", out)
	}
	if h := cl.Inventory().Held(); h.Name() != "diamond_pickaxe" {
		t.Errorf("手に持っているもの %v", h)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	click := func(slot, button int, mode ClickMode) {
		t.Helper()
		start := time.Now()
		if err := cl.ClickSlot(ctx, slot, button, mode); err != nil {
			t.Fatalf("ClickSlot(%d, %d, %d): %v。持ち物 %s", slot, button, mode, err, describeInv(cl.Inventory()))
		}
		t.Logf("ClickSlot(%d, %d, %d) が受け入れられた（%v）", slot, button, mode, time.Since(start))
	}

	// ---- ClickSlot: 石 64 を拾って、空いているスロット（中の最後 35）に置く ----
	if !cl.Inventory().Slots[35].Empty() {
		t.Fatalf("スロット 35 が空でない: %v", cl.Inventory().Slots[35])
	}
	click(SlotHotbar, 0, ClickPickup)
	if inv := cl.Inventory(); inv.Cursor.Name() != "stone" || inv.Cursor.Count != 64 || !inv.Hotbar(0).Empty() {
		t.Errorf("拾ったあと: %s", describeInv(inv))
	}
	click(35, 0, ClickPickup)
	if !has(35, "minecraft:stone") || has(0, "minecraft:stone") {
		t.Errorf("石が持ち物の 35 に移っていない")
	}

	// 右クリックで半分（32）拾い、ホットバー 0 に 1 個ずつ 2 回置き、残りを 35 に戻す
	click(35, 1, ClickPickup)
	click(SlotHotbar, 1, ClickPickup)
	click(SlotHotbar, 1, ClickPickup)
	click(35, 0, ClickPickup)
	inv = cl.Inventory()
	if inv.Hotbar(0).Count != 2 || inv.Slots[35].Count != 62 || !inv.Cursor.Empty() {
		t.Errorf("右クリックのあと: %s", describeInv(inv))
	}
	if !has(0, "minecraft:stone[count=2]") || !has(35, "minecraft:stone[count=62]") {
		t.Errorf("RCON で見た石の個数が合わない")
	}

	// 数字キー: 35 の石とホットバー 1 のエンチャントつきの剣を入れ替える（部品つきのものが動く）
	click(35, 1, ClickSwap)
	if !has(1, "minecraft:stone") || !has(35, "minecraft:diamond_sword") {
		t.Errorf("数字キーの入れ替えが RCON で見えない")
	}
	// 部品つきの剣を左クリックで拾って、ホットバー 1 に戻す（石と入れ替わる）、石は 35 に戻す
	click(35, 0, ClickPickup)
	click(SlotHotbar+1, 0, ClickPickup)
	click(35, 0, ClickPickup)
	if !has(1, "minecraft:diamond_sword") || !has(35, "minecraft:stone[count=62]") {
		t.Errorf("剣を拾って戻したあとが RCON と合わない")
	}

	// ---- 受け入れられないクリック: 石を頭の防具のスロットに置こうとする → ErrClickRejected ----
	click(SlotHotbar, 0, ClickPickup) // 石 2 個を拾う
	err = cl.ClickSlot(ctx, SlotHead, 0, ClickPickup)
	if !errors.Is(err, ErrClickRejected) {
		t.Errorf("石を頭に置くクリック: %v、ErrClickRejected を期待", err)
	} else {
		t.Logf("受け入れられないクリック: %v", err)
	}
	if inv := cl.Inventory(); !inv.Slots[SlotHead].Empty() || inv.Cursor.Name() != "stone" || inv.Cursor.Count != 2 {
		t.Errorf("受け入れられなかったあと、サーバーの内容に戻っていない: %s", describeInv(inv))
	}
	click(SlotHotbar, 0, ClickPickup) // 戻す
	if !has(0, "minecraft:stone[count=2]") {
		t.Errorf("石がホットバー 0 に戻っていない")
	}

	// ---- Drop: リンゴ（ホットバー 2、5 個）を 1 個、そのあと全部捨てる ----
	if err := cl.Drop(ctx, SlotHotbar+2, false); err != nil {
		t.Fatalf("Drop 1 個: %v", err)
	}
	if !has(2, "minecraft:apple[count=4]") {
		t.Errorf("1 個捨てたあと、RCON で見たホットバー 2 がリンゴ 4 個でない")
	}
	if err := cl.Drop(ctx, SlotHotbar+2, true); err != nil {
		t.Fatalf("Drop 全部: %v", err)
	}
	if has(2, "minecraft:apple") {
		t.Errorf("全部捨てたあと、RCON で見たホットバー 2 にリンゴがある")
	}
	if n := count("minecraft:apple"); n != 0 {
		t.Logf("全部捨てたあとの持ち物のリンゴ（バンドルの中を数えるかもしれない）: %d", n)
	}
	if h := cl.Inventory().Hotbar(2); !h.Empty() {
		t.Errorf("捨てたあとのホットバー 2: %v", h)
	}
	cmd(fmt.Sprintf("kill @e[type=item,x=%d,y=%d,z=%d,distance=..8]", x, testserver.StandY, z))

	// 持ち物の窓を閉じても切れない
	if err := cl.CloseContainer(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := cl.Err(); err != nil {
		t.Fatalf("切断された: %v", err)
	}
	if n, last := cl.InventoryReadErrors(); n != 0 {
		t.Errorf("読めなかったパケット %d（最後 %v）", n, last)
	}
	t.Logf("最後の持ち物: %s", describeInv(cl.Inventory()))
}
