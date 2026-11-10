package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kkdai/youtube/v2"
)

func TestReviewSearchUsesNormalizedValues(t *testing.T) {
	s := newPersistenceTestServer(t)
	manifest, records := sampleLibraryImport()
	records[0].File.Title = `ÉCOLE & <Mix> "quoted" Straße`
	records[0].File.Subtitle = nil
	if _, err := s.store.importLibraryRecords(manifest, records); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		q    string
		want int
	}{
		{"école", 1}, {"E\u0301COLE", 1}, {"&", 1}, {"<mix>", 1}, {`"quoted"`, 1}, {"STRASSE", 1}, {"publishedAvailable", 0}, {"outputName", 0},
	} {
		page, err := s.store.loadLibraryFilePage(50, libraryFileCursor{}, libraryFileQuery{Sort: "recent", Filter: libraryFilter{Query: tc.q}})
		if err != nil || page.TotalFiles != tc.want {
			t.Errorf("query %q: files=%d err=%v", tc.q, page.TotalFiles, err)
		}
	}
	if _, err := s.store.db.Exec(`UPDATE library_items SET file_json=json_set(file_json,'$.title','NEUER ÉTÉ') WHERE file_id=?`, importedFileID(records[0].JobID, records[0].File.ID)); err != nil {
		t.Fatal(err)
	}
	page, err := s.store.loadLibraryFilePage(50, libraryFileCursor{}, libraryFileQuery{Sort: "recent", Filter: libraryFilter{Query: "neuer été"}})
	if err != nil || page.TotalFiles != 1 {
		t.Fatalf("updated FTS: %+v %v", page, err)
	}
}

func TestReviewAvailabilityRecoversWithoutLosingMetadata(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	job := waitTerminal(t, s, createJob(t, s, testVideo).ID)
	waitPersistedTerminalJob(t, s, job.ID)
	path := trackedOutputPath(t, s, job.ID, job.Files[0].ID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	page, err := s.store.loadLibraryFilePage(50, libraryFileCursor{}, libraryFileQuery{Sort: "recent"})
	if err != nil || page.TotalFiles != 1 || page.Jobs[0].Files[0].PublishedAvailable {
		t.Fatalf("deleted file: %+v %v", page, err)
	}
	if err := s.reconcileLibrary(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	page, err = s.store.loadLibraryFilePage(50, libraryFileCursor{}, libraryFileQuery{Sort: "recent"})
	if err != nil || !page.Jobs[0].Files[0].PublishedAvailable {
		t.Fatalf("restored file: %+v %v", page, err)
	}
	// Persisted unavailable flags must not block a restored file on use.
	pathTicket := ticketPath(t, s, job.ID, `{"fileId":"`+job.Files[0].ID+`","inline":true}`)
	response := request(s, "GET", pathTicket, "", nil)
	if response.Code != 200 {
		t.Fatalf("restored preview: %d %s", response.Code, response.Body.String())
	}

}

func TestReviewCappedTransferUsesParentDeadline(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, stop := itemTransferContext(parent, nil, 1<<30, 1024)
	defer stop()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("shared cap imposed an item deadline")
	}
	cancel()
	if ctx.Err() != context.Canceled {
		t.Fatal("parent cancellation lost")
	}
	ctx, stop = itemTransferContext(context.Background(), nil, 1024, 0)
	defer stop()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("uncapped transfer lost safety deadline")
	}
}

func TestReviewNativeBytesArriveBeforeWholeChunk(t *testing.T) {
	ready, release := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-3/4")
		w.Header().Set("Content-Length", "4")
		w.WriteHeader(206)
		_, _ = w.Write([]byte("a"))
		w.(http.Flusher).Flush()
		<-release
		_, _ = w.Write([]byte("bcd"))
	}))
	defer httpServer.Close()
	defer once.Do(func() { close(release) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		chunk := nativeRangeChunk{start: 0, end: 3}
		done <- (&server{}).readNativeRange(ctx, httpServer.Client(), youtubeClientProfile{}, &youtube.Format{URL: httpServer.URL, ContentLength: 4}, &chunk, func(int64) {
			select {
			case ready <- struct{}{}:
			default:
			}
		})
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("startup observer waited for the entire chunk")
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestReviewCancellationWithMissingWorkDirectory(t *testing.T) {
	s := newPersistenceTestServer(t)
	job := bulkFixtureJob(t, s, "paused")
	if err := os.Remove(job.dir); err != nil {
		t.Fatal(err)
	}
	response := request(s, "POST", "/api/jobs/"+job.ID+"/cancel", "", nil)
	if response.Code != 200 {
		t.Fatalf("cancel: %d %s", response.Code, response.Body.String())
	}
}

func TestReviewChapterEOFAllowsOnlySmallFinalShortfall(t *testing.T) {
	chapter := mediaChapter{StartMs: 1000, EndMs: 10000}
	if !chapterEOFComplete(9500, chapter, true, 1000) {
		t.Fatal("rounded final duration rejected")
	}
	for _, tc := range []struct {
		samples int64
		last    bool
	}{{9500, false}, {8000, true}, {1000, true}} {
		if chapterEOFComplete(tc.samples, chapter, tc.last, 1000) {
			t.Fatalf("accepted truncated chapter: %+v", tc)
		}
	}
}

func TestReviewUnauthenticatedSettingsCannotSelectExecutable(t *testing.T) {
	s := newPersistenceTestServer(t)
	s.cfg.token = ""
	response := request(s, "PUT", "/api/settings", `{"chromePath":"untrusted.exe"}`, nil)
	if response.Code != 403 {
		t.Fatalf("path mutation: %d %s", response.Code, response.Body.String())
	}
	response = request(s, "PUT", "/api/settings", `{"defaultQuality":"720"}`, nil)
	if response.Code != 200 {
		t.Fatalf("normal preferences: %d %s", response.Code, response.Body.String())
	}
}

func TestReviewExplorerLaunchIgnoresChildExit(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestReviewExplorerChild$")
	command.Env = append(os.Environ(), "REVIEW_EXPLORER_CHILD=1")
	if err := launchTrackedCommand(command); err != nil {
		t.Fatal(err)
	}
	if err := launchTrackedCommand(exec.Command(filepath.Join(t.TempDir(), "missing-explorer"))); err == nil {
		t.Fatal("launch failure hidden")
	}
}
func TestReviewExplorerChild(t *testing.T) {
	if os.Getenv("REVIEW_EXPLORER_CHILD") == "1" {
		os.Exit(1)
	}
}

func TestReviewProgressResetClearsSpeedBaseline(t *testing.T) {
	s := newPersistenceTestServer(t)
	job := bulkFixtureJob(t, s, "paused")
	job.itemProgress = map[int]*itemProgress{1: {progressAt: time.Now().Add(-time.Second), progressBytes: 10000, speed: 9000, total: 20000}}
	s.updateProgress(job, 1, 10, 20000)
	if item := job.itemProgress[1]; item.speed != 0 || item.progressBytes != 10 {
		t.Fatalf("stale speed: %+v", item)
	}
}

func TestReviewOrganizationByteCapRollsBackImport(t *testing.T) {
	s := newPersistenceTestServer(t)
	org := libraryOrganization{Collections: []libraryCollection{}, Favorites: []string{}}
	for i := 0; i < 2000; i++ {
		org.Favorites = append(org.Favorites, fmt.Sprintf("%0250d", i))
	}
	// Fill the saved envelope to within a few bytes of the limit while staying
	// below every count limit, then merge one imported favorite.
	for {
		candidate := org
		candidate.Collections = append(append([]libraryCollection(nil), org.Collections...), libraryCollection{ID: fmt.Sprintf("c%d", len(org.Collections)), Name: fmt.Sprintf("C %d", len(org.Collections)), ItemKeys: []string{org.Favorites[0]}})
		if validateLibraryOrganization(candidate) != nil {
			break
		}
		org = candidate
	}
	if len(org.Collections) >= 200 {
		t.Fatal("fixture hit count limit")
	}
	encoded, _ := json.Marshal(org)
	padding := maxLibraryOrganizationBytes - len(encoded) - 16
	if padding > 0 && padding < 250 {
		org.Favorites = append(org.Favorites, strings.Repeat("z", padding))
	}
	if _, _, err := s.store.saveLibraryOrganization(org); err != nil {
		t.Fatal(err)
	}
	manifest, records := sampleLibraryImport()
	incoming := &libraryExportOrganization{Favorites: []string{"original-job:original-file-1"}}
	_, _, err := s.store.importLibraryDocument(manifest, libraryExportDocument{Manifest: manifest, Records: records, Organization: incoming})
	var conflict *libraryImportConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("oversized merge: %v", err)
	}
	jobs, _ := s.store.loadLibraryJobs()
	if len(jobs) != 0 {
		t.Fatal("failed merge committed imported media")
	}
	after, _ := s.store.loadLibraryOrganization()
	if after.Revision != 1 {
		t.Fatalf("revision changed: %d", after.Revision)
	}
}

func TestReviewExportRejectsUnimportableArchive(t *testing.T) {
	manifest, _ := sampleLibraryImport()
	data := makeLibraryImportArchive(t, manifest, make([]libraryExportRecord, maxLibraryImportRecords+1))
	path := filepath.Join(t.TempDir(), "oversized.zip")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateLibraryExportArchive(path); !errors.Is(err, errLibraryExportLimit) {
		t.Fatalf("record cap: %v", err)
	}
}

func TestReviewNativePartialSkipsBrowserPair(t *testing.T) {
	s := newPersistenceTestServer(t)
	job := bulkFixtureJob(t, s, "paused")
	dir := t.TempDir()
	vp, ap := filepath.Join(dir, "video"), filepath.Join(dir, "audio")
	data := []byte("verified native prefix")
	if err := os.WriteFile(vp, data, 0600); err != nil {
		t.Fatal(err)
	}
	format := &youtube.Format{ItagNo: 137, ContentLength: 100}
	if err := s.saveDownloadPart(job, "abcdefghijk", 1, "video", vp, "native-range", format, int64(len(data)), ""); err != nil {
		t.Fatal(err)
	}
	provider := &checkpointPairFixture{cancel: func() { t.Error("browser replaced native bytes") }}
	selection := streamSelection{kind: "video/mp4", video: format, audio: &youtube.Format{ItagNo: 140, ContentLength: 10}}
	_, _, used, err := s.captureAdaptivePair(context.Background(), job, nil, &youtube.Video{ID: "abcdefghijk"}, selection, vp, ap, 1, 200, nil, provider)
	if err != nil || used {
		t.Fatalf("browser selected over native partial: used=%t err=%v", used, err)
	}
	actual, _ := os.ReadFile(vp)
	if string(actual) != string(data) {
		t.Fatal("native bytes lost")
	}
	part, err := s.store.loadPart(job.ID, 1, "video")
	if err != nil || part.Method != "native-range" {
		t.Fatalf("checkpoint changed: %+v %v", part, err)
	}
}

func TestReviewShutdownClosesLiveEventsBeforeDrain(t *testing.T) {
	s := newPersistenceTestServer(t)
	h := httptest.NewUnstartedServer(s)
	s.cfg.hosts[h.Listener.Addr().String()] = true
	h.Config.RegisterOnShutdown(s.events.close)
	h.Start()
	defer h.Close()
	request, err := http.NewRequest(http.MethodGet, h.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.cfg.token != "" {
		request.Header.Set("Authorization", "Bearer "+s.cfg.token)
	}
	response, err := h.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("SSE status=%d", response.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.Config.Shutdown(ctx); err != nil {
		t.Fatalf("live events delayed shutdown: %v", err)
	}
}

func TestReviewMissingInterruptedMetadataSurvivesSaveAndCanBeForgotten(t *testing.T) {
	s := testServer(t, fixtureClient(2), nil)
	job := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
	waitPersistedTerminalJob(t, s, job.ID)
	missing := job.Files[0]
	if err := os.Remove(trackedOutputPath(t, s, job.ID, missing.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`UPDATE jobs SET status='downloading' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.store.loadJob(s.cfg.root, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	current := &jobState{Job: loaded.job, dir: loaded.dir, fileItems: fileIndexes(loaded.items), fileGroups: fileGroupIndexes(loaded.items)}
	if err := s.store.saveJob(current); err != nil {
		t.Fatal(err)
	}
	exists, err := s.store.hasLibraryItem(job.ID, missing.ID)
	if err != nil || !exists {
		t.Fatalf("normal save pruned metadata: %t %v", exists, err)
	}
	if _, err := s.store.db.Exec(`UPDATE jobs SET status='cancelled' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	response := request(s, "DELETE", "/api/jobs/"+job.ID+"/library-items/"+missing.ID, "", nil)
	if response.Code != 204 {
		t.Fatalf("explicit forget: %d %s", response.Code, response.Body.String())
	}
	exists, err = s.store.hasLibraryItem(job.ID, missing.ID)
	if err != nil || exists {
		t.Fatalf("explicit forget retained metadata: %t %v", exists, err)
	}
}
