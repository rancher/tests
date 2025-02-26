#!/bin/sh
set -e

MAX_RETRIES=3
BUILD_TIMEOUT=300  # 5 minutes timeout
CACHE_DIR="/var/cache/vai-query"
GO_VERSION="1.23.6"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [INFO] $1"
}

error() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [ERROR] $1" >&2
}

# Execute command with timeout
run_with_timeout() {
    command="$1"
    timeout="$2"

    # Create a timeout process
    (
        eval "$command" &
        cmd_pid=$!

        (
            sleep $timeout
            kill $cmd_pid 2>/dev/null
        ) &
        timeout_pid=$!

        wait $cmd_pid 2>/dev/null
        kill $timeout_pid 2>/dev/null
    )
}

# Function to check if Go is installed and working
check_go() {
    if /usr/local/go/bin/go version >/dev/null 2>&1; then
        return 0
    fi
    return 1
}

# Install Go with retries
install_go() {
    mkdir -p $CACHE_DIR
    GO_ARCHIVE="$CACHE_DIR/go${GO_VERSION}.linux-amd64.tar.gz"

    for i in $(seq 1 $MAX_RETRIES); do
        log "Attempting to install Go (attempt $i of $MAX_RETRIES)..."

        if [ ! -f "$GO_ARCHIVE" ]; then
            if ! curl -L -o "$GO_ARCHIVE" "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" --insecure; then
                error "Failed to download Go (attempt $i)"
                continue
            fi
        fi

        if tar -C /usr/local -xzf "$GO_ARCHIVE"; then
            log "Go installed successfully"
            return 0
        else
            error "Failed to extract Go (attempt $i)"
            rm -f "$GO_ARCHIVE"
        fi
    done

    error "Failed to install Go after $MAX_RETRIES attempts"
    return 1
}

# Build vai-query with retries
build_vai_query() {
    mkdir -p /root/vai-query
    cd /root/vai-query

    # Initialize Go module if it doesn't exist
    if [ ! -f go.mod ]; then
        go mod init vai-query
    fi

    # Create or update main.go
    cat << 'EOF' > main.go
package main

import (
    "database/sql"
    "encoding/json"
    "fmt"
    "log"
    "os"
    "strings"
    "time"
    "context"

    "github.com/pkg/errors"
    _ "modernc.org/sqlite"
)

func main() {
    log.SetFlags(log.LstdFlags | log.Lmicroseconds)
    log.Println("Starting VAI database query...")

    // Create context with timeout
    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()

    // Clean up any existing snapshot
    os.Remove("/tmp/snapshot.db")

    // First connection to create snapshot
    log.Println("Opening connection to original database...")
    db, err := sql.Open("sqlite", "/var/lib/rancher/informer_object_cache.db")
    if err != nil {
        log.Fatalf("Failed to open original database: %v", err)
    }

    log.Println("Creating database snapshot...")
    _, err = db.ExecContext(ctx, "VACUUM INTO '/tmp/snapshot.db'")
    if err != nil {
        log.Fatalf("Failed to create snapshot: %v", err)
    }
    db.Close()

    // Wait a moment for filesystem to sync
    time.Sleep(time.Second)

    // Open the snapshot for querying
    log.Println("Opening connection to snapshot database...")
    db, err = sql.Open("sqlite", "/tmp/snapshot.db")
    if err != nil {
        log.Fatalf("Failed to open snapshot: %v", err)
    }
    defer db.Close()

    // Get query parameters
    sqlQuery := os.Getenv("SQL_QUERY")
    tableName := strings.ReplaceAll(os.Getenv("TABLE_NAME"), "\"", "")
    resourceName := os.Getenv("RESOURCE_NAME")
    outputFormat := os.Getenv("OUTPUT_FORMAT")

    if sqlQuery != "" {
        // Execute custom SQL query
        log.Printf("Executing custom SQL query: %s", sqlQuery)

        rows, err := db.QueryContext(ctx, sqlQuery)
        if err != nil {
            log.Fatalf("Query error: %v", err)
        }
        defer rows.Close()

        // Get column names
        columns, err := rows.Columns()
        if err != nil {
            log.Fatalf("Failed to get column names: %v", err)
        }

        // Prepare result storage
        values := make([]interface{}, len(columns))
        valuePtrs := make([]interface{}, len(columns))
        for i := range columns {
            valuePtrs[i] = &values[i]
        }

        results := []map[string]interface{}{}

        // Fetch all rows
        for rows.Next() {
            err := rows.Scan(valuePtrs...)
            if err != nil {
                log.Fatalf("Error scanning row: %v", err)
            }

            // Convert to map for each row
            row := make(map[string]interface{})
            for i, col := range columns {
                val := values[i]

                // Handle nil values
                if val == nil {
                    row[col] = nil
                } else {
                    // Handle different value types
                    switch v := val.(type) {
                    case []byte:
                        row[col] = string(v)
                    default:
                        row[col] = v
                    }
                }
            }
            results = append(results, row)
        }

        if err := rows.Err(); err != nil {
            log.Fatalf("Error iterating rows: %v", err)
        }

        // Output results
        if strings.ToLower(outputFormat) == "json" {
            jsonOutput, err := json.MarshalIndent(results, "", "  ")
            if err != nil {
                log.Fatalf("Error marshaling to JSON: %v", err)
            }
            fmt.Println(string(jsonOutput))
        } else {
            // Default to plain text format
            if len(results) == 0 {
                fmt.Println("No results found")
            } else {
                // Print header
                for i, col := range columns {
                    if i > 0 {
                        fmt.Print("\t")
                    }
                    fmt.Print(col)
                }
                fmt.Println()

                // Print separator
                for i, col := range columns {
                    if i > 0 {
                        fmt.Print("\t")
                    }
                    fmt.Print(strings.Repeat("-", len(col)))
                }
                fmt.Println()

                // Print rows
                for _, result := range results {
                    for i, col := range columns {
                        if i > 0 {
                            fmt.Print("\t")
                        }
                        value := result[col]
                        if value == nil {
                            fmt.Print("<nil>")
                        } else {
                            fmt.Print(value)
                        }
                    }
                    fmt.Println()
                }

                fmt.Printf("\n%d row(s) returned\n", len(results))
            }
        }
    } else if tableName != "" && resourceName != "" {
        // Legacy mode: search for a specific resource in a table
        log.Printf("Querying table '%s' for resource '%s'", tableName, resourceName)

        query := fmt.Sprintf("SELECT \"metadata.name\" FROM \"%s\" WHERE \"metadata.name\" = ?", tableName)
        stmt, err := db.PrepareContext(ctx, query)
        if err != nil {
            log.Fatalf("Failed to prepare query: %v", err)
        }
        defer stmt.Close()

        var result string
        err = stmt.QueryRowContext(ctx, resourceName).Scan(&result)
        if err != nil {
            if errors.Is(err, sql.ErrNoRows) {
                log.Printf("Resource '%s' not found in table '%s'", resourceName, tableName)
                fmt.Printf("Resource not found\n")
            } else {
                log.Printf("Query error: %v", err)
                fmt.Printf("Query error: %v\n", err)
            }
        } else {
            log.Printf("Found resource '%s' in table '%s'", result, tableName)
            fmt.Printf("Found resource: %s\n", result)
        }
    } else {
        log.Println("No valid query parameters provided")
        fmt.Println("Usage options:")
        fmt.Println("1. Set SQL_QUERY environment variable for custom SQL queries")
        fmt.Println("   Optional: Set OUTPUT_FORMAT=json for JSON output")
        fmt.Println("2. Set TABLE_NAME and RESOURCE_NAME environment variables for resource lookup")
    }

    // Clean up
    log.Println("Cleaning up snapshot...")
    os.Remove("/tmp/snapshot.db")
    log.Println("Query operation completed")
}
EOF

    for i in $(seq 1 $MAX_RETRIES); do
        log "Building vai-query (attempt $i of $MAX_RETRIES)..."

        # Get dependencies with timeout
        if ! run_with_timeout "go get github.com/pkg/errors" $BUILD_TIMEOUT; then
            error "Failed to get pkg/errors dependency (attempt $i)"
            continue
        fi

        if ! run_with_timeout "go get modernc.org/sqlite" $BUILD_TIMEOUT; then
            error "Failed to get sqlite dependency (attempt $i)"
            continue
        fi

        # Build with timeout
        if run_with_timeout "go build -o /usr/local/bin/vai-query main.go" $BUILD_TIMEOUT; then
            log "vai-query built successfully"
            return 0
        else
            error "Build failed (attempt $i)"
        fi
    done

    error "Failed to build vai-query after $MAX_RETRIES attempts"
    return 1
}

# Main execution starts here
log "Starting script execution..."

# Ensure cache directory exists
mkdir -p $CACHE_DIR

# Install Go if needed
if ! check_go; then
    log "Go not found. Installing Go..."
    if ! install_go; then
        error "Failed to install Go. Exiting."
        exit 1
    fi
fi

# Always set the PATH to include Go
export PATH=$PATH:/usr/local/go/bin

log "Checking Go version:"
go version

# Build vai-query if needed
if [ ! -f /usr/local/bin/vai-query ]; then
    log "vai-query not found. Building vai-query program..."
    if ! build_vai_query; then
        error "Failed to build vai-query. Exiting."
        exit 1
    fi
else
    log "vai-query program already exists. Using existing binary."
fi

log "Executing the query program..."
SQL_QUERY="${SQL_QUERY}" TABLE_NAME="${TABLE_NAME}" RESOURCE_NAME="${RESOURCE_NAME}" OUTPUT_FORMAT="${OUTPUT_FORMAT}" /usr/local/bin/vai-query

log "Script execution completed."