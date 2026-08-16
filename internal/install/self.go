package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// SelfInstallPath returns the default user-local binary path.
//
// The name MUST carry .exe on Windows. Without it the copy is not executable by
// name: CreateProcess resolves a bare `ctx-wire` through PATHEXT and never
// matches an extensionless file. `ctx-wire init` self-installs through this
// path, so on Windows it used to drop a dead `%USERPROFILE%\.local\bin\ctx-wire`
// next to the real ctx-wire.exe from install.ps1. Reported 2026-08-16 by a user
// whose Copilot hook failed with "hook errored" on every tool call.
func SelfInstallPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin", selfBinaryName()), nil
}

// selfBinaryName is ctx-wire's own executable name for the current platform.
func selfBinaryName() string {
	if runtime.GOOS == "windows" {
		return "ctx-wire.exe"
	}
	return "ctx-wire"
}

// LegacySelfInstallPath returns the pre-fix, extensionless Windows path, so
// uninstall can clean up the dead copy older versions left behind. It returns
// "" on every other platform, where that path is the current one.
func LegacySelfInstallPath() (string, error) {
	if runtime.GOOS != "windows" {
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin", "ctx-wire"), nil
}

// InstallSelf copies the ctx-wire executable at source to dest. The destination
// is written atomically where possible and always ends up executable.
func InstallSelf(source, dest string) (changed bool, err error) {
	if source == "" {
		return false, errors.New("source executable path is empty")
	}
	if dest == "" {
		return false, errors.New("destination path is empty")
	}

	source, err = filepath.Abs(source)
	if err != nil {
		return false, err
	}
	dest, err = filepath.Abs(dest)
	if err != nil {
		return false, err
	}

	if sameFile(source, dest) {
		changed, err := chmodExecutable(dest)
		clearQuarantine(dest)
		return changed, err
	}

	src, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, fmt.Errorf("%s is a directory", source)
	}

	if sameContent(src, dest) {
		changed, err := chmodExecutable(dest)
		clearQuarantine(dest)
		return changed, err
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return false, err
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".ctx-wire-bin-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return false, err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return false, err
	}
	clearQuarantine(dest)
	return true, nil
}

// UninstallSelf removes the installed ctx-wire binary at dest. Missing files
// are treated as no-op. Directories are never removed.
func UninstallSelf(dest string) (removed bool, err error) {
	if dest == "" {
		return false, errors.New("destination path is empty")
	}
	info, err := os.Stat(dest)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	case info.IsDir():
		return false, fmt.Errorf("%s is a directory", dest)
	}
	if err := os.Remove(dest); err != nil {
		// Windows refuses to delete a RUNNING executable, so `ctx-wire uninstall`
		// could never remove its own binary and failed the whole command with a
		// raw "Access is denied". It can, however, RENAME one (which is how
		// selfupdate swaps itself). Move it aside instead: the name disappears
		// from PATH, so the install is gone for every practical purpose, and the
		// leftover is a best-effort cleanup rather than a hard failure.
		if renamed, rerr := renameAsideRunningExe(dest); renamed {
			return true, nil
		} else if rerr != nil {
			return false, fmt.Errorf("%w (also could not move it aside: %v)", err, rerr)
		}
		return false, err
	}
	return true, nil
}

// renameAsideRunningExe moves a locked Windows executable out of the way. It
// reports renamed=false on non-Windows (where deleting a running binary works
// fine, so a failure there is a real error worth surfacing).
func renameAsideRunningExe(dest string) (renamed bool, err error) {
	if runtime.GOOS != "windows" {
		return false, nil
	}
	aside := dest + ".old"
	// A previous uninstall may have left one; the rename below needs the slot.
	_ = os.Remove(aside)
	if rerr := os.Rename(dest, aside); rerr != nil {
		return false, rerr
	}
	return true, nil
}

func sameFile(a, b string) bool {
	ainfo, aerr := os.Stat(a)
	binfo, berr := os.Stat(b)
	return aerr == nil && berr == nil && os.SameFile(ainfo, binfo)
}

func sameContent(src *os.File, dest string) bool {
	dst, err := os.Open(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		return false
	}
	defer dst.Close()

	srcBytes, err := io.ReadAll(src)
	if err != nil {
		return false
	}
	dstBytes, err := io.ReadAll(dst)
	if err != nil {
		return false
	}
	return bytes.Equal(srcBytes, dstBytes)
}

func chmodExecutable(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if info.Mode().Perm() == 0o755 {
		return false, nil
	}
	return true, os.Chmod(path, 0o755)
}

func clearQuarantine(path string) {
	if runtime.GOOS != "darwin" {
		return
	}
	_ = exec.Command("xattr", "-d", "com.apple.quarantine", path).Run()
}
