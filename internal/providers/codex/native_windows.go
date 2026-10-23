//go:build windows

package codex

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	createNewConsole         = 0x00000010
	createUnicodeEnvironment = 0x00000400
	createBreakawayFromJob   = 0x01000000
	processTerminate         = 0x0001
)

// nativeEnvBlock is the current environment plus CI=true, so project test
// runners (vitest, jest) started by the agent do not hang in watch mode.
func nativeEnvBlock() []uint16 {
	var b []uint16
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "CI=") {
			continue
		}
		b = append(b, utf16.Encode([]rune(kv))...)
		b = append(b, 0)
	}
	b = append(b, utf16.Encode([]rune("CI=true"))...)
	return append(b, 0, 0)
}

func commandLine(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = syscall.EscapeArg(a)
	}
	return strings.Join(quoted, " ")
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// DefaultNativeLauncher starts argv in a new console window (Windows Terminal when
// it is the default terminal). CreateProcess is called directly because os/exec
// always redirects stdin to NUL, and the interactive CLIs require a real console.
// No shell parses the arguments, so the prompt is passed intact.
//
// The window must outlive the bridge. Hosts run commands inside a job object that
// is killed when the command ends; when that job forbids breakaway (Antigravity's
// run_command does), the window is created through WMI, outside the caller's job.
func DefaultNativeLauncher(dir string, argv []string) (<-chan struct{}, func(), error) {
	if pid, h, err := createConsole(dir, argv, createNewConsole|createBreakawayFromJob); err == nil {
		return watch(pid, h, nil)
	}
	if pid, script, err := createViaWMI(dir, argv); err == nil {
		h, err := syscall.OpenProcess(syscall.SYNCHRONIZE|processTerminate, false, pid)
		if err == nil {
			return watch(pid, h, func() { _ = os.Remove(script) })
		}
	}
	// Last resort: the window works but closes when the caller's job ends.
	pid, h, err := createConsole(dir, argv, createNewConsole)
	if err != nil {
		return nil, nil, err
	}
	return watch(pid, h, nil)
}

func createConsole(dir string, argv []string, flags uint32) (uint32, syscall.Handle, error) {
	cmdLine, err := syscall.UTF16PtrFromString(commandLine(argv))
	if err != nil {
		return 0, 0, err
	}
	var dirPtr *uint16
	if dir != "" {
		if dirPtr, err = syscall.UTF16PtrFromString(dir); err != nil {
			return 0, 0, err
		}
	}
	env := nativeEnvBlock()
	si := &syscall.StartupInfo{}
	si.Cb = uint32(unsafe.Sizeof(*si))
	pi := &syscall.ProcessInformation{}
	if err := syscall.CreateProcess(nil, cmdLine, nil, nil, false, flags|createUnicodeEnvironment, &env[0], dirPtr, si, pi); err != nil {
		return 0, 0, err
	}
	_ = syscall.CloseHandle(pi.Thread)
	return pi.ProcessId, pi.Process, nil
}

// createViaWMI asks the WMI service to start a PowerShell wrapper that runs argv
// in its own console and waits for it. The wrapper uses ProcessStartInfo with a
// prebuilt argument string, so PowerShell never re-parses the prompt.
func createViaWMI(dir string, argv []string) (uint32, string, error) {
	tmp := filepath.Join(os.TempDir(), "SimpleOrchestrator-launch")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return 0, "", err
	}
	script := filepath.Join(tmp, fmt.Sprintf("launch-%d.ps1", time.Now().UnixNano()))
	var b strings.Builder
	b.WriteString("$env:CI = 'true'\n")
	fmt.Fprintf(&b, "$psi = New-Object System.Diagnostics.ProcessStartInfo\n$psi.FileName = %s\n", psQuote(argv[0]))
	fmt.Fprintf(&b, "$psi.Arguments = %s\n", psQuote(commandLine(argv[1:])))
	if dir != "" {
		fmt.Fprintf(&b, "$psi.WorkingDirectory = %s\n", psQuote(dir))
	}
	b.WriteString("$psi.UseShellExecute = $false\n$p = [System.Diagnostics.Process]::Start($psi)\n$p.WaitForExit()\nexit $p.ExitCode\n")
	// UTF-8 with BOM so Windows PowerShell 5.1 reads non-ASCII prompts correctly.
	if err := os.WriteFile(script, append([]byte{0xEF, 0xBB, 0xBF}, b.String()...), 0o644); err != nil {
		return 0, "", err
	}
	wrapper := commandLine([]string{"powershell.exe", "-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script})
	wd := dir
	if wd == "" {
		wd, _ = os.Getwd()
	}
	create := fmt.Sprintf("$r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{CommandLine=%s; CurrentDirectory=%s}; \"$($r.ReturnValue) $($r.ProcessId)\"", psQuote(wrapper), psQuote(wd))
	out, err := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodeUTF16Base64(create)).Output()
	if err != nil {
		_ = os.Remove(script)
		return 0, "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 || fields[0] != "0" {
		_ = os.Remove(script)
		return 0, "", fmt.Errorf("Win32_Process.Create falhou: %q", strings.TrimSpace(string(out)))
	}
	pid, err := strconv.ParseUint(fields[len(fields)-1], 10, 32)
	if err != nil {
		_ = os.Remove(script)
		return 0, "", err
	}
	return uint32(pid), script, nil
}

func encodeUTF16Base64(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}

func watch(pid uint32, h syscall.Handle, cleanup func()) (<-chan struct{}, func(), error) {
	done := make(chan struct{})
	go func() {
		_, _ = syscall.WaitForSingleObject(h, syscall.INFINITE)
		_ = syscall.CloseHandle(h)
		if cleanup != nil {
			cleanup()
		}
		close(done)
	}()
	kill := func() {
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(int(pid))).Run()
	}
	return done, kill, nil
}
