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
	"strconv"
	"strings"
	"sync"
	"time"

	"dbbridge/internal/history"
	"dbbridge/internal/service"
	"dbbridge/internal/sqllint"
	types "dbbridge/pkg"
)

// maxProgLogs 进度日志环形缓冲上限（与桌面端 ProgressPanel MAX_LOGS 一致）
const maxProgLogs = 500

// progLog 带序号的进度日志（序号用于增量拉取）
type progLog struct {
	seq   int64
	entry types.LogEntry
}

// Server headless Web 服务
type Server struct {
	conn    *service.ConnectionService
	mig     *service.MigrationService
	backup  *service.BackupService
	sql     *service.SQLService
	version string
	store   *history.Store

	// 迁移进度快照中心：Web 模式没有 Wails 事件推送，
	// 前端通过轮询 /api/progress 获取进度与日志（同进程仅一个迁移任务）
	progMu      sync.Mutex
	progInfo    types.ProgressInfo
	progHasInfo bool
	progLogs    []progLog
	progNextSeq int64
}

// Run 启动 Web 服务（阻塞）
func Run(port int, assets embed.FS, version string) {
	// 迁移历史存储（与桌面模式同一套 SQLite 存储，位于用户配置目录）
	store, err := history.NewStore()
	if err != nil {
		log.Printf("初始化迁移历史存储失败（历史功能不可用）: %v", err)
	}

	s := &Server{
		conn:    service.NewConnectionService(),
		mig:     service.NewMigrationService(),
		backup:  service.NewBackupService(),
		sql:     service.NewSQLService(),
		version: version,
		store:   store,
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
	mux.HandleFunc("/api/progress", s.handleProgress)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/history/delete", s.handleHistoryDelete)
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
	log.Printf("  POST /api/cancel, GET /api/status, GET /api/progress?sinceSeq=N,")
	log.Printf("  GET /api/history, POST /api/history/delete {id},")
	log.Printf("  POST /api/backups {config},")
	log.Printf("  POST /api/backups/restore {config, backupName|backupNames}, POST /api/backups/delete {config, backupName},")
	log.Printf("  POST /api/convert-sql {sourceDialect, targetDialect, sql}, POST /api/lint-sql {sourceDialect, targetDialect, sql},")
	log.Printf("  POST /api/validate {source, target, tables?, sampleSize?},")
	log.Printf("  POST /api/guide {config, targetDialect, tables?}, POST /api/compat {source, target, tables?}, POST /api/connstr {config}")

	srv := &http.Server{
		Addr:              addr,
		Handler:           s.withSecurityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      0, // 迁移接口需长连接，逐处理器控制超时
		IdleTimeout:       120 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Web 服务退出: %v", err)
	}
}

// withSecurityHeaders 添加安全响应头中间件
func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
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

// writeError 输出错误响应（success 必须是布尔，前端按真值判断成败）
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"success": false, "error": msg})
}

// maxBodySize 单次请求体上限（10 MB，SQL 脚本转换可能较大）
const maxBodySize = 10 << 20

// decodeBody 解析请求体 JSON 到 v
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
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

// requireMethod 检查请求方法是否为 POST，否则返回 405
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		w.Header().Set("Allow", method)
		writeError(w, http.StatusMethodNotAllowed, "仅支持 "+method+" 方法")
		return false
	}
	return true
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"version": s.version})
}

func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"databases": s.conn.GetSupportedDatabases()})
}

func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
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
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
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
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
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
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var config types.MigrationConfig
	if !decodeBody(w, r, &config) {
		return
	}
	// 重置进度快照，避免残留上一次任务的数据
	s.resetProgress()
	// 阻塞执行；日志打到服务端 stdout（服务器场景下可 journalctl 查看），
	// 同时写入进度快照中心供 /api/progress 轮询（Web 模式没有 Wails 事件推送）
	// 迁移大表可能持续数小时，给足超时
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Hour)
	defer cancel()
	report, err := s.mig.RunWithContext(ctx, config,
		func(info types.ProgressInfo) {
			s.recordProgress(info)
			log.Printf("[进度] %s 表:%s %d/%d (%.1f%%)",
				info.Phase, info.CurrentTable, info.ProcessedRows, info.TotalRows, info.Percent)
		},
		func(entry types.LogEntry) {
			s.recordLog(entry)
			log.Printf("[%s] %s %s", entry.Level, entry.Table, entry.Message)
		},
	)
	// 迁移历史落库（与桌面模式共用 BuildRecord）
	if s.store != nil && report != nil {
		record := history.BuildRecord(config.Source, config.Target, report, err)
		if saveErr := s.store.SaveRecord(record); saveErr != nil {
			log.Printf("保存迁移历史失败: %v", saveErr)
		}
	}
	if err != nil && report == nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": s.mig.Cancel()})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"running": s.mig.IsRunning()})
}

// handleProgress 迁移进度快照（Web 模式轮询替代 Wails migration:progress/migration:log 事件）
// GET /api/progress?sinceSeq=N → {running, hasProgress, progress, logs, lastSeq}，
// logs 仅返回序号大于 sinceSeq 的增量条目
func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	sinceSeq, _ := strconv.ParseInt(r.URL.Query().Get("sinceSeq"), 10, 64)
	running, hasInfo, info, logs, lastSeq := s.progressSnapshot(sinceSeq)
	writeJSON(w, http.StatusOK, map[string]any{
		"running":     running,
		"hasProgress": hasInfo,
		"progress":    info,
		"logs":        logs,
		"lastSeq":     lastSeq,
	})
}

// handleHistory 迁移历史列表
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if s.store == nil {
		writeJSON(w, http.StatusOK, []history.MigrationRecord{})
		return
	}
	records, err := s.store.GetRecords()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if records == nil {
		records = []history.MigrationRecord{}
	}
	writeJSON(w, http.StatusOK, records)
}

// handleHistoryDelete 删除单条迁移历史
func (s *Server) handleHistoryDelete(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if s.store == nil {
		writeError(w, http.StatusInternalServerError, "迁移历史存储不可用")
		return
	}
	if err := s.store.DeleteRecord(req.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ====================================================================
// 迁移进度快照中心
// ====================================================================

// resetProgress 清空进度快照（新任务开始前调用）
func (s *Server) resetProgress() {
	s.progMu.Lock()
	defer s.progMu.Unlock()
	s.progInfo = types.ProgressInfo{}
	s.progHasInfo = false
	s.progLogs = nil
	s.progNextSeq = 0
}

// recordProgress 记录最新进度快照
func (s *Server) recordProgress(info types.ProgressInfo) {
	s.progMu.Lock()
	defer s.progMu.Unlock()
	s.progInfo = info
	s.progHasInfo = true
}

// recordLog 追加进度日志（环形缓冲，超上限丢弃最旧条目）
func (s *Server) recordLog(entry types.LogEntry) {
	s.progMu.Lock()
	defer s.progMu.Unlock()
	s.progNextSeq++
	s.progLogs = append(s.progLogs, progLog{seq: s.progNextSeq, entry: entry})
	if len(s.progLogs) > maxProgLogs {
		s.progLogs = s.progLogs[len(s.progLogs)-maxProgLogs:]
	}
}

// progressSnapshot 返回进度快照与 sinceSeq 之后的增量日志
func (s *Server) progressSnapshot(sinceSeq int64) (running bool, hasInfo bool, info types.ProgressInfo, logs []types.LogEntry, lastSeq int64) {
	s.progMu.Lock()
	defer s.progMu.Unlock()
	running = s.mig.IsRunning()
	hasInfo = s.progHasInfo
	info = s.progInfo
	lastSeq = s.progNextSeq
	logs = []types.LogEntry{}
	for _, pl := range s.progLogs {
		if pl.seq > sinceSeq {
			logs = append(logs, pl.entry)
		}
	}
	return
}

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	// 请求体为 {"config": {...}}（与 /api/backups/restore、/api/backups/delete 一致，
	// 也与 README API 文档和 Web 模式桥接 shim 的约定一致）
	var req struct {
		Config types.ConnectionConfig `json:"config"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	backups, err := s.backup.GetBackupTables(ctx, req.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": backups})
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
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
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
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
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req service.ConvertSQLRequest
	if !decodeBody(w, r, &req) {
		return
	}
	// SQL 转换是纯 CPU 操作，用 goroutine+select 做超时保护
	type result struct {
		r *service.ConvertSQLResult
	}
	done := make(chan result, 1)
	go func() {
		done <- result{s.sql.ConvertSQL(req)}
	}()
	select {
	case res := <-done:
		writeJSON(w, http.StatusOK, res.r)
	case <-time.After(5 * time.Minute):
		writeError(w, http.StatusGatewayTimeout, "SQL 转换超时（5 分钟）")
	}
}

// handleLintSQL SQL 方言兼容性体检
func (s *Server) handleLintSQL(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
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
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
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
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
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
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
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
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req types.ConnectionConfig
	if !decodeBody(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, s.sql.GenerateConnStrings(req))
}
