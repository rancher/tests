package database

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/kubectl"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/sirupsen/logrus"
)

// Extractor handles VAI database extraction from Rancher pods
type Extractor struct {
	client *rancher.Client
}

// NewExtractor creates a new VAI database extractor
func NewExtractor(client *rancher.Client) *Extractor {
	return &Extractor{client: client}
}

// ExtractAll extracts VAI databases from all Rancher pods
func (e *Extractor) ExtractAll() (*SnapshotCollection, error) {
	logrus.Info("Extracting VAI databases from all pods...")

	collection := &SnapshotCollection{
		Snapshots: make(map[string]*Snapshot),
	}

	rancherPods, err := ListRancherPods(e.client)
	if err != nil {
		return nil, fmt.Errorf("failed to list Rancher pods: %v", err)
	}
	logrus.Infof("Found %d Rancher pods", len(rancherPods))

	for i, pod := range rancherPods {
		logrus.Infof("Extracting database from pod %d/%d: %s", i+1, len(rancherPods), pod)

		snapshot, err := e.ExtractFromPod(pod)
		if err != nil {
			logrus.Warnf("Failed to extract database from pod %s: %v", pod, err)
			continue
		}

		collection.Snapshots[pod] = snapshot
		logrus.Infof("Successfully extracted database from pod %s", pod)
	}

	if len(collection.Snapshots) == 0 {
		return nil, fmt.Errorf("no databases were successfully extracted")
	}

	logrus.Infof("Extraction completed. Successfully extracted %d databases", len(collection.Snapshots))
	return collection, nil
}

// ExtractFromPod extracts the VAI database from a specific pod
func (e *Extractor) ExtractFromPod(podName string) (*Snapshot, error) {
	const (
		vaiVacuumURL       = "https://github.com/brudnak/vai-vacuum/releases/download/v1.0.0-beta/vai-vacuum"
		logBufferSize      = "32MB"
		randomStringLength = 5
	)
	tempFile := fmt.Sprintf("/tmp/vai-output-%s", namegen.RandStringLower(randomStringLength))

	shellCmd := fmt.Sprintf(`
		if [ ! -x /tmp/vai-vacuum ]; then
			curl -k -L -sS -o /tmp/vai-vacuum %s || { echo "ERROR: Failed to download vai-vacuum"; exit 1; }
			chmod +x /tmp/vai-vacuum || { echo "ERROR: Failed to chmod vai-vacuum"; exit 1; }
		fi
		# Run vai-vacuum and compress the output to reduce size
		/tmp/vai-vacuum | gzip > %s.gz 2>&1 || { echo "ERROR: vai-vacuum failed with exit code $?"; rm -f %s.gz; exit 1; }
		# Check if file was created and has content
		if [ -f %s.gz ] && [ -s %s.gz ]; then
			ORIG_SIZE=$(/tmp/vai-vacuum | wc -c)
			COMP_SIZE=$(wc -c < %s.gz)
			echo "SUCCESS: Compressed $ORIG_SIZE bytes to $COMP_SIZE bytes"
		else
			echo "ERROR: vai-vacuum produced no output"
			exit 1
		fi
	`, vaiVacuumURL, tempFile, tempFile, tempFile, tempFile, tempFile)

	cmd := []string{
		"kubectl", "exec", podName, "-n", "cattle-system", "-c", "rancher", "--", "sh", "-c", shellCmd,
	}

	output, err := kubectl.Command(e.client, nil, "local", cmd, logBufferSize)
	if err != nil || strings.Contains(output, "ERROR:") {
		return nil, fmt.Errorf("failed to run vai-vacuum: %v, output: %s", err, output)
	}

	if !strings.Contains(output, "SUCCESS:") {
		return nil, fmt.Errorf("vai-vacuum did not complete successfully: %s", output)
	}

	// Read the compressed output
	readCmd := []string{
		"kubectl", "exec", podName, "-n", "cattle-system", "-c", "rancher", "--",
		"sh", "-c", fmt.Sprintf("cat %s.gz | base64", tempFile),
	}

	base64CompressedOutput, err := kubectl.Command(e.client, nil, "local", readCmd, logBufferSize)
	if err != nil {
		return nil, fmt.Errorf("failed to read compressed output: %v", err)
	}

	// Decode and decompress
	compressedData, err := base64.StdEncoding.DecodeString(strings.TrimSpace(base64CompressedOutput))
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 compressed output: %v", err)
	}

	gzReader, err := gzip.NewReader(bytes.NewReader(compressedData))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	base64Data, err := io.ReadAll(gzReader)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress data: %v", err)
	}

	decodedDB, err := base64.StdEncoding.DecodeString(string(base64Data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode vai-vacuum base64 output: %v", err)
	}

	// Validate it's a SQLite database
	if len(decodedDB) < 16 || string(decodedDB[:6]) != "SQLite" {
		return nil, fmt.Errorf("decoded data is not a SQLite database (size: %d)", len(decodedDB))
	}

	// Create a temporary file for the database
	dbTempFile, err := os.CreateTemp("", fmt.Sprintf("%s-vai-*.db", podName))
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %v", err)
	}

	// Write the database to temp file
	if _, err := dbTempFile.Write(decodedDB); err != nil {
		dbTempFile.Close()
		os.Remove(dbTempFile.Name())
		return nil, fmt.Errorf("failed to write database to temp file: %v", err)
	}

	// Get the temp file name before closing
	tempFileName := dbTempFile.Name()

	// Close the file so SQLite can open it
	if err := dbTempFile.Close(); err != nil {
		os.Remove(tempFileName)
		return nil, fmt.Errorf("failed to close temp file: %v", err)
	}

	// Open database in read-only mode
	db, err := sql.Open("sqlite3", tempFileName+"?mode=ro")
	if err != nil {
		os.Remove(tempFileName)
		return nil, fmt.Errorf("failed to open database: %v", err)
	}

	// Verify the database is accessible
	if err := db.Ping(); err != nil {
		db.Close()
		os.Remove(tempFileName)
		return nil, fmt.Errorf("failed to ping database: %v", err)
	}

	logrus.Infof("Successfully extracted %d byte SQLite database from pod %s", len(decodedDB), podName)

	// Reopen the file handle just for cleanup purposes
	tempFileHandle, _ := os.Open(tempFileName)

	return &Snapshot{
		PodName:  podName,
		Data:     decodedDB,
		DB:       db,
		tempFile: tempFileHandle,
	}, nil
}
