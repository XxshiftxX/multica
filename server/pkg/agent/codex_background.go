package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// codexBackgroundCommands tracks native command items, not arbitrary detached
// OS processes. Only commands observed in this execution can keep it alive.
type codexBackgroundCommands struct {
	mu                   sync.Mutex
	commands             map[string]*codexBackgroundCommand
	awaiting             map[string]bool
	changed              chan struct{}
	turnID               string
	turnOpen             bool
	logger               *slog.Logger
	rolloutPath          string
	rolloutOffset        int64
	rolloutTurn          string
	rolloutCompletedTurn string
	rolloutPollCalls     map[string]string
}

type codexBackgroundCommand struct {
	ItemID    string `json:"itemId"`
	ProcessID string `json:"processId"`
	Command   string `json:"command"`
	Status    string `json:"status"`
	ExitCode  *int   `json:"exitCode"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated,omitempty"`
	complete  bool
}

func newCodexBackgroundCommands() *codexBackgroundCommands {
	return &codexBackgroundCommands{commands: make(map[string]*codexBackgroundCommand), awaiting: make(map[string]bool), changed: make(chan struct{}, 1)}
}

// observe runs on the protocol reader. Late completions from an earlier turn
// are accepted only for command IDs already registered in this execution.
func (b *codexBackgroundCommands) observe(method string, params map[string]any, accepted bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	changed := false
	lateCompletion := false
	switch method {
	case "turn/started":
		if !accepted {
			break
		}
		id := extractNestedString(params, "turn", "id")
		if id == "" || id == b.turnID {
			break
		}
		b.turnID, b.turnOpen = id, true
		// A sibling command can complete while a successor's turn/start is
		// in flight. Retain every undelivered result across that boundary.
		changed = true
	case "turn/completed":
		if !accepted {
			break
		}
		b.turnOpen = false
		for id, command := range b.commands {
			if !command.complete {
				b.awaiting[id] = true
			}
		}
		changed = true
	case "item/started", "item/completed":
		item, _ := params["item"].(map[string]any)
		id, _ := item["id"].(string)
		if accepted && method == "item/started" && item["type"] == "agentMessage" && item["phase"] == "final_answer" {
			// 최종 답변 뒤 완료되는 명령도 다음 턴에서 결과를 전달한다.
			for id, command := range b.commands {
				if !command.complete && !b.awaiting[id] {
					b.awaiting[id] = true
					b.log("awaiting", command, "final_answer_while_running")
				}
			}
		}
		if item["type"] != "commandExecution" || id == "" {
			break
		}
		if method == "item/started" {
			processID, _ := item["processId"].(string)
			if !accepted || processID == "" || b.commands[id] != nil {
				break
			}
			command, _ := item["command"].(string)
			b.commands[id] = &codexBackgroundCommand{ItemID: id, ProcessID: processID, Command: command, Status: "inProgress"}
			b.log("registered", b.commands[id], "native_command")
			changed = true
		} else if command := b.commands[id]; command != nil && !command.complete {
			status, _ := item["status"].(string)
			if status != "completed" && status != "failed" && status != "declined" {
				break
			}
			if !accepted {
				b.awaiting[id] = true
			}
			command.Status, command.complete = status, true
			if exitCode, ok := item["exitCode"].(float64); ok {
				value := int(exitCode)
				command.ExitCode = &value
			}
			output, _ := item["aggregatedOutput"].(string)
			command.Truncated = len(output) > 32*1024
			command.Output = truncateUTF8(output, 32*1024)
			lateCompletion, changed = !accepted, true
			b.log("completed", command, "terminal_event")
			// 완료와 도구 반환 기록은 순서가 다를 수 있어 턴 경계까지 보존한다.
		}
	}
	if changed {
		select {
		case b.changed <- struct{}{}:
		default:
		}
	}
	return lateCompletion
}

// 판정 로그에는 명령문·출력·사용자 입력을 포함하지 않는다.
func (b *codexBackgroundCommands) log(phase string, command *codexBackgroundCommand, reason string) {
	if b.logger != nil {
		b.logger.Debug("codex background command", "phase", phase, "reason", reason, "item_id", command.ItemID, "process_id", command.ProcessID, "turn_id", b.turnID)
	}
}

// wait returns when commands left behind by the completed turn have terminal
// events, or a native successor has already started. Process disappearance
// alone is never evidence of success. Cancellation uses the normal process cleanup.
func (b *codexBackgroundCommands) wait(ctx context.Context, processDone <-chan struct{}) (commands []codexBackgroundCommand, nativeSuccessor bool, err error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		b.mu.Lock()
		if b.turnOpen {
			b.mu.Unlock()
			return nil, true, nil
		}
		for id, command := range b.commands {
			if command.complete && !b.awaiting[id] {
				b.log("discarded", command, "no_pending_return")
				delete(b.commands, id)
			}
		}
		pending := false
		commands = nil
		for id := range b.awaiting {
			command := b.commands[id]
			if !command.complete {
				pending = true
				continue
			}
			commands = append(commands, *command)
		}
		if len(commands) > 0 || !pending {
			for _, command := range commands {
				b.log("collected", &command, "continuation_batch")
				delete(b.commands, command.ItemID)
				delete(b.awaiting, command.ItemID)
			}
			b.mu.Unlock()
			sort.Slice(commands, func(i, j int) bool { return commands[i].ItemID < commands[j].ItemID })
			return commands, false, nil
		}
		b.mu.Unlock()
		select {
		case <-b.changed:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-processDone:
			return nil, false, fmt.Errorf("codex exited before background command results were collected")
		}
	}
}

func codexBackgroundTurnParams(threadID string, commands []codexBackgroundCommand) map[string]any {
	remaining := 64 * 1024
	for i := range commands {
		if len(commands[i].Output) > remaining {
			commands[i].Output = truncateUTF8(commands[i].Output, remaining)
			commands[i].Truncated = true
		}
		remaining -= len(commands[i].Output)
	}
	data, _ := json.Marshal(commands)
	return map[string]any{"threadId": threadID, "input": []any{}, "toolOutput": map[string]any{
		"name": "multica_background_commands", "output": string(data),
	}}
}
