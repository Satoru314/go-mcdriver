package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// Decoder はパケットの中身（バイト列）を先頭から順に読む。
// 足りないときは io.ErrUnexpectedEOF を返し、パニックしない。
// 長さの前置きは残りのバイト数と比べてから確保するので、巨大な長さでもメモリを使い切らない。
type Decoder struct {
	buf []byte
	off int
}

// NewDecoder は b を読む Decoder を作る。b は写さずにそのまま使う。
func NewDecoder(b []byte) *Decoder {
	return &Decoder{buf: b}
}

// Remaining は読み残しのバイト数を返す。
func (d *Decoder) Remaining() int {
	return len(d.buf) - d.off
}

// Offset はここまでに読んだバイト数を返す。
func (d *Decoder) Offset() int {
	return d.off
}

// next は n バイトを取り出す。返すスライスは元のバイト列を指す。
func (d *Decoder) next(n int) ([]byte, error) {
	if n < 0 {
		return nil, ErrNegativeLength
	}
	if n > d.Remaining() {
		return nil, io.ErrUnexpectedEOF
	}
	b := d.buf[d.off : d.off+n]
	d.off += n
	return b, nil
}

// ReadBool は Boolean を読む。0 なら false、それ以外は true（Java の readBoolean に合わせる）。
func (d *Decoder) ReadBool() (bool, error) {
	b, err := d.next(1)
	if err != nil {
		return false, err
	}
	return b[0] != 0, nil
}

// ReadInt8 は Byte（符号つき 8 ビット）を読む。
func (d *Decoder) ReadInt8() (int8, error) {
	b, err := d.next(1)
	if err != nil {
		return 0, err
	}
	return int8(b[0]), nil
}

// ReadUByte は Unsigned Byte を読む。
func (d *Decoder) ReadUByte() (uint8, error) {
	b, err := d.next(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

// ReadShort は Short（符号つき 16 ビット、ビッグエンディアン）を読む。
func (d *Decoder) ReadShort() (int16, error) {
	v, err := d.ReadUShort()
	return int16(v), err
}

// ReadUShort は Unsigned Short を読む。
func (d *Decoder) ReadUShort() (uint16, error) {
	b, err := d.next(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b), nil
}

// ReadInt は Int（符号つき 32 ビット）を読む。
func (d *Decoder) ReadInt() (int32, error) {
	b, err := d.next(4)
	if err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(b)), nil
}

// ReadLong は Long（符号つき 64 ビット）を読む。
func (d *Decoder) ReadLong() (int64, error) {
	b, err := d.next(8)
	if err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(b)), nil
}

// ReadFloat は Float（IEEE 754 単精度）を読む。
func (d *Decoder) ReadFloat() (float32, error) {
	b, err := d.next(4)
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(binary.BigEndian.Uint32(b)), nil
}

// ReadDouble は Double（IEEE 754 倍精度）を読む。
func (d *Decoder) ReadDouble() (float64, error) {
	b, err := d.next(8)
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(binary.BigEndian.Uint64(b)), nil
}

// ReadVarInt は VarInt を読む。
func (d *Decoder) ReadVarInt() (int32, error) {
	v, n, err := DecodeVarInt(d.buf[d.off:])
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		return 0, err
	}
	d.off += n
	return v, nil
}

// ReadVarLong は VarLong を読む。
func (d *Decoder) ReadVarLong() (int64, error) {
	v, n, err := DecodeVarLong(d.buf[d.off:])
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		return 0, err
	}
	d.off += n
	return v, nil
}

// ReadLength は長さの前置き（VarInt）を読み、0 以上 max 以下であることを確かめる。
func (d *Decoder) ReadLength(max int) (int, error) {
	n, err := d.ReadVarInt()
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: %d", ErrNegativeLength, n)
	}
	if int(n) > max {
		return 0, fmt.Errorf("%w: %d（上限 %d）", ErrTooLong, n, max)
	}
	return int(n), nil
}

// ReadString は String (max) を読む。max は UTF-16 のコード単位で数えた最大長。
// バイト数が max*3 を超えるか、文字数が max を超えたら ErrStringTooLong を返す。
func (d *Decoder) ReadString(max int) (string, error) {
	n, err := d.ReadVarInt()
	if err != nil {
		return "", err
	}
	if n < 0 {
		return "", fmt.Errorf("%w: %d", ErrNegativeLength, n)
	}
	if int64(n) > int64(max)*3 {
		return "", fmt.Errorf("%w: %d バイト（上限 %d バイト）", ErrStringTooLong, n, max*3)
	}
	b, err := d.next(int(n))
	if err != nil {
		return "", err
	}
	s := string(b)
	if err := checkStringLen(s, max); err != nil {
		return "", err
	}
	return s, nil
}

// ReadIdentifier は Identifier を読み、書式を確かめる。
func (d *Decoder) ReadIdentifier() (string, error) {
	s, err := d.ReadString(MaxIdentifierLen)
	if err != nil {
		return "", err
	}
	if !ValidIdentifier(s) {
		return "", fmt.Errorf("%w: %q", ErrBadIdentifier, s)
	}
	return s, nil
}

// ReadUUID は UUID を読む。
func (d *Decoder) ReadUUID() (UUID, error) {
	var u UUID
	b, err := d.next(16)
	if err != nil {
		return u, err
	}
	copy(u[:], b)
	return u, nil
}

// ReadPosition は Position を読む。
func (d *Decoder) ReadPosition() (Position, error) {
	v, err := d.ReadLong()
	if err != nil {
		return Position{}, err
	}
	return UnpackPosition(v), nil
}

// ReadAngle は Angle を読む。
func (d *Decoder) ReadAngle() (Angle, error) {
	v, err := d.ReadUByte()
	return Angle(v), err
}

// ReadBitSet は BitSet（long の個数と long の並び）を読む。
func (d *Decoder) ReadBitSet() (BitSet, error) {
	n, err := d.ReadLength(d.Remaining() / 8)
	if err != nil {
		return nil, err
	}
	bs := make(BitSet, n)
	for i := range bs {
		v, err := d.ReadLong()
		if err != nil {
			return nil, err
		}
		bs[i] = uint64(v)
	}
	return bs, nil
}

// ReadFixedBitSet は n ビットの Fixed BitSet（ceil(n/8) バイト）を読む。
func (d *Decoder) ReadFixedBitSet(n int) (FixedBitSet, error) {
	if n < 0 {
		return nil, ErrNegativeLength
	}
	b, err := d.next((n + 7) / 8)
	if err != nil {
		return nil, err
	}
	return FixedBitSet(append([]byte(nil), b...)), nil
}

// ReadBytes は n バイトをそのまま読む。返すスライスは写しなので、あとで書き換えてよい。
func (d *Decoder) ReadBytes(n int) ([]byte, error) {
	b, err := d.next(n)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), b...), nil
}

// ReadPrefixedBytes は長さ（VarInt）つきのバイト列を読む。max は最大のバイト数。
func (d *Decoder) ReadPrefixedBytes(max int) ([]byte, error) {
	n, err := d.ReadLength(max)
	if err != nil {
		return nil, err
	}
	return d.ReadBytes(n)
}

// ReadRest は残りのバイトをすべて読む（仕様の「Byte Array」でパケットの最後まで続くもの）。
func (d *Decoder) ReadRest() []byte {
	b, _ := d.ReadBytes(d.Remaining())
	return b
}

// Skip は n バイトを読み飛ばす。
func (d *Decoder) Skip(n int) error {
	_, err := d.next(n)
	return err
}

// ReadLongArray は長さ（VarInt）つきの long の並びを読む（チャンクのブロックの並びなど）。
func (d *Decoder) ReadLongArray() ([]int64, error) {
	n, err := d.ReadLength(d.Remaining() / 8)
	if err != nil {
		return nil, err
	}
	a := make([]int64, n)
	for i := range a {
		if a[i], err = d.ReadLong(); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// ReadPrefixedArray は Prefixed Array（要素数の VarInt と要素の並び）を読む。
// max は要素数の上限。要素は 1 バイト以上あるものとして、残りのバイト数より多い要素数もエラーにする。
// 0 バイトの要素（中身のない構造体など）を読むときは ReadPrefixedArrayN を使う。
func ReadPrefixedArray[T any](d *Decoder, max int, read func(*Decoder) (T, error)) ([]T, error) {
	return readArray(d, min(max, d.Remaining()), read)
}

// ReadPrefixedArrayN は要素の大きさに下限を置かない Prefixed Array の読み方。要素数は max までに限る。
func ReadPrefixedArrayN[T any](d *Decoder, max int, read func(*Decoder) (T, error)) ([]T, error) {
	return readArray(d, max, read)
}

func readArray[T any](d *Decoder, max int, read func(*Decoder) (T, error)) ([]T, error) {
	n, err := d.ReadLength(max)
	if err != nil {
		return nil, err
	}
	// 先に大きく確保しすぎないように、確保の量は残りのバイト数で抑える
	a := make([]T, 0, min(n, d.Remaining()+1))
	for i := 0; i < n; i++ {
		v, err := read(d)
		if err != nil {
			return nil, fmt.Errorf("配列の %d 番目: %w", i, err)
		}
		a = append(a, v)
	}
	return a, nil
}

// ReadPrefixedOptional は Prefixed Optional（Boolean のあとに、true なら値）を読む。
// 値がなければ ok は false。
func ReadPrefixedOptional[T any](d *Decoder, read func(*Decoder) (T, error)) (v T, ok bool, err error) {
	present, err := d.ReadBool()
	if err != nil || !present {
		return v, false, err
	}
	v, err = read(d)
	if err != nil {
		return v, false, err
	}
	return v, true, nil
}
