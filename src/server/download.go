// download.go validates private finalized outputs and serves ticket-scoped
// file or ZIP responses, including single-range support for individual files.
package main

import (
	"archive/zip"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var outputName = regexp.MustCompile(`^[0-9]{6,}-[A-Za-z0-9_-]{11}\.(mp4|webm|mp3|m4a)$`)

// openFinal opens a finalized filename only if it is a safe child of the job
// directory and is a regular file rather than a symlink or other file type.
func openFinal(dir, name string) (*os.File, error) {
	if !outputName.MatchString(name) || filepath.Base(name) != name {
		return nil, errors.New("invalid media name")
	}
	path := filepath.Join(dir, name)
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() == 0 {
		return nil, errors.New("media unavailable")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("media unavailable")
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, errors.New("media changed")
	}
	return f, nil
}

// validRange allows no Range header or one satisfiable `bytes` range for a
// non-empty file. It rejects multi-range headers and bounds it cannot parse or
// use to select any bytes; serving clamps an oversized end to the file's end.
func validRange(value string, size int64) bool {
	if value == "" {
		return true
	}
	if !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") || size == 0 {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(parts) != 2 {
		return false
	}
	if parts[0] == "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		return err == nil && suffix > 0
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return false
	}
	if parts[1] == "" {
		return true
	}
	end, err := strconv.ParseInt(parts[1], 10, 64)
	return err == nil && end >= start
}

// download serves a valid unexpired ticket as one finalized file or a streamed
// ZIP. Individual files support one HTTP byte range; archives do not. Tickets
// are the authorization capability for this route, so no API bearer is needed.
func (s *server) download(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "Method not allowed")
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		fail(w, 429, "Too many active downloads")
		return
	}
	s.mu.Lock()
	t, ok := s.tickets[strings.TrimPrefix(r.URL.Path, "/api/downloads/")]
	j := s.jobs[t.jobID]
	if !ok || !time.Now().Before(t.expires) || j == nil || !terminal(j.Status) {
		s.mu.Unlock()
		fail(w, 404, "Download ticket is invalid or expired")
		return
	}
	files := []mediaFile{}
	unavailable := false
	for _, f := range j.Files {
		if t.fileID == "" || t.fileID == f.ID {
			if !f.ManagedAvailable {
				unavailable = true
				continue
			}
			files = append(files, f)
		}
	}
	if unavailable {
		s.mu.Unlock()
		fail(w, 409, "The app-managed media copy is no longer available")
		return
	}
	j.readers++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); j.readers--; s.mu.Unlock() }()
	if len(files) == 0 {
		fail(w, 404, "Finalized output is unavailable")
		return
	}
	// Validate before sending headers; retention cannot remove an active transfer.
	for _, entry := range files {
		f, err := openFinal(j.dir, entry.Name)
		if err != nil {
			fail(w, 409, "Finalized output is no longer available")
			return
		}
		info, err := f.Stat()
		_ = f.Close()
		if err != nil || info.Size() != entry.Size {
			fail(w, 409, "Finalized output has changed")
			return
		}
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(s.cfg.timeout))
	if t.fileID != "" {
		f, err := openFinal(j.dir, files[0].Name)
		if err != nil {
			fail(w, 409, "Finalized output is unavailable")
			return
		}
		defer f.Close()
		if !validRange(r.Header.Get("Range"), files[0].Size) {
			w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(files[0].Size, 10))
			fail(w, 416, "Only one valid byte range is supported")
			return
		}
		contentType := files[0].MimeType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		disposition := "attachment"
		if t.inline {
			disposition = "inline"
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": files[0].Name}))
		http.ServeContent(w, r, files[0].Name, time.Time{}, f)
		return
	}
	if r.Header.Get("Range") != "" {
		fail(w, 416, "Byte ranges are supported for individual files, not streaming archives")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="youtube-`+j.ID+`.zip"`)
	archive := zip.NewWriter(w)
	for _, entry := range files {
		f, err := openFinal(j.dir, entry.Name)
		if err != nil {
			panic(http.ErrAbortHandler)
		}
		out, err := archive.CreateHeader(&zip.FileHeader{Name: entry.Name, Method: zip.Store})
		if err == nil {
			_, err = io.Copy(out, f)
		}
		_ = f.Close()
		if err != nil {
			// Never send a valid-looking, silently incomplete ZIP after a stream error.
			panic(http.ErrAbortHandler)
		}
	}
	if archive.Close() != nil {
		panic(http.ErrAbortHandler)
	}
}
