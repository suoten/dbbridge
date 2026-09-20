package migrationlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	types "dbbridge/pkg"
)

// 验证日志文件创建、写入与内容格式；writer 为 nil 时静默跳过不 panic
func TestWriterRoundTrip(t *testing.T) {
	w, err := NewWriter()
	if err != nil {
		t.Fatalf("NewWriter 失败: %v", err)
	}
	if w.Path() == "" {
		t.Fatal("日志文件路径不应为空")
	}
	if filepath.Base(w.Path()) == "logs" {
		t.Fatalf("路径应包含日志文件名: %s", w.Path())
	}

	// nil 安全：不应 panic
	var nilWriter *Writer
	nilWriter.Write(types.LogEntry{Level: "INFO", Message: "nil test"})

	w.Write(types.LogEntry{Time: "2026-09-20 14:51:12", Level: "INFO", Table: "ba_icd_jbbm", Message: "数据迁移完成"})
	w.Write(types.LogEntry{Time: "2026-09-20 14:51:12", Level: "ERROR", Message: "包含\n换行的错误详情"})

	if err := w.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	// 重复 Close 应幂等
	if err := w.Close(); err != nil {
		t.Fatalf("重复 Close 应幂等: %v", err)
	}

	data, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "2026-09-20 14:51:12 [INFO] ba_icd_jbbm: 数据迁移完成") {
		t.Errorf("带表名的日志行格式不符:\n%s", content)
	}
	if !strings.Contains(content, "[ERROR] : 包含\n换行的错误详情") {
		t.Errorf("无表名/多行错误日志格式不符:\n%s", content)
	}
}
