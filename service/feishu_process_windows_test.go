package service

import (
	"bufio"
	"bytes"
	"context"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestFeishuProcessHelper(t *testing.T) {
	switch os.Getenv("DPIN_FEISHU_PROCESS_TEST_HELPER") {
	case "success":
		fmt.Fprint(os.Stdout, "synthetic CLI output")
		os.Exit(0)
	case "failure":
		os.Exit(23)
	case "tree":
		child := exec.Command(os.Args[0], "-test.run=^TestFeishuProcessHelper$")
		child.Env = append(os.Environ(), "DPIN_FEISHU_PROCESS_TEST_HELPER=grandchild")
		child.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		if err := child.Start(); err != nil {
			os.Exit(24)
		}
		fmt.Fprintln(os.Stdout, child.Process.Pid)
		_ = child.Wait()
		os.Exit(0)
	case "grandchild":
		for {
			time.Sleep(time.Hour)
		}
	}
}

func TestFeishuProcessOutputAndExit(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	for _, helper := range []string{"success", "failure"} {
		t.Run(helper, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, executable, "-test.run=^TestFeishuProcessHelper$")
			process.Env = append(os.Environ(), "DPIN_FEISHU_PROCESS_TEST_HELPER="+helper)
			var output bytes.Buffer
			process.Stdout = &output
			process.WaitDelay = time.Second
			err := runFeishuCLI(process)
			if helper == "success" {
				require.NoError(t, err)
				assert.Equal(t, "synthetic CLI output", output.String())
			} else {
				var exit *exec.ExitError
				require.ErrorAs(t, err, &exit)
				assert.Equal(t, 23, exit.ExitCode())
			}
		})
	}
	t.Run("cancelled before start", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		process := exec.CommandContext(ctx, executable, "-test.run=^TestFeishuProcessHelper$")
		err := runFeishuCLI(process)
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, process.Process)
	})
}

func TestFeishuProcessCancellationKillsDescendants(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, executable, "-test.run=^TestFeishuProcessHelper$")
	process.Env = append(os.Environ(), "DPIN_FEISHU_PROCESS_TEST_HELPER=tree")
	process.WaitDelay = time.Second
	output, err := process.StdoutPipe()
	require.NoError(t, err)
	finished := make(chan error, 1)
	go func() { finished <- runFeishuCLI(process) }()
	line, err := bufio.NewReader(output).ReadString('\n')
	require.NoError(t, err)
	pid, err := strconv.ParseUint(strings.TrimSpace(line), 10, 32)
	require.NoError(t, err)
	child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	require.NoError(t, err)
	defer windows.CloseHandle(child)
	cancel()
	select {
	case err := <-finished:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("CLI cancellation did not finish")
	}
	result, err := windows.WaitForSingleObject(child, 5000)
	require.NoError(t, err)
	assert.Equal(t, uint32(windows.WAIT_OBJECT_0), result, "CLI descendant survived cancellation")
}

func setFeishuTestDACL(t *testing.T, path, policy string) {
	t.Helper()
	descriptor, err := windows.SecurityDescriptorFromString(policy)
	require.NoError(t, err)
	dacl, _, err := descriptor.DACL()
	require.NoError(t, err)
	require.NoError(t, windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil))
}

func privateFeishuTestDirectory(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	// LocalService is the deployed service identity; the current user also
	// permits the same tests when run as an ordinary developer account.
	policy := fmt.Sprintf("D:P(A;OICI;FA;;;%s)(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;LS)", user.User.Sid.String())
	setFeishuTestDACL(t, root, policy)
	return root, policy
}

func TestFeishuWindowsPrivateFilesystem(t *testing.T) {
	root, policy := privateFeishuTestDirectory(t)
	secret := filepath.Join(root, "synthetic-secret.json")
	require.NoError(t, os.WriteFile(secret, []byte(`{"app_secret":"synthetic-only"}`), 0600))
	t.Run("private service ACL accepted", func(t *testing.T) {
		require.NoError(t, validateFeishuPath(secret, false, true))
	})
	t.Run("new child inherits private ACL", func(t *testing.T) {
		inputs := filepath.Join(root, "inputs")
		require.NoError(t, os.Mkdir(inputs, 0700))
		input := filepath.Join(inputs, "synthetic-input.json")
		require.NoError(t, os.WriteFile(input, []byte(`{"synthetic":true}`), 0600))
		require.NoError(t, validateFeishuPath(inputs, true, true))
		require.NoError(t, validateFeishuPath(input, false, true))
	})
	t.Run("Everyone read rejected for secret", func(t *testing.T) {
		setFeishuTestDACL(t, secret, policy+"(A;;FR;;;WD)")
		require.Error(t, validateFeishuPath(secret, false, true))
		setFeishuTestDACL(t, secret, policy)
	})
	t.Run("public read accepted for binary but public write rejected", func(t *testing.T) {
		path := filepath.Join(root, "synthetic-cli.exe")
		require.NoError(t, os.WriteFile(path, []byte("synthetic binary policy fixture"), 0600))
		setFeishuTestDACL(t, path, policy+"(A;;FR;;;WD)")
		require.NoError(t, validateFeishuPath(path, false, false))
		setFeishuTestDACL(t, path, policy+"(A;;FW;;;WD)")
		require.Error(t, validateFeishuPath(path, false, false))
	})
	t.Run("null DACL rejected", func(t *testing.T) {
		path := filepath.Join(root, "null-policy.json")
		require.NoError(t, os.WriteFile(path, []byte("synthetic"), 0600))
		require.NoError(t, windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, nil, nil))
		require.Error(t, validateFeishuPath(path, false, true))
	})
	t.Run("unsafe future child access rejected", func(t *testing.T) {
		directory := filepath.Join(root, "unsafe-inheritance")
		require.NoError(t, os.Mkdir(directory, 0700))
		setFeishuTestDACL(t, directory, policy+"(A;OIIO;FR;;;WD)")
		require.Error(t, validateFeishuPath(directory, true, true))
	})
}

func TestFeishuWindowsRejectsRedirectedPaths(t *testing.T) {
	root, _ := privateFeishuTestDirectory(t)
	secret := filepath.Join(root, "synthetic.json")
	require.NoError(t, os.WriteFile(secret, []byte("synthetic"), 0600))
	for _, path := range []string{
		`\\server\share\synthetic.json`,
		`\\?\C:\synthetic.json`,
		filepath.VolumeName(root) + "synthetic.json",
		secret + ":hidden-stream",
	} {
		t.Run(path, func(t *testing.T) {
			require.Error(t, validateFeishuPath(path, false, true))
		})
	}
	t.Run("ancestor directory link rejected", func(t *testing.T) {
		target := filepath.Join(root, "target")
		inner := filepath.Join(target, "inner")
		require.NoError(t, os.MkdirAll(inner, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(inner, "synthetic.json"), []byte("synthetic"), 0600))
		link := filepath.Join(root, "redirect")
		if err := os.Symlink(target, link); err != nil {
			if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
				t.Skip("Windows token lacks the directory-symlink privilege")
			}
			require.NoError(t, err)
		}
		require.Error(t, validateFeishuPath(filepath.Join(link, "inner", "synthetic.json"), false, true))
	})
}

func TestFeishuWindowsExecutableFormats(t *testing.T) {
	root, _ := privateFeishuTestDirectory(t)
	executable, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, validateFeishuExecutable(executable), "native test executable must be accepted")
	malformed := filepath.Join(root, "malformed.exe")
	require.NoError(t, os.WriteFile(malformed, []byte("not a PE executable"), 0600))
	require.Error(t, validateFeishuExecutable(malformed))
	for _, variant := range []string{"wrong machine", "DLL image"} {
		t.Run(variant, func(t *testing.T) {
			input, err := os.Open(executable)
			require.NoError(t, err)
			defer input.Close()
			path := filepath.Join(root, strings.ReplaceAll(variant, " ", "-")+".exe")
			output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
			require.NoError(t, err)
			_, err = io.Copy(output, input)
			require.NoError(t, err)
			var raw [4]byte
			_, err = output.ReadAt(raw[:], 0x3c)
			require.NoError(t, err)
			peOffset := int64(binary.LittleEndian.Uint32(raw[:]))
			var value [2]byte
			if variant == "wrong machine" {
				machine := uint16(pe.IMAGE_FILE_MACHINE_ARM64)
				if runtime.GOARCH == "arm64" {
					machine = pe.IMAGE_FILE_MACHINE_AMD64
				}
				binary.LittleEndian.PutUint16(value[:], machine)
				_, err = output.WriteAt(value[:], peOffset+4)
			} else {
				_, err = output.ReadAt(value[:], peOffset+22)
				require.NoError(t, err)
				binary.LittleEndian.PutUint16(value[:], binary.LittleEndian.Uint16(value[:])|pe.IMAGE_FILE_DLL)
				_, err = output.WriteAt(value[:], peOffset+22)
			}
			require.NoError(t, err)
			require.NoError(t, output.Close())
			require.Error(t, validateFeishuExecutable(path))
		})
	}
}

func TestFeishuWindowsEnvironmentIsolation(t *testing.T) {
	root, _ := privateFeishuTestDirectory(t)
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "SystemRoot", "LARKSUITE_CLI_APP_SECRET", "LARKSUITE_CLI_CONFIG_DIR"} {
		t.Setenv(key, "synthetic-personal-state-must-not-be-inherited")
	}
	environment := feishuCLIEnvironment(ResumeArchiveConfig{DataDir: root})
	values := make(map[string]string)
	for _, variable := range environment {
		key, value, _ := strings.Cut(variable, "=")
		values[strings.ToUpper(key)] = value
	}
	home := filepath.Join(root, "cli-home")
	assert.Equal(t, home, values["HOME"])
	assert.Equal(t, home, values["USERPROFILE"])
	assert.Equal(t, home, values["LARKSUITE_CLI_CONFIG_DIR"])
	assert.Equal(t, filepath.Join(home, "appdata"), values["APPDATA"])
	assert.Equal(t, filepath.Join(home, "appdata", "local"), values["LOCALAPPDATA"])
	assert.Equal(t, filepath.Join(home, "tmp"), values["TEMP"])
	assert.Equal(t, filepath.Join(home, "tmp"), values["TMP"])
	systemDirectory, err := windows.GetSystemDirectory()
	require.NoError(t, err)
	assert.Equal(t, systemDirectory, values["PATH"])
	assert.Equal(t, filepath.Dir(systemDirectory), values["SYSTEMROOT"])
	assert.Equal(t, "bot", values["LARKSUITE_CLI_DEFAULT_AS"])
	assert.Equal(t, "bot", values["LARKSUITE_CLI_STRICT_MODE"])
	assert.NotContains(t, values, "LARKSUITE_CLI_APP_SECRET")
	assert.NotContains(t, strings.Join(environment, "\n"), "synthetic-personal-state-must-not-be-inherited")
}
