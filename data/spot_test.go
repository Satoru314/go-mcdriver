package data

import "testing"

// 抜き取り確認。期待値は公式データ（reports, data/minecraft/recipe）から手で書き写したもの。

func TestVersion(t *testing.T) {
	if Version != "1.21.4" || ProtocolVersion != 769 {
		t.Errorf("Version=%q ProtocolVersion=%d, 期待 1.21.4 / 769", Version, ProtocolVersion)
	}
}

func TestStone(t *testing.T) {
	b, ok := BlockByName("minecraft:stone")
	if !ok {
		t.Fatal("stone が無い")
	}
	if b.ID != 1 || b.MinState != 1 || b.NumStates != 1 || b.DefaultState != 1 || len(b.Properties) != 0 {
		t.Errorf("stone = %+v, 期待 ID 1, 状態 1 だけ", *b)
	}
	if s, ok := StateByID(1); !ok || s.Name() != "stone" {
		t.Errorf("StateByID(1) = %v", s)
	}
	if s, ok := StateByID(0); !ok || s.Name() != "air" {
		t.Errorf("StateByID(0) = %v", s)
	}
}

func TestOakStairs(t *testing.T) {
	b, ok := BlockByName("oak_stairs")
	if !ok {
		t.Fatal("oak_stairs が無い")
	}
	if b.ID != 184 || b.NumStates != 80 || b.MinState != 2929 || b.DefaultState != 2940 {
		t.Errorf("oak_stairs = ID %d, 状態 %d 個 %d から, 既定 %d; 期待 184, 80 個 2929 から, 既定 2940",
			b.ID, b.NumStates, b.MinState, b.DefaultState)
	}
	want := map[string]int{"facing": 4, "half": 2, "shape": 5, "waterlogged": 2}
	if len(b.Properties) != len(want) {
		t.Errorf("プロパティの数 %d, 期待 %d", len(b.Properties), len(want))
	}
	for name, n := range want {
		p, ok := b.Property(name)
		if !ok || len(p.Values) != n {
			t.Errorf("プロパティ %s: %v, 期待 値 %d 個", name, p, n)
		}
	}
	def := b.Default()
	wantDef := map[string]string{"facing": "north", "half": "bottom", "shape": "straight", "waterlogged": "false"}
	for k, v := range wantDef {
		if got, _ := def.Get(k); got != v {
			t.Errorf("既定の状態の %s = %q, 期待 %q", k, got, v)
		}
	}
	s, err := b.State(map[string]string{"facing": "east", "half": "top", "shape": "outer_right", "waterlogged": "true"})
	if err != nil || s.ID != 2997 {
		t.Errorf("east/top/outer_right/true = %d (%v), 期待 2997", s.ID, err)
	}
	if got := s.String(); got != "oak_stairs[facing=east,half=top,shape=outer_right,waterlogged=true]" {
		t.Errorf("String() = %q", got)
	}
	if _, err := b.State(map[string]string{"facing": "up"}); err == nil {
		t.Error("facing=up がエラーにならない")
	}
	if b.HasState(2928) || !b.HasState(2929) || !b.HasState(3008) || b.HasState(3009) {
		t.Error("HasState の範囲が 2929..3008 でない")
	}
}

func TestOakDoor(t *testing.T) {
	b, ok := BlockByName("oak_door")
	if !ok {
		t.Fatal("oak_door が無い")
	}
	if b.ID != 204 || b.NumStates != 64 || b.MinState != 4677 || b.DefaultState != 4688 {
		t.Errorf("oak_door = ID %d, 状態 %d 個 %d から, 既定 %d; 期待 204, 64 個 4677 から, 既定 4688",
			b.ID, b.NumStates, b.MinState, b.DefaultState)
	}
	s, err := b.State(map[string]string{"facing": "west", "half": "upper", "hinge": "right", "open": "true", "powered": "false"})
	if err != nil || s.ID != 4714 {
		t.Errorf("west/upper/right/open = %d (%v), 期待 4714", s.ID, err)
	}
	// 状態 ID から戻す
	got, ok := StateByID(4714)
	if !ok || got.Name() != "oak_door" {
		t.Fatalf("StateByID(4714) = %v", got)
	}
	p := got.Properties()
	if p["facing"] != "west" || p["half"] != "upper" || p["hinge"] != "right" || p["open"] != "true" || p["powered"] != "false" {
		t.Errorf("StateByID(4714).Properties() = %v", p)
	}
	// With でプロパティを1つ変える
	closed, ok := got.With("open", "false")
	if !ok {
		t.Fatal("With(open=false) が失敗")
	}
	if v, _ := closed.Get("open"); v != "false" {
		t.Errorf("With(open=false) の open = %q", v)
	}
	if v, _ := closed.Get("facing"); v != "west" {
		t.Errorf("With(open=false) で facing が変わった: %q", v)
	}
}

func TestWater(t *testing.T) {
	b, ok := BlockByName("water")
	if !ok {
		t.Fatal("water が無い")
	}
	if b.ID != 35 || b.NumStates != 16 || b.MinState != 86 || b.DefaultState != 86 || b.Type != "liquid" {
		t.Errorf("water = %+v, 期待 ID 35, 16 個 86 から, 既定 86, Type liquid", *b)
	}
	s, err := b.State(map[string]string{"level": "8"})
	if err != nil || s.ID != 94 {
		t.Errorf("level=8 = %d (%v), 期待 94", s.ID, err)
	}
}

// piston_head は reports のプロパティの順（type, facing, short）と状態 ID の並び（名前の順）が違う
func TestPistonHeadPropertyOrder(t *testing.T) {
	b, _ := BlockByName("piston_head")
	s, err := b.State(map[string]string{"type": "sticky", "facing": "up", "short": "false"})
	if err != nil || s.ID != 2085 {
		t.Errorf("sticky/up/false = %d (%v), 期待 2085", s.ID, err)
	}
	if b.DefaultState != 2068 {
		t.Errorf("既定 %d, 期待 2068", b.DefaultState)
	}
}

func TestPacketIDs(t *testing.T) {
	cases := []struct {
		state, dir, name string
		id               int32
		konst            int32
	}{
		{StatePlay, Clientbound, "keep_alive", 39, PlayClientboundKeepAlive},
		{StatePlay, Clientbound, "level_chunk_with_light", 40, PlayClientboundLevelChunkWithLight},
		{StatePlay, Clientbound, "forget_level_chunk", 34, PlayClientboundForgetLevelChunk},
		{StatePlay, Clientbound, "block_update", 9, PlayClientboundBlockUpdate},
		{StatePlay, Clientbound, "login", 44, PlayClientboundLogin},
		{StatePlay, Serverbound, "keep_alive", 26, PlayServerboundKeepAlive},
		{StatePlay, Serverbound, "move_player_pos", 28, PlayServerboundMovePlayerPos},
		{StateHandshake, Serverbound, "intention", 0, HandshakeServerboundIntention},
		{StateLogin, Clientbound, "login_finished", 2, LoginClientboundLoginFinished},
		{StateConfiguration, Clientbound, "finish_configuration", 3, ConfigurationClientboundFinishConfiguration},
	}
	for _, c := range cases {
		got, ok := PacketID(c.state, c.dir, "minecraft:"+c.name)
		if !ok || got != c.id || c.konst != c.id {
			t.Errorf("%s/%s/%s = %d (ok=%v, 定数 %d), 期待 %d", c.state, c.dir, c.name, got, ok, c.konst, c.id)
		}
		p, ok := PacketByID(c.state, c.dir, c.id)
		if !ok || p.Name != c.name {
			t.Errorf("PacketByID(%s, %s, %d) = %+v", c.state, c.dir, c.id, p)
		}
	}
}

func TestItems(t *testing.T) {
	apple, ok := ItemByName("apple")
	if !ok || apple.ID != 831 || apple.MaxStackSize != 64 || apple.Food == nil ||
		apple.Food.Nutrition != 4 || apple.Food.Saturation != 2.4 || apple.Food.CanAlwaysEat {
		t.Errorf("apple = %+v food=%+v", apple, apple.Food)
	}
	ga, _ := ItemByName("golden_apple")
	if ga.Food == nil || ga.Food.Nutrition != 4 || ga.Food.Saturation != 9.6 || !ga.Food.CanAlwaysEat {
		t.Errorf("golden_apple food = %+v", ga.Food)
	}
	if ep, _ := ItemByName("ender_pearl"); ep.MaxStackSize != 16 || ep.Food != nil {
		t.Errorf("ender_pearl = %+v", ep)
	}
	pick, _ := ItemByName("diamond_pickaxe")
	if pick.ID != 871 || pick.MaxStackSize != 1 || pick.MaxDamage != 1561 || pick.Tool == nil {
		t.Fatalf("diamond_pickaxe = %+v", pick)
	}
	stone, _ := BlockByName("stone")
	if sp, ok := pick.Tool.MiningSpeed(stone.ID); !ok || sp != 8 {
		t.Errorf("ダイヤのつるはしで石の速さ = %v (%v), 期待 8", sp, ok)
	}
	if !pick.Tool.CorrectForDrops(stone.ID) {
		t.Error("ダイヤのつるはしが石に適していない")
	}
	dirt, _ := BlockByName("dirt")
	if _, ok := pick.Tool.MiningSpeed(dirt.ID); ok {
		t.Error("ダイヤのつるはしの規則が土に当たった")
	}
	shears, _ := ItemByName("shears")
	vine, _ := BlockByName("vine")
	if sp, ok := shears.Tool.MiningSpeed(vine.ID); !ok || sp != 2 {
		t.Errorf("ハサミでツタの速さ = %v (%v), 期待 2", sp, ok)
	}
}

func TestTags(t *testing.T) {
	stone, _ := BlockByName("stone")
	if !BlockHasTag(stone.ID, "#minecraft:mineable/pickaxe") {
		t.Error("stone が mineable/pickaxe に無い")
	}
	oak, _ := ItemByName("oak_planks")
	if !ItemHasTag(oak.ID, "planks") {
		t.Error("oak_planks が planks に無い")
	}
	// logs は oak_logs などのタグを参照しているので、展開されていれば oak_log が入る
	log, _ := ItemByName("oak_log")
	if !ItemHasTag(log.ID, "logs") {
		t.Error("oak_log が logs に無い（タグの参照が展開されていない）")
	}
}

func TestRecipes(t *testing.T) {
	r, ok := RecipeByName("oak_planks")
	oakPlanks, _ := ItemByName("oak_planks")
	if !ok || r.Type != "crafting_shapeless" || len(r.Ingredients) != 1 || r.Ingredients[0].Tag != "oak_logs" ||
		!r.HasResult || r.Result.Item != oakPlanks.ID || r.Result.Count != 4 {
		t.Errorf("oak_planks のレシピ = %+v", r)
	}
	ct, ok := RecipeByName("crafting_table")
	if !ok || ct.Type != "crafting_shaped" || len(ct.Pattern) != 2 || ct.Pattern[0] != "##" || ct.Key['#'].Tag != "planks" || ct.Result.Count != 1 {
		t.Errorf("crafting_table のレシピ = %+v", ct)
	}
	if len(RecipesFor(oakPlanks.ID)) == 0 {
		t.Error("RecipesFor(oak_planks) が空")
	}
	cod, _ := RecipeByName("cooked_cod")
	if cod.Type != "smelting" || cod.CookingTime != 200 || cod.Experience != 0.35 {
		t.Errorf("cooked_cod = %+v", cod)
	}
}

func TestRegistries(t *testing.T) {
	r, ok := RegistryByName("minecraft:entity_type")
	if !ok || len(r.Entries) != 149 {
		t.Fatalf("entity_type: ok=%v 数 %d, 期待 149", ok, len(r.Entries))
	}
	if id, ok := r.ID("zombie"); !ok || id != 143 {
		t.Errorf("zombie = %d, 期待 143", id)
	}
	if len(RegistryNames()) != 80 {
		t.Errorf("registry の数 %d, 期待 80", len(RegistryNames()))
	}
}
