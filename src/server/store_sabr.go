package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (s *jobStore) migrateV31() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 31 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`ALTER TABLE download_parts ADD COLUMN sabr_snapshot TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE download_sabr_segments (
		job_id TEXT NOT NULL,item_index INTEGER NOT NULL,part_key TEXT NOT NULL,
		sequence INTEGER NOT NULL,start_byte INTEGER NOT NULL,end_byte INTEGER NOT NULL,duration_ms INTEGER NOT NULL,hash BLOB NOT NULL,
		PRIMARY KEY(job_id,item_index,part_key,sequence),
		FOREIGN KEY(job_id,item_index,part_key) REFERENCES download_parts(job_id,item_index,part_key) ON DELETE CASCADE)`,
		`INSERT INTO schema_migrations(version) VALUES(31)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) saveSABRCheckpoint(jobID string, item int, key string, part downloadPart, state sabrCheckpoint) error {
	encoded, err := encodeSABRCheckpoint(state)
	if err != nil {
		return err
	}
	if len(state.Segments) == 0 {
		return errSABR
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRow(`SELECT source_fingerprint FROM download_parts WHERE job_id=? AND item_index=? AND part_key=?`, jobID, item, key).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if previous != "" && previous != part.SourceFingerprint {
		if _, err = tx.Exec(`DELETE FROM download_parts WHERE job_id=? AND item_index=? AND part_key=?`, jobID, item, key); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO download_parts(job_id,item_index,part_key,path,completed_bytes,expected_bytes,itag,source_fingerprint,method,updated_at,sabr_snapshot)
	VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(job_id,item_index,part_key) DO UPDATE SET path=excluded.path,completed_bytes=excluded.completed_bytes,
	expected_bytes=excluded.expected_bytes,itag=excluded.itag,source_fingerprint=excluded.source_fingerprint,method=excluded.method,updated_at=excluded.updated_at,sabr_snapshot=excluded.sabr_snapshot`,
		jobID, item, key, part.Path, part.CompletedBytes, part.ExpectedBytes, part.Itag, part.SourceFingerprint, part.Method, time.Now().UnixNano(), encoded)
	if err != nil {
		return err
	}
	segment := state.Segments[len(state.Segments)-1]
	_, err = tx.Exec(`INSERT INTO download_sabr_segments(job_id,item_index,part_key,sequence,start_byte,end_byte,duration_ms,hash) VALUES(?,?,?,?,?,?,?,?)
	ON CONFLICT(job_id,item_index,part_key,sequence) DO UPDATE SET start_byte=excluded.start_byte,end_byte=excluded.end_byte,duration_ms=excluded.duration_ms,hash=excluded.hash`,
		jobID, item, key, segment.Sequence, segment.Start, segment.End, segment.Duration, segment.Hash[:])
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *jobStore) loadSABRCheckpoint(jobID string, item int, key string) (*sabrCheckpoint, error) {
	var encoded string
	if err := s.db.QueryRow(`SELECT sabr_snapshot FROM download_parts WHERE job_id=? AND item_index=? AND part_key=?`, jobID, item, key).Scan(&encoded); err != nil {
		return nil, err
	}
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > 64*1024 {
		return nil, errSABR
	}
	var state sabrCheckpoint
	if err := json.Unmarshal([]byte(encoded), &state); err != nil {
		return nil, errSABR
	}
	rows, err := s.db.Query(`SELECT sequence,start_byte,end_byte,duration_ms,hash FROM download_sabr_segments WHERE job_id=? AND item_index=? AND part_key=? ORDER BY start_byte LIMIT ?`, jobID, item, key, maxSABRParts+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var segment sabrSavedSegment
		var hash []byte
		if err := rows.Scan(&segment.Sequence, &segment.Start, &segment.End, &segment.Duration, &hash); err != nil {
			return nil, err
		}
		if len(hash) != len(segment.Hash) {
			return nil, errSABR
		}
		copy(segment.Hash[:], hash)
		state.Segments = append(state.Segments, segment)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &state, nil
}
