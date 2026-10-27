// Package testserver は、go test から使うテスト用の Minecraft サーバー（Paper 1.21.4 build 232）を
// Docker で用意する。
//
//   - 決まった名前のコンテナ（ContainerName）を起動したままにして、次のテストでも再利用する。
//     なければ作って起動し、RCON に応答するまで待つ
//   - 起動の手続きは、ファイルの鍵（flock）で守る。go test ./... のように別々のプロセスが
//     同時に呼んでも、サーバーは 1 つしかできない
//   - テストごとに専用の区画（x を 1000 ずつずらす）と専用の bot 名を割り当てる（NewArea）
//
// Docker は docker コマンド（CLI）で操作する。docker グループが反映されていないシェルでは
// sg docker -c 'go test ...' で動かす。本番サーバー（192.168.40.197）には繋がない。
package testserver

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// ContainerName はテスト用サーバーのコンテナの名前。
	ContainerName = "mcdriver-testserver"
	// Image はサーバーを動かす Java 21 のイメージ。
	Image = "eclipse-temurin:21-jre"
	// GamePort はホスト側（127.0.0.1）のゲームのポート。
	GamePort = 25865
	// RCONPort はホスト側（127.0.0.1）の RCON のポート。
	RCONPort = 25875
	// RCONPassword は RCON のパスワード。127.0.0.1 にしか公開しないので固定でよい。
	RCONPassword = "mcdriver-test"

	// PaperURL は Paper 1.21.4 build 232 の jar。
	PaperURL = "https://fill-data.papermc.io/v1/objects/5ee4f542f628a14c644410b08c94ea42e772ef4d29fe92973636b6813d4eaffc/paper-1.21.4-232.jar"
	// PaperSHA256 は jar の sha256。ダウンロードのたびに必ず確かめる。
	PaperSHA256 = "5ee4f542f628a14c644410b08c94ea42e772ef4d29fe92973636b6813d4eaffc"
	// paperJarName はキャッシュに置く jar の名前。
	paperJarName = "paper-1.21.4-232.jar"

	// labelKey はこの仕組みが作ったコンテナに付ける印。
	labelKey = "io.github.satoru314.go-mcdriver.testserver"
)

// serverProperties はコンテナを作るときに置く server.properties。
var serverProperties = strings.Join([]string{
	"online-mode=false",
	"enforce-secure-profile=false",
	`level-type=minecraft\:flat`,
	"generate-structures=false",
	"spawn-protection=0",
	"white-list=false",
	"enforce-whitelist=false",
	"enable-rcon=true",
	"rcon.port=25575",
	"rcon.password=" + RCONPassword,
	"broadcast-rcon-to-ops=false",
	"max-players=100",
	"view-distance=6",
	"simulation-distance=6",
	"motd=go-mcdriver test server",
	"",
}, "\n")

// gamerules はサーバーに繋ぐたびに RCON で設定する（何度送っても同じ結果になる）。
var gamerules = []string{
	"gamerule doDaylightCycle false",
	"gamerule doWeatherCycle false",
	"gamerule doMobSpawning false",
}

// CacheDir は jar や区画の番号を置く場所。MCDRIVER_CACHE_DIR で変えられる。
func CacheDir() (string, error) {
	if d := os.Getenv("MCDRIVER_CACHE_DIR"); d != "" {
		return d, os.MkdirAll(d, 0o755)
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "go-mcdriver")
	return d, os.MkdirAll(d, 0o755)
}
