// main.go loads process configuration, restores the SQLite-backed service,
// starts the HTTP listener, opens the UI, and shuts workers down gracefully.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	addr, root, token string
	browserPath       string
	origins, hosts    map[string]bool
	maxJobs           int
	maxBytes          int64
	timeout, retain   time.Duration
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// loadConfig reads environment overrides, applies local defaults, and validates
// listener, authentication, origin/host, queue, timeout, and retention limits.
func loadConfig() (config, error) {
	c := config{
		addr: env("ADDR", "127.0.0.1:8080"), root: env("DATA_DIR", "./downloads"),
		browserPath: os.Getenv("CHROME_PATH"),
		token:       os.Getenv("API_TOKEN"), origins: map[string]bool{}, hosts: map[string]bool{},
	}
	host, port, err := net.SplitHostPort(c.addr)
	if err != nil {
		return c, errors.New("ADDR must be a host:port address")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return c, errors.New("ADDR must specify a valid port")
	}
	ip := net.ParseIP(host)
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if strings.ContainsAny(c.token, " \t\r\n") || (!loopback && len(c.token) < 32) {
		return c, errors.New("non-loopback binding requires API_TOKEN of at least 32 characters; tokens cannot contain whitespace")
	}
	for _, origin := range strings.Split(env("ALLOWED_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173,http://localhost:8080,http://127.0.0.1:8080"), ",") {
		origin = strings.TrimSpace(origin)
		u, e := url.Parse(origin)
		if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(origin, "*") {
			return c, errors.New("ALLOWED_ORIGINS must contain exact HTTP(S) origins without trailing slashes")
		}
		c.origins[origin] = true
	}
	if loopback {
		for _, h := range []string{"localhost", "127.0.0.1", "::1", host} {
			c.hosts[strings.ToLower(net.JoinHostPort(h, port))] = true
		}
	}
	for _, h := range strings.Split(os.Getenv("ALLOWED_HOSTS"), ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		u, e := url.Parse("http://" + h)
		if e != nil || u.Host != h || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(h, "* \t\r\n") {
			return c, errors.New("ALLOWED_HOSTS must contain exact host[:port] authorities")
		}
		c.hosts[h] = true
	}
	if !loopback && len(c.hosts) == 0 {
		return c, errors.New("non-loopback binding also requires explicit ALLOWED_HOSTS")
	}
	c.maxJobs, err = strconv.Atoi(env("MAX_JOBS", "32"))
	if err != nil || c.maxJobs < 1 || c.maxJobs > 1000 {
		return c, errors.New("MAX_JOBS must be between 1 and 1000")
	}
	c.maxBytes, err = strconv.ParseInt(env("MAX_JOB_BYTES", "10737418240"), 10, 64)
	if err != nil || c.maxBytes < 1 {
		return c, errors.New("MAX_JOB_BYTES must be a positive byte count")
	}
	c.timeout, err = time.ParseDuration(env("JOB_TIMEOUT", "6h"))
	if err != nil || c.timeout < time.Second {
		return c, errors.New("JOB_TIMEOUT must be at least 1s")
	}
	c.retain, err = time.ParseDuration(env("RETENTION", "24h"))
	if err != nil || c.retain < 5*time.Minute {
		return c, errors.New("RETENTION must be at least 5m")
	}
	return c, nil
}

func randomID(bytes int) string {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		panic("secure random generator unavailable")
	}
	return hex.EncodeToString(b)
}

// newServer opens private storage and SQLite, loads preferences/history, and
// queues interrupted jobs for recovery before returning the HTTP handler state.
func newServer(c config) (*server, error) {
	root, err := filepath.Abs(c.root)
	if err != nil {
		return nil, errors.New("invalid DATA_DIR")
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, errors.New("cannot create private DATA_DIR")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("DATA_DIR must be a real private directory with mode 0700")
	}
	c.root = root
	store, err := openJobStore(root)
	if err != nil {
		return nil, err
	}
	if err := store.saveConfig(c); err != nil {
		_ = store.close()
		return nil, errors.New("cannot persist service configuration")
	}
	settings, err := store.loadAppSettings()
	if err != nil {
		_ = store.close()
		return nil, errors.New("cannot load application preferences")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &server{
		cfg: c, settings: settings, jobs: map[string]*jobState{}, tickets: map[string]ticket{},
		queue: make(chan string, c.maxJobs), slots: make(chan struct{}, 4), scheduleChanged: make(chan struct{}),
		ctx: ctx, stop: cancel,
		store:     store,
		bandwidth: newBandwidthLimiter(settings.BandwidthLimitBytesPerSec),
		events:    newEventBroker(),
		engine:    newNativeClient(c.timeout),
		browserFactory: func(ctx context.Context) (browserMediaProvider, error) {
			return newChromeBrowserProvider(ctx, c.browserPath)
		},
	}
	// Browser E2E fixtures are compiled only with the e2e build tag. The normal
	// production executable links a no-op implementation and therefore cannot
	// switch away from the native YouTube client through environment variables.
	configureE2EFixture(s)
	s.stop = func() {
		cancel()
		s.wg.Wait()
		_ = store.close()
	}
	loaded, err := store.loadJobs(root)
	if err != nil {
		_ = store.close()
		return nil, errors.New("cannot load download history")
	}
	for _, saved := range loaded {
		j := &jobState{Job: saved.job, dir: saved.dir, done: saved.done, fileItems: fileIndexes(saved.items), cancelRequested: saved.cancelled}
		s.jobs[j.ID] = j
		s.order = append(s.order, j.ID)
		if saved.resuming {
			select {
			case s.queue <- j.ID:
			default:
				_ = store.close()
				return nil, errors.New("download history exceeds configured queue capacity")
			}
		}
	}
	return s, nil
}

// main owns listener lifetime and shutdown. The UI is opened only after the
// configured listener is bound; `ADDR` determines the browser URL.
func main() {
	c, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	s, err := newServer(c)
	if err != nil {
		log.Fatal(err)
	}
	s.start()
	h := &http.Server{Addr: c.addr, Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16384}
	listener, err := net.Listen("tcp", c.addr)
	if err != nil {
		s.stop()
		s.wg.Wait()
		log.Fatal(err)
	}
	stopping, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- h.Serve(listener) }()
	log.Printf("Downloader API listening on %s", c.addr)
	if os.Getenv("NO_BROWSER") != "1" {
		if err := openBrowser(browserURL(c.addr)); err != nil {
			log.Printf("Could not open web UI automatically: %v", err)
		}
	}
	select {
	case <-stopping.Done():
	case <-result:
		log.Print("HTTP listener stopped")
	}
	s.stop()
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	_ = h.Shutdown(ctx)
	s.wg.Wait()
	_ = s.store.close()
}
