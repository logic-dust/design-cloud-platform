//go:build !windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"design-cloud-engine/internal/execution"
)

// stdinReader is a global buffered reader wrapping os.Stdin.
// readRequest consumes the first line (JSON header terminated by \n).
// Stream handlers then use stdinReader to read the remaining bytes (file body).
var stdinReader = bufio.NewReader(os.Stdin)

// readRequest reads one newline-terminated JSON line from stdin and parses it
// as execution.ExecuteInput.
//
// Protocol (unified for all actions including stream):
//
//	[compact JSON of execution.ExecuteInput]\n
//	[optional raw file bytes – FILE_UPLOAD only]
//
// json.Marshal always produces compact JSON without embedded newlines, so the
// first '\n' unambiguously marks the end of the header. After readRequest
// returns, stdinReader is positioned at the first byte of the file body (if
// any).
//
// Maximum JSON header line size is 1 MB to prevent DoS.
func readRequest(ctx interface{ Done() <-chan struct{} }) (*execution.ExecuteInput, error) {
	type result struct {
		input *execution.ExecuteInput
		err   error
	}

	ch := make(chan result, 1)
	go func() {
		defer close(ch)

		const maxHeaderSize = 1024 * 1024 // 1 MB

		// ReadBytes('\n') reads up to and including the newline.
		line, err := stdinReader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			ch <- result{err: fmt.Errorf("failed to read request header: %w", err)}
			return
		}

		if len(line) > maxHeaderSize {
			ch <- result{err: fmt.Errorf("header too large (max %d bytes)", maxHeaderSize)}
			return
		}

		// Trim trailing newline and optional CR.
		for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
			line = line[:len(line)-1]
		}

		if len(line) == 0 {
			ch <- result{err: fmt.Errorf("empty request header")}
			return
		}

		var input execution.ExecuteInput
		if err := json.Unmarshal(line, &input); err != nil {
			ch <- result{err: fmt.Errorf("failed to parse JSON: %w", err)}
			return
		}

		// JSON 解码会把 payload 对象解成 map[string]any，与进程内构造时的
		// json.RawMessage 形态不一致；在解码边界收敛回 json.RawMessage，
		// 否则 GetPayload 会拒绝所有携带 payload 的动作。
		if input.Request != nil {
			if err := input.Request.NormalizePayload(); err != nil {
				ch <- result{err: err}
				return
			}
		}

		ch <- result{input: &input}
	}()

	select {
	case <-ctx.Done():
		return nil, context.DeadlineExceeded
	case res := <-ch:
		return res.input, res.err
	}
}

func respondJSON(response execution.ExecuteOutput) {
	data, err := json.Marshal(response)
	if err != nil {
		// Fallback for cases where the main response object cannot be marshaled.
		// Errors from fmt.Fprintln are intentionally discarded: this is already
		// the fallback-of-last-resort and we have nowhere else to report errors.
		_, _ = fmt.Fprintln(os.Stderr, "launcher: internal protocol error: failed to marshal response:", err)
		_, _ = fmt.Fprintln(os.Stdout, `{"success":false,"message":"Error","error":"launcher: failed to marshal response"}`)
		return
	}
	_, _ = fmt.Fprintln(os.Stdout, string(data))
}

func respondError(message string) {
	errMsg := message
	if Version != "" {
		errMsg = message + fmt.Sprintf(" (launcher v%s)", Version)
	}
	response := execution.ExecuteOutput{
		Success: false,
		Error:   errMsg,
	}
	respondJSON(response)
}

func respondSuccessWithWritten(message string, written int64) {
	respondJSON(execution.ExecuteOutput{
		Success: true,
		Output:  message,
		Written: written,
	})
}
