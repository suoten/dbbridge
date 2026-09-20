// Package migrationlog 提供迁移日志的本地文件落盘能力。
//
// 迁移日志在前端界面受屏幕空间限制（长错误信息需要悬停才能看全），
// 失败场景不便查看与复制。本包把每次迁移的完整日志（含失败详情）
// 同步写入用户数据目录下的日志文件，便于事后排查、复制与归档。
package migrationlog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	types "dbbridge/pkg"
)

// Writer 并发安全的迁移日志文件写入器
type Writer struct {
	mu   sync.Mutex
	file *os.File
	path string
}

// NewWriter 在用户数据目录 %AppData%/DBBridge/logs/ 下创建本次迁移的日志文件。
// 文件名格式 migration_20060102_150405.log。创建失败返回错误（调用方降级为
// 仅界面日志，不阻塞迁移）。
func NewWriter() (*Writer, error) {
	dataDir, err := os.UserConfigDir()
	if err != nil {
		dataDir = os.TempDir()
	}
	logDir := filepath.Join(dataDir, "DBBridge", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}
	path := filepath.Join(logDir, fmt.Sprintf("migration_%s.log", time.Now().Format("20060102_150405")))
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("创建日志文件失败: %w", err)
	}
	return &Writer{file: f, path: path}, nil
}

// Write 追加一条日志（线程安全；writer 为 nil 时静默跳过，方便调用方省判空）
func (w *Writer) Write(entry types.LogEntry) {
	if w == nil || w.file == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	fmt.Fprintf(w.file, "%s [%s] %s: %s\n", entry.Time, entry.Level, entry.Table, entry.Message)
}

// Path 返回日志文件完整路径
func (w *Writer) Path() string {
	if w == nil {
		return ""
	}
	return w.path
}

// Close 关闭日志文件
func (w *Writer) Close() error {
	if w == nil || w.file == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.file.Close()
	w.file = nil
	return err
}
