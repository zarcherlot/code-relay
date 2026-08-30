//go:build !windows

package relay

import "path/filepath"

func resolveRootPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
