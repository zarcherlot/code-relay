package relay

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

func logEvent(event string, fields map[string]any) {
	record := map[string]any{"timestamp": time.Now().UTC().Truncate(time.Second).Format(time.RFC3339), "event": event}
	for key, value := range fields {
		lower := strings.ToLower(key)
		if lower == "token" || lower == "secret" || lower == "password" || lower == "authorization" {
			continue
		}
		record[key] = value
	}
	_ = json.NewEncoder(os.Stderr).Encode(record)
}
