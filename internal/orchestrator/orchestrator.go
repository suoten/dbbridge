package orchestrator

import (
	"fmt"
	"strings"
	"time"

	// 导入适配器包，触发 init() 自动注册
	_ "dbbridge/internal/adapter/mysql"
	_ "dbbridge/internal/adapter/postgres"
	_ "dbbridge/internal/adapter/sqlite"

	"dbbridge/pkg"
)

// ProgressCallback 进度回调函数
type ProgressCallback func(info types.ProgressInfo)

// LogCallback 日志回调函数
type LogCallback func(entry types.LogEntry)

// Orchestrator 迁移控制器
type Orchestrator struct {
	config         types.MigrationConfig
	sourceAdapter  types.DatabaseAdapter
	targetAdapter  types.DatabaseAdapter
	onProgress     ProgressCallback
	onLog          LogCallback
	startTime      time.Time
	tablesDone     int
	tablesTotal    int
}

// NewOrchestrator 创建迁移控制器
func NewOrchestrator(config types.MigrationConfig, onProgress ProgressCallback, onLog LogCallback) *Orchestrator {
	return &Orchestrator{
		config:     config,
		onProgress: onProgress,
		onLog:      onLog,
	}
}

// log 记录日志
func (o *Orchestrator) log(level, table, message string) {
	if o.onLog != nil {
		o.onLog(types.LogEntry{
			Time:    time.Now().Format("2006-01-02 15:04:05"),
			Level:   level,
			Table:   table,
			Message: message,
		})
	}
}

// reportProgress 上报进度
func (o *Orchestrator) reportProgress(phase, table string, processedRows, totalRows int64) {
	if o.onProgress != nil {
		elapsed := time.Since(o.startTime)
		var percent float64
		if o.tablesTotal > 0 {
			tablePercent := float64(o.tablesDone) / float64(o.tablesTotal)
			rowPercent := 0.0
			if totalRows > 0 {
				rowPercent = float64(processedRows) / float64(totalRows)
			}
			percent = (tablePercent + rowPercent/float64(o.tablesTotal)) * 100
			if percent > 100 {
				percent = 100
			}
		}

		var remaining string
		if percent > 0 && percent < 100 {
			totalElapsed := elapsed.Seconds()
			remainingSec := totalElapsed / percent * (100 - percent)
			remaining = time.Duration(remainingSec * float64(time.Second)).Round(time.Second).String()
		}

		o.onProgress(types.ProgressInfo{
			Phase:           phase,
			CurrentTable:    table,
			ProcessedRows:   processedRows,
			TotalRows:       totalRows,
			TablesCompleted: o.tablesDone,
			TablesTotal:     o.tablesTotal,
			Percent:         percent,
			Elapsed:         elapsed.Round(time.Second).String(),
			Remaining:       remaining,
		})
	}
}

// Run 执行迁移
func (o *Orchestrator) Run() (*types.MigrationReport, error) {
	o.startTime = time.Now()
	report := &types.MigrationReport{
		StartTime: o.startTime.Format("2006-01-02 15:04:05"),
	}
	var tableReports []types.TableReport

	// 1. 连接源和目标
	if o.config.SQLFilePath == "" {
		// 直连模式
		o.log("INFO", "", "连接源数据库...")
		o.sourceAdapter = types.NewAdapter(o.config.Source.Type)
		if o.sourceAdapter == nil {
			return report, fmt.Errorf("不支持的源数据库类型: %s", o.config.Source.Type)
		}
		if err := o.sourceAdapter.Connect(o.config.Source); err != nil {
			return report, fmt.Errorf("连接源数据库失败: %w", err)
		}
		defer o.sourceAdapter.Close()

		version, _ := o.sourceAdapter.GetVersion()
		o.log("INFO", "", fmt.Sprintf("源数据库: %s", version))
	}

	// 连接目标库
	o.log("INFO", "", "连接目标数据库...")
	o.targetAdapter = types.NewAdapter(o.config.Target.Type)
	if o.targetAdapter == nil {
		return report, fmt.Errorf("不支持的目标数据库类型: %s", o.config.Target.Type)
	}
	if err := o.targetAdapter.Connect(o.config.Target); err != nil {
		return report, fmt.Errorf("连接目标数据库失败: %w", err)
	}
	defer o.targetAdapter.Close()

	version, _ := o.targetAdapter.GetVersion()
	o.log("INFO", "", fmt.Sprintf("目标数据库: %s", version))

	// 2. 获取表列表
	var tables []string
	if len(o.config.Tables) > 0 {
		tables = o.config.Tables
	} else if o.sourceAdapter != nil {
		tableMetas, err := o.sourceAdapter.GetTables()
		if err != nil {
			return report, fmt.Errorf("获取源库表列表失败: %w", err)
		}
		for _, t := range tableMetas {
			tables = append(tables, t.Name)
		}
	}

	o.tablesTotal = len(tables)
	o.log("INFO", "", fmt.Sprintf("待迁移表数: %d", o.tablesTotal))

	// 3. 逐表迁移
	for _, tableName := range tables {
		o.tablesDone++
		tableStart := time.Now()
		tReport := types.TableReport{TableName: tableName, Status: "success"}

		err := o.migrateTable(tableName)
		if err != nil {
			o.log("ERROR", tableName, err.Error())
			tReport.Status = "failed"
			tReport.Error = err.Error()
			report.TablesFailed++
			report.FailedTables = append(report.FailedTables, tableName)
			if !o.config.IgnoreErrors {
				return report, fmt.Errorf("迁移表 %s 失败: %w", tableName, err)
			}
		} else {
			report.TablesSuccess++
		}

		tReport.Duration = time.Since(tableStart).Round(time.Millisecond).String()
		tableReports = append(tableReports, tReport)
	}

	report.TableDetails = tableReports

	// 4. 完成
	report.EndTime = time.Now().Format("2006-01-02 15:04:05")
	report.Duration = time.Since(o.startTime).Round(time.Second).String()
	report.TablesTotal = len(tables)
	o.reportProgress("done", "", 0, 0)
	o.log("INFO", "", fmt.Sprintf("迁移完成！成功: %d, 失败: %d, 耗时: %s",
		report.TablesSuccess, report.TablesFailed, report.Duration))

	return report, nil
}

// migrateTable 迁移单张表
func (o *Orchestrator) migrateTable(tableName string) error {
	// 获取源表结构
	var schema types.TableSchema
	var err error

	if o.sourceAdapter != nil {
		schema, err = o.sourceAdapter.GetTableSchema(tableName)
	} else {
		// SQL 文件模式：从解析器获取
		// TODO: Phase 0 暂时只支持直连模式
		return fmt.Errorf("SQL 文件模式尚未实现")
	}

	if err != nil {
		return fmt.Errorf("获取表结构失败: %w", err)
	}

	o.log("INFO", tableName, fmt.Sprintf("表结构: %d 列, %d 索引", len(schema.Columns), len(schema.Indexes)))

	// 迁移结构
	if !o.config.DataOnly {
		// 删除已存在的表
		if o.config.DropIfExists {
			dropDDL, _ := o.targetAdapter.GenerateDropTableDDL(tableName)
			o.targetAdapter.BeginTx()
			o.dbExec(dropDDL) // 执行但不检查错误（表可能不存在）
			o.targetAdapter.CommitTx()
		}

		// 生成并执行建表 SQL
		createDDL, err := o.targetAdapter.GenerateCreateTableDDL(schema)
		if err != nil {
			return fmt.Errorf("生成建表SQL失败: %w", err)
		}

		o.log("INFO", tableName, "创建表结构...")
		// 分割多条 SQL 语句并逐条执行
		for _, stmt := range splitSQL(createDDL) {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if err := o.targetAdapter.BeginTx(); err != nil {
				return fmt.Errorf("开启事务失败: %w", err)
			}
			if err := o.targetExec(stmt); err != nil {
				o.targetAdapter.RollbackTx()
				return fmt.Errorf("执行建表SQL失败: %w", err)
			}
			if err := o.targetAdapter.CommitTx(); err != nil {
				return fmt.Errorf("提交事务失败: %w", err)
			}
		}
		o.log("INFO", tableName, "表结构创建完成")
	}

	// 迁移数据
	if !o.config.StructureOnly {
		// 如果是仅迁移数据且没有获取结构，则需要获取结构
		if o.config.DataOnly && len(schema.Columns) == 0 {
			schema, err = o.targetAdapter.GetTableSchema(tableName)
			if err != nil {
				return fmt.Errorf("获取目标表结构失败: %w", err)
			}
		}

		var columns []string
		for _, c := range schema.Columns {
			columns = append(columns, c.Name)
		}

		// 获取总行数
		totalRows, _ := o.sourceAdapter.GetRowCount(tableName)
		o.log("INFO", tableName, fmt.Sprintf("开始迁移数据, 总行数: %d", totalRows))

		batchSize := o.config.BatchSize
		if batchSize <= 0 {
			batchSize = 5000
		}

		var processed int64
		offset := 0
		for processed < totalRows {
			limit := batchSize
			rows, err := o.sourceAdapter.ReadData(tableName, offset, limit)
			if err != nil {
				return fmt.Errorf("读取数据失败 (offset=%d): %w", offset, err)
			}
			if len(rows) == 0 {
				break
			}

			if err := o.targetAdapter.WriteData(tableName, columns, rows); err != nil {
				return fmt.Errorf("写入数据失败 (offset=%d): %w", offset, err)
			}

			processed += int64(len(rows))
			offset += len(rows)
			o.reportProgress("data", tableName, processed, totalRows)
		}

		o.log("INFO", tableName, fmt.Sprintf("数据迁移完成, 已迁移 %d 行", processed))
	}

	return nil
}

// targetExec 在目标库执行原始 SQL
func (o *Orchestrator) targetExec(sql string) error {
	// 使用适配器的底层执行能力
	// 通过 WriteData 接口不行，需要一个 Exec 方法
	// 暂时通过 BeginTx + 原始 exec 的方式
	// 这里使用一个变通方案
	executor, ok := o.targetAdapter.(SQLExecutor)
	if ok {
		return executor.Exec(sql)
	}
	return fmt.Errorf("目标适配器不支持直接执行 SQL")
}

// SQLExecutor 可执行原始 SQL 的接口
type SQLExecutor interface {
	Exec(sql string) error
}

// dbExec 直接执行 SQL（不检查错误）
func (o *Orchestrator) dbExec(sql string) {
	executor, ok := o.targetAdapter.(SQLExecutor)
	if ok {
		executor.Exec(sql)
	}
}

// splitSQL 分割多条 SQL 语句
func splitSQL(sqlText string) []string {
	// 简单按分号分割，忽略引号内的分号
	var statements []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false

	for _, ch := range sqlText {
		switch ch {
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
		case ';':
			if !inSingleQuote && !inDoubleQuote {
				stmt := current.String()
				if strings.TrimSpace(stmt) != "" {
					statements = append(statements, stmt)
				}
				current.Reset()
				continue
			}
		}
		current.WriteRune(ch)
	}

	// 最后一条语句
	stmt := current.String()
	if strings.TrimSpace(stmt) != "" {
		statements = append(statements, stmt)
	}

	return statements
}
