package testserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// ensureJar は Paper の jar をキャッシュに用意し、そのパスを返す。
// 置いてある jar も、ダウンロードした jar も、sha256 が PaperSHA256 と一致しなければ使わない。
// 鍵をかけた中で呼ぶこと。
func ensureJar(ctx context.Context, dir string) (string, error) {
	path := filepath.Join(dir, paperJarName)
	if sum, err := fileSHA256(path); err == nil && sum == PaperSHA256 {
		return path, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, PaperURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("testserver: jar をダウンロードできない: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("testserver: jar のダウンロード: HTTP %s", resp.Status)
	}
	tmp, err := os.CreateTemp(dir, paperJarName+".*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return "", fmt.Errorf("testserver: jar のダウンロード: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != PaperSHA256 {
		return "", fmt.Errorf("testserver: jar の sha256 が違う: %s（期待 %s）", sum, PaperSHA256)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
