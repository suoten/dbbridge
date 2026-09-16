package main

import (
	"context"
	"encoding/json"
	"time"

	// 导入适配器包，触发 init() 自动注册
	// 第一阶段：开源主流
	_ "dbbridge/internal/adapter/mysql"
	_ "dbbridge/internal/adapter/postgres"
	_ "dbbridge/internal/adapter/sqlite"
	_ "dbbridge/internal/adapter/mariadb"
	_ "dbbridge/internal/adapter/oceanbase"
	// 第二阶段：云原生与国产化
	_ "dbbridge/internal/adapter/tidb"
	_ "dbbridge/internal/adapter/polardb"
	_ "dbbridge/internal/adapter/opengauss"
	_ "dbbridge/internal/adapter/dameng"
	_ "dbbridge/internal/adapter/kingbase"
	_ "dbbridge/internal/adapter/aurora"
	_ "dbbridge/internal/adapter/cockroachdb"
	// 第三阶段：主流商业与 NoSQL
	_ "dbbridge/internal/adapter/oracle"
	_ "dbbridge/internal/adapter/mssql"
	_ "dbbridge/internal/adapter/db2"
	_ "dbbridge/internal/adapter/mongodb"
	_ "dbbridge/internal/adapter/redis"
	_ "dbbridge/internal/adapter/cassandra"
	_ "dbbridge/internal/adapter/scylladb"
	_ "dbbridge/internal/adapter/influxdb"
	_ "dbbridge/internal/adapter/timescaledb"
	_ "dbbridge/internal/adapter/tdengine"
	// 第四阶段：桌面/文件型数据库
	_ "dbbridge/internal/adapter/access"
	"dbbridge/internal/history"
	"dbbridge/internal/service"
	"dbbridge/internal/sqllint"
	types "dbbridge/pkg"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ====================================================================
// DTO：前端请求/响应结构（仅做 Wails 绑定层的数据搬运，无业务逻辑）
// ====================================================================

// ConnectionRequest 连接请求（前端统一使用）
type ConnectionRequest struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Database string `json:"database"`
	SSLMode  string `json:"sslMode"`
	Charset  string `json:"charset"`
	Instance string `json:"instance"` // MSSQL 实例名
}

// TestConnectionResult 测试连接结果
type TestConnectionResult struct {
	Success bool   `json:"success"`
	Version string `json:"version"`
	Error   string `json:"error,omitempty"`
}

// GetTablesResult 获取表列表结果
type GetTablesResult struct {
	Success bool              `json:"success"`
	Tables  []types.TableMeta `json:"tables,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// GetTableSchemaResult 获取表结构结果
type GetTableSchemaResult struct {
	Success bool               `json:"success"`
	Schema  *types.TableSchema `json:"schema,omitempty"`
	Error   string             `json:"error,omitempty"`
}

// StartMigrationRequest 开始迁移请求
type StartMigrationRequest struct {
	Source          ConnectionRequest `json:"source"`
	Target          ConnectionRequest `json:"target"`
	Tables          []string          `json:"tables,omitempty"`
	StructureOnly   bool              `json:"structureOnly"`
	DataOnly        bool              `json:"dataOnly"`
	BatchSize       int               `json:"batchSize"`
	Concurrency     int               `json:"concurrency"`
	DropIfExists    bool              `json:"dropIfExists"`
	IgnoreErrors    bool              `json:"ignoreErrors"`
	BackupBefore    bool              `json:"backupBefore"`
	AutoRollback    bool              `json:"autoRollback"`
	MigrateTriggers bool              `json:"migrateTriggers"`
	MigrateRoutines bool              `json:"migrateRoutines"`
}

// SimpleResult 通用操作结果
type SimpleResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// ====================================================================
// App：Wails 绑定层
// ====================================================================

// App 主应用结构
type App struct {
	ctx               context.Context
	store             *history.Store
	connectionService *service.ConnectionService
	migrationService  *service.MigrationService
	backupService     *service.BackupService
	sqlService        *service.SQLService
}

// NewApp 创建应用实例
func NewApp() *App {
	return &App{
		connectionService: service.NewConnectionService(),
		migrationService:  service.NewMigrationService(),
		backupService:     service.NewBackupService(),
		sqlService:        service.NewSQLService(),
	}
}

// startup 应用启动时调用
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// 初始化历史记录存储
	store, err := history.NewStore()
	if err != nil {
		wailsRuntime.LogErrorf(ctx, "初始化历史记录存储失败: %v", err)
	} else {
		a.store = store
	}
}

// toConfig DTO → 连接配置
func toConfig(req ConnectionRequest) types.ConnectionConfig {
	return types.ConnectionConfig{
		Type:     types.DatabaseType(req.Type),
		Host:     req.Host,
		Port:     req.Port,
		Username: req.Username,
		Password: req.Password,
		Database: req.Database,
		SSLMode:  req.SSLMode,
		Charset:  req.Charset,
		Instance: req.Instance,
	}
}

// ====================================================================
// 连接与元数据
// ====================================================================

// TestConnection 测试数据库连接
func (a *App) TestConnection(req ConnectionRequest) TestConnectionResult {
	// 连接探测加超时，避免目标库网络黑洞时 UI 永久等待
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	version, err := a.connectionService.TestConnection(ctx, toConfig(req))
	if err != nil {
		return TestConnectionResult{Success: false, Error: err.Error()}
	}
	return TestConnectionResult{Success: true, Version: version}
}

// GetTables 获取数据库的表列表
func (a *App) GetTables(req ConnectionRequest) GetTablesResult {
	ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
	defer cancel()
	tables, err := a.connectionService.GetTables(ctx, toConfig(req))
	if err != nil {
		return GetTablesResult{Success: false, Error: err.Error()}
	}
	return GetTablesResult{Success: true, Tables: tables}
}

// GetTableSchema 获取单个表的结构
func (a *App) GetTableSchema(req ConnectionRequest, tableName string) GetTableSchemaResult {
	ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
	defer cancel()
	schema, err := a.connectionService.GetTableSchema(ctx, toConfig(req), tableName)
	if err != nil {
		return GetTableSchemaResult{Success: false, Error: err.Error()}
	}
	return GetTableSchemaResult{Success: true, Schema: schema}
}

// GetSupportedDatabases 获取已支持的数据库类型列表
func (a *App) GetSupportedDatabases() []string {
	return a.connectionService.GetSupportedDatabases()
}

// ====================================================================
// 迁移
// ====================================================================

// StartMigration 开始迁移（阻塞执行，进度通过事件推送）
func (a *App) StartMigration(req StartMigrationRequest) *types.MigrationReport {
	config := types.MigrationConfig{
		Source:          toConfig(req.Source),
		Target:          toConfig(req.Target),
		Tables:          req.Tables,
		StructureOnly:   req.StructureOnly,
		DataOnly:        req.DataOnly,
		BatchSize:       req.BatchSize,
		Concurrency:     req.Concurrency,
		DropIfExists:    req.DropIfExists,
		IgnoreErrors:    req.IgnoreErrors,
		BackupBefore:    req.BackupBefore,
		AutoRollback:    req.AutoRollback,
		MigrateTriggers: req.MigrateTriggers,
		MigrateRoutines: req.MigrateRoutines,
	}

	report, err := a.migrationService.Run(config,
		func(info types.ProgressInfo) {
			wailsRuntime.EventsEmit(a.ctx, "migration:progress", info)
		},
		func(entry types.LogEntry) {
			wailsRuntime.EventsEmit(a.ctx, "migration:log", entry)
		},
	)

	if err != nil {
		if report == nil {
			report = &types.MigrationReport{Error: err.Error()}
		}
		wailsRuntime.LogErrorf(a.ctx, "迁移失败: %v", err)
	}

	// 保存迁移历史记录
	a.saveHistory(req, report, err)

	return report
}

// CancelMigration 取消当前迁移任务
func (a *App) CancelMigration() bool {
	cancelled := a.migrationService.Cancel()
	if cancelled {
		wailsRuntime.LogInfo(a.ctx, "用户取消了迁移任务")
	}
	return cancelled
}

// saveHistory 保存迁移历史到本地
func (a *App) saveHistory(req StartMigrationRequest, report *types.MigrationReport, migErr error) {
	if a.store == nil || report == nil {
		return
	}

	status := "success"
	// 迁移被拒绝/中止时 orchestrator 返回 (report, err)，report.Error 已带原因；
	// 也有 err==nil 但 report.Error 非空的路径（如取消），都要落为失败，不能写假 success
	if report.Error != "" {
		if report.TablesSuccess > 0 {
			status = "partial"
		} else {
			status = "failed"
		}
	} else if migErr != nil {
		if report.TablesSuccess > 0 {
			status = "partial"
		} else {
			status = "failed"
		}
	}

	// 源库地址
	sourceHost := req.Source.Host
	if req.Source.Type == "sqlite" {
		sourceHost = "(file)"
	}

	// 目标库地址
	targetHost := req.Target.Host
	if req.Target.Type == "sqlite" {
		targetHost = "(file)"
	}

	// 序列化表详情和备份
	var tableDetailsJSON, backupsJSON json.RawMessage
	if len(report.TableDetails) > 0 {
		if data, err := json.Marshal(report.TableDetails); err == nil {
			tableDetailsJSON = data
		} else {
			wailsRuntime.LogErrorf(a.ctx, "序列化表详情失败: %v", err)
		}
	}
	if len(report.Backups) > 0 {
		if data, err := json.Marshal(report.Backups); err == nil {
			backupsJSON = data
		} else {
			wailsRuntime.LogErrorf(a.ctx, "序列化备份信息失败: %v", err)
		}
	}

	record := history.MigrationRecord{
		StartTime:     report.StartTime,
		EndTime:       report.EndTime,
		Duration:      report.Duration,
		SourceType:    req.Source.Type,
		SourceHost:    sourceHost,
		SourceDB:      req.Source.Database,
		TargetType:    req.Target.Type,
		TargetHost:    targetHost,
		TargetDB:      req.Target.Database,
		TablesTotal:   report.TablesTotal,
		TablesSuccess: report.TablesSuccess,
		TablesFailed:  report.TablesFailed,
		TotalRows:     report.TotalRows,
		Status:        status,
		TableDetails:  tableDetailsJSON,
		Backups:       backupsJSON,
	}

	if err := a.store.SaveRecord(record); err != nil {
		wailsRuntime.LogErrorf(a.ctx, "保存迁移历史失败: %v", err)
	}
}

// GetMigrationHistory 获取所有迁移历史记录
func (a *App) GetMigrationHistory() []history.MigrationRecord {
	if a.store == nil {
		return []history.MigrationRecord{}
	}
	records, err := a.store.GetRecords()
	if err != nil {
		return []history.MigrationRecord{}
	}
	return records
}

// DeleteMigrationHistory 删除一条迁移历史记录
func (a *App) DeleteMigrationHistory(id int64) SimpleResult {
	if a.store == nil {
		return SimpleResult{Success: false, Error: "历史存储未初始化"}
	}
	if err := a.store.DeleteRecord(id); err != nil {
		return SimpleResult{Success: false, Error: err.Error()}
	}
	return SimpleResult{Success: true}
}

// ====================================================================
// 备份管理与回滚
// ====================================================================

// GetBackupTablesResult 获取备份表列表结果
type GetBackupTablesResult struct {
	Success bool                      `json:"success"`
	Tables  []service.BackupTableItem `json:"tables,omitempty"`
	Error   string                    `json:"error,omitempty"`
}

// GetBackupTables 获取目标库中所有备份表（以 _bak_ 开头的表）
func (a *App) GetBackupTables(req ConnectionRequest) GetBackupTablesResult {
	ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
	defer cancel()
	backups, err := a.backupService.GetBackupTables(ctx, toConfig(req))
	if err != nil {
		return GetBackupTablesResult{Success: false, Error: err.Error()}
	}
	return GetBackupTablesResult{Success: true, Tables: backups}
}

// RestoreTableResult 回滚结果
type RestoreTableResult struct {
	Success    bool   `json:"success"`
	BackupName string `json:"backupName"`
	Original   string `json:"original,omitempty"`
	Error      string `json:"error,omitempty"`
}

// RestoreRequest 回滚/删除备份请求（前端统一传单个对象）
type RestoreRequest struct {
	Connection  ConnectionRequest `json:"connection"`
	BackupName  string            `json:"backupName,omitempty"`
	BackupNames []string          `json:"backupNames,omitempty"`
}

// RestoreTable 从备份恢复单张表
// 注意：会删除当前同名表并将备份表重命名回原表名（丢弃迁移写入的新数据）
func (a *App) RestoreTable(req RestoreRequest) RestoreTableResult {
	ctx, cancel := context.WithTimeout(a.ctx, 120*time.Second)
	defer cancel()
	originalName, err := a.backupService.RestoreTable(ctx, toConfig(req.Connection), req.BackupName)
	if err != nil {
		return RestoreTableResult{Success: false, BackupName: req.BackupName, Error: err.Error()}
	}
	return RestoreTableResult{Success: true, BackupName: req.BackupName, Original: originalName}
}

// RestoreAllResult 批量回滚结果
type RestoreAllResult struct {
	SuccessCount int      `json:"successCount"`
	FailedCount  int      `json:"failedCount"`
	FailedItems  []string `json:"failedItems,omitempty"`
}

// RestoreAllTables 批量从备份恢复多张表
func (a *App) RestoreAllTables(req RestoreRequest) RestoreAllResult {
	ctx, cancel := context.WithTimeout(a.ctx, 300*time.Second)
	defer cancel()
	r := a.backupService.RestoreAllTables(ctx, toConfig(req.Connection), req.BackupNames)
	return RestoreAllResult{
		SuccessCount: r.SuccessCount,
		FailedCount:  r.FailedCount,
		FailedItems:  r.FailedItems,
	}
}

// DeleteBackup 删除一个备份表（确认迁移无误后清理）
func (a *App) DeleteBackup(req RestoreRequest) SimpleResult {
	ctx, cancel := context.WithTimeout(a.ctx, 120*time.Second)
	defer cancel()
	if err := a.backupService.DeleteBackup(ctx, toConfig(req.Connection), req.BackupName); err != nil {
		return SimpleResult{Success: false, Error: err.Error()}
	}
	return SimpleResult{Success: true}
}

// ====================================================================
// SQL 工具（脚本转换 / 方言体检 / 切换前数据校验）
// ====================================================================

// LintSQLRequest SQL 方言体检请求
type LintSQLRequest struct {
	SourceDialect string `json:"sourceDialect"`
	TargetDialect string `json:"targetDialect"`
	SQL           string `json:"sql"`
}

// ConvertSQL 把 SQL 脚本从源方言转换为目标方言（转换后自动附目标方言体检提示）
func (a *App) ConvertSQL(req service.ConvertSQLRequest) *service.ConvertSQLResult {
	return a.sqlService.ConvertSQL(req)
}

// LintSQL 对 SQL 脚本做源→目标方言兼容性体检（不改写内容，只报告问题）
func (a *App) LintSQL(req LintSQLRequest) *sqllint.Report {
	source, err := service.ParseDialect(req.SourceDialect)
	if err != nil {
		return &sqllint.Report{SourceDialect: req.SourceDialect, TargetDialect: req.TargetDialect}
	}
	target, err := service.ParseDialect(req.TargetDialect)
	if err != nil {
		return &sqllint.Report{SourceDialect: req.SourceDialect, TargetDialect: req.TargetDialect}
	}
	return sqllint.Lint(source, target, req.SQL)
}

// ValidateData 切换前数据校验（行数对比 + 抽样逐字段比对，只读不动数据）
func (a *App) ValidateData(req service.ValidateRequest) *service.ValidateReport {
	// 全库行数统计+抽样比对可能较慢，给足超时
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Minute)
	defer cancel()
	return a.sqlService.ValidateData(ctx, req)
}

// GenerateMigrationGuide 生成迁移适配指南（只读源库元数据 + 转换预演，不动数据）
func (a *App) GenerateMigrationGuide(source ConnectionRequest, targetDialect string, tables []string) *service.GuideReport {
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Minute)
	defer cancel()
	return a.sqlService.GenerateMigrationGuide(ctx, toConfig(source), targetDialect, tables)
}

// CheckCompatibilityRequest 兼容性检查请求
type CheckCompatibilityRequest struct {
	Source ConnectionRequest `json:"source"`
	Target ConnectionRequest `json:"target"`
	Tables []string          `json:"tables,omitempty"`
}

// CheckCompatibility 迁移后扫描目标库结构，与源库逐项对比（只读）
func (a *App) CheckCompatibility(req CheckCompatibilityRequest) *service.CompatReport {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Minute)
	defer cancel()
	return a.sqlService.CheckCompatibility(ctx, service.CheckCompatParams{
		Source: toConfig(req.Source),
		Target: toConfig(req.Target),
		Tables: req.Tables,
	})
}

// GenerateConnStrings 生成各语言连接字符串模板（纯函数不触库）
func (a *App) GenerateConnStrings(req ConnectionRequest) *service.ConnStrResult {
	return a.sqlService.GenerateConnStrings(toConfig(req))
}
