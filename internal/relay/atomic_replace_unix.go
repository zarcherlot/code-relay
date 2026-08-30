//go:build !windows

package relay

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
