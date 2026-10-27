// mcprobe はサーバーに bot として繋ぎ、状態（位置、HP、満腹度、持ち物、時刻、天気、近くの生き物）と
// 出来事を表示し続ける確認用のツール。bot は動かない（何もしない）。
//
// 使い方（リポジトリのルートで）:
//
//	go run ./cmd/mcprobe -addr 127.0.0.1:25865 -name probe1
//
// サーバーはオフラインモード（online-mode=false）である必要がある。Ctrl-C で抜ける。
// 死んだら -respawn（既定 true）で自動でリスポーンする。
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	mcdriver "github.com/Satoru314/go-mcdriver"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:25565", `サーバーの "host:port"`)
	name := flag.String("name", "mcprobe", "bot のプレイヤー名（1〜16 文字）")
	interval := flag.Duration("interval", 2*time.Second, "状態を表示する間隔")
	radius := flag.Float64("radius", 16, "近くの生き物として表示する距離（ブロック）")
	respawn := flag.Bool("respawn", true, "死んだら自動でリスポーンする")
	quiet := flag.Bool("quiet", false, "ブロックの変化と生き物の出現・消滅の出来事を表示しない")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Stdout, *addr, *name, *interval, *radius, *respawn, *quiet); err != nil {
		fmt.Fprintln(os.Stderr, "mcprobe:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, w io.Writer, addr, name string, interval time.Duration, radius float64, respawn, quiet bool) error {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	c, err := mcdriver.Connect(cctx, mcdriver.Config{Addr: addr, Username: name})
	cancel()
	if err != nil {
		return err
	}
	defer c.Close()
	fmt.Fprintf(w, "%s に %s として繋いだ\n", addr, name)

	t := time.NewTicker(interval)
	defer t.Stop()
	evs := c.Events()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(w, "終わる")
			return nil
		case ev, ok := <-evs:
			if !ok {
				return c.Err()
			}
			if line := describeEvent(ev, quiet); line != "" {
				fmt.Fprintf(w, "%s [出来事] %s\n", ev.Time().Format("15:04:05.000"), line)
			}
			if _, dead := ev.(mcdriver.DeathEvent); dead && respawn {
				// Set Health が届いて Dead になるのを少し待ってからリスポーンする
				go func() {
					time.Sleep(500 * time.Millisecond)
					if err := c.Respawn(); err != nil {
						fmt.Fprintln(w, "リスポーンできない:", err)
					}
				}()
			}
			if dc, ok := ev.(mcdriver.DisconnectEvent); ok {
				return dc.Err
			}
		case <-t.C:
			fmt.Fprint(w, status(c, radius))
		}
	}
}

// describeEvent は出来事を 1 行にする。quiet なら多いもの（ブロック、生き物）は空を返す。
func describeEvent(ev mcdriver.Event, quiet bool) string {
	switch ev := ev.(type) {
	case mcdriver.DeathEvent:
		return "死んだ: " + ev.Message
	case mcdriver.RespawnEvent:
		return fmt.Sprintf("リスポーン: %s %v（死んだあと %v）", ev.Dimension, ev.GameMode, ev.AfterDeath)
	case mcdriver.DamageEvent:
		return fmt.Sprintf("ダメージ: %s %.1f → HP %.1f", orUnknown(ev.Type), ev.Amount, ev.Health)
	case mcdriver.TeleportEvent:
		return fmt.Sprintf("位置の強制修正: (%.2f, %.2f, %.2f) 向き %.0f/%.0f", ev.Pos.X, ev.Pos.Y, ev.Pos.Z, ev.Yaw, ev.Pitch)
	case mcdriver.BlockChangeEvent:
		if quiet {
			return ""
		}
		return fmt.Sprintf("ブロック %v: %v → %v", ev.Pos, stateString(ev.Old), stateString(ev.New))
	case mcdriver.EntitySpawnEvent:
		if quiet {
			return ""
		}
		return "出現: " + entityString(ev.Entity)
	case mcdriver.EntityRemoveEvent:
		if quiet {
			return ""
		}
		return "消滅: " + entityString(ev.Entity)
	case mcdriver.ContainerOpenEvent:
		return fmt.Sprintf("容器が開いた: %d %s %q", ev.WindowID, ev.Type, ev.Title)
	case mcdriver.ContainerCloseEvent:
		return fmt.Sprintf("容器が閉じた: %d（サーバーが閉じた %v）", ev.WindowID, ev.ByServer)
	case mcdriver.WeatherEvent:
		return "天気: " + ev.Weather.Kind.String()
	case mcdriver.DisconnectEvent:
		return fmt.Sprintf("切断: %v", ev.Err)
	}
	return fmt.Sprintf("%T", ev)
}

func orUnknown(s string) string {
	if s == "" {
		return "（種類不明）"
	}
	return s
}

func stateString(s interface{ String() string }) (out string) {
	defer func() {
		if recover() != nil {
			out = "（知らない状態）"
		}
	}()
	return s.String()
}

func entityString(e mcdriver.Entity) string {
	n := e.Type.Name
	if e.Name != "" {
		n += " " + e.Name
	}
	if e.Item.Count > 0 {
		n += fmt.Sprintf(" %s×%d", e.Item.Name, e.Item.Count)
	}
	return fmt.Sprintf("#%d %s (%.1f, %.1f, %.1f)", e.ID, n, e.Pos.X, e.Pos.Y, e.Pos.Z)
}

// status は今の状態を数行の文字列にする。
func status(c *mcdriver.Client, radius float64) string {
	var b strings.Builder
	s := c.Self()
	day, tod := c.Time()
	w := c.Weather()
	fmt.Fprintf(&b, "---- %s\n", time.Now().Format("15:04:05"))
	if s.Spawned {
		fmt.Fprintf(&b, "位置 (%.2f, %.2f, %.2f) 向き %.0f/%.0f 接地 %v  %v\n", s.Pos.X, s.Pos.Y, s.Pos.Z, s.Yaw, s.Pitch, s.OnGround, s.GameMode)
	} else {
		fmt.Fprintln(&b, "位置 まだ無い")
	}
	fmt.Fprintf(&b, "HP %.1f  満腹度 %d（隠し %.1f）  経験値 Lv%d  死亡 %v\n", s.Health, s.Food, s.Saturation, s.Experience.Level, s.Dead)
	fmt.Fprintf(&b, "時刻 %d 日目 %05d  天気 %v（雨 %.2f 雷 %.2f）\n", day, tod, w.Kind, w.RainLevel, w.ThunderLevel)

	inv := c.Inventory()
	var items []string
	for i, it := range inv.Slots {
		if !it.Empty() {
			items = append(items, fmt.Sprintf("%d:%v", i, it))
		}
	}
	held := inv.Held()
	fmt.Fprintf(&b, "持ち物 %d 種（手に %v）: %s\n", len(items), held, strings.Join(items, " "))

	type near struct {
		e mcdriver.Entity
		d float64
	}
	var ns []near
	for _, e := range c.Entities() {
		dx, dy, dz := e.Pos.X-s.Pos.X, e.Pos.Y-s.Pos.Y, e.Pos.Z-s.Pos.Z
		if d2 := dx*dx + dy*dy + dz*dz; d2 <= radius*radius {
			ns = append(ns, near{e, d2})
		}
	}
	slices.SortFunc(ns, func(a, b near) int {
		switch {
		case a.d < b.d:
			return -1
		case a.d > b.d:
			return 1
		}
		return 0
	})
	fmt.Fprintf(&b, "近くの生き物 %d（%.0f ブロック以内）\n", len(ns), radius)
	for i, n := range ns {
		if i >= 10 {
			fmt.Fprintf(&b, "  ほか %d\n", len(ns)-10)
			break
		}
		fmt.Fprintf(&b, "  %s\n", entityString(n.e))
	}
	if d := c.EventsDropped(); d > 0 {
		fmt.Fprintf(&b, "捨てた出来事 %d\n", d)
	}
	return b.String()
}
