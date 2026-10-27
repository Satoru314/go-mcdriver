package data

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 生成したデータを、公式データ出力（reports）と全件で照らし合わせる。
//
// 公式データ出力は `go run ./cmd/gendata` で .gendata/generated にできる（.gitignore 済み）。
// 場所は環境変数 MCDATA_GENERATED で変えられる。無ければこのテストは飛ばす。

func generatedDir(t *testing.T) string {
	dir := os.Getenv("MCDATA_GENERATED")
	if dir == "" {
		dir = filepath.Join("..", ".gendata", "generated")
	}
	if _, err := os.Stat(filepath.Join(dir, "reports", "blocks.json")); err != nil {
		t.Skipf("公式データ出力が無い（%s）。go run ./cmd/gendata で作れる", dir)
	}
	return dir
}

func loadJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

type reportRegistries map[string]struct {
	Default string `json:"default"`
	Entries map[string]struct {
		ProtocolID int32 `json:"protocol_id"`
	} `json:"entries"`
}

func TestReportsBlocks(t *testing.T) {
	dir := generatedDir(t)
	var regs reportRegistries
	loadJSON(t, filepath.Join(dir, "reports", "registries.json"), &regs)
	var raw map[string]struct {
		Definition struct {
			Type string `json:"type"`
		} `json:"definition"`
		Properties map[string][]string `json:"properties"`
		States     []struct {
			ID         StateID           `json:"id"`
			Default    bool              `json:"default"`
			Properties map[string]string `json:"properties"`
		} `json:"states"`
	}
	loadJSON(t, filepath.Join(dir, "reports", "blocks.json"), &raw)

	if len(Blocks()) != len(raw) {
		t.Errorf("ブロックの数 %d, reports %d", len(Blocks()), len(raw))
	}
	totalStates := 0
	for name, rb := range raw {
		b, ok := BlockByName(name)
		if !ok {
			t.Errorf("%s が無い", name)
			continue
		}
		if want := regs["minecraft:block"].Entries[name].ProtocolID; int32(b.ID) != want {
			t.Errorf("%s の ID %d, registry %d", name, b.ID, want)
		}
		if "minecraft:"+b.Type != rb.Definition.Type {
			t.Errorf("%s の Type %q, reports %q", name, b.Type, rb.Definition.Type)
		}
		if b.NumStates != len(rb.States) || len(b.Properties) != len(rb.Properties) {
			t.Errorf("%s: 状態 %d 個 プロパティ %d 個, reports %d 個 %d 個", name, b.NumStates, len(b.Properties), len(rb.States), len(rb.Properties))
			continue
		}
		for _, p := range b.Properties {
			if strings.Join(p.Values, ",") != strings.Join(rb.Properties[p.Name], ",") {
				t.Errorf("%s.%s の値 %v, reports %v", name, p.Name, p.Values, rb.Properties[p.Name])
			}
		}
		totalStates += len(rb.States)
		for _, s := range rb.States {
			got, ok := StateByID(s.ID)
			if !ok || got.Block != b {
				t.Errorf("StateByID(%d) が %s でない", s.ID, name)
				continue
			}
			props := got.Properties()
			if len(props) != len(s.Properties) {
				t.Errorf("%s 状態 %d: プロパティ %v, reports %v", name, s.ID, props, s.Properties)
			}
			for k, v := range s.Properties {
				if props[k] != v {
					t.Errorf("%s 状態 %d: %s=%q, reports %q", name, s.ID, k, props[k], v)
				}
			}
			byProps, err := b.State(s.Properties)
			if err != nil || byProps.ID != s.ID {
				t.Errorf("%s.State(%v) = %d (%v), reports %d", name, s.Properties, byProps.ID, err, s.ID)
			}
			if s.Default && b.DefaultState != s.ID {
				t.Errorf("%s の既定 %d, reports %d", name, b.DefaultState, s.ID)
			}
		}
	}
	if totalStates != NumBlockStates {
		t.Errorf("状態の総数 %d, reports %d", NumBlockStates, totalStates)
	}
	if _, ok := StateByID(NumBlockStates); ok {
		t.Error("範囲外の状態 ID が引けた")
	}
}

func TestReportsItems(t *testing.T) {
	dir := generatedDir(t)
	var regs reportRegistries
	loadJSON(t, filepath.Join(dir, "reports", "registries.json"), &regs)
	var raw map[string]struct {
		Components struct {
			MaxStackSize int `json:"minecraft:max_stack_size"`
			MaxDamage    int `json:"minecraft:max_damage"`
			Food         *struct {
				Nutrition    int     `json:"nutrition"`
				Saturation   float32 `json:"saturation"`
				CanAlwaysEat bool    `json:"can_always_eat"`
			} `json:"minecraft:food"`
			Tool *struct {
				Rules []json.RawMessage `json:"rules"`
			} `json:"minecraft:tool"`
		} `json:"components"`
	}
	loadJSON(t, filepath.Join(dir, "reports", "items.json"), &raw)
	if len(Items()) != len(raw) {
		t.Errorf("アイテムの数 %d, reports %d", len(Items()), len(raw))
	}
	foods, tools := 0, 0
	for name, ri := range raw {
		it, ok := ItemByName(name)
		if !ok {
			t.Errorf("%s が無い", name)
			continue
		}
		c := ri.Components
		if want := regs["minecraft:item"].Entries[name].ProtocolID; int32(it.ID) != want {
			t.Errorf("%s の ID %d, registry %d", name, it.ID, want)
		}
		if it.MaxStackSize != c.MaxStackSize || it.MaxDamage != c.MaxDamage {
			t.Errorf("%s: 重ねられる数 %d 耐久 %d, reports %d %d", name, it.MaxStackSize, it.MaxDamage, c.MaxStackSize, c.MaxDamage)
		}
		if (it.Food == nil) != (c.Food == nil) {
			t.Errorf("%s: 食べ物かどうかが違う", name)
		} else if c.Food != nil {
			foods++
			if *it.Food != (Food{c.Food.Nutrition, c.Food.Saturation, c.Food.CanAlwaysEat}) {
				t.Errorf("%s: food %+v, reports %+v", name, *it.Food, *c.Food)
			}
		}
		if (it.Tool == nil) != (c.Tool == nil) {
			t.Errorf("%s: 道具かどうかが違う", name)
		} else if c.Tool != nil {
			tools++
			if len(it.Tool.Rules) != len(c.Tool.Rules) {
				t.Errorf("%s: 道具の規則 %d 個, reports %d 個", name, len(it.Tool.Rules), len(c.Tool.Rules))
			}
		}
	}
	t.Logf("アイテム %d（食べ物 %d, 道具 %d）", len(raw), foods, tools)
}

func TestReportsPackets(t *testing.T) {
	dir := generatedDir(t)
	var raw map[string]map[string]map[string]struct {
		ProtocolID int32 `json:"protocol_id"`
	}
	loadJSON(t, filepath.Join(dir, "reports", "packets.json"), &raw)
	n := 0
	for st, dirs := range raw {
		for d, ps := range dirs {
			for name, p := range ps {
				n++
				if got, ok := PacketID(st, d, name); !ok || got != p.ProtocolID {
					t.Errorf("%s/%s/%s = %d (ok=%v), reports %d", st, d, name, got, ok, p.ProtocolID)
				}
			}
		}
	}
	if len(Packets()) != n {
		t.Errorf("パケットの数 %d, reports %d", len(Packets()), n)
	}
}

func TestReportsRegistries(t *testing.T) {
	dir := generatedDir(t)
	var regs reportRegistries
	loadJSON(t, filepath.Join(dir, "reports", "registries.json"), &regs)
	if len(RegistryNames()) != len(regs) {
		t.Errorf("registry の数 %d, reports %d", len(RegistryNames()), len(regs))
	}
	for name, rr := range regs {
		r, ok := RegistryByName(name)
		if !ok {
			t.Errorf("registry %s が無い", name)
			continue
		}
		if len(r.Entries) != len(rr.Entries) {
			t.Errorf("registry %s の項目 %d 個, reports %d 個", name, len(r.Entries), len(rr.Entries))
		}
		if strings.TrimPrefix(rr.Default, "minecraft:") != r.Default {
			t.Errorf("registry %s の既定 %q, reports %q", name, r.Default, rr.Default)
		}
		for e, v := range rr.Entries {
			if id, ok := r.ID(e); !ok || id != v.ProtocolID {
				t.Errorf("registry %s の %s = %d (ok=%v), reports %d", name, e, id, ok, v.ProtocolID)
			}
		}
	}
}

func TestReportsTagsAndRecipes(t *testing.T) {
	dir := generatedDir(t)
	tagDir := filepath.Join(dir, "data", "minecraft", "tags")
	check := func(kind string, names []string, has func(name, entry string) (bool, bool)) {
		count := 0
		filepath.WalkDir(filepath.Join(tagDir, kind), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			count++
			rel, _ := filepath.Rel(filepath.Join(tagDir, kind), path)
			tag := strings.TrimSuffix(filepath.ToSlash(rel), ".json")
			var f struct {
				Values []string `json:"values"`
			}
			loadJSON(t, path, &f)
			for _, v := range f.Values {
				if strings.HasPrefix(v, "#") {
					continue // 参照先のタグの中身は、そのタグのファイルで確かめる
				}
				in, known := has(tag, v)
				if !known || !in {
					t.Errorf("%s のタグ %s に %s が無い（known=%v）", kind, tag, v, known)
				}
			}
			return nil
		})
		if len(names) != count {
			t.Errorf("%s のタグの数 %d, データ %d", kind, len(names), count)
		}
	}
	check("block", BlockTagNames(), func(tag, entry string) (bool, bool) {
		b, ok := BlockByName(entry)
		if !ok {
			return false, false
		}
		return BlockHasTag(b.ID, tag), true
	})
	check("item", ItemTagNames(), func(tag, entry string) (bool, bool) {
		it, ok := ItemByName(entry)
		if !ok {
			return false, false
		}
		return ItemHasTag(it.ID, tag), true
	})

	files, err := filepath.Glob(filepath.Join(dir, "data", "minecraft", "recipe", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(Recipes()) != len(files) {
		t.Errorf("レシピの数 %d, データ %d", len(Recipes()), len(files))
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		r, ok := RecipeByName(name)
		if !ok {
			t.Errorf("レシピ %s が無い", name)
			continue
		}
		var raw struct {
			Type string `json:"type"`
		}
		loadJSON(t, f, &raw)
		if "minecraft:"+r.Type != raw.Type {
			t.Errorf("レシピ %s の種類 %q, データ %q", name, r.Type, raw.Type)
		}
	}
}
