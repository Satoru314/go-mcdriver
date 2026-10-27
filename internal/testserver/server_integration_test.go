//go:build integration

package testserver

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// 単位 0 の合格条件: テスト用サーバーが立ち上がり（または再利用され）、RCON で list と setblock が通る。
//
//	sg docker -c 'go test -tags integration -run TestServer -v ./internal/testserver/'
//
// MCDRIVER_EXPECT_REUSE=1 を付けると、すでに動いていたサーバーを再利用したことも確かめる。
func TestServerListAndSetblock(t *testing.T) {
	s := Start(t)
	if os.Getenv("MCDRIVER_EXPECT_REUSE") != "" && !s.Reused {
		t.Errorf("動いているサーバーを再利用するはずが、新しく起動した（新規作成=%v）", s.Created)
	}
	rc := s.RCON(t)

	out := Command(t, rc, "list")
	t.Logf("list: %q", out)
	if !strings.Contains(out, "players online") {
		t.Errorf("list の応答が思っていた形でない: %q", out)
	}

	for _, rule := range []string{"doDaylightCycle", "doWeatherCycle", "doMobSpawning"} {
		out := Command(t, rc, "gamerule "+rule)
		t.Logf("gamerule %s: %q", rule, out)
		if !strings.Contains(out, "false") {
			t.Errorf("gamerule %s が false でない: %q", rule, out)
		}
	}

	a := s.NewArea(t)
	a.ForceLoad(t, rc)
	x, z := a.Center()

	// 平らなワールドの地表の高さを確かめる
	if out := Command(t, rc, fmt.Sprintf("execute if block %d %d %d minecraft:grass_block", x, SurfaceY, z)); !strings.Contains(out, "passed") {
		t.Errorf("y=%d が草ブロックでない: %q", SurfaceY, out)
	}
	if out := Command(t, rc, fmt.Sprintf("execute if block %d %d %d minecraft:air", x, StandY, z)); !strings.Contains(out, "passed") {
		t.Errorf("y=%d が空気でない: %q", StandY, out)
	}

	// setblock で置いて、置けたことを確かめる
	out = Command(t, rc, fmt.Sprintf("setblock %d %d %d minecraft:stone", x, StandY, z))
	t.Logf("setblock: %q", out)
	if !strings.Contains(out, "Changed the block") {
		t.Errorf("setblock の応答: %q", out)
	}
	if out := Command(t, rc, fmt.Sprintf("execute if block %d %d %d minecraft:stone", x, StandY, z)); !strings.Contains(out, "passed") {
		t.Errorf("置いた石が無い: %q", out)
	}
	Command(t, rc, fmt.Sprintf("setblock %d %d %d minecraft:air", x, StandY, z))
}

// 並列のテストに別々の区画と bot 名が割り当てられる。
func TestServerParallelAreas(t *testing.T) {
	s := Start(t)
	names := make(chan string, 4)
	t.Run("group", func(t *testing.T) {
		for i := range 4 {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				t.Parallel()
				a := s.NewArea(t)
				rc := s.RCON(t)
				x, z := a.Center()
				a.ForceLoad(t, rc)
				out := Command(t, rc, fmt.Sprintf("setblock %d %d %d minecraft:oak_planks", x, StandY, z))
				if !strings.Contains(out, "Changed the block") {
					t.Errorf("setblock: %q", out)
				}
				Command(t, rc, fmt.Sprintf("setblock %d %d %d minecraft:air", x, StandY, z))
				names <- a.BotName
			})
		}
	})
	close(names)
	seen := map[string]bool{}
	for n := range names {
		if seen[n] {
			t.Errorf("bot 名 %s が重なった", n)
		}
		seen[n] = true
	}
}
