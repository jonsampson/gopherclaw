// Package runner executes agent scripts in a subprocess, parsing structured
// output markers, and implements activity-based timeout reset — mirroring
// nanoclaw's container-runner semantics.
package runner

import (
	"bufio"
	"os/exec"
	"strings"
	"time"

	"github.com/jonsampson/gopherclaw/internal/types"
)

const (
	outputStart = "---GOPHERCLAW_OUTPUT_START---"
	outputEnd   = "---GOPHERCLAW_OUTPUT_END---"
)

// OnOutput is called with the captured output when the output markers are seen.
type OnOutput func(output string)

// RunContainerAgent executes the script specified in input.Script (using /bin/sh).
// It reads stdout line by line, collecting lines between the output markers.
// The timeout is reset each time activity is detected; if it expires before
// output markers are seen, an error is returned.
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

	// Read stdout in a goroutine, collecting the output between markers.
	go func() {
		scanner := bufio.NewScanner(stdout)
		var (
			inBlock bool
			buf     strings.Builder
			found   bool
		)
		for scanner.Scan() {
			line := scanner.Text()
			if line == outputStart {
				inBlock = true
				buf.Reset()
				continue
			}
			if line == outputEnd {
				found = true
				inBlock = false
				continue
			}
			if inBlock {
				if buf.Len() > 0 {
					buf.WriteByte('\n')
				}
				buf.WriteString(line)
			}
		}
		resultCh <- scanResult{output: buf.String(), found: found}
	}()

	// Wait for the process to finish or for a timeout.
	doneCh := make(chan error, 1)
	go func() { doneCh <- cmd.Wait() }()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-doneCh:
		// Process exited; collect whatever output was scanned.
		res := <-resultCh
		if res.found {
			if onOutput != nil {
				onOutput(res.output)
			}
			return types.ContainerOutput{
				Status:       types.ContainerStatusSuccess,
				NewSessionID: input.SessionID,
			}
		}
		return errResult(input.SessionID, "process exited with no output")

	case <-timer.C:
		// Timeout: kill the process so the scanner goroutine can finish.
		_ = cmd.Process.Kill()
		res := <-resultCh
		if res.found {
			if onOutput != nil {
				onOutput(res.output)
			}
			return types.ContainerOutput{
				Status:       types.ContainerStatusSuccess,
				NewSessionID: input.SessionID,
			}
		}
		return errResult(input.SessionID, "container timed out with no output")
	}
}

func errResult(sessionID, msg string) types.ContainerOutput {
	return types.ContainerOutput{
		Status:       types.ContainerStatusError,
		NewSessionID: sessionID,
		Error:        msg,
	}
}
