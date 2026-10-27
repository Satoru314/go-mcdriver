package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// runDataGenerator はサーバー jar の公式データ出力を実行し、workDir/generated に書き出す。
// java が "docker" なら eclipse-temurin:21-jre のコンテナで動かす（CT102 には Java がないため）。
// それ以外なら java の実行ファイルのパスとして使う。
func runDataGenerator(ctx context.Context, java, jarPath, workDir string) error {
	outDir := filepath.Join(workDir, "generated")
	if err := os.RemoveAll(outDir); err != nil {
		return err
	}
	jarName := filepath.Base(jarPath)
	// --reports: blocks.json, registries.json, packets.json, items.json など
	// --server: レシピなどのデータパック（data/minecraft/...）
	args := []string{"-DbundlerMainClass=net.minecraft.data.Main", "-jar", jarName,
		"--reports", "--server", "--output", "generated"}

	ctx, cancel := context.WithTimeout(ctx, 110*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if java == "docker" {
		abs, err := filepath.Abs(workDir)
		if err != nil {
			return err
		}
		dockerArgs := []string{"run", "--rm",
			"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
			"-v", abs + ":/work", "-w", "/work",
			"-e", "HOME=/work",
			"eclipse-temurin:21-jre", "java"}
		dockerArgs = append(dockerArgs, args...)
		// docker グループに後から入ったシェルでも動くように sg docker を通す
		cmd = exec.CommandContext(ctx, "sg", "docker", "-c", "docker "+shellJoin(dockerArgs))
	} else {
		cmd = exec.CommandContext(ctx, java, args...)
		cmd.Dir = workDir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Println("公式データ出力を実行:", strings.Join(cmd.Args, " "))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("公式データ出力: %w", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "reports", "blocks.json")); err != nil {
		return fmt.Errorf("reports/blocks.json ができていない: %w", err)
	}
	return nil
}

// shellJoin は sg -c に渡すため、引数を単引用符で囲んでつなぐ。
func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}
