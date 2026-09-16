//go:build agentintegration

package agent

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMcodeRealACPContextAndTool은 인증된 MiniMax Code ACP 턴의 전체 흐름을 검증한다.
// 워크스페이스의 확인용 데이터로 작업 디렉터리 지침, 프로젝트 스킬 탐색,
// 파일 접근과 ACP 도구 이벤트 전달을 함께 확인한다.
func TestMcodeRealACPContextAndTool(t *testing.T) {
	requireRealAgentTest(t)
	if testing.Short() {
		t.Skip("skipping real CLI integration test in -short mode")
	}

	path, err := exec.LookPath("mcode")
	if err != nil {
		t.Skip("mcode not on PATH; skipping real CLI integration test")
	}
	if version, err := exec.Command(path, "--version").CombinedOutput(); err == nil {
		t.Logf("mcode --version: %s", strings.TrimSpace(string(version)))
	} else {
		t.Logf("mcode version unavailable: %v (%s)", err, strings.TrimSpace(string(version)))
	}

	workDir := t.TempDir()
	writeMcodeTestFile(t, filepath.Join(workDir, "AGENTS.md"), `# Multica 연동 테스트 컨텍스트

For every response in this workspace, include the exact marker AGENTS-MCODE-OK.
`)
	writeMcodeTestFile(t, filepath.Join(workDir, ".minimax", "skills", "multica-integration-test", "SKILL.md"), `---
name: multica-integration-test
description: Use when asked to run the Multica MiniMax Code integration test.
---
# Multica 연동 테스트 스킬

Read tool-canary.txt with a file-reading tool. Include the exact marker SKILL-MCODE-OK and the file contents in the final response.
`)
	writeMcodeTestFile(t, filepath.Join(workDir, "tool-canary.txt"), "TOOL-MCODE-OK\n")

	backend, err := New("mcode", Config{ExecutablePath: path, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new mcode backend: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx,
		"Run the multica-integration-test skill. Follow the workspace instructions and return its requested evidence.",
		ExecOptions{Cwd: workDir, Timeout: 210 * time.Second},
	)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var toolUses, toolResults int
	var canaryToolResult bool
	messagesDone := make(chan struct{})
	go func() {
		defer close(messagesDone)
		for message := range session.Messages {
			switch message.Type {
			case MessageToolUse:
				toolUses++
			case MessageToolResult:
				toolResults++
				if strings.Contains(message.Output, "TOOL-MCODE-OK") {
					canaryToolResult = true
				}
			}
		}
	}()

	var result Result
	select {
	case result = <-session.Result:
	case <-time.After(240 * time.Second):
		t.Fatal("timeout waiting for real mcode result")
	}
	<-messagesDone

	if result.Status != "completed" {
		t.Fatalf("real mcode run did not complete: status=%q error=%q output=%q", result.Status, result.Error, result.Output)
	}
	for _, marker := range []string{"AGENTS-MCODE-OK", "SKILL-MCODE-OK", "TOOL-MCODE-OK"} {
		if !strings.Contains(result.Output, marker) {
			t.Fatalf("real mcode output missing %s: %q", marker, result.Output)
		}
	}
	if toolUses == 0 || toolResults == 0 || !canaryToolResult {
		t.Fatalf("real mcode stream did not expose canary file execution: uses=%d results=%d canary_result=%t", toolUses, toolResults, canaryToolResult)
	}
	if result.SessionID == "" {
		t.Fatal("real mcode run returned an empty session id")
	}
	t.Logf("real mcode integration test OK: session=%s tools=%d/%d output=%q", result.SessionID, toolUses, toolResults, result.Output)
}

func writeMcodeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s parent: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
