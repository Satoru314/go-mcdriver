package testserver

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/internal/rcon"
)

// Server は用意できたテスト用サーバー。
type Server struct {
	// Name はコンテナの名前。
	Name string
	// GameAddr はゲームの接続先（"127.0.0.1:port"）。
	GameAddr string
	// RCONAddr は RCON の接続先。
	RCONAddr string
	// RCONPassword は RCON のパスワード。
	RCONPassword string
	// Reused は、呼んだ時点ですでに動いていたサーバーをそのまま使ったか。
	// false なら、コンテナを作った、または止まっていたものを起動した。
	Reused bool
	// Created は、コンテナをこの呼び出しで新しく作ったか。
	Created bool
	// StartedAt はコンテナが起動した時刻（docker の記録）。
	StartedAt string

	cacheDir string
}

// ReadyTimeout は起動してから RCON に応答するまで待つ時間の既定値。
// 最初の起動は、イメージの取得と Paper の準備（Mojang の jar の取得と変換）で数分かかる。
// MCDRIVER_TESTSERVER_WAIT（例 "10m"）で変えられる。
const ReadyTimeout = 5 * time.Minute

// Ensure はテスト用サーバーを用意する。動いていればそれを使い、なければ作って起動し、
// RCON に応答するまで待つ。最後にゲームのルールを設定する。
// 別々のプロセスから同時に呼んでもよい（ファイルの鍵で 1 つずつ順に進む）。
func Ensure(ctx context.Context) (*Server, error) {
	dir, err := CacheDir()
	if err != nil {
		return nil, fmt.Errorf("testserver: キャッシュの場所: %w", err)
	}
	lock, err := lockFile(ctx, filepath.Join(dir, "testserver.lock"))
	if err != nil {
		return nil, err
	}
	defer lock.Close()

	s := &Server{
		Name:         ContainerName,
		GameAddr:     net.JoinHostPort("127.0.0.1", strconv.Itoa(GamePort)),
		RCONAddr:     net.JoinHostPort("127.0.0.1", strconv.Itoa(RCONPort)),
		RCONPassword: RCONPassword,
		cacheDir:     dir,
	}

	st, err := inspectContainer(ctx, s.Name)
	if err != nil {
		return nil, fmt.Errorf("testserver: %w", err)
	}
	if st.Running {
		// 動いているなら、すぐに RCON が通るか確かめる
		if err := s.ping(ctx, 5*time.Second); err == nil {
			s.Reused = true
		}
	}
	if !s.Reused {
		if !st.Exists {
			if err := s.create(ctx); err != nil {
				return nil, err
			}
			s.Created = true
		}
		if !st.Running {
			if _, err := docker(ctx, "start", s.Name); err != nil {
				return nil, fmt.Errorf("testserver: %w", err)
			}
		}
		if err := s.waitReady(ctx); err != nil {
			return nil, err
		}
	}
	if st, err = inspectContainer(ctx, s.Name); err == nil {
		s.StartedAt = st.StartedAt
	}
	if err := s.applyGamerules(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// create は jar を用意し、設定ファイルを書いて、コンテナを作る。
func (s *Server) create(ctx context.Context) error {
	jar, err := ensureJar(ctx, s.cacheDir)
	if err != nil {
		return err
	}
	cfg, err := os.MkdirTemp(s.cacheDir, "config-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cfg)
	if err := os.WriteFile(filepath.Join(cfg, "server.properties"), []byte(serverProperties), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(cfg, "eula.txt"), []byte("eula=true\n"), 0o644); err != nil {
		return err
	}
	if err := os.Chmod(cfg, 0o755); err != nil {
		return err
	}
	if err := createContainer(ctx, s.Name, jar, cfg); err != nil {
		return fmt.Errorf("testserver: コンテナを作れない: %w", err)
	}
	return nil
}

// ping は RCON で list を 1 回送る。
func (s *Server) ping(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := rcon.Dial(ctx, s.RCONAddr, s.RCONPassword)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Command(ctx, "list")
	return err
}

// waitReady は RCON に応答するまで、短い確認を繰り返して待つ。
func (s *Server) waitReady(ctx context.Context) error {
	wait := ReadyTimeout
	if v := os.Getenv("MCDRIVER_TESTSERVER_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			wait = d
		}
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	var last error
	for {
		if last = s.ping(ctx, 3*time.Second); last == nil {
			return nil
		}
		// コンテナが落ちていたら待っても無駄
		if st, err := inspectContainer(ctx, s.Name); err == nil && !st.Running {
			return fmt.Errorf("testserver: コンテナが止まった。ログ:\n%s", Logs(context.WithoutCancel(ctx), 40))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("testserver: %v 待っても RCON が応答しない（最後のエラー: %v）。ログ:\n%s",
				wait, last, Logs(context.WithoutCancel(ctx), 40))
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *Server) applyGamerules(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, err := rcon.Dial(ctx, s.RCONAddr, s.RCONPassword)
	if err != nil {
		return fmt.Errorf("testserver: RCON: %w", err)
	}
	defer c.Close()
	for _, cmd := range gamerules {
		if _, err := c.Command(ctx, cmd); err != nil {
			return fmt.Errorf("testserver: %s: %w", cmd, err)
		}
	}
	return nil
}

// ---- go test から使う入口 ----

var (
	startMu  sync.Mutex
	started  *Server
	startErr error
)

// Start はテスト用サーバーを用意して返す。同じプロセスの中では 2 回目からは前の結果を使う。
// 用意できなければテストを失敗にする。
func Start(t testing.TB) *Server {
	t.Helper()
	startMu.Lock()
	defer startMu.Unlock()
	if started == nil && startErr == nil {
		ctx, cancel := context.WithTimeout(context.Background(), ReadyTimeout+3*time.Minute)
		started, startErr = Ensure(ctx)
		cancel()
		if started != nil {
			t.Logf("testserver: %s（ゲーム %s, RCON %s）再利用=%v 新規作成=%v 起動時刻=%s",
				started.Name, started.GameAddr, started.RCONAddr, started.Reused, started.Created, started.StartedAt)
		}
	}
	if startErr != nil {
		t.Fatalf("テスト用サーバーを用意できない: %v", startErr)
	}
	return started
}

// RCON は RCON の接続を作って返す。テストの終わりに閉じる。
func (s *Server) RCON(t testing.TB) *rcon.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := rcon.Dial(ctx, s.RCONAddr, s.RCONPassword)
	if err != nil {
		t.Fatalf("RCON に繋がらない: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// Command は RCON でコマンドを 1 つ送り、応答を返す。失敗したらテストを失敗にする。
func Command(t testing.TB, c *rcon.Client, cmd string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := c.Command(ctx, cmd)
	if err != nil {
		t.Fatalf("RCON %q: %v", cmd, err)
	}
	return out
}
