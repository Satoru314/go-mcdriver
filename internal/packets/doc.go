// Package packets は Play の段階のパケットを 1 つずつ読み書きする。
//
// 読む関数は Read<名前>(d)、書く関数は Write<名前>(e, ...) の形にする。
// 形は protocol 769（1.21.4）。仕様は https://minecraft.wiki/w/Java_Edition_protocol を参照する。
package packets
