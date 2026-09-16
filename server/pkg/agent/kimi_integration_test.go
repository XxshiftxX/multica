//go:build agentintegration

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestKimiRealACPUsage는 실제 `kimi acp` 연동 흐름을 실행하고
// 작업 결과에 토큰별 사용량이 포함되는지 검증한다.
//
// This is the test that would have caught #6448 before it shipped: kimi-code
// 0.33.0 reports no usage over ACP at all, so any fix validated only against a
// hand-written ACP fixture passes while the real runtime still reports nothing.
//
// The model matters: the CLI rejects thinking=off on some models with
// `400 invalid thinking`, and a turn that dies there writes no usage record.
// 계정의 기본 모델에 이 제약이 있으면 KIMI_TEST_MODEL로 테스트 모델을 지정한다.
func TestKimiRealACPUsage(t *testing.T) {
	requireRealAgentTest(t)
	if testing.Short() {
		t.Skip("skipping real CLI integration test in -short mode")
	}
	path, err := exec.LookPath("kimi")
	if err != nil {
		t.Skip("kimi not on PATH; skipping real CLI integration test")
	}
	if version, err := exec.Command(path, "--version").CombinedOutput(); err == nil {
		t.Logf("kimi CLI version: %s", strings.TrimSpace(string(version)))
	}

	backend, err := New("kimi", Config{ExecutablePath: path, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new kimi backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx, "Reply with exactly one word: alpha", ExecOptions{
		Timeout: 150 * time.Second,
		Model:   kimiTestModel(),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	result := <-session.Result
	t.Logf("status=%q error=%q output=%q", result.Status, result.Error, result.Output)
	if result.Status != "completed" {
		t.Fatalf("expected completed, got %q (%s)", result.Status, result.Error)
	}

	if len(result.Usage) == 0 {
		t.Fatal("no usage reported: the wire-log fallback did not fire")
	}
	var total TokenUsage
	for model, u := range result.Usage {
		t.Logf("usage[%s] = input:%d output:%d cacheRead:%d cacheWrite:%d",
			model, u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
		total.InputTokens += u.InputTokens
		total.OutputTokens += u.OutputTokens
		total.CacheReadTokens += u.CacheReadTokens
		total.CacheWriteTokens += u.CacheWriteTokens
	}
	if total.InputTokens <= 0 {
		t.Errorf("input tokens = %d, want > 0", total.InputTokens)
	}
	if total.OutputTokens <= 0 {
		t.Errorf("output tokens = %d, want > 0", total.OutputTokens)
	}
}

func kimiTestModel() string {
	if model := strings.TrimSpace(os.Getenv("KIMI_TEST_MODEL")); model != "" {
		return model
	}
	return ""
}

// TestKimiRealMcpConfigReachesSession은 Multica 형식의 agent.mcp_config로
// 이 패키지의 백엔드에서 실제 `kimi acp`를 실행하고 MCP 서버의 연결과 호출을 검증한다.
//
// Users reported that MCP configured in Multica "never reaches kimi", pointing
// at the bare `kimi acp` launch line as evidence (MUL-5846). That line carries
// no MCP flags because the CLI has none — kimi takes MCP over ACP session/new
// instead — so only an end-to-end run against the real binary can settle it.
// The oracle is the MCP server process itself: it appends to a log when spawned
// and returns a sentinel from its one tool, so neither a hand-written ACP
// fixture nor the model's own description of its tools can fake a pass.
func TestKimiRealMcpConfigReachesSession(t *testing.T) {
	requireRealAgentTest(t)
	if testing.Short() {
		t.Skip("skipping real CLI integration test in -short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("shell-script MCP fixture is POSIX-only")
	}
	path, err := exec.LookPath("kimi")
	if err != nil {
		t.Skip("kimi not on PATH; skipping real CLI integration test")
	}
	if version, err := exec.Command(path, "--version").CombinedOutput(); err == nil {
		t.Logf("kimi CLI version: %s", strings.TrimSpace(string(version)))
	}

	dir := t.TempDir()
	spawnLog := filepath.Join(dir, "spawned.log")
	serverPath := filepath.Join(dir, "mcp-probe")
	writeTestExecutable(t, serverPath, []byte(stdioMcpProbeScript(spawnLog)))

	backend, err := New("kimi", Config{ExecutablePath: path, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new kimi backend: %v", err)
	}

	// Exactly the shape the daemon forwards from agent.mcp_config.
	mcpConfig := fmt.Sprintf(`{"mcpServers":{"multicaprobe":{"command":%q,"args":[],"env":{}}}}`, serverPath)

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	session, err := backend.Execute(ctx,
		"Call the multica_probe_ping tool and reply with exactly what it returned. Do nothing else.",
		ExecOptions{
			Timeout:   210 * time.Second,
			Cwd:       dir,
			Model:     kimiTestModel(),
			McpConfig: json.RawMessage(mcpConfig),
		})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	result := <-session.Result
	t.Logf("status=%q error=%q output=%q", result.Status, result.Error, result.Output)

	// Ground truth: did kimi actually start the MCP server we configured?
	spawned, err := os.ReadFile(spawnLog)
	t.Logf("MCP server saw:\n%s", spawned)
	if err != nil || !bytes.Contains(spawned, []byte("SPAWNED")) {
		t.Fatalf("MCP server was never started by kimi (log=%q err=%v) — the managed mcp_config did not reach the session", spawned, err)
	}
	if !bytes.Contains(spawned, []byte("tools/list")) {
		t.Fatalf("kimi started the MCP server but never listed its tools: %q", spawned)
	}
	if !strings.Contains(result.Output, "MULTICA_MCP_OK") {
		t.Fatalf("agent output does not contain the tool's sentinel: %q", result.Output)
	}
}

// stdioMcpProbeScript returns a minimal POSIX-sh MCP stdio server. It records
// every request it receives in spawnLog so the test can assert on the process
// rather than on anything the model says.
func stdioMcpProbeScript(spawnLog string) string {
	return `#!/bin/sh
LOG=` + fmt.Sprintf("%q", spawnLog) + `
echo SPAWNED >> "$LOG"
while IFS= read -r line; do
  echo "$line" >> "$LOG"
  id=` + "`" + `printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p'` + "`" + `
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"multica-probe","version":"1.0.0"}}}\n' "$id"
      ;;
    *'"method":"tools/list"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"multica_probe_ping","description":"Returns MULTICA_MCP_OK.","inputSchema":{"type":"object","properties":{},"additionalProperties":false}}]}}\n' "$id"
      ;;
    *'"method":"tools/call"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"MULTICA_MCP_OK"}],"isError":false}}\n' "$id"
      ;;
    *'"method":"resources/list"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"resources":[]}}\n' "$id"
      ;;
    *'"method":"prompts/list"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"prompts":[]}}\n' "$id"
      ;;
    *'"id"'*)
      printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"method not found"}}\n' "$id"
      ;;
  esac
done
`
}
