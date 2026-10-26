//go:build !windows && !linux

package llm

import "os/exec"

// Elsewhere llama-server outlives a Ledger that is killed (a normal exit stops it).
func tieToParent(cmd *exec.Cmd) {}

func afterStart(cmd *exec.Cmd) {}
