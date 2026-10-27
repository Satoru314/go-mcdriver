package data

import "gonum.org/v1/gonum/spatial/r3"

// bx は生成コード用に箱を作る。
func bx(x0, y0, z0, x1, y1, z1 float64) r3.Box {
	return r3.Box{Min: r3.Vec{X: x0, Y: y0, Z: z0}, Max: r3.Vec{X: x1, Y: y1, Z: z1}}
}

// CollisionBoxes は、この状態の当たり判定の箱の一覧を返す。座標はブロックの中の 0〜1（ブロックの北西の下の角が原点、
// +x が東、+y が上、+z が南）で、ワールドの座標にするにはブロックの位置を足す。当たり判定が無いもの（空気、草、水など）は空。
// フェンスや壁のように 1 より高い（1.5）箱もある。
// 値は minecraft-data の blockCollisionShapes から取ったもの。返した一覧は共有しているので書き換えないこと。
func (s BlockState) CollisionBoxes() []r3.Box {
	if s.ID < 0 || s.ID >= NumBlockStates {
		return nil
	}
	return collisionShapes[stateCollisionShape[s.ID]]
}

// HasCollision は当たり判定の箱があるかを返す。
func (s BlockState) HasCollision() bool { return len(s.CollisionBoxes()) > 0 }

// IsFullCube は当たり判定がちょうど 0〜1 の立方体1つかを返す。
func (s BlockState) IsFullCube() bool {
	b := s.CollisionBoxes()
	return len(b) == 1 && b[0] == r3.Box{Max: r3.Vec{X: 1, Y: 1, Z: 1}}
}
