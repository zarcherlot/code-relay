package relay

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// The recommended deployment uses GitHub Actions' runner service, so the
// legacy local watcher is not started automatically.  These compatibility
// operations expose an explicit state instead of spawning a second scheduler.
func WatcherStatus(root string) map[string]any {
	path := filepath.Join(root, newMeta, "watcher.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{"status": "managed-by-github-actions"}
	}
	var value map[string]any
	if json.Unmarshal(data, &value) != nil {
		return map[string]any{"status": "stopped", "error": "watcher state 无法读取"}
	}
	return value
}

func StopWatcher(root string) map[string]any {
	value := WatcherStatus(root)
	value["status"] = "stopped"
	_ = atomicJSON(filepath.Join(root, newMeta, "watcher.json"), value)
	return value
}
