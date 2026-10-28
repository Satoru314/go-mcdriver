package main

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Go のソースとして書き出す部分。整形は最後に go/format が行う。

func writeVersion(w *bytes.Buffer, v versionJSON) {
	fmt.Fprintf(w, "// Version は生成元のサーバーのバージョン。\nconst Version = %q\n\n", v.ID)
	fmt.Fprintf(w, "// ProtocolVersion はハンドシェイクで送るプロトコル番号（jar の version.json より）。\nconst ProtocolVersion = %d\n\n", v.ProtocolVersion)
	fmt.Fprintf(w, "// WorldVersion はワールドのデータのバージョン（jar の version.json より）。\nconst WorldVersion = %d\n", v.WorldVersion)
	fmt.Fprintf(w, "\n// MinecraftDataTag と MinecraftDataCommit は、公式データに無い情報を取った PrismarineJS/minecraft-data のタグとコミット。\n")
	fmt.Fprintf(w, "const (\nMinecraftDataTag = %q\nMinecraftDataCommit = %q\n)\n", mcdataTag, mcdataCommit)
}

func writeBlocks(w *bytes.Buffer, blocks []block, md *mcdata) {
	total := 0
	for _, b := range blocks {
		total += b.NumStates
	}
	fmt.Fprintf(w, "// NumBlockStates は全ブロックの状態の数。\nconst NumBlockStates = %d\n\n", total)
	w.WriteString("var blocks = []Block{\n")
	for _, b := range blocks {
		fmt.Fprintf(w, "{ID: %d, Name: %q, Type: %q, MinState: %d, NumStates: %d, DefaultState: %d",
			b.ID, b.Name, b.Type, b.MinState, b.NumStates, b.DefaultState)
		if len(b.Properties) > 0 {
			w.WriteString(", Properties: []Property{")
			for _, p := range b.Properties {
				fmt.Fprintf(w, "{%q, %s}, ", p.Name, strSlice(p.Values))
			}
			w.WriteString("}")
		}
		// ここから minecraft-data の情報
		ex := md.Blocks[b.ID]
		fmt.Fprintf(w, ", Hardness: %s, BlastResistance: %s", flt(ex.Hardness), flt(ex.Resistance))
		if ex.Diggable {
			w.WriteString(", Diggable: true")
		}
		if ex.HarvestTools != nil {
			fmt.Fprintf(w, ", HarvestTools: []ItemID{%s}", intList(ex.HarvestTools))
		}
		if ex.EmitLight != 0 {
			fmt.Fprintf(w, ", EmitLight: %d", ex.EmitLight)
		}
		if ex.FilterLight != 0 {
			fmt.Fprintf(w, ", FilterLight: %d", ex.FilterLight)
		}
		if ex.Transparent {
			w.WriteString(", Transparent: true")
		}
		w.WriteString("},\n")
	}
	w.WriteString("}\n")
}

// writeShapes は当たり判定の形の一覧と、状態 ID ごとの形の番号を書く。
func writeShapes(w *bytes.Buffer, md *mcdata) {
	w.WriteString("import \"gonum.org/v1/gonum/spatial/r3\"\n\n")
	w.WriteString("// collisionShapes は当たり判定の形の一覧（minecraft-data の形の番号の順）。座標はブロックの中の 0〜1。\n")
	w.WriteString("var collisionShapes = [][]r3.Box{\n")
	for i, sh := range md.Shapes {
		fmt.Fprintf(w, "/* %d */ {", i)
		for _, b := range sh {
			fmt.Fprintf(w, "bx(%s, %s, %s, %s, %s, %s), ", f64(b[0]), f64(b[1]), f64(b[2]), f64(b[3]), f64(b[4]), f64(b[5]))
		}
		w.WriteString("},\n")
	}
	w.WriteString("}\n\n")
	w.WriteString("// stateCollisionShape は状態 ID ごとの collisionShapes の番号。\n")
	w.WriteString("var stateCollisionShape = [NumBlockStates]uint16{\n")
	for i := 0; i < len(md.StateShape); i += 32 {
		end := min(i+32, len(md.StateShape))
		w.WriteString(intList(md.StateShape[i:end]) + ",\n")
	}
	w.WriteString("}\n")
}

// writeEntities はエンティティの種類（entity_type の registry の順）と大きさを書く。
func writeEntities(w *bytes.Buffer, md *mcdata) {
	w.WriteString("var entityTypes = []EntityType{\n")
	for _, e := range md.Entities {
		fmt.Fprintf(w, "{ID: %d, Name: %q, Width: %s, Height: %s},\n", e.ID, e.Name, flt(e.Width), flt(e.Height))
	}
	w.WriteString("}\n")
}

func writeItems(w *bytes.Buffer, items []item, blockIndex map[string]int) {
	w.WriteString("var items = []Item{\n")
	for _, it := range items {
		fmt.Fprintf(w, "{ID: %d, Name: %q, MaxStackSize: %d", it.ID, it.Name, it.MaxStackSize)
		if it.MaxDamage != 0 {
			fmt.Fprintf(w, ", MaxDamage: %d", it.MaxDamage)
		}
		if f := it.Food; f != nil {
			fmt.Fprintf(w, ", Food: &Food{Nutrition: %d, Saturation: %s, CanAlwaysEat: %t}", f.Nutrition, flt(f.Saturation), f.CanAlwaysEat)
		}
		if c := it.Consumable; c != nil {
			w.WriteString(", Consumable: &Consumable{")
			if c.ConsumeSeconds != nil {
				fmt.Fprintf(w, "ConsumeSeconds: %s, ", flt(*c.ConsumeSeconds))
			}
			if c.Animation != "" {
				fmt.Fprintf(w, "Animation: %q", c.Animation)
			}
			w.WriteString("}")
		}
		if len(it.Modifiers) > 0 {
			w.WriteString(", AttributeModifiers: []AttributeModifier{")
			for _, m := range it.Modifiers {
				fmt.Fprintf(w, "{Attribute: %q, ID: %q, Amount: %s, Operation: %q, Slot: %q}, ", m.Attribute, m.ID, f64(m.Amount), m.Operation, m.Slot)
			}
			w.WriteString("}")
		}
		if t := it.Tool; t != nil {
			w.WriteString(", Tool: &Tool{")
			if t.DefaultMiningSpeed != nil {
				fmt.Fprintf(w, "DefaultMiningSpeed: %s, ", flt(*t.DefaultMiningSpeed))
			}
			if t.DamagePerBlock != nil {
				fmt.Fprintf(w, "DamagePerBlock: %d, HasDamagePerBlock: true, ", *t.DamagePerBlock)
			}
			w.WriteString("Rules: []ToolRule{")
			for _, r := range t.Rules {
				w.WriteString("{")
				if r.Tag != "" {
					fmt.Fprintf(w, "Tag: %q, ", r.Tag)
				}
				if len(r.Blocks) > 0 {
					w.WriteString("Blocks: []BlockID{")
					for _, n := range r.Blocks {
						fmt.Fprintf(w, "%d, ", blockIndex[n])
					}
					w.WriteString("}, ")
				}
				if r.Speed != nil {
					fmt.Fprintf(w, "Speed: %s, HasSpeed: true, ", flt(*r.Speed))
				}
				if r.CorrectForDrops != nil {
					fmt.Fprintf(w, "CorrectForDrops: %t, HasCorrectForDrops: true", *r.CorrectForDrops)
				}
				w.WriteString("}, ")
			}
			w.WriteString("}}")
		}
		w.WriteString("},\n")
	}
	w.WriteString("}\n")
}

func writeTags(w *bytes.Buffer, blockTags, itemTags map[string][]int) {
	write := func(varName, typ string, tags map[string][]int) {
		names := make([]string, 0, len(tags))
		for n := range tags {
			names = append(names, n)
		}
		sort.Strings(names)
		fmt.Fprintf(w, "var %s = map[string][]%s{\n", varName, typ)
		for _, n := range names {
			fmt.Fprintf(w, "%q: {%s},\n", n, intList(tags[n]))
		}
		w.WriteString("}\n\n")
	}
	w.WriteString("// タグは、ほかのタグへの参照を展開し、ID の昇順に並べてある。\n\n")
	write("blockTags", "BlockID", blockTags)
	write("itemTags", "ItemID", itemTags)
}

func writeRecipes(w *bytes.Buffer, recipes []recipe) {
	ing := func(field string, g ingredient) string {
		if g.Tag == "" && len(g.Items) == 0 {
			return ""
		}
		return field + ": " + ingLit(g) + ", "
	}
	w.WriteString("var recipes = []Recipe{\n")
	for _, r := range recipes {
		fmt.Fprintf(w, "{Name: %q, Type: %q", r.Name, r.Type)
		if r.Category != "" {
			fmt.Fprintf(w, ", Category: %q", r.Category)
		}
		if r.Group != "" {
			fmt.Fprintf(w, ", Group: %q", r.Group)
		}
		if len(r.Pattern) > 0 {
			fmt.Fprintf(w, ", Pattern: %s", strSlice(r.Pattern))
		}
		if len(r.Key) > 0 {
			keys := make([]string, 0, len(r.Key))
			for k := range r.Key {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			w.WriteString(", Key: map[rune]Ingredient{")
			for _, k := range keys {
				fmt.Fprintf(w, "%s: %s, ", strconv.QuoteRune([]rune(k)[0]), ingLit(r.Key[k]))
			}
			w.WriteString("}")
		}
		if len(r.Ingredients) > 0 {
			w.WriteString(", Ingredients: []Ingredient{")
			for _, g := range r.Ingredients {
				w.WriteString(ingLit(g) + ", ")
			}
			w.WriteString("}")
		}
		rest := ing("Input", r.Input) + ing("Material", r.Material) + ing("Template", r.Template) +
			ing("Base", r.Base) + ing("Addition", r.Addition)
		if rest != "" {
			w.WriteString(", " + strings.TrimSuffix(rest, ", "))
		}
		if r.Result != nil {
			fmt.Fprintf(w, ", Result: ItemStack{Item: %d, Count: %d", r.Result.Item, r.Result.Count)
			if r.Result.HasComponents {
				w.WriteString(", HasComponents: true")
			}
			w.WriteString("}, HasResult: true")
		}
		if r.Experience != 0 {
			fmt.Fprintf(w, ", Experience: %s", flt(r.Experience))
		}
		if r.CookingTime != 0 {
			fmt.Fprintf(w, ", CookingTime: %d", r.CookingTime)
		}
		w.WriteString("},\n")
	}
	w.WriteString("}\n")
}

func ingLit(g ingredient) string {
	if g.Tag != "" {
		return fmt.Sprintf("Ingredient{Tag: %q}", g.Tag)
	}
	return fmt.Sprintf("Ingredient{Items: []ItemID{%s}}", intList(g.Items))
}

func writePackets(w *bytes.Buffer, packets []packet) {
	w.WriteString("// パケット ID の定数。名前は <状態><向き><公式の名前を CamelCase にしたもの>。\nconst (\n")
	for _, p := range packets {
		fmt.Fprintf(w, "%s%s%s int32 = 0x%02x\n", camel(p.State), camel(p.Direction), camel(p.Name), p.ID)
	}
	w.WriteString(")\n\n")
	w.WriteString("var packets = []Packet{\n")
	for _, p := range packets {
		fmt.Fprintf(w, "{State: %q, Direction: %q, Name: %q, ID: 0x%02x},\n", p.State, p.Direction, p.Name, p.ID)
	}
	w.WriteString("}\n")
}

func writeRegistries(w *bytes.Buffer, regs []registry) {
	w.WriteString("var registries = map[string]*Registry{\n")
	for _, r := range regs {
		fmt.Fprintf(w, "%q: {Name: %q, Default: %q, Entries: %s},\n", r.Name, r.Name, r.Default, strSlice(r.Entries))
	}
	w.WriteString("}\n")
}

// ---- 小さな書式の補助 ----

func strSlice(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = strconv.Quote(s)
	}
	return "[]string{" + strings.Join(q, ", ") + "}"
}

func intList(ns []int) string {
	q := make([]string, len(ns))
	for i, n := range ns {
		q[i] = strconv.Itoa(n)
	}
	return strings.Join(q, ", ")
}

// flt は float32 として表せる最短の表記にする（公式データの float は Java の float）。
func flt(f float64) string {
	return strconv.FormatFloat(float64(float32(f)), 'g', -1, 32)
}

// f64 は float64 の最短の表記にする（当たり判定の箱の座標は Java の double）。
func f64(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// camel は "level_chunk_with_light" を "LevelChunkWithLight" にする。
func camel(s string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '/' || r == '.' }) {
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}
