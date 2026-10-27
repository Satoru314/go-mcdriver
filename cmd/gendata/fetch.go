package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// manifestURL は Mojang のバージョン一覧（piston-meta）。
const manifestURL = "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json"

var httpClient = &http.Client{Timeout: 90 * time.Second}

// versionManifest はバージョン一覧のうち、使う部分だけ。
type versionManifest struct {
	Versions []struct {
		ID   string `json:"id"`
		URL  string `json:"url"`
		SHA1 string `json:"sha1"`
	} `json:"versions"`
}

// versionInfo はバージョンごとの JSON のうち、サーバー jar の部分だけ。
type versionInfo struct {
	Downloads struct {
		Server struct {
			SHA1 string `json:"sha1"`
			Size int64  `json:"size"`
			URL  string `json:"url"`
		} `json:"server"`
	} `json:"downloads"`
}

// fetchServerJar は version のサーバー jar を workDir/server.jar に置く。
// すでにあって sha1 が合えばダウンロードしない。sha1 が合わなければエラーにする。
func fetchServerJar(version, workDir string) (string, error) {
	var man versionManifest
	if _, err := getJSON(manifestURL, "", &man); err != nil {
		return "", fmt.Errorf("バージョン一覧: %w", err)
	}
	var verURL, verSHA1 string
	for _, v := range man.Versions {
		if v.ID == version {
			verURL, verSHA1 = v.URL, v.SHA1
			break
		}
	}
	if verURL == "" {
		return "", fmt.Errorf("バージョン %s が一覧にない", version)
	}
	var info versionInfo
	if _, err := getJSON(verURL, verSHA1, &info); err != nil {
		return "", fmt.Errorf("バージョン情報: %w", err)
	}
	srv := info.Downloads.Server
	if srv.URL == "" || srv.SHA1 == "" {
		return "", fmt.Errorf("バージョン %s にサーバー jar の情報がない", version)
	}

	jarPath := filepath.Join(workDir, "server-"+version+".jar")
	if sum, err := fileSHA1(jarPath); err == nil && sum == srv.SHA1 {
		fmt.Printf("サーバー jar はダウンロード済み（sha1 %s）\n", sum)
		return jarPath, nil
	}
	fmt.Printf("サーバー jar をダウンロード: %s\n", srv.URL)
	body, err := get(srv.URL)
	if err != nil {
		return "", err
	}
	if sum := sha1Hex(body); sum != srv.SHA1 {
		return "", fmt.Errorf("サーバー jar の sha1 が合わない: 期待 %s, 実際 %s", srv.SHA1, sum)
	}
	if err := os.WriteFile(jarPath, body, 0o644); err != nil {
		return "", err
	}
	fmt.Printf("sha1 確認済み: %s\n", srv.SHA1)
	return jarPath, nil
}

// getJSON は url を取得して v に読み込む。wantSHA1 が空でなければ本文の sha1 を確かめる。
func getJSON(url, wantSHA1 string, v any) ([]byte, error) {
	body, err := get(url)
	if err != nil {
		return nil, err
	}
	if wantSHA1 != "" {
		if sum := sha1Hex(body); sum != wantSHA1 {
			return nil, fmt.Errorf("%s の sha1 が合わない: 期待 %s, 実際 %s", url, wantSHA1, sum)
		}
	}
	return body, json.Unmarshal(body, v)
}

func get(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func sha1Hex(b []byte) string {
	s := sha1.Sum(b)
	return hex.EncodeToString(s[:])
}

func fileSHA1(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha1Hex(b), nil
}
