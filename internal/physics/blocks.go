package physics

import (
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Satoru314/go-mcdriver/data"
	"gonum.org/v1/gonum/spatial/r3"
)

// ブロックの性質のうち、動きに効くもの。公式データにも minecraft-data にも無いので、
// 本家 1.21.4 の Blocks.java（BlockBehaviour.Properties の friction / speedFactor / jumpFactor）の値を表で持つ。

// 滑りやすさ（friction）。表に無いブロックは DefaultFriction。
var frictionTable = map[string]float32{
	"ice":         0.98,
	"packed_ice":  0.98,
	"frosted_ice": 0.98,
	"blue_ice":    0.989,
	"slime_block": 0.8,
}

// 歩く速さの倍率（speedFactor）。表に無いブロックは 1。
var speedFactorTable = map[string]float32{
	"soul_sand":   0.4,
	"honey_block": 0.4,
}

// ジャンプの倍率（jumpFactor）。表に無いブロックは 1。
var jumpFactorTable = map[string]float32{
	"honey_block": 0.5,
}

// 中に水を含むブロック（水そのもの以外）。本家ではブロックの FluidState が水の源。
var alwaysWater = map[string]bool{
	"bubble_column": true,
	"kelp":          true,
	"kelp_plant":    true,
	"seagrass":      true,
	"tall_seagrass": true,
}

// fluidKind は液体の種類。
type fluidKind uint8

const (
	noFluid fluidKind = iota
	water
	lava
)

// specialKind は、中に入ると動きに効く（entityInside）か、当たり判定が状況で変わるブロック。
type specialKind uint8

const (
	kindNone         specialKind = iota
	kindWeb                      // クモの巣（WebBlock）
	kindBerryBush                // 甘い実の茂み（SweetBerryBushBlock）
	kindPowderSnow               // 粉雪（PowderSnowBlock）
	kindBubbleColumn             // 泡の柱（BubbleColumnBlock）
	kindScaffolding              // 足場（ScaffoldingBlock）
)

// stateInfo は状態 ID ごとに前もって求めた、動きに効く性質。
type stateInfo struct {
	friction    float32
	speedFactor float32
	jumpFactor  float32
	fluid       fluidKind
	fluidAmount uint8 // 液体の量（1〜8）。本家の FluidState.getAmount
	falling     bool  // 落ちてくる流れ（本家の FlowingFluid.FALLING。液体ブロックの level が 8 以上）
	climbable   bool  // 梯子・つるなど（タグ minecraft:climbable）
	trapdoor    bool
	open        bool   // trapdoor の open
	ladder      bool   // ladder ブロック
	facing      string // trapdoor と ladder の facing
	fenceLike   bool   // fences タグ（getOnPos の特別扱い）
	wallLike    bool   // walls タグか柵のゲート（getOnPos の特別扱い）
	bounce      float64
	slime       bool
	bed         bool
	air         bool // 空気（BlockState.isAir）

	// blocksMotion は本家の BlockState.blocksMotion（クモの巣と竹の芽以外で、legacySolid）。流れの向きの計算で使う
	blocksMotion bool
	// sturdy は横の面（北・東・南・西）が丸ごと埋まっているか（本家の isFaceSturdy(..., SupportType.FULL)）
	sturdy [4]bool
	// ice は IceBlock（氷・薄氷）。落ちてくる流れの計算で、面が埋まっていても壁にしない
	ice bool

	kind specialKind
	// drag は泡の柱が下向き（drag=true、マグマブロックの上）か
	drag bool
	// scaffoldBottom と scaffoldDistance は足場の bottom と distance
	scaffoldBottom   bool
	scaffoldDistance int
}

var (
	infoOnce sync.Once
	infos    []stateInfo
)

func stateInfos() []stateInfo {
	infoOnce.Do(func() {
		infos = make([]stateInfo, data.NumBlockStates)
		for i := range infos {
			s, ok := data.StateByID(data.StateID(i))
			if !ok {
				continue
			}
			infos[i] = makeInfo(s)
		}
	})
	return infos
}

func makeInfo(s data.BlockState) stateInfo {
	b := s.Block
	in := stateInfo{friction: DefaultFriction, speedFactor: 1, jumpFactor: 1}
	if f, ok := frictionTable[b.Name]; ok {
		in.friction = f
	}
	if f, ok := speedFactorTable[b.Name]; ok {
		in.speedFactor = f
	}
	if f, ok := jumpFactorTable[b.Name]; ok {
		in.jumpFactor = f
	}
	switch {
	case b.Name == "water" || b.Name == "lava":
		in.fluid = water
		if b.Name == "lava" {
			in.fluid = lava
		}
		lv, _ := s.Get("level")
		n, _ := strconv.Atoi(lv)
		// level 0 は源（量 8）、1〜7 は流れ（量 8-level）、8 以上は落ちてくる流れ（量 8、FALLING）。
		// 本家の LiquidBlock の stateCache と同じ
		switch {
		case n == 0:
			in.fluidAmount = 8
		case n >= 8:
			in.fluidAmount, in.falling = 8, true
		default:
			in.fluidAmount = uint8(8 - n)
		}
	case alwaysWater[b.Name]:
		in.fluid, in.fluidAmount = water, 8
	default:
		if v, ok := s.Get("waterlogged"); ok && v == "true" {
			in.fluid, in.fluidAmount = water, 8
		}
	}
	in.climbable = data.BlockHasTag(b.ID, "climbable")
	in.ladder = b.Name == "ladder"
	in.trapdoor = b.Type == "trapdoor" || strings.HasSuffix(b.Name, "_trapdoor")
	if in.trapdoor || in.ladder {
		in.facing, _ = s.Get("facing")
		v, _ := s.Get("open")
		in.open = v == "true"
	}
	in.fenceLike = data.BlockHasTag(b.ID, "fences")
	in.wallLike = data.BlockHasTag(b.ID, "walls") || data.BlockHasTag(b.ID, "fence_gates")
	in.slime = b.Name == "slime_block"
	in.bed = b.Type == "bed"
	in.air = b.Name == "air" || b.Name == "cave_air" || b.Name == "void_air"
	in.ice = b.Name == "ice" || b.Name == "frosted_ice"
	switch b.Name {
	case "cobweb":
		in.kind = kindWeb
	case "sweet_berry_bush":
		in.kind = kindBerryBush
	case "powder_snow":
		in.kind = kindPowderSnow
	case "bubble_column":
		in.kind = kindBubbleColumn
		v, _ := s.Get("drag")
		in.drag = v == "true"
	case "scaffolding":
		in.kind = kindScaffolding
		v, _ := s.Get("bottom")
		in.scaffoldBottom = v == "true"
		d, _ := s.Get("distance")
		in.scaffoldDistance, _ = strconv.Atoi(d)
	}
	solid := legacySolid(s)
	in.blocksMotion = solid && b.Name != "cobweb" && b.Name != "bamboo_sapling"
	in.sturdy = sturdyFaces(s)
	return in
}

// forceSolidOn と forceSolidOff は、本家 1.21.4 の Blocks.java で BlockBehaviour.Properties に
// forceSolidOn() / forceSolidOff() を付けたブロック（ofLegacyCopy で写したものを含む）。
// 当たり判定の大きさに関わらず legacySolid がこの値になる。
var (
	forceSolidOn = setOf(
		"cobweb", "moving_piston", "cake", "chain", "turtle_egg", "conduit", "bamboo_sapling", "bamboo", "bell",
		"lantern", "soul_lantern", "amethyst_cluster", "large_amethyst_bud", "medium_amethyst_bud", "small_amethyst_bud",
		"sculk_vein", "lightning_rod", "pointed_dripstone",
		"light_weighted_pressure_plate", "heavy_weighted_pressure_plate",
	)
	forceSolidOff = setOf("ladder", "snow", "end_rod", "chorus_plant", "chorus_flower", "azalea", "flowering_azalea", "big_dripleaf")
	// forceSolidOnSuffix は名前の終わりで決まるもの（看板、旗、感圧板、柵のゲート、壁、ろうそく付きケーキ、珊瑚の死骸、シュルカーボックス）
	forceSolidOnSuffix = []string{
		"_sign", "_banner", "_pressure_plate", "_fence_gate", "_wall", "candle_cake", "shulker_box",
	}
	forceSolidOnPrefix = []string{"dead_"}
)

func setOf(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// legacySolid は本家の BlockStateBase.calculateSolid（isSolid の元）。
// forceSolidOn/Off が無ければ、当たり判定の外枠の 3 辺の平均が 0.7291666666666666 以上か、高さが 1 以上なら固い。
// 足場と粉雪は本家では形が状況で変わる（dynamicShape）ので、固くない。
func legacySolid(s data.BlockState) bool {
	n := s.Block.Name
	if forceSolidOn[n] {
		return true
	}
	if forceSolidOff[n] {
		return false
	}
	for _, suf := range forceSolidOnSuffix {
		if strings.HasSuffix(n, suf) {
			return true
		}
	}
	for _, pre := range forceSolidOnPrefix {
		// 珊瑚の死骸（ブロック・珊瑚・扇・壁の扇）
		if strings.HasPrefix(n, pre) && strings.Contains(n, "coral") {
			return true
		}
	}
	if n == "scaffolding" || n == "powder_snow" {
		return false
	}
	boxes := s.CollisionBoxes()
	if len(boxes) == 0 {
		return false
	}
	b := boxes[0]
	for _, x := range boxes[1:] {
		b.Min = r3.Vec{X: min(b.Min.X, x.Min.X), Y: min(b.Min.Y, x.Min.Y), Z: min(b.Min.Z, x.Min.Z)}
		b.Max = r3.Vec{X: max(b.Max.X, x.Max.X), Y: max(b.Max.Y, x.Max.Y), Z: max(b.Max.Z, x.Max.Z)}
	}
	sx, sy, sz := b.Max.X-b.Min.X, b.Max.Y-b.Min.Y, b.Max.Z-b.Min.Z
	return (sx+sy+sz)/3 >= 0.7291666666666666 || sy >= 1
}

// 横の向き。本家の Direction.Plane.HORIZONTAL と同じ順（北、東、南、西）。
type horizontal struct{ dx, dz int }

var horizontals = [4]horizontal{{0, -1}, {1, 0}, {0, 1}, {-1, 0}}

// sturdyFaces は横の 4 つの面が丸ごと埋まっているかを返す（本家の isFaceSturdy の FULL）。
// 本家は面を支える形（getBlockSupportShape）で決める。ふつうは当たり判定と同じで、違うものだけ別に扱う
// （葉は空、泥とソウルサンドは立方体、雪は層の数 × 2/16 の高さ）。
func sturdyFaces(s data.BlockState) [4]bool {
	var out [4]bool
	n := s.Block.Name
	boxes := s.CollisionBoxes()
	switch {
	case data.BlockHasTag(s.Block.ID, "leaves"):
		return out
	case n == "mud" || n == "soul_sand":
		boxes = []r3.Box{{Max: r3.Vec{X: 1, Y: 1, Z: 1}}}
	case n == "snow":
		v, _ := s.Get("layers")
		l, _ := strconv.Atoi(v)
		boxes = []r3.Box{{Max: r3.Vec{X: 1, Y: float64(l) * 2 / 16, Z: 1}}}
	}
	for i, d := range horizontals {
		out[i] = faceFull(boxes, d)
	}
	return out
}

// faceFull は箱の集まりの、向き d の面（ブロックの端）が 1×1 を丸ごと覆うかを返す。
func faceFull(boxes []r3.Box, d horizontal) bool {
	const eps = 1e-7
	// 面に接する箱を、面の上の (u, v) = (y, 横) の長方形にする
	type rect struct{ u0, u1, v0, v1 float64 }
	var rs []rect
	us, vs := []float64{0, 1}, []float64{0, 1}
	for _, b := range boxes {
		var touch bool
		var v0, v1 float64
		switch {
		case d.dx > 0:
			touch, v0, v1 = b.Max.X >= 1-eps, b.Min.Z, b.Max.Z
		case d.dx < 0:
			touch, v0, v1 = b.Min.X <= eps, b.Min.Z, b.Max.Z
		case d.dz > 0:
			touch, v0, v1 = b.Max.Z >= 1-eps, b.Min.X, b.Max.X
		default:
			touch, v0, v1 = b.Min.Z <= eps, b.Min.X, b.Max.X
		}
		if !touch {
			continue
		}
		r := rect{max(0, b.Min.Y), min(1, b.Max.Y), max(0, v0), min(1, v1)}
		rs = append(rs, r)
		us = append(us, r.u0, r.u1)
		vs = append(vs, r.v0, r.v1)
	}
	if len(rs) == 0 {
		return false
	}
	// 座標の切れ目で分けた小さな長方形の真ん中が、どれかに覆われているか
	sort.Float64s(us)
	sort.Float64s(vs)
	for i := 0; i+1 < len(us); i++ {
		if us[i+1]-us[i] < eps {
			continue
		}
		cu := (us[i] + us[i+1]) / 2
		for j := 0; j+1 < len(vs); j++ {
			if vs[j+1]-vs[j] < eps {
				continue
			}
			cv := (vs[j] + vs[j+1]) / 2
			covered := false
			for _, r := range rs {
				if cu > r.u0 && cu < r.u1 && cv > r.v0 && cv < r.v1 {
					covered = true
					break
				}
			}
			if !covered {
				return false
			}
		}
	}
	return true
}

func infoOf(s data.BlockState) *stateInfo {
	if s.Block == nil || s.ID < 0 || s.ID >= data.NumBlockStates {
		return &airInfo
	}
	return &stateInfos()[s.ID]
}

var airInfo = stateInfo{friction: DefaultFriction, speedFactor: 1, jumpFactor: 1, air: true}

// ownHeight は液体の、その 1 ブロックの中での高さ（本家の FluidState.getOwnHeight = 量/9）。
func (in *stateInfo) ownHeight() float32 { return float32(in.fluidAmount) / 9 }
