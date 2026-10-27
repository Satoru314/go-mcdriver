//go:build unix

package testserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// lockFile は path のファイルに排他の鍵（flock）をかける。ほかのプロセスが持っていれば、
// ctx が終わるまで短い間隔で取り直す。戻り値のファイルを Close すると鍵が外れる。
func lockFile(ctx context.Context, path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			f.Close()
			return nil, fmt.Errorf("testserver: 鍵をかけられない（%s）: %w", path, err)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, fmt.Errorf("testserver: 鍵を待つあいだに中断された（%s）: %w", path, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
