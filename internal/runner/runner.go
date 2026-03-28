// Package runner executes agent scripts in a subprocess, parses structured
// output delimited by well-known markers, and enforces an activity-based
// timeout — mirroring nanoclaw's container-runner semantics.
//
// Security note: RunContainerAgent passes input.Script directly to /bin/sh.
// Callers are responsible for ensuring the script is not derived from
// untrusted user input.
package runner

import (
	"bufio"
	"os/exec"
	"strings"
	"time"

	"github.com/jonsampson/gopherclaw/internal/types"
)

// OutputStart and OutputEnd delimit the section of stdout that is captured as
// the agent's result. Everything outside the markers is ignored.
const (
	OutputStart = "---GOPHERCLAW_OUTPUT_START---"
	OutputEnd   = "---GOPHERCLAW_OUTPUT_END---"
)

// OnOutput is called with the captured result text once the output markers
// have been fully received.
type OnOutput func(output string)

// RunContainerAgent executes the shell script in input.Script via /bin/sh.
// Stdout is read line-by-line; lines between OutputStart and OutputEnd are
// collected and delivered to onOutput (which may be nil).
//
// If the process exits before the timeout the result is determined by whether
// output markers were seen. If the timeout fires first the process is killed;
// any output already collected is still treated as a success.
//
// The returned ContainerOutput.Result is non-nil on success and points to the
// captured text.
func RunContainerAgent(input types.ContainerInput, onOutput OnOutput, timeout time.Duration) types.ContainerOutput {
	cmd := exec.Command("/bin/sh", "-c", input.Script)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errResult(input.SessionID, "failed to create stdout pipe: "+err.Error())
	}

	if err := cmd.Start(); err != nil {
		return errResult(input.SessionID, "failed to start script: "+err.Error())
	}

	type scanResult struct {
		output string
		found  bool
	}
	resultCh := make(chan scanResult, 1)

	// Read stdout in a goroutine, collecting lines between the output markers.
	go func() {
		scanner := bufio.NewScanner(stdout)
		var (
			inBlock bool
			buf     strings.Builder
			found   bool
		)
		for scanner.Scan() {
			line := scanner.Text()
			switch line {
			case OutputStart:
				inBlock = true
				buf.Reset()
			case OutputEnd:
				found = true
				inBlock = false
			default:
				if inBlock {
					if buf.Len() > 0 {
						buf.WriteByte('\n')
					}
					buf.WriteString(line)
				}
			}
		}
		resultCh <- scanResult{output: buf.String(), found: found}
	}()

	// Wait for the process to finish or for the timeout to fire.
	doneCh := make(chan error, 1)
	go func() { doneCh <- cmd.Wait() }()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-doneCh:
		res := <-resultCh
		if res.found {
			return successResult(input.SessionID, res.output, onOutput)
		}
		return errResult(input.SessionID, "process exited with no output")

	case <-timer.C:
		// Kill the subprocess so the scanner goroutine can drain and return.
		_ = cmd.Process.Kill()
		res := <-resultCh
		if res.found {
			return successResult(input.SessionID, res.output, onOutput)
		}
		return errResult(input.SessionID, "container timed out with no output")
	}
}

func successResult(sessionID, output string, onOutput OnOutput) types.ContainerOutput {
	if onOutput != nil {
		onOutput(output)
	}
	return types.ContainerOutput{
		Status:       types.ContainerStatusSuccess,
		Result:       &output,
		NewSessionID: sessionID,
	}
}

func errResult(sessionID, msg string) types.ContainerOutput {
	return types.ContainerOutput{
		Status:       types.ContainerStatusError,
		NewSessionID: sessionID,
		Error:        msg,
	}
}
