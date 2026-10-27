package data

import (
	"fmt"
	"sort"
)

// BlockID はブロックの registry の番号（protocol_id）。
type BlockID int32

// StateID はブロックの状態の番号。チャンクやブロックの変化のパケットで使う。
type StateID int32

// Property はブロックの状態のプロパティ（例 facing）と、取りうる値の一覧（公式データの順。値の番号が状態 ID に効く）。
type Property struct {
	Name   string
	Values []string
}

// Block は1種類のブロック。
//
// 状態 ID は MinState から MinState+NumStates-1 まで連続していて、
// 各プロパティの値の番号を、Properties の最初のものを最上位の桁として並べた数を MinState に足したものになる。
// Properties はプロパティの名前の順（これが状態 ID の並びを決める）。値は公式データの順。
type Block struct {
	ID           BlockID
	Name         string     // "minecraft:" を外した名前（例 "oak_stairs"）
	Type         string     // 公式データの definition.type（ブロックの作りの種類。例 "stair", "door", "liquid"）
	Properties   []Property // 状態のプロパティ（名前の順。無いブロックは空）
	MinState     StateID
	NumStates    int
	DefaultState StateID // 置いたときの既定の状態
}

var blockByName = func() map[string]*Block {
	m := make(map[string]*Block, len(blocks))
	for i := range blocks {
		m[blocks[i].Name] = &blocks[i]
	}
	return m
}()

// Blocks は全ブロックを ID の順に返す（中身を書き換えないこと）。
func Blocks() []Block { return blocks }

// BlockByName は名前でブロックを引く。"minecraft:" は付いていてもいなくてもよい。
func BlockByName(name string) (*Block, bool) {
	b, ok := blockByName[trimNS(name)]
	return b, ok
}

// BlockByID は ID でブロックを引く。
func BlockByID(id BlockID) (*Block, bool) {
	if id < 0 || int(id) >= len(blocks) {
		return nil, false
	}
	return &blocks[id], true
}

// HasState は状態 ID がこのブロックのものかを返す。
func (b *Block) HasState(id StateID) bool {
	return id >= b.MinState && int(id-b.MinState) < b.NumStates
}

// Property は名前でプロパティを引く。
func (b *Block) Property(name string) (*Property, bool) {
	for i := range b.Properties {
		if b.Properties[i].Name == name {
			return &b.Properties[i], true
		}
	}
	return nil, false
}

// Default は既定の状態を返す。
func (b *Block) Default() BlockState {
	return BlockState{ID: b.DefaultState, Block: b}
}

// State は、指定したプロパティの値を持つ状態を返す。指定しなかったプロパティは既定の状態の値になる。
// 知らないプロパティや値を指定するとエラーになる。
func (b *Block) State(props map[string]string) (BlockState, error) {
	s := b.Default()
	for k, v := range props {
		var ok bool
		s, ok = s.With(k, v)
		if !ok {
			return BlockState{}, fmt.Errorf("data: %s にプロパティ %s=%q はない", b.Name, k, v)
		}
	}
	return s, nil
}

// BlockState はブロックの1つの状態。
type BlockState struct {
	ID    StateID
	Block *Block
}

// StateByID は状態 ID から状態を引く。
func StateByID(id StateID) (BlockState, bool) {
	if id < 0 || id >= NumBlockStates {
		return BlockState{}, false
	}
	// blocks は MinState の昇順に並んでいる
	i := sort.Search(len(blocks), func(i int) bool { return blocks[i].MinState > id }) - 1
	return BlockState{ID: id, Block: &blocks[i]}, true
}

// Name はブロックの名前。
func (s BlockState) Name() string { return s.Block.Name }

// valueIndex は i 番目のプロパティの値の番号を返す。
func (s BlockState) valueIndex(i int) int {
	idx := int(s.ID - s.Block.MinState)
	for j := len(s.Block.Properties) - 1; j > i; j-- {
		idx /= len(s.Block.Properties[j].Values)
	}
	return idx % len(s.Block.Properties[i].Values)
}

// stride は i 番目のプロパティの値が1つ変わったときの状態 ID の差。
func (s BlockState) stride(i int) int {
	st := 1
	for j := len(s.Block.Properties) - 1; j > i; j-- {
		st *= len(s.Block.Properties[j].Values)
	}
	return st
}

// Get はプロパティの値を返す。そのプロパティが無ければ false。
func (s BlockState) Get(name string) (string, bool) {
	for i, p := range s.Block.Properties {
		if p.Name == name {
			return p.Values[s.valueIndex(i)], true
		}
	}
	return "", false
}

// Properties は全プロパティの値を返す（新しい map）。
func (s BlockState) Properties() map[string]string {
	m := make(map[string]string, len(s.Block.Properties))
	for i, p := range s.Block.Properties {
		m[p.Name] = p.Values[s.valueIndex(i)]
	}
	return m
}

// With はプロパティを1つ変えた状態を返す。プロパティか値が無ければ false。
func (s BlockState) With(name, value string) (BlockState, bool) {
	for i, p := range s.Block.Properties {
		if p.Name != name {
			continue
		}
		for vi, v := range p.Values {
			if v == value {
				diff := (vi - s.valueIndex(i)) * s.stride(i)
				return BlockState{ID: s.ID + StateID(diff), Block: s.Block}, true
			}
		}
		return BlockState{}, false
	}
	return BlockState{}, false
}

// String は "oak_stairs[facing=north,half=top,...]" の形にする（プロパティは名前の順）。
func (s BlockState) String() string {
	if len(s.Block.Properties) == 0 {
		return s.Block.Name
	}
	out := s.Block.Name + "["
	for i, p := range s.Block.Properties {
		if i > 0 {
			out += ","
		}
		out += p.Name + "=" + p.Values[s.valueIndex(i)]
	}
	return out + "]"
}
