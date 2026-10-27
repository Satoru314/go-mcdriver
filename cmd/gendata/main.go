// gendata は Mojang の公式サーバー jar から公式データを書き出し、data/*_gen.go を作る。
//
// 使い方（リポジトリのルートで）:
//
//	go run ./cmd/gendata                 # jar の取得（sha1 確認）→ データ出力 → 生成
//	go run ./cmd/gendata -skip-run       # 書き出し済みの .gendata/generated から生成だけ行う
//
// 公式データに無い情報（硬さ、当たり判定の形、光、エンティティの大きさなど）は、
// PrismarineJS の minecraft-data の固定したコミットから取り、公式データと突き合わせる（mcdata.go）。
//
// 作業フォルダ（既定 .gendata）は .gitignore 済み。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	version := flag.String("version", "1.21.4", "Minecraft のバージョン")
	workDir := flag.String("work", ".gendata", "jar と書き出したデータを置く作業フォルダ")
	outDir := flag.String("out", "data", "生成した Go コードを置くフォルダ")
	java := flag.String("java", "docker", `java の実行ファイル。"docker" なら eclipse-temurin:21-jre のコンテナで動かす`)
	skipRun := flag.Bool("skip-run", false, "jar の取得とデータ出力を飛ばし、書き出し済みのデータから生成する")
	flag.Parse()

	if err := run(*version, *workDir, *outDir, *java, *skipRun); err != nil {
		fmt.Fprintln(os.Stderr, "gendata:", err)
		os.Exit(1)
	}
}

func run(version, workDir, outDir, java string, skipRun bool) error {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	jar := filepath.Join(workDir, "server-"+version+".jar")
	if !skipRun {
		var err error
		jar, err = fetchServerJar(version, workDir)
		if err != nil {
			return err
		}
		if err := runDataGenerator(context.Background(), java, jar, workDir); err != nil {
			return err
		}
	}
	if version != mcdataVersion {
		return fmt.Errorf("minecraft-data のファイルは %s に固定している（指定は %s）", mcdataVersion, version)
	}
	// minecraft-data は -skip-run でも取得する（取得済みで sha256 が合えば何もしない）
	if err := fetchMCData(workDir); err != nil {
		return err
	}
	return generate(version, workDir, outDir, jar)
}
