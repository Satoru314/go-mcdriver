package testserver

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// dockerBin は docker コマンドの名前。MCDRIVER_DOCKER で変えられる。
func dockerBin() string {
	if b := os.Getenv("MCDRIVER_DOCKER"); b != "" {
		return b
	}
	return "docker"
}

// docker は docker コマンドを動かし、標準出力を返す。失敗したら標準エラーをエラーに入れる。
func docker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, dockerBin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// containerState はコンテナの状態。
type containerState struct {
	Exists    bool
	Running   bool
	StartedAt string
}

func inspectContainer(ctx context.Context, name string) (containerState, error) {
	out, err := docker(ctx, "container", "inspect", "-f", "{{.State.Running}} {{.State.StartedAt}}", name)
	if err != nil {
		if strings.Contains(err.Error(), "No such container") || strings.Contains(err.Error(), "no such container") {
			return containerState{}, nil
		}
		return containerState{}, err
	}
	f := strings.Fields(out)
	if len(f) < 2 {
		return containerState{}, fmt.Errorf("docker inspect の出力が読めない: %q", out)
	}
	return containerState{Exists: true, Running: f[0] == "true", StartedAt: f[1]}, nil
}

// createContainer はテスト用サーバーのコンテナを作る（起動はしない）。
// jar は読み取り専用で /opt/paper.jar に入れ、ワールドなどはコンテナの中の /data に置く。
func createContainer(ctx context.Context, name, jarPath, configDir string) error {
	_, err := docker(ctx, "container", "create",
		"--name", name,
		"--label", labelKey+"=1",
		"--restart", "unless-stopped",
		"-p", fmt.Sprintf("127.0.0.1:%d:25565", GamePort),
		"-p", fmt.Sprintf("127.0.0.1:%d:25575", RCONPort),
		"-v", jarPath+":/opt/paper.jar:ro",
		"-w", "/data",
		Image,
		"java", "-Xms1G", "-Xmx2G", "-Duser.home=/data", "-jar", "/opt/paper.jar", "--nogui",
	)
	if err != nil {
		return err
	}
	// server.properties と eula.txt を /data に入れる。-w で /data はすでにあるので、
	// フォルダごとではなく中身（configDir/.）を写す
	if _, err := docker(ctx, "cp", configDir+"/.", name+":/data"); err != nil {
		docker(context.WithoutCancel(ctx), "rm", "-f", name)
		return err
	}
	return nil
}

// Logs はコンテナのログの最後の n 行を返す（テストが失敗したときの記録用）。
func Logs(ctx context.Context, n int) string {
	out, err := docker(ctx, "logs", "--tail", fmt.Sprint(n), ContainerName)
	if err != nil {
		return err.Error()
	}
	return out
}
