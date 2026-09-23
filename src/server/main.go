// main.go loads process configuration, restores the SQLite-backed service,
// starts the HTTP listener, opens the UI, and shuts workers down gracefully.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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

type runtimeOptions struct {
	noBrowser bool
	help      bool
	version   bool
}

func runtimeUsage() string {
	return "Usage: youtube-downloader [--background|--no-browser|--version]\n\n" +
		"  --background  Run the local service without opening the UI automatically.\n" +
		"  --no-browser  Alias for --background; useful for scripts and CI.\n" +
		"  --version     Print embedded version/build metadata and exit.\n" +
		"  -h, --help    Show this help text.\n"
}

func parseRuntimeOptions(args []string) (runtimeOptions, error) {
	var options runtimeOptions
	for _, arg := range args {
		switch arg {
		case "--background", "--no-browser":
			options.noBrowser = true
		case "-h", "--help":
			options.help = true
		case "--version":
			options.version = true
		default:
			return runtimeOptions{}, fmt.Errorf("unknown argument %q", arg)
		}
	}
	return options, nil
}

func automaticBrowserEnabled(options runtimeOptions, noBrowserEnv string) bool {
	return !options.noBrowser && noBrowserEnv != "1"
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
		// The bundled UI is same-origin with the configured listener. Vite emits
		// crossorigin attributes for hashed assets, so browsers may send an Origin
		// header even when loading those same-origin files. Always allow the
		// listener's loopback authorities at its actual port; network-visible
		// bindings still require explicit ALLOWED_ORIGINS/ALLOWED_HOSTS.
		for _, h := range []string{"localhost", "127.0.0.1", "::1", host} {
			authority := strings.ToLower(net.JoinHostPort(h, port))
			c.hosts[authority] = true
			c.origins["http://"+authority] = true
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
	retention := strings.TrimSpace(env("RETENTION", "never"))
	if !strings.EqualFold(retention, "never") {
		c.retain, err = time.ParseDuration(retention)
		if err != nil || (c.retain != 0 && c.retain < 5*time.Minute) {
			return c, errors.New("RETENTION must be 'never', zero, or at least 5m")
		}
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
		slots: make(chan struct{}, 4), scheduleChanged: make(chan struct{}),
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
	// The scheduler scans queued jobs when it starts; no per-job wake token
	// or fixed-size channel is needed for resumed jobs.
	for _, saved := range loaded {
		j := &jobState{Job: saved.job, dir: saved.dir, done: saved.done, fileItems: fileIndexes(saved.items), cancelRequested: saved.cancelled}
		s.jobs[j.ID] = j
		s.order = append(s.order, j.ID)
	}
	return s, nil
}

// main owns listener lifetime and shutdown. The UI is opened only after the
// configured listener is bound; `ADDR` determines the browser URL.
func main() {
	options, err := parseRuntimeOptions(os.Args[1:])
	if err != nil {
		log.Printf("%v\n%s", err, runtimeUsage())
		os.Exit(2)
	}
	if options.help {
		fmt.Print(runtimeUsage())
		return
	}
	if options.version {
		build := currentBuildInfo()
		fmt.Printf("youtube-downloader %s (commit %s, built %s)\n", build.Version, build.Commit, build.Date)
		return
	}

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
	uiURL := browserURL(c.addr)
	log.Printf("Downloader API listening on %s", c.addr)
	if automaticBrowserEnabled(options, os.Getenv("NO_BROWSER")) {
		if err := openBrowser(uiURL); err != nil {
			log.Printf("Could not open web UI automatically: %v", err)
		}
	} else {
		log.Printf("Background/no-browser mode active; open %s to use the UI", uiURL)
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
