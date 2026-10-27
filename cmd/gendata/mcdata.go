package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 公式データに無い情報（ブロックの硬さ・爆発耐性・壊せるか・落とすのに必要な道具・光・当たり判定の形、
// エンティティの大きさ）は、PrismarineJS の minecraft-data（JSON）から取る。
// 取得元は特定のコミットに固定し、ファイルごとに sha256 を確かめる。

// mcdataCommit は minecraft-data のタグ 3.117.0（2026-09-22 公開の最新の安定版）のコミット。
const (
	mcdataTag    = "3.117.0"
	mcdataCommit = "15411f050f040e4c5df09352a75a9912ad8c3eeb"
	mcdataRawURL = "https://raw.githubusercontent.com/PrismarineJS/minecraft-data/" + mcdataCommit + "/"
	// mcdataVersion は minecraft-data の中のバージョンの名前。下の sha256 はこのバージョンのファイルのもの
	mcdataVersion = "1.21.4"
)

// mcdataFile は取得するファイル1つ。Path はリポジトリの中の場所。
type mcdataFile struct {
	Key    string // dataPaths.json の項目名（dataPaths.json 自体は空）
	Path   string
	SHA256 string
}

var mcdataFiles = []mcdataFile{
	{"", "data/dataPaths.json", "de619e27bec3f62a9f3b0326b55b5b63a6e7abc22717ce50b34185ed187d3359"},
	{"blocks", "data/pc/1.21.4/blocks.json", "16a26f1dba3e8ae7a5a05a7e25f724ba924b0bacbd17481992d027fbaf70e3e8"},
	{"blockCollisionShapes", "data/pc/1.21.4/blockCollisionShapes.json", "f2d07593c4188ff05109b62481e5644713e8f3349a7a6a3f63c04dc93b4bc555"},
	{"entities", "data/pc/1.21.4/entities.json", "fbf1a0c683a8f0624d75d094f95cd6a2a24f19560466e62cfc2ba7e95a6e9b67"},
	// items.json は harvestTools のアイテムの番号を、公式データのアイテムと照らし合わせるためだけに使う
	{"items", "data/pc/1.21.4/items.json", "f88fb4279715c23e86ff103a09e20a4b15d6b8aa52559bb4d7364258a9d8225d"},
}

// mcdataDir は workDir の中の、固定したコミットのファイルを置くフォルダ。
func mcdataDir(workDir string) string {
	return filepath.Join(workDir, "minecraft-data", mcdataCommit)
}

// fetchMCData は minecraft-data のファイルを mcdataDir(workDir) に置く。
// すでにあって sha256 が合えばダウンロードしない。ダウンロードしたものの sha256 が合わなければエラーにする。
func fetchMCData(workDir string) error {
	dir := mcdataDir(workDir)
	for _, f := range mcdataFiles {
		dst := filepath.Join(dir, filepath.FromSlash(f.Path))
		if b, err := os.ReadFile(dst); err == nil && sha256Hex(b) == f.SHA256 {
			continue
		}
		url := mcdataRawURL + f.Path
		fmt.Printf("minecraft-data を取得: %s\n", url)
		body, err := get(url)
		if err != nil {
			return err
		}
		if sum := sha256Hex(body); sum != f.SHA256 {
			return fmt.Errorf("%s の sha256 が合わない: 期待 %s, 実際 %s", f.Path, f.SHA256, sum)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, body, 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("minecraft-data %s（%s）の %d ファイル: sha256 確認済み\n", mcdataTag, mcdataCommit, len(mcdataFiles))
	return nil
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// ---- 読み込みと公式データとの突き合わせ ----

// blockExtra は minecraft-data から取ったブロックの情報。
type blockExtra struct {
	Hardness     float64 // -1 は壊せない
	Resistance   float64
	Diggable     bool
	HarvestTools []int // 落とすのに必要な道具（アイテム ID の昇順）。nil は道具が要らない
	EmitLight    int
	FilterLight  int
	Transparent  bool
}

// entityInfo はエンティティの種類の大きさ。
type entityInfo struct {
	ID     int
	Name   string
	Width  float64
	Height float64
}

// mcdata は minecraft-data から取り込んだもの。
type mcdata struct {
	Blocks     []blockExtra   // ブロック ID の順
	Shapes     [][][6]float64 // 当たり判定の形（minecraft-data の形の番号の順）。箱は minX, minY, minZ, maxX, maxY, maxZ
	StateShape []int          // 状態 ID ごとの形の番号
	Entities   []entityInfo   // entity_type の registry の順
}

// mismatchError は公式データと合わなかったものの一覧。生成を止めるために返す。
type mismatchError struct{ List []string }

func (e *mismatchError) Error() string {
	return fmt.Sprintf("minecraft-data が公式データと %d 件合わない:\n  %s", len(e.List), strings.Join(e.List, "\n  "))
}

// loadMCData は minecraft-data を読み、公式データ（blocks, items, registries, ブロックのタグ）と突き合わせる。
// 1つでも合わなければ、合わなかったもの全部の一覧を *mismatchError で返す（推測で値を埋めない）。
func loadMCData(workDir string, blocks []block, items []item, regs []registry, blockTags map[string][]int) (*mcdata, error) {
	dir := mcdataDir(workDir)
	path := func(key string) string {
		for _, f := range mcdataFiles {
			if f.Key == key {
				return filepath.Join(dir, filepath.FromSlash(f.Path))
			}
		}
		panic("知らないファイル " + key)
	}
	var bad []string
	badf := func(format string, a ...any) { bad = append(bad, fmt.Sprintf(format, a...)) }

	// dataPaths.json で、使うファイルが 1.21.4 のフォルダのものかを確かめる
	var dataPaths struct {
		PC map[string]map[string]string `json:"pc"`
	}
	if err := readJSON(filepath.Join(dir, "data", "dataPaths.json"), &dataPaths); err != nil {
		return nil, err
	}
	for _, f := range mcdataFiles {
		if f.Key == "" {
			continue
		}
		want := "data/" + dataPaths.PC[mcdataVersion][f.Key] + "/" + f.Key + ".json"
		if want != f.Path {
			badf("dataPaths.json: %s %s は %s を指している（取得したのは %s）", mcdataVersion, f.Key, want, f.Path)
		}
	}

	// アイテム（harvestTools の番号の意味を確かめるため）
	var mItems []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := readJSON(path("items"), &mItems); err != nil {
		return nil, err
	}
	if len(mItems) != len(items) {
		badf("アイテムの数: minecraft-data %d, 公式 %d", len(mItems), len(items))
	}
	for _, mi := range mItems {
		if mi.ID < 0 || mi.ID >= len(items) || items[mi.ID].Name != mi.Name {
			badf("アイテム %s: minecraft-data の ID %d が公式データと合わない", mi.Name, mi.ID)
		}
	}

	// ブロック
	var mBlocks []struct {
		ID           int             `json:"id"`
		Name         string          `json:"name"`
		Hardness     *float64        `json:"hardness"`
		Resistance   *float64        `json:"resistance"`
		Diggable     *bool           `json:"diggable"`
		Transparent  *bool           `json:"transparent"`
		EmitLight    *int            `json:"emitLight"`
		FilterLight  *int            `json:"filterLight"`
		DefaultState int             `json:"defaultState"`
		MinStateID   int             `json:"minStateId"`
		MaxStateID   int             `json:"maxStateId"`
		HarvestTools map[string]bool `json:"harvestTools"`
		States       []struct {
			Name      string   `json:"name"`
			Type      string   `json:"type"`
			NumValues int      `json:"num_values"`
			Values    []string `json:"values"`
		} `json:"states"`
	}
	if err := readJSON(path("blocks"), &mBlocks); err != nil {
		return nil, err
	}
	blockIndex := map[string]int{}
	for _, b := range blocks {
		blockIndex[b.Name] = b.ID
	}
	md := &mcdata{Blocks: make([]blockExtra, len(blocks))}
	seen := make([]bool, len(blocks))
	if len(mBlocks) != len(blocks) {
		badf("ブロックの数: minecraft-data %d, 公式 %d", len(mBlocks), len(blocks))
	}
	for _, mb := range mBlocks {
		id, ok := blockIndex[mb.Name]
		if !ok {
			badf("ブロック %s: 公式データに無い", mb.Name)
			continue
		}
		b := blocks[id]
		if seen[id] {
			badf("ブロック %s: minecraft-data に2回ある", mb.Name)
			continue
		}
		seen[id] = true
		if mb.ID != b.ID {
			badf("ブロック %s: ID minecraft-data %d, 公式 %d", mb.Name, mb.ID, b.ID)
		}
		if mb.MinStateID != b.MinState || mb.MaxStateID != b.MinState+b.NumStates-1 || mb.DefaultState != b.DefaultState {
			badf("ブロック %s: 状態 ID minecraft-data %d〜%d 既定 %d, 公式 %d〜%d 既定 %d", mb.Name,
				mb.MinStateID, mb.MaxStateID, mb.DefaultState, b.MinState, b.MinState+b.NumStates-1, b.DefaultState)
		}
		// プロパティの名前と値（順番も）を比べる。bool のものは minecraft-data に値の一覧が無い
		if len(mb.States) != len(b.Properties) {
			badf("ブロック %s: プロパティの数 minecraft-data %d, 公式 %d", mb.Name, len(mb.States), len(b.Properties))
		}
		n := 1
		for _, st := range mb.States {
			n *= st.NumValues
			vals := st.Values
			if st.Type == "bool" && vals == nil {
				vals = []string{"true", "false"}
			}
			var official []string
			for _, p := range b.Properties {
				if p.Name == st.Name {
					official = p.Values
				}
			}
			if official == nil {
				badf("ブロック %s: プロパティ %s が公式データに無い", mb.Name, st.Name)
			} else if strings.Join(vals, ",") != strings.Join(official, ",") || st.NumValues != len(official) {
				badf("ブロック %s: プロパティ %s の値 minecraft-data %v（%d 個）, 公式 %v", mb.Name, st.Name, vals, st.NumValues, official)
			}
		}
		if n != b.NumStates {
			badf("ブロック %s: 状態の数 minecraft-data %d, 公式 %d", mb.Name, n, b.NumStates)
		}
		if mb.Hardness == nil || mb.Resistance == nil || mb.Diggable == nil || mb.Transparent == nil || mb.EmitLight == nil || mb.FilterLight == nil {
			badf("ブロック %s: hardness, resistance, diggable, transparent, emitLight, filterLight のどれかが無い", mb.Name)
			continue
		}
		if *mb.EmitLight < 0 || *mb.EmitLight > 15 || *mb.FilterLight < 0 || *mb.FilterLight > 15 {
			badf("ブロック %s: emitLight %d / filterLight %d が 0〜15 の外", mb.Name, *mb.EmitLight, *mb.FilterLight)
		}
		ex := blockExtra{Hardness: *mb.Hardness, Resistance: *mb.Resistance, Diggable: *mb.Diggable,
			EmitLight: *mb.EmitLight, FilterLight: *mb.FilterLight, Transparent: *mb.Transparent}
		// 壊せない（hardness -1）なら diggable は偽のはず。逆に diggable が偽で hardness が 0 以上なのは空気だけ
		if ex.Hardness < 0 && ex.Diggable || !ex.Diggable && ex.Hardness >= 0 && !strings.HasSuffix(mb.Name, "air") {
			badf("ブロック %s: hardness %v と diggable %v が食い違う", mb.Name, ex.Hardness, ex.Diggable)
		}
		if mb.HarvestTools != nil {
			ex.HarvestTools = []int{}
			for k, v := range mb.HarvestTools {
				var it int
				if _, err := fmt.Sscanf(k, "%d", &it); err != nil || !v || it < 0 || it >= len(items) {
					badf("ブロック %s: harvestTools の項目 %q: %v が読めない", mb.Name, k, v)
					continue
				}
				ex.HarvestTools = append(ex.HarvestTools, it)
			}
			sort.Ints(ex.HarvestTools)
		}
		md.Blocks[id] = ex
	}
	for id, ok := range seen {
		if !ok {
			badf("ブロック %s: minecraft-data に無い", blocks[id].Name)
		}
	}

	// harvestTools を公式データの道具の規則（correct_for_drops）と照らし合わせる。
	// 道具が要るブロックについて、「そのブロックに correct_for_drops が真になる道具」の集まりが harvestTools と一致するか
	for id, ex := range md.Blocks {
		if ex.HarvestTools == nil {
			continue
		}
		var correct []int
		for _, it := range items {
			if it.Tool != nil && toolCorrectForDrops(it.Tool, blocks[id].Name, id, blockTags) {
				correct = append(correct, it.ID)
			}
		}
		if intList(correct) != intList(ex.HarvestTools) {
			badf("ブロック %s: harvestTools %v が公式の道具の規則 %v と違う", blocks[id].Name, itemNames(items, ex.HarvestTools), itemNames(items, correct))
		}
	}

	// 当たり判定の形
	var mShapes struct {
		Blocks map[string]json.RawMessage `json:"blocks"`
		Shapes map[string][][]float64     `json:"shapes"`
	}
	if err := readJSON(path("blockCollisionShapes"), &mShapes); err != nil {
		return nil, err
	}
	md.Shapes = make([][][6]float64, len(mShapes.Shapes))
	for k, boxes := range mShapes.Shapes {
		var i int
		if _, err := fmt.Sscanf(k, "%d", &i); err != nil || i < 0 || i >= len(md.Shapes) || md.Shapes[i] != nil {
			badf("当たり判定の形の番号 %q が 0〜%d の連番でない", k, len(md.Shapes)-1)
			continue
		}
		md.Shapes[i] = [][6]float64{}
		for _, bx := range boxes {
			if len(bx) != 6 || bx[0] > bx[3] || bx[1] > bx[4] || bx[2] > bx[5] {
				badf("当たり判定の形 %s: 箱 %v が読めない", k, bx)
				continue
			}
			md.Shapes[i] = append(md.Shapes[i], [6]float64(bx))
		}
	}
	if len(md.Shapes) > 1<<16 {
		badf("当たり判定の形が %d 個あり、uint16 の番号に入らない", len(md.Shapes))
	}
	total := 0
	for _, b := range blocks {
		total += b.NumStates
	}
	md.StateShape = make([]int, total)
	if len(mShapes.Blocks) != len(blocks) {
		badf("当たり判定のブロックの数: minecraft-data %d, 公式 %d", len(mShapes.Blocks), len(blocks))
	}
	for name, raw := range mShapes.Blocks {
		id, ok := blockIndex[name]
		if !ok {
			badf("当たり判定のブロック %s: 公式データに無い", name)
			continue
		}
		b := blocks[id]
		var one int
		var list []int
		if json.Unmarshal(raw, &one) == nil {
			// 全状態で同じ形
			list = make([]int, b.NumStates)
			for i := range list {
				list[i] = one
			}
		} else if err := json.Unmarshal(raw, &list); err != nil {
			badf("当たり判定のブロック %s: %s が読めない", name, raw)
			continue
		}
		if len(list) != b.NumStates {
			badf("当たり判定のブロック %s: 状態の数 minecraft-data %d, 公式 %d", name, len(list), b.NumStates)
			continue
		}
		for i, s := range list {
			if s < 0 || s >= len(md.Shapes) {
				badf("当たり判定のブロック %s: 形の番号 %d が無い", name, s)
			}
			md.StateShape[b.MinState+i] = s
		}
	}
	for _, b := range blocks {
		if _, ok := mShapes.Blocks[b.Name]; !ok {
			badf("当たり判定のブロック %s: minecraft-data に無い", b.Name)
		}
	}

	// エンティティ
	var mEntities []struct {
		ID     int      `json:"id"`
		Name   string   `json:"name"`
		Width  *float64 `json:"width"`
		Height *float64 `json:"height"`
	}
	if err := readJSON(path("entities"), &mEntities); err != nil {
		return nil, err
	}
	reg := findRegistry(regs, "entity_type")
	if len(mEntities) != len(reg) {
		badf("エンティティの数: minecraft-data %d, 公式 %d", len(mEntities), len(reg))
	}
	md.Entities = make([]entityInfo, len(reg))
	for _, me := range mEntities {
		if me.ID < 0 || me.ID >= len(reg) || reg[me.ID] != me.Name {
			badf("エンティティ %s: minecraft-data の ID %d が公式の entity_type と合わない", me.Name, me.ID)
			continue
		}
		if md.Entities[me.ID].Name != "" {
			badf("エンティティ %s: minecraft-data に2回ある", me.Name)
			continue
		}
		if me.Width == nil || me.Height == nil || *me.Width < 0 || *me.Height < 0 {
			badf("エンティティ %s: width か height が無い", me.Name)
			continue
		}
		md.Entities[me.ID] = entityInfo{ID: me.ID, Name: me.Name, Width: *me.Width, Height: *me.Height}
	}
	for id, e := range md.Entities {
		if e.Name == "" {
			badf("エンティティ %s: minecraft-data に無い", reg[id])
		}
	}

	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, &mismatchError{List: bad}
	}
	return md, nil
}

// toolCorrectForDrops は、道具がブロックに「適した道具」かを、ゲームと同じく
// 規則を順に見て、ブロックに当てはまり correct_for_drops を持つ最初の規則で決める。
func toolCorrectForDrops(t *tool, blockName string, id int, blockTags map[string][]int) bool {
	for _, r := range t.Rules {
		if r.CorrectForDrops == nil || !ruleMatches(r, blockName, id, blockTags) {
			continue
		}
		return *r.CorrectForDrops
	}
	return false
}

func ruleMatches(r toolRule, blockName string, id int, blockTags map[string][]int) bool {
	if r.Tag != "" {
		for _, x := range blockTags[r.Tag] {
			if x == id {
				return true
			}
		}
		return false
	}
	for _, n := range r.Blocks {
		if n == blockName {
			return true
		}
	}
	return false
}

func itemNames(items []item, ids []int) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = items[id].Name
	}
	return out
}
