package database

import (
	"database/sql"
	"fmt"
)

// Query provides methods for querying VAI database snapshots
type Query struct{}

// NewQuery creates a new Query helper
func NewQuery() *Query {
	return &Query{}
}

// Execute runs an arbitrary SQL query and returns the results
func (q *Query) Execute(snapshot *Snapshot, query string, args ...interface{}) (*QueryResult, error) {
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()

	rows, err := snapshot.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %v", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %v", err)
	}

	result := &QueryResult{
		Columns: columns,
		Rows:    make([]map[string]interface{}, 0),
	}

	values := make([]interface{}, len(columns))
	valuePtrs := make([]interface{}, len(columns))
	for i := range columns {
		valuePtrs[i] = &values[i]
	}

	for rows.Next() {
		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %v", err)
		}

		rowMap := make(map[string]interface{})
		for i, col := range columns {
			// Handle NULL values
			if values[i] != nil {
				// Handle []byte (common for SQLite)
				if b, ok := values[i].([]byte); ok {
					rowMap[col] = string(b)
				} else {
					rowMap[col] = values[i]
				}
			} else {
				rowMap[col] = nil
			}
		}
		result.Rows = append(result.Rows, rowMap)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %v", err)
	}

	return result, nil
}

// ExecuteScalar runs a query that returns a single value
func (q *Query) ExecuteScalar(snapshot *Snapshot, query string, args ...interface{}) (interface{}, error) {
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()

	var result interface{}
	err := snapshot.DB.QueryRow(query, args...).Scan(&result)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scalar query failed: %v", err)
	}

	// Handle []byte for SQLite
	if b, ok := result.([]byte); ok {
		return string(b), nil
	}

	return result, nil
}

// ExecuteCount runs a COUNT query and returns the integer result
func (q *Query) ExecuteCount(snapshot *Snapshot, query string, args ...interface{}) (int, error) {
	result, err := q.ExecuteScalar(snapshot, query, args...)
	if err != nil {
		return 0, err
	}

	if result == nil {
		return 0, nil
	}

	// Type assertion with various numeric types
	switch v := result.(type) {
	case int64:
		return int(v), nil
	case int:
		return v, nil
	case float64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("unexpected count result type: %T", result)
	}
}
