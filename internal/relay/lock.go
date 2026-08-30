package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type taskLock struct {
	path string
}

func acquireTaskLock(root, taskID string, timeout time.Duration) (*taskLock, error) {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, taskID)
	path, err := projectPath(root, newMeta, "locks", "task-"+name+".lock")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_ = json.NewEncoder(file).Encode(map[string]any{"pid": os.Getpid(), "created_at": time.Now().Unix()})
			_ = file.Sync()
			_ = file.Close()
			return &taskLock{path: path}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > 2*time.Hour {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("task lock is busy: %s", filepath.Base(path))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (lock *taskLock) release() {
	if lock != nil && lock.path != "" {
		_ = os.Remove(lock.path)
	}
}
