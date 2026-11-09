package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/kkdai/youtube/v2"
)

func TestRepeatedAudioJobReusesPublishedTrack(t *testing.T) {
	fake := fixtureClient(1)
	fake.videoFn = func(_ context.Context, id string) (*youtube.Video, error) {
		video := fixtureVideo(id)
		video.Formats = youtube.FormatList{{ItagNo: 140, MimeType: `audio/mp4; codecs="mp4a.40.2"`, AudioChannels: 2, AudioSampleRate: "44100", ContentLength: int64(len(fixtureData))}}
		return video, nil
	}
	s := testServer(t, fake, nil)
	body := `{"url":"` + testVideo + `","quality":"best","mediaType":"audio","audioFormat":"m4a","rightsConfirmed":true}`
	run := func() Job {
		t.Helper()
		var created Job
		response := request(s, "POST", "/api/jobs", body, nil)
		if response.Code != 202 {
			t.Fatalf("create job: %d %s", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		return waitTerminal(t, s, created.ID)
	}
	first, second := run(), run()
	if first.Status != "completed" || second.Status != "completed" || len(first.Files) != 1 || len(second.Files) != 1 {
		t.Fatalf("jobs did not complete cleanly: %+v / %+v", first, second)
	}
	published, _ := filepath.Glob(filepath.Join(s.settings.DownloadLocation, "*", "*.m4a"))
	if len(published) != 1 {
		t.Fatalf("repeat run published %d audio files, want 1: %v", len(published), published)
	}
	if first.Files[0].ID == second.Files[0].ID || second.Files[0].OutputName != first.Files[0].OutputName {
		t.Fatalf("second Library entry was not a distinct reference to the shared file: %+v / %+v", first.Files[0], second.Files[0])
	}
	for _, job := range []Job{first, second} {
		waitPersistedTerminalJob(t, s, job.ID)
		for _, suffix := range []string{"/all", "/library-files/" + job.Files[0].ID} {
			response := request(s, "DELETE", "/api/jobs/"+job.ID+suffix, "", nil)
			if response.Code != 409 {
				t.Fatalf("shared audio deletion was allowed: %d %s", response.Code, response.Body.String())
			}
		}
		path := ticketPath(t, s, job.ID, `{"fileId":"`+job.Files[0].ID+`","inline":true}`)
		if response := request(s, "GET", path, "", nil); response.Code != 200 || response.Body.String() != fixtureData {
			t.Fatalf("shared audio was removed: %d %s", response.Code, response.Body.String())
		}
	}
	keys := []string{first.ID + ":" + first.Files[0].ID, second.ID + ":" + second.Files[0].ID}
	if _, _, err := s.store.saveLibraryOrganization(libraryOrganization{Favorites: keys,
		Collections: []libraryCollection{{ID: "c_shared", Name: "Shared tracks", ItemKeys: keys}}}); err != nil {
		t.Fatal(err)
	}
	archivePath, cleanup, err := s.createLibraryExport()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	target := newPersistenceTestServer(t)
	if response := importPortableOrganizationArchive(t, target, archive); response.Code != 200 {
		t.Fatalf("shared audio archive import: %d %s", response.Code, response.Body.String())
	}
	organization, err := target.store.loadLibraryOrganization()
	if err != nil || len(organization.Favorites) != 2 || len(organization.Collections) != 1 || len(organization.Collections[0].ItemKeys) != 2 || organization.Favorites[0] == organization.Favorites[1] {
		t.Fatalf("shared audio identities lost during archive round trip: %+v %v", organization, err)
	}
}

func TestRepeatedMP3JobReusesOnlyMatchingBitrate(t *testing.T) {
	data, err := os.ReadFile("testdata/audio/silence.m4a")
	if err != nil {
		t.Fatal(err)
	}
	fake := fixtureClient(1)
	fake.videoFn = func(_ context.Context, id string) (*youtube.Video, error) {
		video := fixtureVideo(id)
		video.Formats = youtube.FormatList{{ItagNo: 140, MimeType: `audio/mp4; codecs="mp4a.40.2"`, AudioChannels: 2, AudioSampleRate: "44100", ContentLength: int64(len(data))}}
		return video, nil
	}
	fake.streamFn = func(context.Context, *youtube.Video, *youtube.Format) (io.ReadCloser, int64, error) {
		return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
	}
	s := testServer(t, fake, nil)
	run := func(bitrate string) {
		t.Helper()
		body := `{"url":"` + testVideo + `","quality":"best","mediaType":"audio","audioFormat":"mp3","audioBitrate":"` + bitrate + `","rightsConfirmed":true}`
		response := request(s, "POST", "/api/jobs", body, nil)
		var created Job
		if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &created) != nil {
			t.Fatalf("create job: %d %s", response.Code, response.Body.String())
		}
		if done := waitTerminal(t, s, created.ID); done.Status != "completed" || len(done.Files) != 1 {
			t.Fatalf("%s job failed: %+v", bitrate, done)
		}
	}
	count := func() int {
		published, _ := filepath.Glob(filepath.Join(s.settings.DownloadLocation, "*", "*.mp3"))
		return len(published)
	}
	run("192k")
	run("192k")
	if got := count(); got != 1 {
		t.Fatalf("same bitrate produced %d files, want 1", got)
	}
	run("128k")
	if got := count(); got != 2 {
		t.Fatalf("a different bitrate must produce its own file; found %d", got)
	}
}

func TestRepeatedAudioJobPublishesInChangedLibraryFolder(t *testing.T) {
	data, err := os.ReadFile("testdata/audio/silence.m4a")
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"m4a", "mp3"} {
		t.Run(format, func(t *testing.T) {
			fake := fixtureClient(1)
			fake.videoFn = func(_ context.Context, id string) (*youtube.Video, error) {
				video := fixtureVideo(id)
				video.Formats = youtube.FormatList{{ItagNo: 140, MimeType: `audio/mp4; codecs="mp4a.40.2"`, AudioChannels: 2, AudioSampleRate: "44100", ContentLength: int64(len(data))}}
				return video, nil
			}
			fake.streamFn = func(context.Context, *youtube.Video, *youtube.Format) (io.ReadCloser, int64, error) {
				return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
			}
			s := testServer(t, fake, nil)
			run := func() Job {
				t.Helper()
				body := `{"url":"` + testVideo + `","quality":"best","mediaType":"audio","audioFormat":"` + format + `","rightsConfirmed":true}`
				response := request(s, "POST", "/api/jobs", body, nil)
				var created Job
				if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &created) != nil {
					t.Fatalf("create job: %d %s", response.Code, response.Body.String())
				}
				done := waitTerminal(t, s, created.ID)
				if done.Status != "completed" || len(done.Files) != 1 {
					t.Fatalf("audio job failed: %+v", done)
				}
				return done
			}
			first := run()
			newFolder := t.TempDir()
			s.mu.Lock()
			s.settings.DownloadLocation = newFolder
			s.mu.Unlock()
			second := run()
			secondPath := trackedOutputPath(t, s, second.ID, second.Files[0].ID)
			if secondPath != filepath.Join(newFolder, filepath.FromSlash(second.Files[0].OutputRelativePath)) {
				t.Fatalf("new job reused the old Library folder: %s", secondPath)
			}
			for _, job := range []Job{first, second} {
				path := ticketPath(t, s, job.ID, `{"fileId":"`+job.Files[0].ID+`","inline":true}`)
				response := request(s, "GET", path, "", nil)
				if response.Code != 200 || int64(response.Body.Len()) != job.Files[0].Size {
					t.Fatalf("published audio cannot be played: %d %s", response.Code, response.Body.String())
				}
			}
		})
	}
}
