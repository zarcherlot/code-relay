package relay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Daemon is an optional local deployment path. GitHub Actions remains the
// default scheduler; keeping this implementation isolated makes its security
// boundary and lifecycle easier to audit without touching core orchestration.
func Daemon(root, role string, interval float64, addr string) error {
	if role != "orchestrator" && role != "checkpoint" {
		return errors.New("role must be orchestrator or checkpoint")
	}
	if interval < 1 || interval > 3600 {
		return errors.New("poll interval must be 1..3600")
	}
	if err := validateDaemonAddress(addr); err != nil {
		return err
	}
	var err error
	root, err = pathWithinRoot(root, root)
	if err != nil {
		return err
	}
	d := &daemon{root: root, role: role, requests: map[string][]time.Time{}}
	stopWatcher := make(chan struct{})
	if role == "checkpoint" {
		go func() {
			for {
				select {
				case <-stopWatcher:
					return
				default:
					if err := syncRunbooks(root); err != nil {
						fmt.Fprintln(os.Stderr, err)
					}
					time.Sleep(time.Duration(interval * float64(time.Second)))
				}
			}
		}()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", d.handle)
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() { <-ctx.Done(); close(stopWatcher); _ = server.Shutdown(context.Background()) }()
	fmt.Println("Code Relay daemon listening on", addr)
	logEvent("daemon_started", map[string]any{"role": role, "addr": addr})
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func validateDaemonAddress(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return errors.New("daemon addr 必须是 host:port")
	}
	secret := os.Getenv("CODE_RELAY_WEBHOOK_SECRET")
	if secret != "" && len(secret) < 32 {
		return errors.New("CODE_RELAY_WEBHOOK_SECRET 至少需要 32 个字符")
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	if secret == "" {
		return errors.New("daemon 监听非本机地址时必须配置 CODE_RELAY_WEBHOOK_SECRET")
	}
	return nil
}

type daemon struct {
	root, role string
	mu         sync.Mutex
	requests   map[string][]time.Time
}

func (d *daemon) allowRequest(client string) bool {
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	recent := d.requests[client][:0]
	for _, stamp := range d.requests[client] {
		if now.Sub(stamp) < time.Minute {
			recent = append(recent, stamp)
		}
	}
	if len(recent) >= 60 {
		d.requests[client] = recent
		return false
	}
	d.requests[client] = append(recent, now)
	return true
}

func (d *daemon) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	client := r.RemoteAddr
	if host, _, splitErr := net.SplitHostPort(r.RemoteAddr); splitErr == nil {
		client = host
	}
	if !d.allowRequest(client) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	contentType := strings.ToLower(strings.Split(r.Header.Get("Content-Type"), ";")[0])
	if contentType != "application/json" {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	if r.ContentLength > 1<<20 {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(body) > 1<<20 {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	secret := os.Getenv("CODE_RELAY_WEBHOOK_SECRET")
	if secret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		want := "sha256=" + fmt.Sprintf("%x", mac.Sum(nil))
		if !hmac.Equal([]byte(sig), []byte(want)) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	m, pathErr := projectPath(d.root, newMeta)
	if pathErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if err := ensurePrivateDir(m); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	delivery := r.Header.Get("X-GitHub-Delivery")
	d.mu.Lock()
	defer d.mu.Unlock()
	if delivery != "" {
		var deliveries []string
		_ = readJSON(filepath.Join(m, "deliveries.json"), &deliveries)
		for _, seen := range deliveries {
			if seen == delivery {
				w.WriteHeader(http.StatusAccepted)
				return
			}
		}
		deliveries = append(deliveries, delivery)
		if len(deliveries) > 10000 {
			deliveries = deliveries[len(deliveries)-10000:]
		}
		if err := atomicJSON(filepath.Join(m, "deliveries.json"), deliveries); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	p := filepath.Join(m, "events.jsonl")
	if info, e := os.Stat(p); e == nil && info.Size() >= maxQueue {
		w.WriteHeader(http.StatusInsufficientStorage)
		return
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(body, '\n')); err != nil {
		w.WriteHeader(http.StatusInsufficientStorage)
		return
	}
	if err := f.Sync(); err != nil {
		w.WriteHeader(http.StatusInsufficientStorage)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
