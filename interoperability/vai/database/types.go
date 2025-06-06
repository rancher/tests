package database

import (
	"database/sql"
	"os"
	"sync"
)

// Snapshot represents a VAI database snapshot from a single pod
type Snapshot struct {
	PodName  string
	Data     []byte
	DB       *sql.DB
	tempFile *os.File
	mu       sync.Mutex
}

// SnapshotCollection manages multiple database snapshots
type SnapshotCollection struct {
	Snapshots map[string]*Snapshot
}

// QueryResult represents a generic query result
type QueryResult struct {
	Columns []string
	Rows    []map[string]interface{}
}

func (s *Snapshot) Cleanup() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var err error
	if s.DB != nil {
		err = s.DB.Close()
	}

	if s.tempFile != nil {
		name := s.tempFile.Name()
		s.tempFile.Close()
		os.Remove(name)
	}

	return err
}

func (sc *SnapshotCollection) Cleanup() error {
	for _, snapshot := range sc.Snapshots {
		if snapshot != nil {
			snapshot.Cleanup()
		}
	}
	return nil
}
