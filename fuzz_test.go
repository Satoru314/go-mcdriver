package mcdriver

// 受信の振り分け全体の fuzz。偽のサーバーがゲームに入ったあと、でたらめなパケットを流し、
// ドライバが panic しない（ErrInternal で切れない）こと、固まらない（Keep Alive に答えるか、エラーで切れる）こと、
// Close で必ず片付くことを確かめる。
//
//	go test -run '^$' -fuzz FuzzPlayPackets -fuzztime 60s -parallel 8 .
//
// 1 回ごとに 127.0.0.1 の接続を 1 本作るので、1 秒に数千本の接続になる。ふつうに閉じると TIME_WAIT の接続と
// conntrack の記録（120 秒残る）が積もり、CT102 では conntrack の表があふれてパケットが捨てられ、
// 同じコンテナのほかのテスト（偽のサーバーの「接続が来ない」や Docker のテスト用サーバーへの接続）まで止まった
// （2026-10-28、ホストのカーネルの記録 "nf_conntrack: table full"）。そのため、終わるときはサーバー側から RST で切る。
// それでも長く回すときは、ほかのテストと同時に回さない。

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// fuzzPlayIDs は fuzz で送るパケットの ID（処理が登録されているものと client.go が扱うもの）。
// 設定の段階に戻る Start Configuration は、そのあと設定の段階のパケットを待ち続けるので除く。
func fuzzPlayIDs() []int32 {
	ids := []int32{
		data.PlayClientboundKeepAlive, data.PlayClientboundPing, data.PlayClientboundLogin,
		data.PlayClientboundPlayerPosition, data.PlayClientboundDisconnect, data.PlayClientboundUpdateTags,
		data.PlayClientboundCookieRequest, data.PlayClientboundResourcePackPush, data.PlayClientboundBossEvent,
	}
	for id := range playHandlers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	return slices.DeleteFunc(ids, func(id int32) bool { return id == data.PlayClientboundStartConfiguration })
}

// fuzzPacket は fuzz の入力の 1 パケット分（ID の添字 1 バイト、長さ 2 バイト、中身）を作る。
func fuzzPacket(ids []int32, id int32, build func(e *protocol.Encoder)) []byte {
	e := protocol.NewEncoder()
	build(e)
	b := e.Bytes()
	out := []byte{byte(slices.Index(ids, id)), 0, 0}
	binary.BigEndian.PutUint16(out[1:], uint16(len(b)))
	return append(out, b...)
}

func FuzzPlayPackets(f *testing.F) {
	ids := fuzzPlayIDs()

	// 種: 正しいパケットの並び
	tp := fuzzPacket(ids, data.PlayClientboundPlayerPosition, func(e *protocol.Encoder) {
		packets.WritePlayerPosition(e, packets.PlayerPosition{TeleportID: 1, X: 0.5, Y: -60, Z: 0.5})
	})
	health := fuzzPacket(ids, data.PlayClientboundSetHealth, func(e *protocol.Encoder) {
		e.WriteFloat(5)
		e.WriteVarInt(10)
		e.WriteFloat(0)
	})
	block := fuzzPacket(ids, data.PlayClientboundBlockUpdate, func(e *protocol.Encoder) {
		e.WritePosition(protocol.Position{X: 1, Y: -60, Z: 1})
		e.WriteVarInt(1)
	})
	f.Add(tp)
	f.Add(slices.Concat(tp, health, block))
	f.Add(slices.Concat(tp, fuzzPacket(ids, data.PlayClientboundPlayerPosition, func(e *protocol.Encoder) {
		// 位置と速さに NaN と無限大
		packets.WritePlayerPosition(e, packets.PlayerPosition{TeleportID: 2, X: math.NaN(), Y: math.Inf(1), Z: 1e300, VelX: math.NaN()})
	})))
	f.Add([]byte{0, 0xff, 0xff, 1, 2, 3})
	f.Add(bytes.Repeat([]byte{7, 0, 3, 0x80, 0x80, 0x80}, 10))

	f.Fuzz(func(t *testing.T, in []byte) {
		if len(in) > 64*1024 {
			return
		}
		s, c := playClientCfg(t, Config{})
		for len(in) >= 3 {
			id := ids[int(in[0])%len(ids)]
			n := min(int(binary.BigEndian.Uint16(in[1:3])), len(in)-3)
			body := in[3 : 3+n]
			in = in[3+n:]
			s.nc.SetWriteDeadline(time.Now().Add(2 * time.Second))
			if err := protocol.WritePacket(s.nc, protocol.Packet{ID: id, Data: body}, s.th); err != nil {
				break // ドライバが切った
			}
		}
		// Keep Alive を送り、答えが返る（固まっていない）か、切れるまで読む
		const ka = 0x1234567
		s.nc.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if protocol.WritePacket(s.nc, protocol.Packet{ID: data.PlayClientboundKeepAlive, Data: binary.BigEndian.AppendUint64(nil, ka)}, s.th) == nil {
			s.nc.SetReadDeadline(time.Now().Add(10 * time.Second))
			for {
				p, err := protocol.ReadPacket(s.br, s.th)
				if err != nil {
					var ne interface{ Timeout() bool }
					if errors.As(err, &ne) && ne.Timeout() {
						t.Fatalf("ドライバが Keep Alive に答えず、切りもしない（Err = %v）", c.Err())
					}
					break
				}
				if p.ID == data.PlayServerboundKeepAlive && len(p.Data) == 8 && binary.BigEndian.Uint64(p.Data) == ka {
					break
				}
			}
		}
		if err := c.Err(); errors.Is(err, ErrInternal) {
			t.Fatalf("panic で切れた: %v", err)
		}
		// 状態を読む関数が使えること（鍵が残っていない）
		c.Self()
		c.Inventory()
		c.Entities()
		c.Block(BlockPos{})
		c.Weather()

		// サーバー側から RST で切る（TIME_WAIT の接続が積もって、繋ぐポートが尽きないように）
		if tc, ok := s.nc.(*net.TCPConn); ok {
			tc.SetLinger(0)
		}
		s.nc.Close()

		closed := make(chan struct{})
		go func() {
			c.Close()
			close(closed)
		}()
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Fatal("Close が戻らない")
		}
		if err := c.Err(); errors.Is(err, ErrInternal) {
			t.Fatalf("panic で切れた: %v", err)
		}
		// Events のチャネルは閉じている（最後は DisconnectEvent）
		var last Event
		for ev := range c.Events() {
			last = ev
		}
		if _, ok := last.(DisconnectEvent); !ok {
			t.Fatalf("最後の出来事が %T", last)
		}
	})
}
