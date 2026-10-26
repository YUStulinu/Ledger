package llm

import (
	"os/exec"
	"syscall"
)

// tieToParent has the kernel kill llama-server when Ledger exits, even when Ledger is killed and
// cannot stop it.
func tieToParent(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

func afterStart(cmd *exec.Cmd) {}
