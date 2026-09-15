package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexBackgroundReturnHistory(t *testing.T) {
	for _, polled := range []bool{false, true} {
		b := newCodexBackgroundCommands()
		b.observe("turn/started", backgroundEvent("turn/started", "current", nil), true)
		b.observe("item/started", backgroundEvent("item/started", "job", nil), true)
		b.commands["job"].ProcessID = "42"
		// 완료 이벤트가 원본 도구 반환을 읽는 시점보다 먼저 도착한 상황이다.
		b.observe("item/completed", backgroundEvent("item/completed", "job", map[string]any{"status": "completed", "aggregatedOutput": "failure"}), true)
		b.observe("turn/completed", backgroundEvent("turn/completed", "current", nil), true)
		b.rolloutPath = filepath.Join(t.TempDir(), "rollout.jsonl")
		records := []any{
			map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "old"}},
			map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "output": `{"chunk_id":"old","wall_time_seconds":0.25,"session_id":42}`}},
		}
		write := func() {
			var data []byte
			for _, record := range records {
				line, _ := json.Marshal(record)
				data = append(data, line...)
				data = append(data, '\n')
			}
			if err := os.WriteFile(b.rolloutPath, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		write()
		if err := b.reconcileReturns(); !errors.Is(err, errCodexReturnHistoryPending) {
			t.Fatalf("현재 턴 기록 대기 예상: %v", err)
		}
		if b.awaiting["job"] {
			t.Fatal("이전 턴의 반환을 현재 명령에 적용함")
		}
		records = append(records,
			map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "current"}},
			map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "output": []any{map[string]any{"type": "input_text", "text": `{"chunk_id":"new","wall_time_seconds":0.25,"session_id":42}`}}}},
		)
		if polled {
			records = append(records,
				map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "poll", "input": "text(await tools.write_stdin({session_id:42,chars:\"\",yield_time_ms:5000}));"}},
				map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "poll", "output": `{"chunk_id":"polled","wall_time_seconds":0.25,"exit_code":0,"output":"failure"}`}},
			)
		}
		records = append(records, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_complete", "turn_id": "current"}})
		write()
		if err := b.reconcileReturns(); err != nil {
			t.Fatal(err)
		}
		commands, _, err := b.wait(context.Background(), nil)
		want := 1
		if polled {
			want = 0
		}
		if err != nil || len(commands) != want {
			t.Fatalf("polled=%v commands=%+v err=%v", polled, commands, err)
		}
		if err := b.reconcileReturns(); err != nil {
			t.Fatal(err)
		}
		commands, _, _ = b.wait(context.Background(), nil)
		if len(commands) != 0 {
			t.Fatal("같은 반환 기록을 두 번 전달함")
		}
	}
}

func TestCodexBackgroundReturnMetadataDoesNotParseCommandOutput(t *testing.T) {
	b := newCodexBackgroundCommands()
	b.commands["job"] = &codexBackgroundCommand{ProcessID: "42"}
	b.observeReturnedSessions(`{"chunk_id":"sync","wall_time_seconds":0.25,"exit_code":0,"output":"Process running with session ID 42"}`)
	b.observeReturnedSessions("Chunk ID: abc\nProcess exited with code 0\nOutput:\nProcess running with session ID 42\n")
	if b.awaiting["job"] {
		t.Fatal("명령의 출력 내용을 실행 메타데이터로 오인함")
	}
	b.observeReturnedSessions("Chunk ID: abc\nWall time: 0.25 seconds\nProcess running with session ID 42\nOutput:\n")
	if !b.awaiting["job"] {
		t.Fatal("직접 도구 호출의 실행 중 세션을 누락함")
	}
}

func TestCodexBackgroundReturnHistoryErrors(t *testing.T) {
	for _, data := range []string{"invalid\n", `{"type":"response_item"}`} {
		b := newCodexBackgroundCommands()
		b.rolloutPath = filepath.Join(t.TempDir(), "rollout.jsonl")
		if err := os.WriteFile(b.rolloutPath, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if b.reconcileReturns() == nil {
			t.Fatal("손상되거나 미완성인 기록을 성공으로 처리함")
		}
	}
}

func TestCodexBackgroundReturnHistoryWaitsForFlush(t *testing.T) {
	b := newCodexBackgroundCommands()
	b.turnID = "current"
	b.rolloutPath = filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(b.rolloutPath, []byte(`{"type":"event_msg","payload":`), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		f, err := os.OpenFile(b.rolloutPath, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			done <- err
			return
		}
		_, err = f.WriteString("{\"type\":\"task_complete\",\"turn_id\":\"current\"}}\n")
		_ = f.Close()
		done <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := b.awaitReturns(ctx)
	if writeErr := <-done; writeErr != nil {
		t.Fatal(writeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := b.awaitReturns(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("취소 무시: %v", err)
	}
}

func TestCodexBackgroundPollCallCorrelation(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`text(await tools.write_stdin({session_id:42,chars:"",yield_time_ms:5000}));`, "42"},
		{`const result = await tools.write_stdin({"session_id":42,"chars":""}); text(JSON.stringify(result));`, "42"},
		{`text(await tools.write_stdin({session_id:session}));`, ""},
		{`text(await tools.write_stdin({session_id:42})); text(await tools.exec_command({cmd:"true"}));`, ""},
		{`text("tools.write_stdin({session_id:42})");`, ""},
	} {
		got := codexLiteralPollSession(map[string]any{"type": "custom_tool_call", "name": "exec", "input": tc.input})
		if got != tc.want {
			t.Fatalf("폴링 연결: got=%q want=%q input=%q", got, tc.want, tc.input)
		}
	}
	if got := codexLiteralPollSession(map[string]any{"type": "function_call", "name": "write_stdin", "arguments": `{"session_id":42}`}); got != "42" {
		t.Fatalf("직접 폴링: %q", got)
	}
}
