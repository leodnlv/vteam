package execx

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Self is the absolute path of the running vteam binary, as it was invoked (a symlink stays a symlink).
func Self() (string, error) {
	if path, err := exec.LookPath(os.Args[0]); err == nil {
		return filepath.Abs(path)
	}
	return os.Executable()
}
