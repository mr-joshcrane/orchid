package minidb

import (
	"fmt"
)

// Table represents a single data table within the database.
type Table struct {
	rows    map[string]map[string]string
	// indexes structure: map[indexName]map[fieldName][]IDs
	indexes map[string]map[string][]string
}

// DB manages the collection of tables.
type DB struct {
	tables map[string]*Table
}

// NewDB creates and returns a new initialized database connection.
func NewDB() *DB {
	return &DB{tables: make(map[string]*Table)}
}

// CreateTable creates a new table in the database if it doesn't already exist.
func (db *DB) CreateTable(name string) error {
	if _, exists := db.tables[name]; exists {
		return fmt.Errorf("table %s already exists", name)
	}
	db.tables[name] = &Table{
		rows:    make(map[string]map[string]string),
		indexes: make(map[string]map[string][]string),
	}
	return nil
}

// Table retrieves an existing table by name or creates it if it does not exist.
func (db *DB) Table(name string) (*Table, error) {
	if t, exists := db.tables[name]; exists {
		return t, nil
	}
	t := &Table{
		rows:    make(map[string]map[string]string),
		indexes: make(map[string]map[string][]string),
	}
	db.tables[name] = t
	return t, nil
}

// Insert adds a new record to the table.
func (t *Table) Insert(id string, row map[string]string) error {
	if t.rows == nil {
		t.rows = make(map[string]map[string]string)
	}
	if t.rows[id] != nil {
		return fmt.Errorf("record with id %s already exists", id)
	}
	t.rows[id] = make(map[string]string)
	for field, value := range row {
		t.rows[id][field] = value
	}
	return nil
}

// Get retrieves a record by its ID. Returns the row and true if found.
func (t *Table) Get(id string) (map[string]string, bool) {
	row, ok := t.rows[id]
	return row, ok
}

// Delete removes a record by ID and cleans up all associated indexes.
func (t *Table) Delete(id string) error {
	if _, ok := t.rows[id]; !ok {
		return nil // ID not found, treat as success for deletion context
	}

	// 1. Delete from rows
	delete(t.rows, id)

	// 2. Delete from indexes
	for indexName, fieldIndexMap := range t.indexes {
		for fieldName, idList := range fieldIndexMap {
			// Filter out the deleted ID from the list
			filteredList := []string{}
			for _, existingID := range idList {
				if existingID != id {
					filteredList = append(filteredList, existingID)
				}
			}

			// Update the index only if elements were removed
			if len(filteredList) < len(idList) {
				t.indexes[indexName][fieldName] = filteredList
			}
		}
	}

	return nil
}

// Query finds all records matching a specific field value.
func (t *Table) Query(field string, value string) []map[string]string {
	results := []map[string]string{}
	for _, row := range t.rows {
		if row[field] == value {
			// Create a copy of the row to avoid modifying the original map
			resultRow := make(map[string]string)
			for k, v := range row {
				resultRow[k] = v
			}
			results = append(results, resultRow)
		}
	}
	return results
}

// AddIndex initializes an index structure for a given field name.
func (t *Table) AddIndex(field string) {
	if t.indexes == nil {
		t.indexes = make(map[string]map[string][]string)
	}
	// We use the field name itself as the index name for simplicity here.
	if _, ok := t.indexes[field]; !ok {
		t.indexes[field] = make(map[string][]string)
	}
}