package chunk

import (
	"fmt"
	"math/bits"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// LightData は明るさの部分（Chunk Data and Update Light と Update Light の後半）。
//
//	Sky Light Mask (BitSet), Block Light Mask (BitSet),
//	Empty Sky Light Mask (BitSet), Empty Block Light Mask (BitSet),
//	Sky Light Arrays (Prefixed Array of Prefixed Array of Byte (2048)),
//	Block Light Arrays (同じ形)
//
// マスクのビット i は明るさのセクション i（0 がワールドの一番下のさらに 1 つ下）を表す。
// 配列は、マスクの立っているビットの小さい順に並ぶ。
type LightData struct {
	SkyMask, BlockMask           protocol.BitSet
	EmptySkyMask, EmptyBlockMask protocol.BitSet
	Sky, Block                   [][]byte
}

// ReadLightData は明るさの部分を読む。配列は 1 つ 2048 バイトでなければエラー。
func ReadLightData(d *protocol.Decoder) (*LightData, error) {
	ld := &LightData{}
	var err error
	for _, m := range []*protocol.BitSet{&ld.SkyMask, &ld.BlockMask, &ld.EmptySkyMask, &ld.EmptyBlockMask} {
		if *m, err = d.ReadBitSet(); err != nil {
			return nil, err
		}
	}
	readArr := func(d *protocol.Decoder) ([]byte, error) {
		b, err := d.ReadPrefixedBytes(LightBytes)
		if err != nil {
			return nil, err
		}
		if len(b) != LightBytes {
			return nil, fmt.Errorf("%w: 明るさの配列が %d バイト（%d バイトのはず）", ErrBadData, len(b), LightBytes)
		}
		return b, nil
	}
	if ld.Sky, err = protocol.ReadPrefixedArray(d, d.Remaining()/(LightBytes+2), readArr); err != nil {
		return nil, fmt.Errorf("sky light: %w", err)
	}
	if ld.Block, err = protocol.ReadPrefixedArray(d, d.Remaining()/(LightBytes+2), readArr); err != nil {
		return nil, fmt.Errorf("block light: %w", err)
	}
	return ld, nil
}

// ReadUpdateLight は Update Light パケットの中身（Chunk X (VarInt), Chunk Z (VarInt), Light Data）を読む。
func ReadUpdateLight(d *protocol.Decoder) (x, z int32, ld *LightData, err error) {
	if x, err = d.ReadVarInt(); err != nil {
		return
	}
	if z, err = d.ReadVarInt(); err != nil {
		return
	}
	ld, err = ReadLightData(d)
	return
}

// Light はチャンクの明るさ。セクションごとに 2048 バイトの配列を持つ。
// 配列が nil のセクションは、まだ明るさを受け取っていない（値が分からない）。
// 配列は書き換えずに丸ごと差し替える（中身を書き換えないこと。空のセクションは配列を共有している）。
type Light struct {
	Sky, Block [][]byte
}

// zeroLight は「空」と送られたセクションが共有する、すべて 0 の配列。
var zeroLight = make([]byte, LightBytes)

func newLight(n int) Light {
	return Light{Sky: make([][]byte, n), Block: make([][]byte, n)}
}

// countBits はマスクのビット 0〜n-1 のうち立っている数を返す。
func countBits(m protocol.BitSet, n int) int {
	c := 0
	for i, w := range m {
		if i*64 >= n {
			break
		}
		if rest := n - i*64; rest < 64 {
			w &= 1<<rest - 1
		}
		c += bits.OnesCount64(w)
	}
	return c
}

// applyLayer はマスクと配列を 1 種類の明るさ（sky か block）に反映する。
// Java 版と同じく、マスクが立っていれば配列を、空のマスクが立っていれば 0 を入れ、どちらもなければ変えない。
// 範囲の外のビットと余った配列は使わない。配列が足りなければエラー。
func applyLayer(dst [][]byte, mask, empty protocol.BitSet, arrs [][]byte) error {
	if need := countBits(mask, len(dst)); len(arrs) < need {
		return fmt.Errorf("%w: 明るさの配列が %d 個（マスクでは %d 個）", ErrBadData, len(arrs), need)
	}
	k := 0
	for i := range dst {
		switch {
		case mask.Get(i):
			dst[i] = arrs[k]
			k++
		case empty.Get(i):
			dst[i] = zeroLight
		}
	}
	return nil
}

// ApplyLight は明るさのデータを反映する（Chunk Data の後半、または Update Light）。
// エラーのときは何も変えない。
func (c *Column) ApplyLight(ld *LightData) error {
	n := c.Format.LightSections()
	if len(c.Light.Sky) != n {
		c.Light = newLight(n)
	}
	sky := append([][]byte(nil), c.Light.Sky...)
	blk := append([][]byte(nil), c.Light.Block...)
	if err := applyLayer(sky, ld.SkyMask, ld.EmptySkyMask, ld.Sky); err != nil {
		return fmt.Errorf("sky light: %w", err)
	}
	if err := applyLayer(blk, ld.BlockMask, ld.EmptyBlockMask, ld.Block); err != nil {
		return fmt.Errorf("block light: %w", err)
	}
	c.Light.Sky, c.Light.Block = sky, blk
	return nil
}

// Nibble は明るさの配列の中の座標（0〜15）の値（0〜15）を返す。
// 番号は (y*16+z)*16+x で、偶数番は下位 4 ビット、奇数番は上位 4 ビット。
func Nibble(arr []byte, x, y, z int) int {
	i := BlockIndex(x, y, z)
	return int(arr[i>>1]>>((i&1)*4)) & 15
}

// lightAt は明るさを読む共通の処理。
func (c *Column) lightAt(layers [][]byte, x, y, z int) (int, bool, error) {
	if x < 0 || x > 15 || z < 0 || z > 15 {
		return 0, false, fmt.Errorf("%w: x=%d z=%d", ErrOutOfRange, x, z)
	}
	// 明るさのセクションはワールドの上下に 1 つずつ多い
	i := (y-c.Format.MinY)>>4 + 1
	if i < 0 || i >= len(layers) {
		return 0, false, fmt.Errorf("%w: y=%d", ErrOutOfRange, y)
	}
	arr := layers[i]
	if arr == nil {
		return 0, false, nil
	}
	return Nibble(arr, x, y&15, z), true, nil
}

// SkyLight はチャンクの中の座標の空の明るさ（0〜15）を返す。
// y はワールドの一番下の 16 下から、一番上の 16 上まで読める。まだ受け取っていなければ ok は false。
func (c *Column) SkyLight(x, y, z int) (level int, ok bool, err error) {
	return c.lightAt(c.Light.Sky, x, y, z)
}

// BlockLight はチャンクの中の座標のブロックの明るさ（0〜15）を返す。
func (c *Column) BlockLight(x, y, z int) (level int, ok bool, err error) {
	return c.lightAt(c.Light.Block, x, y, z)
}

// LightSection は明るさのセクション i（0 がワールドの一番下の 1 つ下）の sky と block の配列を返す。
// まだ受け取っていなければ nil。返す配列は書き換えないこと。
func (c *Column) LightSection(i int) (sky, block []byte) {
	if i < 0 || i >= len(c.Light.Sky) {
		return nil, nil
	}
	return c.Light.Sky[i], c.Light.Block[i]
}
