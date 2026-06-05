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
func SelfInstallPath() (string, error) {
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
		return false, err
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
