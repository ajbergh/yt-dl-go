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
	addr, root, token     string
	legacyRoot            string
	dataDirOverridden     bool
	runtimeSettingsFromDB bool
	browserPath           string
	browserPathEnv        bool
	origins, hosts        map[string]bool
	maxJobs               int
	maxBytes              int64
	timeout, retain       time.Duration
	maxBytesEnv           bool
	timeoutEnv            bool
	retainEnv             bool
	downloadSlots         int
	downloadSlotsEnv      bool
}

type runtimeOptions struct {
	noBrowser         bool
	help              bool
	version           bool
	migrateLegacyData bool
}

func runtimeUsage() string {
	return "Usage: youtube-downloader [--background|--no-browser|--version|--migrate-legacy-data]\n\n" +
		"  --background  Run the local service without opening the UI automatically.\n" +
		"  --no-browser  Alias for --background; useful for scripts and CI.\n" +
		"  --version     Print embedded version/build metadata and exit.\n" +
		"  --migrate-legacy-data  Copy legacy ./downloads data into the per-user data directory.\n" +
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
		case "--migrate-legacy-data":
			options.migrateLegacyData = true
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

func parseJobTimeoutSetting(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "none") {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || (duration != 0 && duration < time.Second) || duration < 0 {
		return 0, errors.New("jobTimeout must be none, 0, or at least 1s")
	}
	return duration, nil
}

func parseRetentionSetting(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "never") {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || (duration != 0 && duration < 5*time.Minute) || duration < 0 {
		return 0, errors.New("retention must be never, 0, or at least 5m")
	}
	return duration, nil
}

func defaultDataDir() (string, error) {
	if runtime.GOOS == "windows" {
		localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if localAppData == "" || !filepath.IsAbs(localAppData) {
			return "", errors.New("LOCALAPPDATA must be set to an absolute path")
		}
		return filepath.Join(localAppData, "yt-dl-go"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !filepath.IsAbs(home) {
		return "", errors.New("cannot determine the per-user data directory")
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "yt-dl-go"), nil
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" && filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "yt-dl-go"), nil
	}
	return filepath.Join(home, ".local", "share", "yt-dl-go"), nil
}

// loadConfig reads environment overrides, applies local defaults, and validates
// listener, authentication, origin/host, queue, timeout, and retention limits.
func loadConfig() (config, error) {
	root := strings.TrimSpace(os.Getenv("DATA_DIR"))
	dataDirOverridden := root != ""
	if !dataDirOverridden {
		var err error
		root, err = defaultDataDir()
		if err != nil {
			return config{}, err
		}
	}
	legacyRoot, err := filepath.Abs("./downloads")
	if err != nil {
		return config{}, errors.New("cannot resolve legacy DATA_DIR")
	}
	c := config{
		addr: env("ADDR", "127.0.0.1:8080"), root: root, legacyRoot: legacyRoot, dataDirOverridden: dataDirOverridden,
		runtimeSettingsFromDB: true,
		browserPath:           os.Getenv("CHROME_PATH"), browserPathEnv: os.Getenv("CHROME_PATH") != "",
		maxBytesEnv: os.Getenv("MAX_JOB_BYTES") != "", timeoutEnv: os.Getenv("JOB_TIMEOUT") != "", retainEnv: os.Getenv("RETENTION") != "",
		downloadSlots: 4, downloadSlotsEnv: os.Getenv("DOWNLOAD_SLOTS") != "",
		token: os.Getenv("API_TOKEN"), origins: map[string]bool{}, hosts: map[string]bool{},
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
	origins := defaultDevOrigins()
	if configured := strings.TrimSpace(os.Getenv("ALLOWED_ORIGINS")); configured != "" {
		origins = strings.Split(configured, ",")
	}
	for _, origin := range origins {
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
	jobTimeout := strings.TrimSpace(os.Getenv("JOB_TIMEOUT"))
	c.timeout, err = parseJobTimeoutSetting(jobTimeout)
	if err != nil {
		return c, errors.New("JOB_TIMEOUT must be 'none', 0, or at least 1s")
	}
	retention := strings.TrimSpace(env("RETENTION", "never"))
	c.retain, err = parseRetentionSetting(retention)
	if err != nil {
		return c, errors.New("RETENTION must be 'never', zero, or at least 5m")
	}
	c.downloadSlots, err = strconv.Atoi(env("DOWNLOAD_SLOTS", "4"))
	if err != nil || c.downloadSlots < 1 || c.downloadSlots > 16 {
		return c, errors.New("DOWNLOAD_SLOTS must be between 1 and 16")
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
	if err := checkPrivateDataDirACL(root); err != nil {
		return nil, fmt.Errorf("DATA_DIR must be private to the current Windows user: %w", err)
	}
	c.root = root
	store, err := openJobStore(root)
	if err != nil {
		return nil, err
	}
	settings, err := store.loadAppSettings()
	if err != nil {
		_ = store.close()
		return nil, errors.New("cannot load application preferences")
	}
	if c.runtimeSettingsFromDB {
		if !c.maxBytesEnv {
			c.maxBytes = settings.MaxJobBytes
		}
		if !c.timeoutEnv {
			c.timeout, err = parseJobTimeoutSetting(settings.JobTimeout)
			if err != nil {
				_ = store.close()
				return nil, fmt.Errorf("invalid saved job timeout: %w", err)
			}
		}
		if !c.retainEnv {
			c.retain, err = parseRetentionSetting(settings.Retention)
			if err != nil {
				_ = store.close()
				return nil, fmt.Errorf("invalid saved retention: %w", err)
			}
		}
		if !c.browserPathEnv {
			c.browserPath = settings.ChromePath
		}
		if !c.downloadSlotsEnv {
			c.downloadSlots = settings.DownloadSlots
		}
	} else {
		if c.maxBytes < 1 {
			c.maxBytes = settings.MaxJobBytes
		}
		if c.downloadSlots < 1 {
			c.downloadSlots = settings.DownloadSlots
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	browserPool := newChromeBrowserPool(ctx, c.browserPath)
	s := &server{
		cfg: c, settings: settings, jobs: map[string]*jobState{}, tickets: map[string]ticket{},
		slots: make(chan struct{}, c.downloadSlots), scheduleChanged: make(chan struct{}),
		ctx: ctx, stop: cancel,
		store:          store,
		bandwidth:      newBandwidthLimiter(settings.BandwidthLimitBytesPerSec),
		events:         newEventBroker(),
		engine:         newNativeClient(0),
		browserFactory: browserPool.Acquire,
	}
	// Browser E2E fixtures are compiled only with the e2e build tag. The normal
	// production executable links a no-op implementation and therefore cannot
	// switch away from the native YouTube client through environment variables.
	configureE2EFixture(s)
	s.stop = func() {
		cancel()
		s.wg.Wait()
		_ = browserPool.Close()
		if s.persistenceWriter != nil {
			s.persistenceWriter.close()
		}
		_ = store.close()
	}
	loaded, err := store.loadActiveJobs(root)
	if err != nil {
		_ = store.close()
		return nil, errors.New("cannot load active downloads")
	}
	// The scheduler scans queued jobs when it starts; no per-job wake token
	// or fixed-size channel is needed for resumed jobs.
	for _, saved := range loaded {
		j := &jobState{Job: saved.job, dir: saved.dir, done: saved.done, fileItems: fileIndexes(saved.items), fileGroups: fileGroupIndexes(saved.items), cancelRequested: saved.cancelled}
		s.jobs[j.ID] = j
		s.order = append(s.order, j.ID)
	}
	s.persistenceWriter = newPersistenceWriter(store)
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
	if options.migrateLegacyData {
		if err := migrateLegacyData(c.legacyRoot, c.root); err != nil {
			log.Fatal(err)
		}
		log.Printf("Legacy data copied to %s; the original directory was left in place.", c.root)
		return
	}
	if !c.dataDirOverridden && legacyDataAvailable(c.legacyRoot, c.root) {
		log.Printf("Legacy download data was found at %s. Close any running downloader and run --migrate-legacy-data to copy it into %s; the original data will be kept.", c.legacyRoot, c.root)
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
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	_ = h.Shutdown(ctx)
	s.stop()
	s.wg.Wait()
}
