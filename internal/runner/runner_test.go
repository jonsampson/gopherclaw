package runner_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jonsampson/gopherclaw/internal/runner"
	"github.com/jonsampson/gopherclaw/internal/types"
)

const outputStart = "---GOPHERCLAW_OUTPUT_START---"
const outputEnd = "---GOPHERCLAW_OUTPUT_END---"

// TestTimeoutAfterOutput: container times out after output has been received.
// Expected: status=success, session preserved, onOutput called.
func TestTimeoutAfterOutput(t *testing.T) {
	var capturedOutput string
	onOutput := func(s string) { capturedOutput = s }

	// Script: emit output markers then block (sleep)
	script := "echo " + outputStart + "\necho 'hello from agent'\necho " + outputEnd + "\nsleep 30\n"

	input := types.ContainerInput{
		Prompt:      "test",
		SessionID:   "session-123",
		GroupFolder: t.TempDir(),
		ChatJID:     "test@g.us",
		Script:      script,
	}

	result := runner.RunContainerAgent(input, onOutput, 500*time.Millisecond)

	if result.Status != types.ContainerStatusSuccess {
		t.Errorf("expected success after timeout with output, got %v (err: %s)", result.Status, result.Error)
	}
	if result.NewSessionID != "session-123" {
		t.Errorf("expected session-123, got %q", result.NewSessionID)
	}
	if capturedOutput == "" {
		t.Error("expected onOutput to be called")
	}
}

// TestTimeoutWithNoOutput: container times out without any output.
// Expected: status=error, error contains "timed out", onOutput not called.
func TestTimeoutWithNoOutput(t *testing.T) {
	var onOutputCalled bool
	onOutput := func(s string) { onOutputCalled = true }

	// Script: block without producing output
	script := "sleep 30\n"

	input := types.ContainerInput{
		Prompt:      "test",
		SessionID:   "session-456",
		GroupFolder: t.TempDir(),
		ChatJID:     "test@g.us",
		Script:      script,
	}

	result := runner.RunContainerAgent(input, onOutput, 300*time.Millisecond)

	if result.Status != types.ContainerStatusError {
		t.Errorf("expected error when no output + timeout, got %v", result.Status)
	}
	if !strings.Contains(result.Error, "timed out") {
		t.Errorf("expected error to contain 'timed out', got %q", result.Error)
	}
	if onOutputCalled {
		t.Error("onOutput should not have been called when no output was produced")
	}
}

// TestNormalExitAfterOutput: container exits normally after producing output.
// Expected: status=success, session preserved, no timeout needed.
func TestNormalExitAfterOutput(t *testing.T) {
	var capturedOutput string
	onOutput := func(s string) { capturedOutput = s }

	script := "echo " + outputStart + "\necho 'done'\necho " + outputEnd + "\n"

	input := types.ContainerInput{
		Prompt:      "test",
		SessionID:   "session-456",
		GroupFolder: t.TempDir(),
		ChatJID:     "test@g.us",
		Script:      script,
	}

	result := runner.RunContainerAgent(input, onOutput, 5*time.Second)

	if result.Status != types.ContainerStatusSuccess {
		t.Errorf("expected success on normal exit, got %v (err: %s)", result.Status, result.Error)
	}
	if result.NewSessionID != "session-456" {
		t.Errorf("expected session-456, got %q", result.NewSessionID)
	}
	if capturedOutput == "" {
		t.Error("expected onOutput to be called")
	}
}
