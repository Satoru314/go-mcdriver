package protocol

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Encoder はパケットの中身を後ろに足していく。
// 値が型で表せないとき（長すぎる文字列、範囲の外の Position など）はエラーを返し、何も足さない。
type Encoder struct {
	buf []byte
}

// NewEncoder は空の Encoder を作る。
func NewEncoder() *Encoder {
	return &Encoder{}
}

// Bytes はここまでに書いたバイト列を返す。
func (e *Encoder) Bytes() []byte {
	return e.buf
}

// Len はここまでに書いたバイト数を返す。
func (e *Encoder) Len() int {
	return len(e.buf)
}

// Reset は書いた中身を捨てる（確保した領域は使い回す）。
func (e *Encoder) Reset() {
	e.buf = e.buf[:0]
}

// WriteBool は Boolean を書く。
func (e *Encoder) WriteBool(v bool) {
	if v {
		e.buf = append(e.buf, 1)
	} else {
		e.buf = append(e.buf, 0)
	}
}

// WriteInt8 は Byte を書く。
func (e *Encoder) WriteInt8(v int8) {
	e.buf = append(e.buf, byte(v))
}

// WriteUByte は Unsigned Byte を書く。
func (e *Encoder) WriteUByte(v uint8) {
	e.buf = append(e.buf, v)
}

// WriteShort は Short を書く。
func (e *Encoder) WriteShort(v int16) {
	e.buf = binary.BigEndian.AppendUint16(e.buf, uint16(v))
}

// WriteUShort は Unsigned Short を書く。
func (e *Encoder) WriteUShort(v uint16) {
	e.buf = binary.BigEndian.AppendUint16(e.buf, v)
}

// WriteInt は Int を書く。
func (e *Encoder) WriteInt(v int32) {
	e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(v))
}

// WriteLong は Long を書く。
func (e *Encoder) WriteLong(v int64) {
	e.buf = binary.BigEndian.AppendUint64(e.buf, uint64(v))
}

// WriteFloat は Float を書く。
func (e *Encoder) WriteFloat(v float32) {
	e.buf = binary.BigEndian.AppendUint32(e.buf, math.Float32bits(v))
}

// WriteDouble は Double を書く。
func (e *Encoder) WriteDouble(v float64) {
	e.buf = binary.BigEndian.AppendUint64(e.buf, math.Float64bits(v))
}

// WriteVarInt は VarInt を書く。
func (e *Encoder) WriteVarInt(v int32) {
	e.buf = AppendVarInt(e.buf, v)
}

// WriteVarLong は VarLong を書く。
func (e *Encoder) WriteVarLong(v int64) {
	e.buf = AppendVarLong(e.buf, v)
}

// WriteLength は長さの前置き（VarInt）を書く。int32 に収まらない長さは ErrTooLong。
func (e *Encoder) WriteLength(n int) error {
	if n < 0 {
		return ErrNegativeLength
	}
	if n > math.MaxInt32 {
		return fmt.Errorf("%w: %d", ErrTooLong, n)
	}
	e.WriteVarInt(int32(n))
	return nil
}

// WriteString は String (max) を書く。最大長を超えたら ErrStringTooLong を返す。
func (e *Encoder) WriteString(s string, max int) error {
	if err := checkStringLen(s, max); err != nil {
		return err
	}
	e.WriteVarInt(int32(len(s)))
	e.buf = append(e.buf, s...)
	return nil
}

// WriteIdentifier は Identifier を書く。書式が正しくなければ ErrBadIdentifier を返す。
func (e *Encoder) WriteIdentifier(s string) error {
	if !ValidIdentifier(s) {
		return fmt.Errorf("%w: %q", ErrBadIdentifier, s)
	}
	return e.WriteString(s, MaxIdentifierLen)
}

// WriteUUID は UUID を書く。
func (e *Encoder) WriteUUID(u UUID) {
	e.buf = append(e.buf, u[:]...)
}

// WritePosition は Position を書く。範囲の外なら ErrOutOfRange を返す。
func (e *Encoder) WritePosition(p Position) error {
	v, err := p.Pack()
	if err != nil {
		return err
	}
	e.WriteLong(v)
	return nil
}

// WriteAngle は Angle を書く。
func (e *Encoder) WriteAngle(a Angle) {
	e.buf = append(e.buf, byte(a))
}

// WriteBitSet は BitSet を書く。
func (e *Encoder) WriteBitSet(b BitSet) {
	e.WriteVarInt(int32(len(b)))
	for _, v := range b {
		e.WriteLong(int64(v))
	}
}

// WriteFixedBitSet は n ビットの Fixed BitSet を書く。b が短ければ 0 で埋め、長ければエラー。
func (e *Encoder) WriteFixedBitSet(b FixedBitSet, n int) error {
	size := (n + 7) / 8
	if n < 0 || len(b) > size {
		return fmt.Errorf("%w: FixedBitSet %d バイト（%d ビット）", ErrTooLong, len(b), n)
	}
	e.buf = append(e.buf, b...)
	for i := len(b); i < size; i++ {
		e.buf = append(e.buf, 0)
	}
	return nil
}

// WriteBytes はバイト列をそのまま書く。
func (e *Encoder) WriteBytes(b []byte) {
	e.buf = append(e.buf, b...)
}

// WritePrefixedBytes は長さ（VarInt）つきのバイト列を書く。
func (e *Encoder) WritePrefixedBytes(b []byte) error {
	if err := e.WriteLength(len(b)); err != nil {
		return err
	}
	e.buf = append(e.buf, b...)
	return nil
}

// WriteLongArray は長さ（VarInt）つきの long の並びを書く。
func (e *Encoder) WriteLongArray(a []int64) error {
	if err := e.WriteLength(len(a)); err != nil {
		return err
	}
	for _, v := range a {
		e.WriteLong(v)
	}
	return nil
}

// WritePrefixedArray は Prefixed Array を書く。途中でエラーになったら、書きかけを取り消してエラーを返す。
func WritePrefixedArray[T any](e *Encoder, items []T, write func(*Encoder, T) error) error {
	start := len(e.buf)
	if err := e.WriteLength(len(items)); err != nil {
		return err
	}
	for i, v := range items {
		if err := write(e, v); err != nil {
			e.buf = e.buf[:start]
			return fmt.Errorf("配列の %d 番目: %w", i, err)
		}
	}
	return nil
}

// WritePrefixedOptional は Prefixed Optional を書く。ok が false なら false だけを書く。
func WritePrefixedOptional[T any](e *Encoder, v T, ok bool, write func(*Encoder, T) error) error {
	start := len(e.buf)
	e.WriteBool(ok)
	if !ok {
		return nil
	}
	if err := write(e, v); err != nil {
		e.buf = e.buf[:start]
		return err
	}
	return nil
}
