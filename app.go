package main

import (
	"context"
	"fmt"
	"time"

	// 导入适配器包，触发 init() 自动注册
	_ "dbbridge/internal/adapter/mysql"
	_ "dbbridge/internal/adapter/postgres"
	_ "dbbridge/internal/adapter/sqlite"
	"dbbridge/internal/orchestrator"
	"dbbridge/pkg"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App 主应用结构
type App struct {
	ctx context.Context
}

// NewApp 创建应用实例
func NewApp() *App {
	return &App{}
}

// startup 应用启动时调用
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// TestConnectionRequest 测试连接请求
type TestConnectionRequest struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Database string `json:"database"`
	SSLMode  string `json:"sslMode"`
	Charset  string `json:"charset"`
}

// TestConnectionResult 测试连接结果
type TestConnectionResult struct {
	Success bool   `json:"success"`
	Version string `json:"version"`
	Error   string `json:"error,omitempty"`
}

// TestConnection 测试数据库连接
func (a *App) TestConnection(req TestConnectionRequest) TestConnectionResult {
	dbType := types.DatabaseType(req.Type)
	adapter := types.NewAdapter(dbType)
	if adapter == nil {
		return TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("不支持的数据库类型: %s", req.Type),
		}
	}
	defer adapter.Close()

	config := types.ConnectionConfig{
		Type:     dbType,
		Host:     req.Host,
		Port:     req.Port,
		Username: req.Username,
		Password: req.Password,
		Database: req.Database,
		SSLMode:  req.SSLMode,
		Charset:  req.Charset,
	}

	if err := adapter.Connect(config); err != nil {
		return TestConnectionResult{
			Success: false,
			Error:   err.Error(),
		}
	}

	version, err := adapter.GetVersion()
	if err != nil {
		version = "unknown"
	}

	return TestConnectionResult{
		Success: true,
		Version: version,
	}
}

// GetTablesResult 获取表列表结果
type GetTablesResult struct {
	Success bool              `json:"success"`
	Tables  []types.TableMeta `json:"tables,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// GetTables 获取数据库的表列表
func (a *App) GetTables(req TestConnectionRequest) GetTablesResult {
	dbType := types.DatabaseType(req.Type)
	adapter := types.NewAdapter(dbType)
	if adapter == nil {
		return GetTablesResult{
			Success: false,
			Error:   fmt.Sprintf("不支持的数据库类型: %s", req.Type),
		}
	}
	defer adapter.Close()

	config := types.ConnectionConfig{
		Type:     dbType,
		Host:     req.Host,
		Port:     req.Port,
		Username: req.Username,
		Password: req.Password,
		Database: req.Database,
		SSLMode:  req.SSLMode,
		Charset:  req.Charset,
	}

	if err := adapter.Connect(config); err != nil {
		return GetTablesResult{
			Success: false,
			Error:   err.Error(),
		}
	}

	tables, err := adapter.GetTables()
	if err != nil {
		return GetTablesResult{
			Success: false,
			Error:   err.Error(),
		}
	}

	return GetTablesResult{
		Success: true,
		Tables:  tables,
	}
}

// GetTableSchemaResult 获取表结构结果
type GetTableSchemaResult struct {
	Success bool               `json:"success"`
	Schema *types.TableSchema  `json:"schema,omitempty"`
	Error  string              `json:"error,omitempty"`
}

// GetTableSchema 获取单个表的结构
func (a *App) GetTableSchema(req TestConnectionRequest, tableName string) GetTableSchemaResult {
	dbType := types.DatabaseType(req.Type)
	adapter := types.NewAdapter(dbType)
	if adapter == nil {
		return GetTableSchemaResult{
			Success: false,
			Error:   fmt.Sprintf("不支持的数据库类型: %s", req.Type),
		}
	}
	defer adapter.Close()

	config := types.ConnectionConfig{
		Type:     dbType,
		Host:     req.Host,
		Port:     req.Port,
		Username: req.Username,
		Password: req.Password,
		Database: req.Database,
		SSLMode:  req.SSLMode,
		Charset:  req.Charset,
	}

	if err := adapter.Connect(config); err != nil {
		return GetTableSchemaResult{
			Success: false,
			Error:   err.Error(),
		}
	}

	schema, err := adapter.GetTableSchema(tableName)
	if err != nil {
		return GetTableSchemaResult{
			Success: false,
			Error:   err.Error(),
		}
	}

	return GetTableSchemaResult{
		Success: true,
		Schema:  &schema,
	}
}

// StartMigrationRequest 开始迁移请求
type StartMigrationRequest struct {
	Source        TestConnectionRequest `json:"source"`
	Target       TestConnectionRequest `json:"target"`
	Tables       []string              `json:"tables,omitempty"`
	StructureOnly bool                 `json:"structureOnly"`
	DataOnly      bool                 `json:"dataOnly"`
	BatchSize     int                   `json:"batchSize"`
	DropIfExists  bool                  `json:"dropIfExists"`
	IgnoreErrors  bool                 `json:"ignoreErrors"`
}

// StartMigration 开始迁移
func (a *App) StartMigration(req StartMigrationRequest) *types.MigrationReport {
	config := types.MigrationConfig{
		Source: types.ConnectionConfig{
			Type:     types.DatabaseType(req.Source.Type),
			Host:     req.Source.Host,
			Port:     req.Source.Port,
			Username: req.Source.Username,
			Password: req.Source.Password,
			Database: req.Source.Database,
			SSLMode:  req.Source.SSLMode,
			Charset:  req.Source.Charset,
		},
		Target: types.ConnectionConfig{
			Type:     types.DatabaseType(req.Target.Type),
			Host:     req.Target.Host,
			Port:     req.Target.Port,
			Username: req.Target.Username,
			Password: req.Target.Password,
			Database: req.Target.Database,
			SSLMode:  req.Target.SSLMode,
			Charset:  req.Target.Charset,
		},
		Tables:        req.Tables,
		StructureOnly: req.StructureOnly,
		DataOnly:      req.DataOnly,
		BatchSize:     req.BatchSize,
		DropIfExists:  req.DropIfExists,
		IgnoreErrors:  req.IgnoreErrors,
	}

	orch := orchestrator.NewOrchestrator(
		config,
		func(info types.ProgressInfo) {
			wailsRuntime.EventsEmit(a.ctx, "migration:progress", info)
		},
		func(entry types.LogEntry) {
			wailsRuntime.EventsEmit(a.ctx, "migration:log", entry)
		},
	)

	report, err := orch.Run()
	if err != nil {
		if report == nil {
			report = &types.MigrationReport{}
		}
		report.EndTime = time.Now().Format("2006-01-02 15:04:05")
		wailsRuntime.LogErrorf(a.ctx, "迁移失败: %v", err)
	}
	return report
}

// GetSupportedDatabases 获取已支持的数据库类型列表
func (a *App) GetSupportedDatabases() []string {
	dbs := types.SupportedDatabases()
	result := make([]string, len(dbs))
	for i, d := range dbs {
		result[i] = string(d)
	}
	return result
}
