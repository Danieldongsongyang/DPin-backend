package service

import (
	"debug/pe"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	_ "time/tzdata" // Windows services need Shanghai timestamps without a Go installation.
	"unsafe"

	"golang.org/x/sys/windows"
)

func availableDiskBytes(path string) (uint64, error) {
	directory, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(directory, &available, nil, nil); err != nil {
		return 0, err
	}
	return available, nil
}

func validateFeishuLocalPath(path string) error {
	if strings.HasPrefix(path, `\\`) || strings.Contains(strings.TrimPrefix(path, filepath.VolumeName(path)), ":") {
		return errors.New("CLI requires a local filesystem path")
	}
	return nil
}

func feishuPathIsReparsePoint(path string) bool {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attributes, err := windows.GetFileAttributes(pointer)
	return err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func validateFeishuPermissions(path string, info os.FileInfo, private bool) error {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return errors.New("CLI filesystem ACL cannot be read")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return errors.New("CLI service identity cannot be checked")
	}
	trusted := func(sid *windows.SID) bool {
		return sid != nil && (sid.Equals(user.User.Sid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) ||
			sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinLocalServiceSid))
	}
	owner, _, err := descriptor.Owner()
	if err != nil || !trusted(owner) {
		return errors.New("CLI filesystem owner is not trusted")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.New("CLI filesystem requires an explicit access policy")
	}
	const fileDeleteChild = 0x0040
	const mutable = windows.GENERIC_ALL | windows.GENERIC_WRITE | windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA |
		windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | fileDeleteChild | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var entry *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, i, &entry) != nil {
			return errors.New("CLI filesystem access entry cannot be checked")
		}
		if !info.IsDir() && entry.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		switch entry.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&entry.SidStart))
			if !trusted(sid) && ((private && entry.Mask != 0) || entry.Mask&mutable != 0) {
				return errors.New("CLI filesystem access is too broad")
			}
		default:
			return errors.New("CLI filesystem access policy is not supported")
		}
	}
	return nil
}

func validateFeishuExecutable(path string) error {
	binary, err := pe.Open(path)
	if err != nil {
		return &FeishuArchiveFailure{Code: "CLI_ARCH_MISMATCH"}
	}
	defer binary.Close()
	var want uint16
	switch runtime.GOARCH {
	case "amd64":
		want = pe.IMAGE_FILE_MACHINE_AMD64
	case "arm64":
		want = pe.IMAGE_FILE_MACHINE_ARM64
	default:
		return &FeishuArchiveFailure{Code: "CLI_ARCH_UNSUPPORTED"}
	}
	if binary.Machine != want || binary.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || binary.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		return &FeishuArchiveFailure{Code: "CLI_ARCH_MISMATCH"}
	}
	return nil
}

func feishuPlatformDirectories() []string {
	return []string{"cli-home/appdata", "cli-home/appdata/local"}
}

func feishuPlatformEnvironment(home string) []string {
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		// An empty SystemRoot makes the supervised process fail rather than
		// inheriting an interactive user's environment or search path.
		return []string{"SystemRoot=", "PATH="}
	}
	return []string{"SystemRoot=" + filepath.Dir(systemDirectory), "PATH=" + systemDirectory,
		"USERPROFILE=" + home, "APPDATA=" + filepath.Join(home, "appdata"), "LOCALAPPDATA=" + filepath.Join(home, "appdata", "local"),
		"TEMP=" + filepath.Join(home, "tmp"), "TMP=" + filepath.Join(home, "tmp")}
}

// NTFS write-through flushes the file's metadata as well as its contents.
// Windows does not support POSIX fsync on a read-only directory handle.
func createFeishuPrivateFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, windows.FILE_SHARE_READ, nil, windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_WRITE_THROUGH, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func syncFeishuCreatedFile(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_WRITE_THROUGH|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.FlushFileBuffers(handle)
}
