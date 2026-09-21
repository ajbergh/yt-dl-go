// thumbnail.go captures durable, bounded YouTube artwork into private job
// storage and serves it only through authenticated job-scoped API requests.
package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

const maxThumbnailBytes int64 = 5 << 20

var thumbnailFileID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func thumbnailPath(j *jobState, file mediaFile) (string, error) {
	if j == nil || !thumbnailFileID.MatchString(file.ID) || filepath.Base(file.ID) != file.ID {
		return "", errors.New("invalid thumbnail metadata")
	}
	return filepath.Join(j.dir, ".thumbnail-"+file.ID+".bin"), nil
}

func allowedThumbnailMime(value string) bool {
	return value == "image/jpeg" || value == "image/png" || value == "image/webp"
}

func fetchThumbnail(ctx context.Context, timeout time.Duration, rawURL, destination string) (string, error) {
	if safeInspectedThumbnailURL(rawURL) == "" {
		return "", errors.New("thumbnail URL is not an approved YouTube image URL")
	}
	thumbTimeout := 30 * time.Second
	if timeout > 0 && timeout < thumbTimeout {
		thumbTimeout = timeout
	}
	client := nativeHTTPClient(thumbTimeout)
	baseRedirect := client.CheckRedirect
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if safeInspectedThumbnailURL(r.URL.String()) == "" {
			return errors.New("thumbnail redirect left approved YouTube image hosts")
		}
		return baseRedirect(r, via)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg;q=0.9,*/*;q=0.1")
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("thumbnail server returned a non-success response")
	}
	if response.ContentLength > maxThumbnailBytes {
		return "", errors.New("thumbnail exceeds the size limit")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxThumbnailBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) == 0 || int64(len(data)) > maxThumbnailBytes {
		return "", errors.New("thumbnail is empty or exceeds the size limit")
	}
	mimeType := http.DetectContentType(data)
	if !allowedThumbnailMime(mimeType) {
		return "", errors.New("thumbnail content type is not supported")
	}

	part := destination + ".part"
	_ = os.Remove(part)
	if _, err := os.Lstat(destination); err == nil {
		return "", errors.New("thumbnail destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	file, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = os.Remove(part)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(part, destination); err != nil {
		return "", err
	}
	cleanup = false
	return mimeType, nil
}

func openTrackedThumbnail(j *jobState, file mediaFile) (*os.File, error) {
	if !file.ThumbnailLocalAvailable || !allowedThumbnailMime(file.ThumbnailMimeType) {
		return nil, errors.New("local thumbnail is unavailable")
	}
	path, err := thumbnailPath(j, file)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maxThumbnailBytes {
		return nil, errors.New("local thumbnail is unavailable")
	}
	handle, err := os.Open(path)
	if err != nil {
		return nil, errors.New("local thumbnail is unavailable")
	}
	after, err := handle.Stat()
	if err != nil || !os.SameFile(before, after) {
		_ = handle.Close()
		return nil, errors.New("local thumbnail changed")
	}
	return handle, nil
}

func (s *server) captureThumbnail(ctx context.Context, j *jobState, file *mediaFile) {
	if file == nil || file.ThumbnailURL == "" {
		return
	}
	destination, err := thumbnailPath(j, *file)
	if err != nil {
		return
	}
	fetcher := s.thumbnailFetcher
	if fetcher == nil {
		fetcher = func(ctx context.Context, rawURL, destination string) (string, error) {
			return fetchThumbnail(ctx, s.cfg.timeout, rawURL, destination)
		}
	}
	mimeType, err := fetcher(ctx, file.ThumbnailURL, destination)
	if err != nil || !allowedThumbnailMime(mimeType) {
		_ = os.Remove(destination)
		return
	}
	handle, err := os.Open(destination)
	if err != nil {
		return
	}
	info, statErr := handle.Stat()
	_ = handle.Close()
	if statErr != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxThumbnailBytes {
		_ = os.Remove(destination)
		return
	}
	file.ThumbnailMimeType = mimeType
	file.ThumbnailLocalAvailable = true
}

func (s *server) serveThumbnail(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		fail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	values := r.URL.Query()["fileId"]
	if len(values) != 1 || values[0] == "" || !thumbnailFileID.MatchString(values[0]) {
		fail(w, http.StatusBadRequest, "fileId must identify one finalized file")
		return
	}
	fileID := values[0]

	s.mu.Lock()
	j := s.jobs[jobID]
	if j == nil {
		s.mu.Unlock()
		fail(w, http.StatusNotFound, "Job not found")
		return
	}
	var selected mediaFile
	found := false
	for _, file := range j.Files {
		if file.ID == fileID {
			selected = file
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		fail(w, http.StatusNotFound, "File not found")
		return
	}
	if !selected.ThumbnailLocalAvailable {
		s.mu.Unlock()
		fail(w, http.StatusNotFound, "Local thumbnail is unavailable")
		return
	}
	j.readers++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		j.readers--
		s.mu.Unlock()
	}()

	handle, err := openTrackedThumbnail(j, selected)
	if err != nil {
		fail(w, http.StatusConflict, "Local thumbnail is unavailable")
		return
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		fail(w, http.StatusConflict, "Local thumbnail is unavailable")
		return
	}
	w.Header().Set("Content-Type", selected.ThumbnailMimeType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Content-Disposition", "inline")
	http.ServeContent(w, r, "thumbnail", time.Time{}, handle)
}
