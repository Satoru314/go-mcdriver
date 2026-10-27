package mcdriver

import (
	"fmt"
	"math"

	"gonum.org/v1/gonum/spatial/r3"
)

// BlockPos はブロックの位置（整数の座標）。+x が東、+z が南、+y が上。
type BlockPos struct {
	X, Y, Z int
}

// BlockPosOf は小数の位置を含むブロックの位置を返す（それぞれ下に丸める。-0.5 は -1）。
func BlockPosOf(v r3.Vec) BlockPos {
	return BlockPos{X: int(math.Floor(v.X)), Y: int(math.Floor(v.Y)), Z: int(math.Floor(v.Z))}
}

// Offset は dx, dy, dz だけずらした位置を返す。
func (p BlockPos) Offset(dx, dy, dz int) BlockPos {
	return BlockPos{X: p.X + dx, Y: p.Y + dy, Z: p.Z + dz}
}

// Neighbor は面 f の側の隣のブロックの位置を返す。
func (p BlockPos) Neighbor(f Face) BlockPos {
	n := f.normal()
	return p.Offset(n[0], n[1], n[2])
}

// Add は p に q を足した位置を返す。
func (p BlockPos) Add(q BlockPos) BlockPos { return p.Offset(q.X, q.Y, q.Z) }

// Vec はブロックの一番小さい角（北西の下）の位置を返す。
func (p BlockPos) Vec() r3.Vec { return r3.Vec{X: float64(p.X), Y: float64(p.Y), Z: float64(p.Z)} }

// Center はブロックの中心の位置を返す。
func (p BlockPos) Center() r3.Vec {
	return r3.Vec{X: float64(p.X) + 0.5, Y: float64(p.Y) + 0.5, Z: float64(p.Z) + 0.5}
}

func (p BlockPos) String() string { return fmt.Sprintf("(%d, %d, %d)", p.X, p.Y, p.Z) }

// Face はブロックの面（6 方向）。値は Minecraft の Direction の番号と同じ（ブロックを置くパケットなどでそのまま使う）。
type Face int

// 面の方向。
const (
	Down  Face = iota // -y
	Up                // +y
	North             // -z
	South             // +z
	West              // -x
	East              // +x
)

// Faces は 6 方向を番号の順に並べたもの。
var Faces = [6]Face{Down, Up, North, South, West, East}

var faceNormals = [6][3]int{
	Down:  {0, -1, 0},
	Up:    {0, 1, 0},
	North: {0, 0, -1},
	South: {0, 0, 1},
	West:  {-1, 0, 0},
	East:  {1, 0, 0},
}

var faceNames = [6]string{"down", "up", "north", "south", "west", "east"}

// Valid は 6 方向のどれかなら true を返す。
func (f Face) Valid() bool { return f >= Down && f <= East }

func (f Face) normal() [3]int {
	if !f.Valid() {
		panic(fmt.Sprintf("mcdriver: 正しくない Face %d", int(f)))
	}
	return faceNormals[f]
}

// Normal は面の外向きの単位ベクトルを返す。
func (f Face) Normal() r3.Vec {
	n := f.normal()
	return r3.Vec{X: float64(n[0]), Y: float64(n[1]), Z: float64(n[2])}
}

// Opposite は反対の面を返す。
func (f Face) Opposite() Face { return f ^ 1 }

// String は "down" "up" "north" "south" "west" "east"（ブロックの状態の facing の値と同じ）を返す。
func (f Face) String() string {
	if !f.Valid() {
		return fmt.Sprintf("Face(%d)", int(f))
	}
	return faceNames[f]
}
