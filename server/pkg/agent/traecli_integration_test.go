//go:build agentintegration

package agent

import (
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestTraecliRealACP는 실제 `traecli acp serve` 실행 파일의 전체 연동 흐름을 검증한다.
func TestTraecliRealACP(t *testing.T) {
	requireRealAgentTest(t)
	if testing.Short() {
		t.Skip("skipping real CLI integration test in -short mode")
	}
	path, err := exec.LookPath("traecli")
	if err != nil {
		t.Skip("traecli not on PATH; skipping real CLI integration test")
	}

	backend, err := New("traecli", Config{ExecutablePath: path, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new traecli backend: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx, "Reply with exactly one word: pong. Do not use any tools.", ExecOptions{
		Cwd:     t.TempDir(),
		Timeout: 80 * time.Second,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	select {
	case result := <-session.Result:
		if result.Status != "completed" {
			t.Fatalf("real traecli run did not complete: status=%q error=%q", result.Status, result.Error)
		}
		if !strings.Contains(strings.ToLower(result.Output), "pong") {
			t.Fatalf("expected real traecli output to contain 'pong', got %q", result.Output)
		}
		if result.SessionID == "" {
			t.Error("expected a non-empty session id from real traecli")
		}
		t.Logf("real traecli integration test OK: session=%s output=%q", result.SessionID, result.Output)
	case <-time.After(90 * time.Second):
		t.Fatal("timeout waiting for real traecli result")
	}
}
