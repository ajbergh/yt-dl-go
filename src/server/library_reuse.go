package main

import (
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
)

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// findPublishedAudio returns an already published audio file for the same video,
// extension and MP3 bitrate in the job's Library folder. Re-running a playlist
// would otherwise download, encode and publish the same track again as "(2)".
func (s *jobStore) findPublishedAudio(job *jobState, id string) (mediaFile, bool) {
	if s == nil || !videoID.MatchString(id) {
		return mediaFile{}, false
	}
	pattern := regexp.MustCompile(`^[0-9]{6,}-` + regexp.QuoteMeta(id) + `\.` + regexp.QuoteMeta(job.AudioFormat) + `$`)
	rows, err := s.db.Query(`SELECT li.file_json,li.output_path FROM library_items li
		JOIN library_sources j ON j.source_job_id=li.source_job_id
		WHERE json_extract(li.file_json,'$.name') LIKE ? ESCAPE '\' AND json_extract(li.file_json,'$.mediaType')='audio' AND j.audio_bitrate=? AND j.download_location=?
		ORDER BY li.created_at DESC`, "%-"+likeEscaper.Replace(id)+"."+job.AudioFormat, job.AudioBitrate, job.DownloadLocation)
	if err != nil {
		return mediaFile{}, false
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		var path sql.NullString
		if rows.Scan(&data, &path) != nil {
			continue
		}
		var file mediaFile
		if json.Unmarshal([]byte(data), &file) != nil || !pattern.MatchString(file.Name) || !file.PublishedAvailable {
			continue
		}
		if path.String != "" {
			file.OutputPath = path.String
		}
		if file.OutputPath == "" || file.Size <= 0 {
			continue
		}
		existing, openErr := openLibraryMedia(job, file)
		if openErr != nil {
			continue
		}
		if existing.Close() != nil {
			continue
		}
		return file, true
	}
	return mediaFile{}, false
}
