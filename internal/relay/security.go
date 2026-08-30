package relay

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var remoteCredential = regexp.MustCompile(`(?i)((?:https?|ssh|git)://)[^/@\s]+@`)

func validateTaskID(value string) error {
	if !taskID.MatchString(value) {
		return fmt.Errorf("非法 task_id: %s", value)
	}
	return nil
}

func pathWithinRoot(root, candidate string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbs, candidateAbs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return "", errors.New("路径必须位于工程根目录内")
	}
	if err := rejectSymlinkComponents(rootAbs); err != nil {
		return "", err
	}
	if err := rejectSymlinkComponents(candidateAbs); err != nil {
		return "", err
	}
	return candidateAbs, nil
}

func projectPath(root string, elements ...string) (string, error) {
	parts := append([]string{root}, elements...)
	return pathWithinRoot(root, filepath.Join(parts...))
}

func rejectSymlinkComponents(path string) error {
	current, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for {
		info, statErr := os.Lstat(current)
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝符号链接路径: %s", current)
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return nil
}

func sanitizeRemote(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 2048 || strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == ' ' || r == '\t' || r == '\n' }) >= 0 {
		return "", errors.New("origin URL 非法或过长")
	}
	lower := strings.ToLower(value)
	for _, prefix := range []string{"ext::", "file://", "git+file://", "fd::"} {
		if strings.HasPrefix(lower, prefix) {
			return "", errors.New("不允许使用本地协议或 ext transport")
		}
	}
	if strings.HasPrefix(value, "git@github.com:") {
		value = "https://github.com/" + strings.TrimPrefix(value, "git@github.com:")
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" {
			return "", errors.New("origin URL 非法")
		}
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		parsed.Path = strings.TrimSuffix(parsed.Path, ".git")
		remotePath := strings.Trim(strings.TrimSuffix(parsed.Path, ".git"), "/")
		if remotePath == "" || strings.IndexFunc(remotePath, func(r rune) bool { return r < 32 || r == ' ' || r == '\t' || r == '\n' }) >= 0 {
			return "", errors.New("origin URL 缺少合法仓库路径")
		}
		if strings.EqualFold(parsed.Hostname(), "github.com") {
			if !strings.Contains(remotePath, "/") {
				return "", errors.New("GitHub origin 必须包含 owner/repository")
			}
			return "https://github.com/" + remotePath, nil
		}
		return strings.TrimSuffix(parsed.String(), "/"), nil
	}
	// Accept scp-style SSH remotes, but never preserve the user component.
	if colon := strings.Index(value, ":"); colon > 0 {
		host := value[:colon]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		path := strings.TrimSuffix(strings.TrimPrefix(value[colon+1:], "/"), ".git")
		if host != "" && path != "" {
			return "ssh://" + host + "/" + path, nil
		}
	}
	return "", errors.New("origin URL 必须是远程 Git URL")
}

func redactSensitive(value string) string {
	value = remoteCredential.ReplaceAllString(value, `${1}***@`)
	for key := range sensitiveEnvKeys {
		if secret := os.Getenv(key); secret != "" {
			value = strings.ReplaceAll(value, secret, "***")
		}
	}
	return value
}

type tailBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
	cut   bool
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit}
}

func (buffer *tailBuffer) Write(data []byte) (int, error) {
	written := len(data)
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if buffer.limit <= 0 {
		return written, nil
	}
	if len(data) >= buffer.limit {
		buffer.cut = buffer.cut || len(data) > buffer.limit || len(buffer.data) > 0
		buffer.data = append(buffer.data[:0], data[len(data)-buffer.limit:]...)
		return written, nil
	}
	buffer.data = append(buffer.data, data...)
	if overflow := len(buffer.data) - buffer.limit; overflow > 0 {
		buffer.cut = true
		copy(buffer.data, buffer.data[overflow:])
		buffer.data = buffer.data[:buffer.limit]
	}
	return written, nil
}

func (buffer *tailBuffer) truncated() bool {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.cut
}

func (buffer *tailBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(append([]byte(nil), buffer.data...))
}
