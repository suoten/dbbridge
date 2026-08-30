package history

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// MigrationRecord 迁移历史记录
type MigrationRecord struct {
	ID            int64           `json:"id"`
	StartTime     string          `json:"startTime"`
	EndTime       string          `json:"endTime"`
	Duration      string          `json:"duration"`
	SourceType    string          `json:"sourceType"`
	SourceHost    string          `json:"sourceHost"`
	SourceDB      string          `json:"sourceDB"`
	TargetType    string          `json:"targetType"`
	TargetHost    string          `json:"targetHost"`
	TargetDB      string          `json:"targetDB"`
	TablesTotal   int             `json:"tablesTotal"`
	TablesSuccess int             `json:"tablesSuccess"`
	TablesFailed  int             `json:"tablesFailed"`
	TotalRows     int64           `json:"totalRows"`
	Status        string          `json:"status"` // "success", "failed", "partial"
	TableDetails  json.RawMessage `json:"tableDetails,omitempty"`
	Backups       json.RawMessage `json:"backups,omitempty"`
	CreatedAt     string          `json:"createdAt"`
}

// Store 迁移历史存储
type Store struct {
	db *sql.DB
}

// NewStore 创建历史记录存储（使用用户数据目录下的 SQLite 文件）
func NewStore() (*Store, error) {
	// 获取用户数据目录
	dataDir, err := os.UserConfigDir()
	if err != nil {
		dataDir = os.TempDir()
	}
	appDir := filepath.Join(dataDir, "DBBridge")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}

	dbPath := filepath.Join(appDir, "history.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开历史数据库失败: %w", err)
	}

	// 初始化表结构
	if err := initSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

func initSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS migration_records (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		start_time TEXT NOT NULL,
		end_time TEXT NOT NULL,
		duration TEXT NOT NULL,
		source_type TEXT NOT NULL,
		source_host TEXT NOT NULL,
		source_db TEXT NOT NULL,
		target_type TEXT NOT NULL,
		target_host TEXT NOT NULL,
		target_db TEXT NOT NULL,
		tables_total INTEGER NOT NULL DEFAULT 0,
		tables_success INTEGER NOT NULL DEFAULT 0,
		tables_failed INTEGER NOT NULL DEFAULT 0,
		total_rows INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'unknown',
		table_details TEXT,
		backups TEXT,
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_migration_created ON migration_records(created_at DESC);
	`
	_, err := db.Exec(schema)
	return err
}

// SaveRecord 保存一条迁移记录
func (s *Store) SaveRecord(record MigrationRecord) error {
	if record.CreatedAt == "" {
		record.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
	}
	tableDetails, _ := json.Marshal(record.TableDetails)
	backups, _ := json.Marshal(record.Backups)

	// 如果 JSON 是 null，存空字符串
	tdStr := string(tableDetails)
	if tdStr == "null" {
		tdStr = ""
	}
	bkStr := string(backups)
	if bkStr == "null" {
		bkStr = ""
	}

	_, err := s.db.Exec(`
		INSERT INTO migration_records
		(start_time, end_time, duration, source_type, source_host, source_db,
		 target_type, target_host, target_db, tables_total, tables_success,
		 tables_failed, total_rows, status, table_details, backups, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		record.StartTime, record.EndTime, record.Duration,
		record.SourceType, record.SourceHost, record.SourceDB,
		record.TargetType, record.TargetHost, record.TargetDB,
		record.TablesTotal, record.TablesSuccess, record.TablesFailed,
		record.TotalRows, record.Status, tdStr, bkStr, record.CreatedAt,
	)
	return err
}

// GetRecords 获取所有迁移记录（按时间倒序）
func (s *Store) GetRecords() ([]MigrationRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, start_time, end_time, duration, source_type, source_host, source_db,
		       target_type, target_host, target_db, tables_total, tables_success,
		       tables_failed, total_rows, status, table_details, backups, created_at
		FROM migration_records
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []MigrationRecord
	for rows.Next() {
		var r MigrationRecord
		var tableDetails, backups sql.NullString
		err := rows.Scan(
			&r.ID, &r.StartTime, &r.EndTime, &r.Duration,
			&r.SourceType, &r.SourceHost, &r.SourceDB,
			&r.TargetType, &r.TargetHost, &r.TargetDB,
			&r.TablesTotal, &r.TablesSuccess, &r.TablesFailed,
			&r.TotalRows, &r.Status, &tableDetails, &backups, &r.CreatedAt,
		)
		if err != nil {
			continue
		}
		if tableDetails.Valid && tableDetails.String != "" {
			r.TableDetails = json.RawMessage(tableDetails.String)
		}
		if backups.Valid && backups.String != "" {
			r.Backups = json.RawMessage(backups.String)
		}
		records = append(records, r)
	}
	return records, nil
}

// DeleteRecord 删除一条记录
func (s *Store) DeleteRecord(id int64) error {
	_, err := s.db.Exec("DELETE FROM migration_records WHERE id = ?", id)
	return err
}

// Close 关闭存储
func (s *Store) Close() error {
	return s.db.Close()
}
