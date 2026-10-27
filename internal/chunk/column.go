package chunk

import (
	"fmt"

	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// Column は 1 つのチャンク（16×高さ×16 ブロックの柱）。
type Column struct {
	// X, Z はチャンクの座標（ブロックの座標を 16 で割って下に丸めたもの）
	X, Z int32
	// Format は読むときに使った値（高さ、ビット数、空気の判定）
	Format Format
	// Sections は下から順のセクション。Format.Sections() 個
	Sections []Section
	// Heightmaps は高さマップ（1.21.4 ではネットワーク形式の NBT のコンパウンド）。読んだまま持つ
	Heightmaps any
	// BlockEntities はブロックエンティティ（看板・チェストなど）。受け取ったときのまま持つ
	BlockEntities []BlockEntity
	// Light は明るさ
	Light Light
}

// BlockEntity はチャンクのデータに入っているブロックエンティティ 1 つ。
type BlockEntity struct {
	// X, Z はチャンクの中の座標（0〜15）、Y はワールドの y
	X, Y, Z int
	// Type はブロックエンティティの種類の ID（minecraft:block_entity_type のレジストリの番号）
	Type int32
	// Data は中身（ネットワーク形式の NBT）。なければ nil
	Data any
}

// NewColumn は空気（状態 ID 0）だけのチャンクを作る（テストや、まだ受け取っていない場所の仮置き用）。
func NewColumn(x, z int32, f Format) (*Column, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	c := &Column{X: x, Z: z, Format: f, Sections: make([]Section, f.Sections())}
	for i := range c.Sections {
		c.Sections[i] = Section{Blocks: NewUniform(SectionBlocks, 0), Biomes: NewUniform(SectionBiomes, 0)}
	}
	c.Light = newLight(f.LightSections())
	return c, nil
}

// DecodeSections はチャンクのデータ部分（Chunk Data パケットの Data）からセクションを読む。
// セクションは下から順に Format.Sections() 個並ぶ。
// 1 つのセクションは: ブロック数（Short）、ブロック状態の paletted container、バイオームの paletted container。
// 後ろに余ったバイトは Java 版と同じく読み捨てる。
func DecodeSections(data []byte, f Format) ([]Section, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	d := protocol.NewDecoder(data)
	secs := make([]Section, f.Sections())
	for i := range secs {
		s := &secs[i]
		var err error
		if s.BlockCount, err = d.ReadShort(); err != nil {
			return nil, fmt.Errorf("セクション %d のブロック数: %w", i, err)
		}
		if s.Blocks, err = readContainer(d, blockRule(f.BlockStateBits)); err != nil {
			return nil, fmt.Errorf("セクション %d のブロック: %w", i, err)
		}
		if s.Biomes, err = readContainer(d, biomeRule(f.BiomeBits)); err != nil {
			return nil, fmt.Errorf("セクション %d のバイオーム: %w", i, err)
		}
	}
	return secs, nil
}

// ReadChunkPacket は Chunk Data and Update Light パケットの中身（パケット ID のあと）を読む。
//
//	Chunk X (Int), Chunk Z (Int),
//	Heightmaps (NBT), Size (VarInt), Data (Byte Array),
//	Block Entities (Prefixed Array: Packed XZ (UByte), Y (Short), Type (VarInt), Data (NBT)),
//	Light Data（ReadLightData を参照）
func ReadChunkPacket(d *protocol.Decoder, f Format) (*Column, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	x, err := d.ReadInt()
	if err != nil {
		return nil, err
	}
	z, err := d.ReadInt()
	if err != nil {
		return nil, err
	}
	c := &Column{X: x, Z: z, Format: f}
	if c.Heightmaps, err = nbt.ReadNetwork(d); err != nil {
		return nil, fmt.Errorf("高さマップ: %w", err)
	}
	data, err := d.ReadPrefixedBytes(MaxChunkDataSize)
	if err != nil {
		return nil, fmt.Errorf("チャンクのデータ: %w", err)
	}
	if c.Sections, err = DecodeSections(data, f); err != nil {
		return nil, err
	}
	// ブロックエンティティ 1 つは最低 5 バイト（XZ 1、Y 2、Type 1、NBT 1）
	if c.BlockEntities, err = protocol.ReadPrefixedArray(d, d.Remaining()/5, readBlockEntity); err != nil {
		return nil, fmt.Errorf("ブロックエンティティ: %w", err)
	}
	ld, err := ReadLightData(d)
	if err != nil {
		return nil, fmt.Errorf("明るさ: %w", err)
	}
	c.Light = newLight(f.LightSections())
	if err := c.ApplyLight(ld); err != nil {
		return nil, err
	}
	return c, nil
}

func readBlockEntity(d *protocol.Decoder) (BlockEntity, error) {
	var be BlockEntity
	xz, err := d.ReadUByte()
	if err != nil {
		return be, err
	}
	y, err := d.ReadShort()
	if err != nil {
		return be, err
	}
	if be.Type, err = d.ReadVarInt(); err != nil {
		return be, err
	}
	if be.Data, err = nbt.ReadNetwork(d); err != nil {
		return be, err
	}
	be.X, be.Y, be.Z = int(xz>>4), int(y), int(xz&15)
	return be, nil
}

// sectionAt はワールドの y からセクションとその中の y を求める。
func (c *Column) sectionAt(y int) (*Section, int, error) {
	i := (y - c.Format.MinY) >> 4
	if y < c.Format.MinY || i >= len(c.Sections) {
		return nil, 0, fmt.Errorf("%w: y=%d", ErrOutOfRange, y)
	}
	return &c.Sections[i], y & 15, nil
}

// Block はチャンクの中の座標（x, z は 0〜15、y はワールドの y）のブロック状態 ID を返す。
// 範囲の外なら ErrOutOfRange。
func (c *Column) Block(x, y, z int) (int32, error) {
	if x < 0 || x > 15 || z < 0 || z > 15 {
		return 0, fmt.Errorf("%w: x=%d z=%d", ErrOutOfRange, x, z)
	}
	s, ly, err := c.sectionAt(y)
	if err != nil {
		return 0, err
	}
	return s.Block(x, ly, z), nil
}

// Biome はチャンクの中の座標のバイオーム ID を返す。
func (c *Column) Biome(x, y, z int) (int32, error) {
	if x < 0 || x > 15 || z < 0 || z > 15 {
		return 0, fmt.Errorf("%w: x=%d z=%d", ErrOutOfRange, x, z)
	}
	s, ly, err := c.sectionAt(y)
	if err != nil {
		return 0, err
	}
	return s.Biome(x, ly, z), nil
}

// SetBlock はチャンクの中の座標のブロック状態 ID を state にする（Block Update の反映）。
func (c *Column) SetBlock(x, y, z int, state int32) error {
	if x < 0 || x > 15 || z < 0 || z > 15 {
		return fmt.Errorf("%w: x=%d z=%d", ErrOutOfRange, x, z)
	}
	if err := checkID(state, c.Format.BlockStateBits); err != nil {
		return err
	}
	s, ly, err := c.sectionAt(y)
	if err != nil {
		return err
	}
	c.setInSection(s, x, ly, z, state)
	return nil
}

func (c *Column) setInSection(s *Section, x, y, z int, state int32) {
	old := s.Blocks.Set(BlockIndex(x, y, z), uint16(state))
	if isAir := c.Format.IsAir; isAir != nil {
		if isAir(int32(old)) && !isAir(state) {
			s.BlockCount++
		} else if !isAir(int32(old)) && isAir(state) {
			s.BlockCount--
		}
	}
}

// BlockChange はセクションの中の 1 ブロックの変化（Update Section Blocks の 1 要素）。
type BlockChange struct {
	X, Y, Z int   // セクションの中の座標（0〜15）
	State   int32 // 新しいブロック状態 ID
}

// ApplySectionUpdate はセクション sectionY（ワールドの y を 16 で割って下に丸めたもの）の
// 複数のブロックを変える（Update Section Blocks の反映）。
// 1 つでも正しくない変化があれば、何も変えずにエラーを返す。
func (c *Column) ApplySectionUpdate(sectionY int, changes []BlockChange) error {
	i := sectionY - c.Format.MinY>>4
	if i < 0 || i >= len(c.Sections) {
		return fmt.Errorf("%w: セクション y=%d", ErrOutOfRange, sectionY)
	}
	for _, ch := range changes {
		if ch.X < 0 || ch.X > 15 || ch.Y < 0 || ch.Y > 15 || ch.Z < 0 || ch.Z > 15 {
			return fmt.Errorf("%w: %+v", ErrOutOfRange, ch)
		}
		if err := checkID(ch.State, c.Format.BlockStateBits); err != nil {
			return err
		}
	}
	s := &c.Sections[i]
	for _, ch := range changes {
		c.setInSection(s, ch.X, ch.Y, ch.Z, ch.State)
	}
	return nil
}
