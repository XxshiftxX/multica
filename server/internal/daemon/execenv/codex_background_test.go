package execenv

import (
	"strings"
	"testing"
)

func TestCodexBackgroundContinuationBrief(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		for _, enabled := range []bool{false, true} {
			brief := buildMetaSkillContent(provider, TaskContextForEnv{IssueID: "issue", CodexBackgroundContinuation: enabled})
			wantContinuation := provider == "codex" && enabled
			if got := strings.Contains(brief, "completion results are delivered to the same thread in a continuation turn"); got != wantContinuation {
				t.Fatalf("provider=%s enabled=%v continuation=%v", provider, enabled, got)
			}
			if got := strings.Contains(brief, "There is no background-completion wakeup"); got == wantContinuation {
				t.Fatalf("provider=%s enabled=%v has contradictory lifecycle guidance", provider, enabled)
			}
			if !strings.Contains(brief, "persistent service handoff") || !strings.Contains(brief, "never kill it if it is the reported daemon PID") {
				t.Fatal("service/process ownership instructions disappeared")
			}
		}
	}
}
