//go:build !windows

package service

import (
	"debug/macho"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

func availableDiskBytes(path string) (uint64, error) {
	var disk syscall.Statfs_t
	if err := syscall.Statfs(path, &disk); err != nil {
		return 0, err
	}
	return uint64(disk.Bavail) * uint64(disk.Bsize), nil
}

func validateFeishuLocalPath(string) error { return nil }

func feishuPathIsReparsePoint(string) bool { return false }

func validateFeishuPermissions(_ string, info os.FileInfo, private bool) error {
	if (private && info.Mode().Perm()&0077 != 0) || (!private && info.Mode().Perm()&0022 != 0) {
		return errors.New("CLI filesystem permissions are too broad")
	}
	return nil
}

func validateFeishuExecutable(path string) error {
	if runtime.GOOS != "darwin" {
		return &FeishuArchiveFailure{Code: "CLI_ARCH_UNSUPPORTED"}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0100 == 0 {
		return &FeishuArchiveFailure{Code: "CLI_PATH_INVALID"}
	}
	binary, err := macho.Open(path)
	if err != nil {
		return &FeishuArchiveFailure{Code: "CLI_ARCH_MISMATCH"}
	}
	defer binary.Close()
	want := macho.CpuArm64
	if runtime.GOARCH == "amd64" {
		want = macho.CpuAmd64
	} else if runtime.GOARCH != "arm64" {
		return &FeishuArchiveFailure{Code: "CLI_ARCH_UNSUPPORTED"}
	}
	if binary.Cpu != want {
		return &FeishuArchiveFailure{Code: "CLI_ARCH_MISMATCH"}
	}
	return nil
}

func feishuPlatformDirectories() []string { return nil }

func feishuPlatformEnvironment(home string) []string {
	return []string{"PATH=/usr/bin:/bin", "TMPDIR=" + filepath.Join(home, "tmp")}
}

func createFeishuPrivateFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
}

func syncFeishuCreatedFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("CLI durable file is not regular")
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
