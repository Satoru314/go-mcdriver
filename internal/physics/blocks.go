package physics

import (
	"strconv"
	"strings"
	"sync"

	"github.com/Satoru314/go-mcdriver/data"
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

// stateInfo は状態 ID ごとに前もって求めた、動きに効く性質。
type stateInfo struct {
	friction    float32
	speedFactor float32
	jumpFactor  float32
	fluid       fluidKind
	fluidAmount uint8 // 液体の量（1〜8）。本家の FluidState.getAmount
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
		// level 0 は源（量 8）、1〜7 は流れ（量 8-level）、8 以上は落ちてくる流れ（量 8）
		switch {
		case n == 0 || n >= 8:
			in.fluidAmount = 8
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
	return in
}

func infoOf(s data.BlockState) *stateInfo {
	if s.Block == nil || s.ID < 0 || s.ID >= data.NumBlockStates {
		return &airInfo
	}
	return &stateInfos()[s.ID]
}

var airInfo = stateInfo{friction: DefaultFriction, speedFactor: 1, jumpFactor: 1}

// ownHeight は液体の、その 1 ブロックの中での高さ（本家の FluidState.getOwnHeight = 量/9）。
func (in *stateInfo) ownHeight() float32 { return float32(in.fluidAmount) / 9 }
