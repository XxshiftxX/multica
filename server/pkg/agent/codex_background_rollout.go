package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var codexRunningSessionLine = regexp.MustCompile(`(?m)^Process running with session ID ([0-9]+)\r?$`)
var errCodexReturnHistoryPending = errors.New("codex return history is not flushed through turn completion")

// 코드 실행 결과에서 세션 ID는 생략되므로 명시적인 단일 폴링 호출과 연결한다.
// 임의 JavaScript는 평가하지 않는다. 동적·복수 호출은 회수됐다고 추측하지 않는다.
var codexLiteralPoll = regexp.MustCompile(`^\s*(?:const\s+\w+\s*=\s*)?(?:text\(\s*)?await\s+tools\.write_stdin\(\s*\{([^{}]*)\}`)
var codexPollSessionID = regexp.MustCompile(`(?:^|,)\s*["']?session_id["']?\s*:\s*([0-9]+)\s*(?:,|$)`)

func codexLiteralPollSession(item map[string]any) string {
	if item["type"] == "function_call" && item["name"] == "write_stdin" {
		arguments, _ := item["arguments"].(string)
		var params struct {
			SessionID int64 `json:"session_id"`
		}
		if json.Unmarshal([]byte(arguments), &params) == nil && params.SessionID > 0 {
			return strconv.FormatInt(params.SessionID, 10)
		}
	}
	if item["type"] == "custom_tool_call" && item["name"] == "exec" {
		input, _ := item["input"].(string)
		if strings.Count(input, "tools.") != 1 {
			return ""
		}
		if match := codexLiteralPoll.FindStringSubmatch(input); len(match) == 2 {
			if session := codexPollSessionID.FindStringSubmatch(match[1]); len(session) == 2 {
				return session[1]
			}
		}
	}
	return ""
}

func codexReturnedExit(output any) bool {
	switch value := output.(type) {
	case []any:
		for _, block := range value {
			if item, ok := block.(map[string]any); ok && (item["type"] == "input_text" || item["type"] == "text") {
				if codexReturnedExit(item["text"]) {
					return true
				}
			}
		}
	case string:
		var result map[string]any
		if json.Unmarshal([]byte(value), &result) == nil && result["chunk_id"] != nil && result["wall_time_seconds"] != nil && result["session_id"] == nil {
			_, ok := result["exit_code"].(float64)
			return ok
		}
		if strings.HasPrefix(value, "Chunk ID:") {
			metadata, _, _ := strings.Cut(value, "\nOutput:")
			return strings.Contains(metadata, "\nProcess exited with code ")
		}
	}
	return false
}

// 완료 이벤트와 파일 flush 사이의 짧은 지연만 기다리며 모델 호출은 하지 않는다.
func (b *codexBackgroundCommands) awaitReturns(ctx context.Context) error {
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := b.reconcileReturns()
		if !errors.Is(err, errCodexReturnHistoryPending) {
			return err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-deadline.C:
			timer.Stop()
			return err
		case <-timer.C:
		}
	}
}

func (c *codexClient) captureBackgroundRolloutPath(result json.RawMessage) {
	if c.background == nil {
		return
	}
	var response struct {
		Thread struct {
			Path string `json:"path"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &response) == nil {
		c.background.rolloutPath = response.Thread.Path
	}
}

// App Server의 공개 완료 이벤트는 모델에게 반환한 도구 결과와 다르다.
// 새 스레드와 재개된 스레드 모두 같은 로컬 rollout에서 실제 반환을 확인한다.
// 현재 턴의 반환 메타데이터만 해석하며 명령 출력 내부는 재귀적으로 읽지 않는다.
func (b *codexBackgroundCommands) reconcileReturns() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.turnOpen {
		return nil
	}
	if b.rolloutPath == "" {
		return fmt.Errorf("codex background continuation requires a local thread rollout path")
	}
	f, err := os.Open(b.rolloutPath)
	if err != nil {
		return fmt.Errorf("open codex background return history: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < b.rolloutOffset {
		return fmt.Errorf("codex return history shrank during execution")
	}
	if _, err := f.Seek(b.rolloutOffset, io.SeekStart); err != nil {
		return err
	}
	r := bufio.NewReader(f)
	for {
		line, err := readCodexReturnLine(r)
		if err == io.EOF {
			if len(line) != 0 {
				return errCodexReturnHistoryPending
			}
			break
		}
		if err != nil {
			return fmt.Errorf("read codex background return history: %w", err)
		}
		var record struct {
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			return fmt.Errorf("invalid codex background return history")
		}
		b.rolloutOffset += int64(len(line))
		p := record.Payload
		if record.Type == "event_msg" && p["type"] == "task_complete" {
			b.rolloutCompletedTurn, _ = p["turn_id"].(string)
		}
		if record.Type == "event_msg" && p["type"] == "task_started" {
			b.rolloutTurn, _ = p["turn_id"].(string)
		}
		turn := b.rolloutTurn
		if metadata, ok := p["internal_chat_message_metadata_passthrough"].(map[string]any); ok {
			if id, ok := metadata["turn_id"].(string); ok {
				turn = id
			}
		}
		if id, ok := p["turn_id"].(string); ok {
			turn = id
		}
		if turn != b.turnID {
			continue
		}
		if record.Type == "response_item" && (p["type"] == "function_call" || p["type"] == "custom_tool_call") {
			if processID := codexLiteralPollSession(p); processID != "" {
				if b.rolloutPollCalls == nil {
					b.rolloutPollCalls = make(map[string]string)
				}
				callID, _ := p["call_id"].(string)
				b.rolloutPollCalls[callID] = processID
			}
		}

		if record.Type == "response_item" && (p["type"] == "custom_tool_call_output" || p["type"] == "function_call_output") {
			b.observeReturnedSessions(p["output"])
			if callID, ok := p["call_id"].(string); ok {
				if processID := b.rolloutPollCalls[callID]; processID != "" && codexReturnedExit(p["output"]) {
					for id, command := range b.commands {
						if command.ProcessID == processID {
							delete(b.awaiting, id)
							b.log("consumed", command, "terminal_poll_returned")
						}
					}
				}
				delete(b.rolloutPollCalls, callID)
			}
		}
	}
	if b.rolloutCompletedTurn != b.turnID {
		return errCodexReturnHistoryPending
	}
	return nil
}

func readCodexReturnLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		if len(line)+len(part) > 16*1024*1024 {
			return nil, fmt.Errorf("codex return history record exceeds 16 MiB")
		}
		line = append(line, part...)
		if err != bufio.ErrBufferFull {
			return line, err
		}
	}
}

func (b *codexBackgroundCommands) observeReturnedSessions(output any) {
	switch value := output.(type) {
	case []any:
		for _, block := range value {
			if item, ok := block.(map[string]any); ok && (item["type"] == "input_text" || item["type"] == "text") {
				b.observeReturnedSessions(item["text"])
			}
		}
	case string:
		var result map[string]any
		processID := ""
		if json.Unmarshal([]byte(value), &result) == nil && result["chunk_id"] != nil && result["wall_time_seconds"] != nil {
			if id, ok := result["session_id"].(float64); ok {
				processID = strconv.FormatInt(int64(id), 10)
			}
		} else if strings.HasPrefix(value, "Chunk ID:") {
			metadata, _, _ := strings.Cut(value, "\nOutput:")
			if match := codexRunningSessionLine.FindStringSubmatch(metadata); len(match) == 2 {
				processID = match[1]
			}
		}
		if processID == "" {
			return
		}
		for id, command := range b.commands {
			if command.ProcessID == processID {
				b.awaiting[id] = true
				b.log("awaiting", command, "returned_running_session")
			}
		}
	}
}
