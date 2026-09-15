//go:build agentintegration

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestCodexRealBackgroundContinuation exercises the actual Multica backend:
// a failed native background command must wake a successor that writes the
// recovery artifact before the backend returns its final result.
func TestCodexRealBackgroundContinuation(t *testing.T) {
	testCodexRealBackgroundContinuation(t, false)
}

func TestCodexRealEarlyBackgroundContinuation(t *testing.T) {
	testCodexRealBackgroundContinuation(t, true)
}

func TestCodexRealBackgroundDirectPoll(t *testing.T) {
	requireRealAgentSmoke(t)
	path, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	backend, err := New("codex", Config{ExecutablePath: path, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), CodexBackgroundContinuation: true})
	if err != nil {
		t.Fatal(err)
	}
	session, err := backend.Execute(context.Background(), "This is an isolated test. Do not access the network or spawn agents. Run `sleep 3; printf POLL_FINISHED` using exec_command with yield_time_ms=250. Then use write_stdin to collect that session's final output, waiting until exit code 0. Reply exactly POLL_COLLECTED. Do not leave the result uncollected.", ExecOptions{Cwd: t.TempDir(), Timeout: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	turns := 0
	for message := range session.Messages {
		if message.Type == MessageStatus && message.Status == "running" {
			turns++
		}
	}
	result := <-session.Result
	if result.Status != "completed" || strings.TrimSpace(result.Output) != "POLL_COLLECTED" || turns != 1 {
		t.Fatalf("직접 폴링 뒤 불필요한 재개: turns=%d result=%+v", turns, result)
	}
}

func testCodexRealBackgroundContinuation(t *testing.T, early bool) {
	t.Helper()
	requireRealAgentSmoke(t)
	path, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var logs synchronizedBuffer
	backend, err := New("codex", Config{ExecutablePath: path, Logger: slog.New(slog.NewTextHandler(&logs, nil)), CodexBackgroundContinuation: true})
	if err != nil {
		t.Fatal(err)
	}
	prompt := "This is an isolated background continuation test. Do not access the network or spawn agents. " +
		"First run exactly `sleep 12; printf MULTICA_EXPECTED_FAILURE; exit 1` using exec_command with yield_time_ms=1000. " +
		"When it returns a running session ID, immediately end this turn with exactly BACKGROUND_PENDING. Do not poll or call write_stdin. " +
		"Multica will deliver the completed command as tool output in a new turn. When that result arrives, verify its exitCode is 1 and output includes MULTICA_EXPECTED_FAILURE, then run `printf repaired > recovery.txt` and reply exactly BACKGROUND_RECOVERED."
	if early {
		prompt = strings.Replace(prompt, "sleep 12", "sleep 4", 1)
		prompt = strings.Replace(prompt, "immediately end this turn", "first run a separate synchronous `sleep 6` command with yield_time_ms=10000, then end this turn", 1)
	}
	options := ExecOptions{Cwd: dir, Timeout: 2 * time.Minute}
	if early {
		// 운영 이슈처럼 기존 스레드를 새 App Server 프로세스에서 재개한다.
		seed, err := backend.Execute(context.Background(), "Reply exactly READY. Do not call any tools.", options)
		if err != nil {
			t.Fatal(err)
		}
		for range seed.Messages {
		}
		result := <-seed.Result
		if result.Status != "completed" || result.SessionID == "" {
			t.Fatalf("스레드 준비 실패: %+v", result)
		}
		options.ResumeSessionID = result.SessionID
	}
	session, err := backend.Execute(context.Background(), prompt, options)
	if err != nil {
		t.Fatal(err)
	}
	var turns int
	seenPending, seenCompletion := false, false
	completedBeforePending := false
	for message := range session.Messages {
		if message.Type == MessageStatus && message.Status == "running" {
			turns++
		}
		if message.Type == MessageToolResult && strings.Contains(message.Output, "MULTICA_EXPECTED_FAILURE") {
			seenCompletion = true
			completedBeforePending = !seenPending
		}
		if message.Type == MessageText && strings.TrimSpace(message.Content) == "BACKGROUND_PENDING" {
			seenPending = true
		}
	}
	result := <-session.Result
	if early && result.SessionID != options.ResumeSessionID {
		t.Fatalf("다른 스레드로 재개됨: %s", result.SessionID)
	}
	if result.Status != "completed" || strings.TrimSpace(result.Output) != "BACKGROUND_RECOVERED" {
		t.Fatalf("result=%+v\nlogs=%s", result, logs.String())
	}
	if turns != 2 || !strings.Contains(logs.String(), "codex background continuation") {
		t.Fatalf("no continuation observed: turns=%d logs=%s", turns, logs.String())
	}
	if !seenPending || !seenCompletion || completedBeforePending != early {
		t.Fatalf("완료 순서 불일치: pending=%v completion=%v before=%v want=%v", seenPending, seenCompletion, completedBeforePending, early)
	}
	data, err := os.ReadFile(filepath.Join(dir, "recovery.txt"))
	if err != nil || string(data) != "repaired" {
		t.Fatalf("recovery=%q err=%v", data, err)
	}
	t.Logf("same-thread continuation passed: turns=%d session=%s duration_ms=%d", turns, result.SessionID, result.DurationMs)
}

// TestCodexRealBackgroundProtocol observes the installed app-server's command
// completion contract after a model turn exits. It never touches a project.
func TestCodexRealBackgroundProtocol(t *testing.T) {
	requireRealAgentSmoke(t)
	path, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "app-server", "-c", "features.multi_agent=false")
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { signalProcessGroup(cmd, syscall.SIGKILL); return nil }
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = t.TempDir()
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := startOwnedProcessTree(cmd, slog.Default()); err != nil {
		t.Fatal(err)
	}
	events := make(chan map[string]any, 256)
	c := &codexClient{cfg: Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, stdin: in,
		pending: make(map[int]*pendingRPC), processDone: make(chan struct{}), handshakeTimeout: 30 * time.Second}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		s := bufio.NewScanner(out)
		s.Buffer(make([]byte, 4096), 8*1024*1024)
		for s.Scan() {
			line := s.Text()
			c.handleLine(line)
			var event map[string]any
			if json.Unmarshal([]byte(line), &event) == nil && event["method"] != nil {
				select {
				case events <- event:
				default:
				}
			}
		}
		c.markProcessExited(errCodexProcessExited)
	}()
	defer func() { _ = in.Close(); cancel(); _ = cmd.Wait(); <-readerDone; releaseProcessGroup(cmd) }()
	request := func(method string, params map[string]any) json.RawMessage {
		t.Helper()
		result, err := c.request(ctx, method, params)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		return result
	}
	request("initialize", map[string]any{"clientInfo": map[string]any{"name": "multica-background-probe", "version": "0.1"}, "capabilities": map[string]any{"experimentalApi": true}})
	result := request("thread/start", map[string]any{"cwd": cmd.Dir, "ephemeral": true, "approvalPolicy": "never", "sandbox": "danger-full-access",
		"developerInstructions": "This is an isolated background-process protocol test. Follow the exact user instruction. Do not read files, access the network, or spawn agents."})
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(result, &thread); err != nil || thread.Thread.ID == "" {
		t.Fatalf("thread/start: %s", result)
	}
	c.setThreadID(thread.Thread.ID)
	request("turn/start", map[string]any{"threadId": thread.Thread.ID, "input": []map[string]any{{"type": "text", "text": "Run exactly `sleep 12; printf MULTICA_BACKGROUND_FINISHED` with exec_command and yield_time_ms=1000. When the tool returns a running session ID, immediately finish this turn with exactly BACKGROUND_PENDING. Do not call write_stdin or wait for completion. This deliberately tests what happens after your turn ends."}}})
	var afterTurn <-chan time.Time
	turns, completions := 0, 0
	commandFinished := false
	for {
		select {
		case event := <-events:
			method, _ := event["method"].(string)
			params, _ := event["params"].(map[string]any)
			if method == "turn/started" {
				turns++
				t.Logf("turn started: %s", extractNestedString(params, "turn", "id"))
			}
			if method == "item/started" || method == "item/completed" {
				item, _ := params["item"].(map[string]any)
				if item["type"] == "commandExecution" || item["type"] == "agentMessage" {
					body, _ := json.Marshal(item)
					t.Logf("%s %s", method, body)
				}
				if method == "item/completed" && item["type"] == "commandExecution" && item["exitCode"] == float64(0) && item["aggregatedOutput"] == "MULTICA_BACKGROUND_FINISHED" {
					commandFinished = true
				}
			}
			if method == "turn/completed" {
				completions++
				t.Logf("turn completed: %s", extractNestedString(params, "turn", "status"))
				if afterTurn == nil {
					terminals := request("thread/backgroundTerminals/list", map[string]any{"threadId": thread.Thread.ID})
					var list struct {
						Data []json.RawMessage `json:"data"`
					}
					if err := json.Unmarshal(terminals, &list); err != nil || len(list.Data) == 0 {
						t.Fatalf("turn did not leave a native background command: %s", terminals)
					}
					t.Logf("background terminals: %s", terminals)
					afterTurn = time.After(18 * time.Second)
				}
			}
		case <-afterTurn:
			t.Logf("after wait: turns=%d completions=%d terminals=%s", turns, completions, request("thread/backgroundTerminals/list", map[string]any{"threadId": thread.Thread.ID}))
			if !commandFinished {
				t.Fatal("no successful command completion event after turn exit")
			}
			return
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}
