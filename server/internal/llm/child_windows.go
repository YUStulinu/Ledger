package llm

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

func tieToParent(cmd *exec.Cmd) {}

// afterStart puts llama-server in a job object that the system closes, killing it, when Ledger
// exits, even when Ledger is killed and cannot stop it.
func afterStart(cmd *exec.Cmd) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
	}
	// The job handle stays open for the life of Ledger; the system closes it when Ledger exits.
}
