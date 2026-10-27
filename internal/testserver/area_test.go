package testserver

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// 区画の番号が、並列に取っても重ならず、1 から順に増えるか（Docker は使わない）。
func TestAllocAreaIDConcurrent(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const n = 100
	var mu sync.Mutex
	seen := map[int]bool{}
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := allocAreaID(ctx, dir)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if seen[id] {
				t.Errorf("番号 %d が重なった", id)
			}
			seen[id] = true
		}()
	}
	wg.Wait()
	for i := 1; i <= n; i++ {
		if !seen[i] {
			t.Errorf("番号 %d が出ていない", i)
		}
	}
}

// 上限を超えたら 1 に戻る。
func TestAllocAreaIDWrap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "areas"), []byte("29000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := allocAreaID(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Fatalf("上限のあとの番号 = %d、期待は 1", id)
	}
}

func TestAreaFor(t *testing.T) {
	a := areaFor(29000)
	if a.X0 != 29_000_000 || a.Z0 != 0 {
		t.Fatalf("区画の位置 = %d,%d", a.X0, a.Z0)
	}
	if len(a.BotName) > 16 {
		t.Fatalf("bot 名が長すぎる: %q", a.BotName)
	}
	if b := areaFor(3); b.BotName != "mcdbot3" || b.X0 != 3000 {
		t.Fatalf("areaFor(3) = %+v", b)
	}
}
