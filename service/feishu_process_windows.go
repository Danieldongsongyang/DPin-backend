package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Start suspended so no CLI code can create a child before job assignment.
// Closing the non-inheritable job handle also removes descendants after a
// successful CLI exit, not only after a timeout.
func runFeishuCLI(process *exec.Cmd) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("CLI job creation failed: %w", err)
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return fmt.Errorf("CLI job limits failed: %w", err)
	}
	if process.SysProcAttr == nil {
		process.SysProcAttr = &syscall.SysProcAttr{}
	}
	process.SysProcAttr.HideWindow = true
	process.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW
	var assignment sync.Mutex
	process.Cancel = func() error {
		assignment.Lock()
		defer assignment.Unlock()
		if process.Process == nil {
			return os.ErrProcessDone
		}
		return windows.TerminateJobObject(job, 1)
	}
	// CommandContext's cancellation goroutine must not terminate an empty job
	// between Start and AssignProcessToJobObject and then leave the CLI alive.
	assignment.Lock()
	if err := process.Start(); err != nil {
		assignment.Unlock()
		return err
	}
	handle, setupErr := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(process.Process.Pid))
	if setupErr == nil {
		setupErr = windows.AssignProcessToJobObject(job, handle)
		windows.CloseHandle(handle)
	}
	if setupErr == nil {
		setupErr = resumeFeishuCLIThread(uint32(process.Process.Pid))
	}
	if setupErr != nil {
		// Fail closed: the unassigned process is still suspended; assigned
		// descendants, if any, belong to the job and cannot survive this call.
		_ = process.Process.Kill()
		_ = windows.TerminateJobObject(job, 1)
	}
	assignment.Unlock()
	if setupErr != nil {
		_ = process.Wait()
		return fmt.Errorf("CLI process supervision failed: %w", setupErr)
	}
	return process.Wait()
}

// os/exec closes CreateProcess's primary thread handle. Before a suspended
// executable has run, its unique thread can be recovered through Toolhelp.
func resumeFeishuCLIThread(processID uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	var threadID uint32
	for {
		if entry.OwnerProcessID == processID {
			if threadID != 0 {
				return errors.New("suspended CLI has multiple threads")
			}
			threadID = entry.ThreadID
		}
		err := windows.Thread32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if err != nil {
			return err
		}
	}
	if threadID == 0 {
		return errors.New("suspended CLI thread is missing")
	}
	thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, threadID)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(thread)
	previous, err := windows.ResumeThread(thread)
	if err != nil {
		return err
	}
	if previous != 1 {
		return errors.New("unexpected CLI thread suspension count")
	}
	return nil
}
