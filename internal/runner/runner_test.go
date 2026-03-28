// Package runner_test exercises RunContainerAgent timeout and output parsing.
package runner_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jonsampson/gopherclaw/internal/runner"
	"github.com/jonsampson/gopherclaw/internal/types"
)

// scriptWithOutput builds a shell script that emits the output markers around
// the given body text, then optionally appends extra commands.
func scriptWithOutput(body, suffix string) string {
	return "echo " + runner.OutputStart + "\n" +
		"echo '" + body + "'\n" +
		"echo " + runner.OutputEnd + "\n" +
		suffix
}

func run(t *testing.T, script string, timeout time.Duration) (types.ContainerOutput, string) {
	t.Helper()
	var captured string
	result := runner.RunContainerAgent(
		types.ContainerInput{
			Prompt:      "test",
			SessionID:   "sess-1",
			GroupFolder: t.TempDir(),
			ChatJID:     "test@g.us",
			Script:      script,
		},
		func(s string) { captured = s },
		timeout,
	)
	return result, captured
}

// ---- Timeout behaviour ----

func TestTimeoutAfterOutput_ReturnsSuccess(t *testing.T) {
	// Script emits output then sleeps; times out but output was already captured.
	result, captured := run(t, scriptWithOutput("hello from agent", "sleep 30\n"), 500*time.Millisecond)

	if result.Status != types.ContainerStatusSuccess {
		t.Errorf("expected success, got %v (err: %s)", result.Status, result.Error)
	}
	if result.NewSessionID != "sess-1" {
		t.Errorf("expected sess-1, got %q", result.NewSessionID)
	}
	if captured == "" {
		t.Error("expected onOutput to be called")
	}
	if result.Result == nil {
		t.Error("expected Result to be set on success")
	}
}

func TestTimeoutWithNoOutput_ReturnsError(t *testing.T) {
	var called bool
	result := runner.RunContainerAgent(
		types.ContainerInput{Script: "sleep 30\n", GroupFolder: t.TempDir()},
		func(string) { called = true },
		300*time.Millisecond,
	)

	if result.Status != types.ContainerStatusError {
		t.Errorf("expected error when no output + timeout, got %v", result.Status)
	}
	if !strings.Contains(result.Error, "timed out") {
		t.Errorf("expected 'timed out' in error, got %q", result.Error)
	}
	if called {
		t.Error("onOutput should not be called when no output was produced")
	}
	if result.Result != nil {
		t.Error("expected Result to be nil on error")
	}
}

// ---- Normal exit ----

func TestNormalExitAfterOutput_ReturnsSuccess(t *testing.T) {
	result, captured := run(t, scriptWithOutput("done", ""), 5*time.Second)

	if result.Status != types.ContainerStatusSuccess {
		t.Errorf("expected success on normal exit, got %v (err: %s)", result.Status, result.Error)
	}
	if result.NewSessionID != "sess-1" {
		t.Errorf("expected sess-1, got %q", result.NewSessionID)
	}
	if captured == "" {
		t.Error("expected onOutput to be called")
	}
	if result.Result == nil || !strings.Contains(*result.Result, "done") {
		t.Errorf("expected Result to contain 'done', got %v", result.Result)
	}
}

func TestNormalExitNoOutput_ReturnsError(t *testing.T) {
	// Script exits cleanly but never writes output markers.
	result, _ := run(t, "echo 'no markers here'\n", 2*time.Second)

	if result.Status != types.ContainerStatusError {
		t.Errorf("expected error when no markers, got %v", result.Status)
	}
}

// ---- Edge cases ----

func TestOnOutputNil_DoesNotPanic(t *testing.T) {
	// Passing nil as onOutput must not panic.
	result := runner.RunContainerAgent(
		types.ContainerInput{Script: scriptWithOutput("hi", ""), GroupFolder: t.TempDir()},
		nil,
		2*time.Second,
	)
	if result.Status != types.ContainerStatusSuccess {
		t.Errorf("expected success with nil onOutput, got %v", result.Status)
	}
}

func TestMultilineOutput_CapturedCorrectly(t *testing.T) {
	// Use printf with a trailing newline so the last line is terminated before
	// the OutputEnd marker arrives on its own line.
	script := "echo " + runner.OutputStart + "\n" +
		"printf 'line1\\nline2\\nline3\\n'\n" +
		"echo " + runner.OutputEnd + "\n"

	result, captured := run(t, script, 2*time.Second)

	if result.Status != types.ContainerStatusSuccess {
		t.Fatalf("expected success, got %v", result.Status)
	}
	for _, want := range []string{"line1", "line2", "line3"} {
		if !strings.Contains(captured, want) {
			t.Errorf("expected %q in output, got: %q", want, captured)
		}
	}
}

func TestOnlyLastOutputBlock_Used(t *testing.T) {
	// When output markers appear more than once, the last complete block wins.
	script := "echo " + runner.OutputStart + "\n" +
		"echo 'first'\n" +
		"echo " + runner.OutputEnd + "\n" +
		"echo " + runner.OutputStart + "\n" +
		"echo 'second'\n" +
		"echo " + runner.OutputEnd + "\n"

	result, captured := run(t, script, 2*time.Second)

	if result.Status != types.ContainerStatusSuccess {
		t.Fatalf("expected success, got %v", result.Status)
	}
	// The scanner resets buf on each OutputStart so the last block is "second".
	if !strings.Contains(captured, "second") {
		t.Errorf("expected 'second' from last output block, got %q", captured)
	}
}
