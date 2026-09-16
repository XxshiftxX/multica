//go:build agentintegration

package agent

import (
	"os"
	"testing"
)

func requireRealAgentTest(t *testing.T) {
	t.Helper()
	if os.Getenv("MULTICA_RUN_REAL_AGENT_TESTS") != "1" {
		t.Skip("set MULTICA_RUN_REAL_AGENT_TESTS=1 to allow real agent CLI and account access")
	}
	t.Log("REAL AGENT INTEGRATION TEST: this test may access an authenticated account and consume quota")
}
