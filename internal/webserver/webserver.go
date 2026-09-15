// Package webserver 提供无界面（headless）Web 服务模式。
//
// 用途：Linux/服务器部署（systemd 容器等）无法运行 Wails GUI，
// `--web` 模式以标准 HTTP 服务对外提供同一套迁移能力，
// 前端可通过 REST API（或脚本/curl）调用。
// 服务同时托管 frontend/dist 静态资源，浏览器可直接访问首页。
package webserver

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"dbbridge/internal/service"
	"dbbridge/internal/sqllint"
	types "dbbridge/pkg"
)

// Server headless Web 服务
type Server struct {
	conn    *service.ConnectionService
	mig     *service.MigrationService
	backup  *service.BackupService
	sql     *service.SQLService
	version string
}

// Run 启动 Web 服务（阻塞）
func Run(port int, assets embed.FS, version string) {
	s := &Server{
		conn:    service.NewConnectionService(),
		mig:     service.NewMigrationService(),
		backup:  service.NewBackupService(),
		sql:     service.NewSQLService(),
		version: version,
	}

	mux := http.NewServeMux()

	// API 路由
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/version", s.handleVersion)
	mux.HandleFunc("/api/databases", s.handleDatabases)
	mux.HandleFunc("/api/test-connection", s.handleTestConnection)
	mux.HandleFunc("/api/tables", s.handleTables)
	mux.HandleFunc("/api/schema", s.handleSchema)
	mux.HandleFunc("/api/migrate", s.handleMigrate)
	mux.HandleFunc("/api/cancel", s.handleCancel)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/backups", s.handleBackups)
	mux.HandleFunc("/api/backups/restore", s.handleRestoreBackup)
	mux.HandleFunc("/api/backups/delete", s.handleDeleteBackup)
	mux.HandleFunc("/api/convert-sql", s.handleConvertSQL)
	mux.HandleFunc("/api/lint-sql", s.handleLintSQL)
	mux.HandleFunc("/api/validate", s.handleValidate)
	mux.HandleFunc("/api/guide", s.handleGuide)
	mux.HandleFunc("/api/compat", s.handleCompat)
	mux.HandleFunc("/api/connstr", s.handleConnStr)

	// 静态资源（前端 SPA）
	dist, err := fs.Sub(assets, "frontend/dist")
	if err == nil {
		fileServer := http.FileServer(http.FS(dist))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// SPA 路由回退：非 API 路径找不到文件时回退到 index.html
			path := strings.TrimPrefix(r.URL.Path, "/")
			if path != "" {
				if _, err := fs.Stat(dist, path); err != nil {
					r.URL.Path = "/"
				}
			}
			fileServer.ServeHTTP(w, r)
		})
	} else {
		log.Printf("警告: 前端静态资源加载失败: %v", err)
	}

	addr := fmt.Sprintf(":%d", port)
	log.Printf("DBBridge Web 服务已启动 (版本 %s): http://0.0.0.0%s", version, addr)
	log.Printf("API: GET /api/health, GET /api/version, GET /api/databases,")
	log.Printf("  POST /api/test-connection {ConnectionConfig}, POST /api/tables {ConnectionConfig},")
	log.Printf("  POST /api/schema {config, table}, POST /api/migrate {MigrationConfig}（阻塞，返回报告）,")
	log.Printf("  POST /api/cancel, GET /api/status, GET /api/backups {config},")
	log.Printf("  POST /api/backups/restore {config, backupName|backupNames}, POST /api/backups/delete {config, backupName},")
	log.Printf("  POST /api/convert-sql {sourceDialect, targetDialect, sql}, POST /api/lint-sql {sourceDialect, targetDialect, sql},")
	log.Printf("  POST /api/validate {source, target, tables?, sampleSize?},")
	log.Printf("  POST /api/guide {config, targetDialect, tables?}, POST /api/compat {source, target, tables?}, POST /api/connstr {config}")

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Web 服务退出: %v", err)
	}
}

// ====================================================================
// 通用工具
// ====================================================================

// writeJSON 输出 JSON 响应
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError 输出错误响应
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"success": "false", "error": msg})
}

// decodeBody 解析请求体 JSON 到 v
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "请求体 JSON 解析失败: "+err.Error())
		return false
	}
	return true
}

// ====================================================================
// 处理器
// ====================================================================

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.version})
}

func (s *Server) handleDatabases(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"databases": s.conn.GetSupportedDatabases()})
}

func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	var config types.ConnectionConfig
	if !decodeBody(w, r, &config) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	version, err := s.conn.TestConnection(ctx, config)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "version": version})
}

func (s *Server) handleTables(w http.ResponseWriter, r *http.Request) {
	var config types.ConnectionConfig
	if !decodeBody(w, r, &config) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	tables, err := s.conn.GetTables(ctx, config)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "tables": tables})
}

func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config types.ConnectionConfig `json:"config"`
		Table  string                 `json:"table"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	schema, err := s.conn.GetTableSchema(ctx, req.Config, req.Table)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "schema": schema})
}

func (s *Server) handleMigrate(w http.ResponseWriter, r *http.Request) {
	var config types.MigrationConfig
	if !decodeBody(w, r, &config) {
		return
	}
	// 阻塞执行；日志打到服务端 stdout（服务器场景下可 journalctl 查看）
	report, err := s.mig.Run(config,
		func(info types.ProgressInfo) {
			log.Printf("[进度] %s 表:%s %d/%d (%.1f%%)",
				info.Phase, info.CurrentTable, info.ProcessedRows, info.TotalRows, info.Percent)
		},
		func(entry types.LogEntry) {
			log.Printf("[%s] %s %s", entry.Level, entry.Table, entry.Message)
		},
	)
	if err != nil && report == nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleCancel(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": s.mig.Cancel()})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"running": s.mig.IsRunning()})
}

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request) {
	var config types.ConnectionConfig
	if !decodeBody(w, r, &config) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	backups, err := s.backup.GetBackupTables(ctx, config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": backups})
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config      types.ConnectionConfig `json:"config"`
		BackupName  string                 `json:"backupName"`
		BackupNames []string               `json:"backupNames"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	if len(req.BackupNames) > 0 {
		result := s.backup.RestoreAllTables(ctx, req.Config, req.BackupNames)
		writeJSON(w, http.StatusOK, result)
		return
	}
	originalName, err := s.backup.RestoreTable(ctx, req.Config, req.BackupName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "restored": originalName})
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config     types.ConnectionConfig `json:"config"`
		BackupName string                 `json:"backupName"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := s.backup.DeleteBackup(ctx, req.Config, req.BackupName); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// handleConvertSQL SQL 脚本方言转换
func (s *Server) handleConvertSQL(w http.ResponseWriter, r *http.Request) {
	var req service.ConvertSQLRequest
	if !decodeBody(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, s.sql.ConvertSQL(req))
}

// handleLintSQL SQL 方言兼容性体检
func (s *Server) handleLintSQL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceDialect string `json:"sourceDialect"`
		TargetDialect string `json:"targetDialect"`
		SQL           string `json:"sql"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	source, err := service.ParseDialect(req.SourceDialect)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, err := service.ParseDialect(req.TargetDialect)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sqllint.Lint(source, target, req.SQL))
}

// handleValidate 切换前数据校验（只读不动数据）
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	var req service.ValidateRequest
	if !decodeBody(w, r, &req) {
		return
	}
	// 全库行数统计+抽样比对可能较慢，给足超时
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	writeJSON(w, http.StatusOK, s.sql.ValidateData(ctx, req))
}

// handleGuide 迁移适配指南（只读源库元数据 + 转换预演）
func (s *Server) handleGuide(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config        types.ConnectionConfig `json:"config"`
		TargetDialect string                 `json:"targetDialect"`
		Tables        []string               `json:"tables,omitempty"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	writeJSON(w, http.StatusOK, s.sql.GenerateMigrationGuide(ctx, req.Config, req.TargetDialect, req.Tables))
}

// handleCompat 迁移后结构兼容性检查（只读）
func (s *Server) handleCompat(w http.ResponseWriter, r *http.Request) {
	var req service.CheckCompatParams
	if !decodeBody(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	writeJSON(w, http.StatusOK, s.sql.CheckCompatibility(ctx, req))
}

// handleConnStr 连接字符串生成（纯函数不触库）
func (s *Server) handleConnStr(w http.ResponseWriter, r *http.Request) {
	var req types.ConnectionConfig
	if !decodeBody(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, s.sql.GenerateConnStrings(req))
}
