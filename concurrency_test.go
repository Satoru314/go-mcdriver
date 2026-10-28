package mcdriver

// 並行性の確かめ（go test -race で回す）。受信の goroutine・毎ティックの goroutine・使う側の goroutine が
// 同時に同じ状態を触っても競合せず、Close で必ず全部が戻ること。

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
	"gonum.org/v1/gonum/spatial/r3"
)

// サーバーがいろいろなパケットを送り続けるあいだに、使う側の goroutine が読む・操作する・行動を待つ。
// 途中で Close し、待っていた行動（Dig、Place、ClickSlot、Attack）が全部戻ることを確かめる。
func TestConcurrentUseAndClose(t *testing.T) {
	for _, closeByServer := range []bool{false, true} {
		name := "Close"
		if closeByServer {
			name = "サーバーが切る"
		}
		t.Run(name, func(t *testing.T) {
			s, c := playClientCfg(t, Config{EventBuffer: 8})
			s.sendChunks(flatColumn(t))
			s.teleport(packets.PlayerPosition{TeleportID: 1, X: 8.5, Y: -60, Z: 8.5}, 8.5, -60, 8.5, true)

			// サーバー役: 届いたものを読み捨てる goroutine と、送り続ける goroutine
			stop := make(chan struct{})
			var sw sync.WaitGroup
			sw.Add(2)
			go func() {
				defer sw.Done()
				for {
					s.nc.SetReadDeadline(time.Now().Add(5 * time.Second))
					if _, err := protocol.ReadPacket(s.br, s.th); err != nil {
						return
					}
				}
			}()
			go func() {
				defer sw.Done()
				stone, air := stateOf(t, "stone"), stateOf(t, "air")
				zombie := entType(t, "zombie")
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					default:
					}
					var pkts []protocol.Packet
					add := func(id int32, build func(e *protocol.Encoder)) {
						e := protocol.NewEncoder()
						build(e)
						pkts = append(pkts, protocol.Packet{ID: id, Data: e.Bytes()})
					}
					st := stone
					if i%2 == 0 {
						st = air
					}
					add(data.PlayClientboundBlockUpdate, func(e *protocol.Encoder) {
						e.WritePosition(protocol.Position{X: i % 16, Y: -59, Z: 3})
						e.WriteVarInt(int32(st))
					})
					add(data.PlayClientboundAddEntity, func(e *protocol.Encoder) {
						packets.WriteSpawnEntity(e, packets.SpawnEntity{EntityID: int32(100 + i%5), Type: int32(zombie.ID), X: 9, Y: -60, Z: 9})
					})
					add(data.PlayClientboundMoveEntityPos, func(e *protocol.Encoder) {
						packets.WriteMoveEntity(e, packets.MoveEntity{EntityID: int32(100 + i%5), HasPos: true, DX: 10})
					})
					add(data.PlayClientboundSetHealth, func(e *protocol.Encoder) {
						packets.WriteSetHealth(e, packets.SetHealth{Health: float32(10 + i%10), Food: 20})
					})
					add(data.PlayClientboundSetTime, func(e *protocol.Encoder) {
						packets.WriteSetTime(e, packets.SetTime{WorldAge: int64(i), DayTime: int64(i * 20), Ticking: true})
					})
					add(data.PlayClientboundContainerSetContent, func(e *protocol.Encoder) {
						packets.WriteContainerContent(e, packets.ContainerContent{WindowID: 0, StateID: int32(i), Slots: make([]packets.Slot, InventorySlots)})
					})
					if i%20 == 0 {
						add(data.PlayClientboundPlayerPosition, func(e *protocol.Encoder) {
							packets.WritePlayerPosition(e, packets.PlayerPosition{TeleportID: int32(10 + i), X: 8.5, Y: -60, Z: 8.5})
						})
					}
					for _, p := range pkts {
						s.nc.SetWriteDeadline(time.Now().Add(5 * time.Second))
						if protocol.WritePacket(s.nc, p, s.th) != nil {
							return
						}
					}
					time.Sleep(time.Millisecond)
				}
			}()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var uw sync.WaitGroup
			// 読む側
			for range 4 {
				uw.Add(1)
				go func() {
					defer uw.Done()
					for ctx.Err() == nil {
						c.Self()
						c.Block(BlockPos{X: 3, Y: -59, Z: 3})
						c.Light(BlockPos{X: 3, Y: -59, Z: 3})
						c.Entities()
						c.Inventory()
						c.Container()
						c.TimeInfo()
						c.Weather()
						c.AttackCharge()
						c.DigTicks(BlockPos{X: 8, Y: -61, Z: 8})
						c.EventsDropped()
						c.Pose()
						if v, err := c.WorldView(BlockPos{X: 0, Y: -64, Z: 0}, BlockPos{X: 15, Y: -56, Z: 15}); err == nil {
							v.Block(BlockPos{X: 1, Y: -60, Z: 1})
						}
					}
				}()
			}
			// 操作する側
			uw.Add(1)
			go func() {
				defer uw.Done()
				for i := 0; ctx.Err() == nil; i++ {
					c.SetControls(Controls{Forward: i%2 == 0, Jump: i%3 == 0, Sneak: i%5 == 0})
					c.Look(float32(i*7), float32(i%90))
					c.LookAt(r3.Vec{X: 9, Y: -59, Z: 9})
					c.SelectHotbar(i % 9)
					time.Sleep(time.Millisecond)
				}
			}()
			// 出来事を読む側（読み終わるのはチャネルが閉じたとき）
			evDone := make(chan Event, 1)
			go func() {
				var last Event
				for ev := range c.Events() {
					last = ev
				}
				evDone <- last
			}()
			// 待つ行動。どれも Close で戻ること
			acts := map[string]func() error{
				"Dig": func() error { return c.Dig(context.Background(), BlockPos{X: 8, Y: -61, Z: 9}, Up) },
				"Place": func() error {
					_, err := c.Place(context.Background(), BlockPos{X: 8, Y: -61, Z: 9}, Up, PlaceOpts{})
					return err
				},
				"UseBlock":  func() error { return c.UseBlock(context.Background(), BlockPos{X: 8, Y: -61, Z: 9}, Up) },
				"ClickSlot": func() error { return c.ClickSlot(context.Background(), 36, 0, ClickPickup) },
				"Attack":    func() error { return c.Attack(100) },
			}
			for _, f := range acts {
				uw.Add(1)
				go func() {
					defer uw.Done()
					for ctx.Err() == nil {
						if err := f(); err != nil {
							if errors.Is(err, ErrInternal) {
								t.Errorf("panic: %v", err)
							}
							select {
							case <-c.Done():
								return
							default:
							}
						}
					}
				}()
			}

			time.Sleep(500 * time.Millisecond)
			if closeByServer {
				s.nc.Close()
			} else {
				go c.Close()
				go c.Close()
			}
			select {
			case <-c.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("Done が閉じない")
			}
			c.Close()
			cancel()
			uwDone := make(chan struct{})
			go func() { uw.Wait(); close(uwDone) }()
			select {
			case <-uwDone:
			case <-time.After(10 * time.Second):
				buf := make([]byte, 1<<20)
				t.Fatalf("切断のあとも戻らない行動がある:\n%s", buf[:runtime.Stack(buf, true)])
			}
			close(stop)
			s.nc.Close()
			sw.Wait()
			select {
			case last := <-evDone:
				if _, ok := last.(DisconnectEvent); !ok {
					t.Errorf("最後の出来事が %T", last)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Events のチャネルが閉じない")
			}
			if c.Err() == nil {
				t.Error("切断のあと Err が nil")
			}
			if !closeByServer && !errors.Is(c.Err(), ErrClosed) {
				t.Errorf("Err = %v、期待 ErrClosed", c.Err())
			}
		})
	}
}
