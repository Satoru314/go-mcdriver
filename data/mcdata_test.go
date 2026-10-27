package data

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gonum.org/v1/gonum/spatial/r3"
)

// minecraft-data から取った情報の確かめ。
// 抜き取り確認の期待値は、ゲームの値（Minecraft Wiki とゲームのソースで知られている値）を手で書いたもの。
// 全件の確認は、取得した minecraft-data の JSON と生成したデータを照らし合わせる。

func mustBlock(t *testing.T, name string) *Block {
	t.Helper()
	b, ok := BlockByName(name)
	if !ok {
		t.Fatalf("%s が無い", name)
	}
	return b
}

func mustState(t *testing.T, name string, props map[string]string) BlockState {
	t.Helper()
	s, err := mustBlock(t, name).State(props)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustItem(t *testing.T, name string) ItemID {
	t.Helper()
	it, ok := ItemByName(name)
	if !ok {
		t.Fatalf("アイテム %s が無い", name)
	}
	return it.ID
}

func TestHardness(t *testing.T) {
	for _, c := range []struct {
		name     string
		hardness float32
		blast    float32
	}{
		{"stone", 1.5, 6},
		{"obsidian", 50, 1200},
		{"dirt", 0.5, 0.5},
		{"oak_planks", 2, 3},
		{"oak_log", 2, 2},
		{"sand", 0.5, 0.5},
		{"short_grass", 0, 0},
		{"torch", 0, 0},
		{"bedrock", -1, 3600000},
		{"water", 100, 100},
	} {
		b := mustBlock(t, c.name)
		if b.Hardness != c.hardness || b.BlastResistance != c.blast {
			t.Errorf("%s: 硬さ %v 爆発耐性 %v, 期待 %v / %v", c.name, b.Hardness, b.BlastResistance, c.hardness, c.blast)
		}
	}
	if b := mustBlock(t, "bedrock"); b.Diggable {
		t.Error("bedrock が壊せることになっている")
	}
	if b := mustBlock(t, "barrier"); b.Diggable || b.Hardness != -1 {
		t.Errorf("barrier: Diggable %v 硬さ %v, 期待 false / -1", b.Diggable, b.Hardness)
	}
	if b := mustBlock(t, "air"); b.Diggable {
		t.Error("air が壊せることになっている")
	}
	for _, n := range []string{"stone", "dirt", "obsidian", "short_grass"} {
		if !mustBlock(t, n).Diggable {
			t.Errorf("%s が壊せないことになっている", n)
		}
	}
}

func TestHarvestTools(t *testing.T) {
	const hand = ItemID(-1)
	wood, stone, iron, diamond, netherite := mustItem(t, "wooden_pickaxe"), mustItem(t, "stone_pickaxe"),
		mustItem(t, "iron_pickaxe"), mustItem(t, "diamond_pickaxe"), mustItem(t, "netherite_pickaxe")
	shovel := mustItem(t, "wooden_shovel")
	for _, c := range []struct {
		block string
		tool  ItemID
		want  bool
	}{
		{"dirt", hand, true}, {"oak_log", hand, true}, {"oak_planks", hand, true},
		{"stone", hand, false}, {"stone", shovel, false}, {"stone", wood, true}, {"stone", netherite, true},
		{"cobblestone", wood, true},
		{"iron_ore", wood, false}, {"iron_ore", stone, true},
		{"diamond_ore", stone, false}, {"diamond_ore", iron, true},
		{"obsidian", iron, false}, {"obsidian", diamond, true}, {"obsidian", netherite, true},
	} {
		if got := mustBlock(t, c.block).CanHarvest(c.tool); got != c.want {
			t.Errorf("%s を %d で掘って落とすか: %v, 期待 %v", c.block, c.tool, got, c.want)
		}
	}
	if mustBlock(t, "dirt").NeedsTool() || !mustBlock(t, "stone").NeedsTool() {
		t.Error("NeedsTool: dirt は道具が要らず、stone は要るはず")
	}
	// stone はつるはし 6 種類（木・石・鉄・金・ダイヤ・ネザライト）だけで落とす
	if n := len(mustBlock(t, "stone").HarvestTools); n != 6 {
		t.Errorf("stone の HarvestTools が %d 個, 期待 6", n)
	}
}

func TestLight(t *testing.T) {
	for _, c := range []struct {
		name        string
		emit        uint8
		filter      uint8
		transparent bool
	}{
		{"stone", 0, 15, false},
		{"air", 0, 0, true},
		{"glass", 0, 0, true},
		{"torch", 14, 0, true},
		{"glowstone", 15, 15, false},
		{"lava", 15, 1, true},
		{"water", 0, 1, true},
		{"oak_leaves", 0, 1, true},
		{"sea_lantern", 15, 15, false},
	} {
		b := mustBlock(t, c.name)
		if b.EmitLight != c.emit || b.FilterLight != c.filter || b.Transparent != c.transparent {
			t.Errorf("%s: 光 %d 減衰 %d 透明 %v, 期待 %d / %d / %v", c.name, b.EmitLight, b.FilterLight, b.Transparent, c.emit, c.filter, c.transparent)
		}
	}
}

func box(x0, y0, z0, x1, y1, z1 float64) r3.Box { return bx(x0, y0, z0, x1, y1, z1) }

// sameShape は箱の一覧が同じ形を占めるかを返す（箱の分け方は問わない）。
// 1/32 の格子の点（箱の境目に乗らない、各升目の中心）が、両方で同じく箱の中にあるかで比べる。
// 高さは 1.5 まで見る（フェンスなど）。
func sameShape(a, b []r3.Box) bool {
	inside := func(bs []r3.Box, p r3.Vec) bool {
		for _, x := range bs {
			if p.X > x.Min.X && p.X < x.Max.X && p.Y > x.Min.Y && p.Y < x.Max.Y && p.Z > x.Min.Z && p.Z < x.Max.Z {
				return true
			}
		}
		return false
	}
	const n = 32
	for i := range n {
		for j := range n * 3 / 2 {
			for k := range n {
				p := r3.Vec{X: (float64(i) + 0.5) / n, Y: (float64(j) + 0.5) / n, Z: (float64(k) + 0.5) / n}
				if inside(a, p) != inside(b, p) {
					return false
				}
			}
		}
	}
	return (len(a) == 0) == (len(b) == 0)
}

func TestCollisionBoxes(t *testing.T) {
	full := []r3.Box{box(0, 0, 0, 1, 1, 1)}
	for _, c := range []struct {
		name  string
		props map[string]string
		want  []r3.Box
	}{
		{"air", nil, nil},
		{"stone", nil, full},
		{"short_grass", nil, nil},
		{"torch", nil, nil},
		{"water", nil, nil},
		{"oak_slab", map[string]string{"type": "bottom"}, []r3.Box{box(0, 0, 0, 1, 0.5, 1)}},
		{"oak_slab", map[string]string{"type": "top"}, []r3.Box{box(0, 0.5, 0, 1, 1, 1)}},
		{"oak_slab", map[string]string{"type": "double"}, full},
		// 北向きの下付きの階段: 下半分の板と、北側（z が小さい側）の上半分
		{"oak_stairs", map[string]string{"facing": "north", "half": "bottom", "shape": "straight"},
			[]r3.Box{box(0, 0, 0, 1, 0.5, 1), box(0, 0.5, 0, 1, 1, 0.5)}},
		{"oak_stairs", map[string]string{"facing": "south", "half": "top", "shape": "straight"},
			[]r3.Box{box(0, 0.5, 0, 1, 1, 1), box(0, 0, 0.5, 1, 0.5, 1)}},
		// 何にもつながっていないフェンスは柱だけで、高さ 1.5
		{"oak_fence", nil, []r3.Box{box(0.375, 0, 0.375, 0.625, 1.5, 0.625)}},
		{"snow", map[string]string{"layers": "1"}, nil},
		{"snow", map[string]string{"layers": "2"}, []r3.Box{box(0, 0, 0, 1, 0.125, 1)}},
		{"farmland", nil, []r3.Box{box(0, 0, 0, 1, 0.9375, 1)}},
		{"soul_sand", nil, []r3.Box{box(0, 0, 0, 1, 0.875, 1)}},
	} {
		s := mustState(t, c.name, c.props)
		if got := s.CollisionBoxes(); !sameShape(got, c.want) {
			t.Errorf("%v の箱 %v, 期待 %v", s, got, c.want)
		}
	}
	// 依頼の確認: 北向き・下付きの階段の箱は2つ（水あり・なしとも）
	for _, wl := range []string{"true", "false"} {
		s := mustState(t, "oak_stairs", map[string]string{"facing": "north", "half": "bottom", "waterlogged": wl})
		if n := len(s.CollisionBoxes()); n != 2 {
			t.Errorf("%v の箱が %d 個, 期待 2", s, n)
		}
	}
	if !mustBlock(t, "stone").Default().IsFullCube() || mustState(t, "oak_slab", map[string]string{"type": "bottom"}).IsFullCube() {
		t.Error("IsFullCube: stone は真、下付きハーフブロックは偽のはず")
	}
	if mustBlock(t, "air").Default().HasCollision() {
		t.Error("air に当たり判定がある")
	}
}

func TestEntitySize(t *testing.T) {
	for _, c := range []struct {
		name          string
		width, height float32
	}{
		{"zombie", 0.6, 1.95},
		{"player", 0.6, 1.8},
		{"creeper", 0.6, 1.7},
		{"skeleton", 0.6, 1.99},
		{"spider", 1.4, 0.9},
		{"cow", 0.9, 1.4},
		{"item", 0.25, 0.25},
		{"arrow", 0.5, 0.5},
		{"villager", 0.6, 1.95},
		{"iron_golem", 1.4, 2.7},
	} {
		e, ok := EntityTypeByName("minecraft:" + c.name)
		if !ok {
			t.Errorf("%s が無い", c.name)
			continue
		}
		if e.Width != c.width || e.Height != c.height {
			t.Errorf("%s: %v×%v, 期待 %v×%v", c.name, e.Width, e.Height, c.width, c.height)
		}
	}
	reg, _ := RegistryByName("entity_type")
	if len(EntityTypes()) != len(reg.Entries) {
		t.Fatalf("エンティティの種類 %d, registry %d", len(EntityTypes()), len(reg.Entries))
	}
	for i, name := range reg.Entries {
		e, ok := EntityTypeByID(EntityTypeID(i))
		if !ok || e.Name != name || int(e.ID) != i {
			t.Errorf("EntityTypeByID(%d) = %v, registry は %s", i, e, name)
		}
	}
}

// ---- 全件の確認（取得した minecraft-data の JSON と照らし合わせる） ----

// mcdataPCDir は取得済みの minecraft-data の pc/1.21.4 のフォルダ。無ければテストを飛ばす。
// 場所は環境変数 MCDATA_MINECRAFT_DATA（data/pc/1.21.4 を含むフォルダ）で変えられる。
func mcdataPCDir(t *testing.T) string {
	dir := os.Getenv("MCDATA_MINECRAFT_DATA")
	if dir == "" {
		dir = filepath.Join("..", ".gendata", "minecraft-data", MinecraftDataCommit)
	}
	dir = filepath.Join(dir, "data", "pc", "1.21.4")
	if _, err := os.Stat(filepath.Join(dir, "blocks.json")); err != nil {
		t.Skipf("minecraft-data が無い（%s）。go run ./cmd/gendata で取得できる", dir)
	}
	return dir
}

func TestMCDataBlocksAll(t *testing.T) {
	dir := mcdataPCDir(t)
	var raw []struct {
		ID           int             `json:"id"`
		Name         string          `json:"name"`
		Hardness     float64         `json:"hardness"`
		Resistance   float64         `json:"resistance"`
		Diggable     bool            `json:"diggable"`
		Transparent  bool            `json:"transparent"`
		EmitLight    int             `json:"emitLight"`
		FilterLight  int             `json:"filterLight"`
		MinStateID   int             `json:"minStateId"`
		MaxStateID   int             `json:"maxStateId"`
		HarvestTools map[string]bool `json:"harvestTools"`
	}
	loadJSON(t, filepath.Join(dir, "blocks.json"), &raw)
	if len(raw) != len(Blocks()) {
		t.Errorf("ブロックの数 %d, minecraft-data %d", len(Blocks()), len(raw))
	}
	for _, r := range raw {
		b, ok := BlockByName(r.Name)
		if !ok {
			t.Errorf("%s が無い", r.Name)
			continue
		}
		if int(b.ID) != r.ID || int(b.MinState) != r.MinStateID || int(b.MinState)+b.NumStates-1 != r.MaxStateID {
			t.Errorf("%s: ID %d 状態 %d〜, minecraft-data %d %d〜%d", r.Name, b.ID, b.MinState, r.ID, r.MinStateID, r.MaxStateID)
		}
		if b.Hardness != float32(r.Hardness) || b.BlastResistance != float32(r.Resistance) || b.Diggable != r.Diggable ||
			b.Transparent != r.Transparent || int(b.EmitLight) != r.EmitLight || int(b.FilterLight) != r.FilterLight {
			t.Errorf("%s: 生成 %v/%v/%v/%v/%d/%d, minecraft-data %+v", r.Name, b.Hardness, b.BlastResistance, b.Diggable,
				b.Transparent, b.EmitLight, b.FilterLight, r)
		}
		if (r.HarvestTools == nil) != (b.HarvestTools == nil) || len(r.HarvestTools) != len(b.HarvestTools) {
			t.Errorf("%s: HarvestTools %v, minecraft-data %v", r.Name, b.HarvestTools, r.HarvestTools)
			continue
		}
		for _, id := range b.HarvestTools {
			if !r.HarvestTools[fmt.Sprint(id)] {
				t.Errorf("%s: HarvestTools の %d が minecraft-data に無い", r.Name, id)
			}
		}
	}
}

func TestMCDataShapesAll(t *testing.T) {
	dir := mcdataPCDir(t)
	var raw struct {
		Blocks map[string]json.RawMessage `json:"blocks"`
		Shapes map[string][][6]float64    `json:"shapes"`
	}
	loadJSON(t, filepath.Join(dir, "blockCollisionShapes.json"), &raw)
	if len(raw.Blocks) != len(Blocks()) {
		t.Errorf("ブロックの数 %d, minecraft-data %d", len(Blocks()), len(raw.Blocks))
	}
	checked := 0
	for _, b := range Blocks() {
		var ids []int
		var one int
		if json.Unmarshal(raw.Blocks[b.Name], &one) == nil {
			for range b.NumStates {
				ids = append(ids, one)
			}
		} else if err := json.Unmarshal(raw.Blocks[b.Name], &ids); err != nil {
			t.Errorf("%s: %v", b.Name, err)
			continue
		}
		if len(ids) != b.NumStates {
			t.Errorf("%s: 状態 %d 個, minecraft-data %d 個", b.Name, b.NumStates, len(ids))
			continue
		}
		for i, id := range ids {
			s, _ := StateByID(b.MinState + StateID(i))
			var want []r3.Box
			for _, v := range raw.Shapes[fmt.Sprint(id)] {
				want = append(want, box(v[0], v[1], v[2], v[3], v[4], v[5]))
			}
			got := s.CollisionBoxes()
			if len(got) != len(want) {
				t.Errorf("%v: 箱 %v, minecraft-data %v", s, got, want)
				continue
			}
			for k := range got {
				if got[k] != want[k] {
					t.Errorf("%v: 箱 %v, minecraft-data %v", s, got, want)
					break
				}
			}
			checked++
		}
	}
	if checked != NumBlockStates {
		t.Errorf("確かめた状態 %d, 全部で %d", checked, NumBlockStates)
	}
}

func TestMCDataEntitiesAll(t *testing.T) {
	dir := mcdataPCDir(t)
	var raw []struct {
		ID     int     `json:"id"`
		Name   string  `json:"name"`
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
	}
	loadJSON(t, filepath.Join(dir, "entities.json"), &raw)
	if len(raw) != len(EntityTypes()) {
		t.Errorf("エンティティの数 %d, minecraft-data %d", len(EntityTypes()), len(raw))
	}
	for _, r := range raw {
		e, ok := EntityTypeByName(r.Name)
		if !ok {
			t.Errorf("%s が無い", r.Name)
			continue
		}
		if int(e.ID) != r.ID || e.Width != float32(r.Width) || e.Height != float32(r.Height) {
			t.Errorf("%s: %+v, minecraft-data %+v", r.Name, *e, r)
		}
	}
}
