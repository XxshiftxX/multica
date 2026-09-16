//go:build agentintegration

package agent

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// TestDshRealRuntime은 Multica 백엔드, 설치된 DSH 프로필, 모델 공급자와 최종 결과까지 검증한다.
// 설정된 DeepSeek 모델을 호출하고 API 사용량을 소모하므로 명시적으로 허용해야 실행한다.
func TestDshRealRuntime(t *testing.T) {
	if os.Getenv("MULTICA_RUN_REAL_AGENT_TESTS") != "1" {
		t.Skip("set MULTICA_RUN_REAL_AGENT_TESTS=1 to run the DSH integration test")
	}
	path := os.Getenv("MULTICA_DSH_PATH")
	if path == "" {
		t.Fatal("MULTICA_DSH_PATH is required")
	}
	b, err := New("dsh", Config{ExecutablePath: path, TaskID: "dsh-real-test", Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cwd := t.TempDir()
	session, err := b.Execute(ctx, "Remember the code word PINEAPPLE. Reply with exactly DSH_MULTICA_OK and nothing else.", ExecOptions{
		Cwd: cwd, Model: "deepseek-official/deepseek-v4-flash", ThinkingLevel: "off",
		Timeout: 90 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Messages {
	}
	result := <-session.Result
	if result.Status != "completed" {
		t.Fatalf("DSH integration test failed: status=%q error=%q", result.Status, result.Error)
	}
	if strings.TrimSpace(result.Output) != "DSH_MULTICA_OK" {
		t.Fatalf("unexpected DSH output: %q", result.Output)
	}
	if result.SessionID == "" {
		t.Fatal("DSH integration test returned no session ID")
	}

	resumed, err := b.Execute(ctx, "Reply with exactly the code word from the previous turn and nothing else.", ExecOptions{
		Cwd: cwd, Model: "deepseek-official/deepseek-v4-flash", ThinkingLevel: "off",
		ResumeSessionID: result.SessionID, Timeout: 90 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range resumed.Messages {
	}
	resumeResult := <-resumed.Result
	if resumeResult.Status != "completed" {
		t.Fatalf("DSH resume test failed: status=%q error=%q", resumeResult.Status, resumeResult.Error)
	}
	if strings.TrimSpace(resumeResult.Output) != "PINEAPPLE" {
		t.Fatalf("DSH resume lost conversation context: %q", resumeResult.Output)
	}
	if resumeResult.SessionID != result.SessionID {
		t.Fatalf("DSH resume changed session ID: %q -> %q", result.SessionID, resumeResult.SessionID)
	}
}
