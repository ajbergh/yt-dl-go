package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWholeJobDeletionPreflightsSharedMediaAndCaptions(t *testing.T) {
	for _, pending := range []bool{false, true} {
		for _, caption := range []bool{false, true} {
			name := "durable-media"
			if pending {
				name = "pending-media"
			}
			if caption {
				name += "-caption"
			}
			t.Run(name, func(t *testing.T) {
				s := testServer(t, fixtureClient(2), nil)
				job := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
				waitPersistedTerminalJob(t, s, job.ID)
				paths := []string{trackedOutputPath(t, s, job.ID, job.Files[0].ID), trackedOutputPath(t, s, job.ID, job.Files[1].ID)}
				aliasFile := job.Files[1]
				aliasFile.ID = "shared-alias-file"
				aliasFile.OutputPath = paths[1]
				if caption {
					aliasFile.OutputPath = filepath.Join(s.settings.DownloadLocation, "alias.mp4")
					aliasFile.OutputRelativePath = "alias.mp4"
					aliasFile.Subtitle = &subtitleFile{OutputPath: paths[1], OutputRelativePath: job.Files[1].OutputRelativePath, PublishedAvailable: true}
				}
				alias := &jobState{Job: Job{ID: "shared-alias-job", Status: "downloading", DownloadLocation: s.settings.DownloadLocation, Files: []mediaFile{aliasFile}}}
				if pending {
					s.mu.Lock()
					s.jobs[alias.ID] = alias
					s.mu.Unlock()
				} else if err := s.store.saveJob(alias); err != nil {
					t.Fatal(err)
				}
				response := request(s, "DELETE", "/api/jobs/"+job.ID+"/all", "", nil)
				if response.Code != 409 {
					t.Fatalf("shared output deletion: %d %s", response.Code, response.Body.String())
				}
				for _, path := range paths {
					if _, err := os.Stat(path); err != nil {
						t.Fatalf("ownership rejection removed a sibling or shared file: %v", err)
					}
				}
			})
		}
	}
}
