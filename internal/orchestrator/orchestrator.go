// Package orchestrator 实现迁移编排：连接 → 逐表迁结构/迁数据 → 报告。
//
// 设计要点：
//   - 全链路 context 贯通，支持取消（CancelMigration）；
//   - 多表并发迁移（config.Concurrency，默认 4），进度与统计并发安全；
//   - 数据读取优先走主键游标分页（深翻页 O(1)），无单列主键时回退 OFFSET；
//   - 所有错误显式传播，不再静默吞错。
package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	types "dbbridge/pkg"
)

// ProgressCallback 进度回调函数
type ProgressCallback func(info types.ProgressInfo)

// LogCallback 日志回调函数
type LogCallback func(entry types.LogEntry)

// Orchestrator 迁移控制器
type Orchestrator struct {
	config        types.MigrationConfig
	sourceAdapter types.DatabaseAdapter
	targetAdapter types.DatabaseAdapter
	onProgress    ProgressCallback
	onLog         LogCallback
	startTime     time.Time

	// 并发安全状态
	mu           sync.Mutex
	tablesDone   int
	tablesTotal  int
	backups      []types.BackupInfo
	tableReports map[int]types.TableReport // 按表序号保存，保证报告顺序稳定
}

// NewOrchestrator 创建迁移控制器
func NewOrchestrator(config types.MigrationConfig, onProgress ProgressCallback, onLog LogCallback) *Orchestrator {
	return &Orchestrator{
		config:       config,
		onProgress:   onProgress,
		onLog:        onLog,
		tableReports: make(map[int]types.TableReport),
	}
}

// workerCount 计算并发数：默认 4，上限 8
func (o *Orchestrator) workerCount() int {
	n := o.config.Concurrency
	if n <= 0 {
		n = 4
	}
	if n > 8 {
		n = 8
	}
	return n
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
func (o *Orchestrator) reportProgress(ctx context.Context, phase, table string, processedRows, totalRows int64) {
	if o.onProgress == nil {
		return
	}
	o.mu.Lock()
	tablesDone, tablesTotal := o.tablesDone, o.tablesTotal
	o.mu.Unlock()

	elapsed := time.Since(o.startTime)
	var percent float64
	if tablesTotal > 0 {
		tablePercent := float64(tablesDone) / float64(tablesTotal)
		rowPercent := 0.0
		if totalRows > 0 {
			rowPercent = float64(processedRows) / float64(totalRows)
		}
		percent = (tablePercent + rowPercent/float64(tablesTotal)) * 100
		if percent > 100 {
			percent = 100
		}
	}

	var remaining string
	if percent > 0 && percent < 100 {
		remainingSec := elapsed.Seconds() / percent * (100 - percent)
		remaining = time.Duration(remainingSec * float64(time.Second)).Round(time.Second).String()
	}

	o.onProgress(types.ProgressInfo{
		Phase:           phase,
		CurrentTable:    table,
		ProcessedRows:   processedRows,
		TotalRows:       totalRows,
		TablesCompleted: tablesDone,
		TablesTotal:     tablesTotal,
		Percent:         percent,
		Elapsed:         elapsed.Round(time.Second).String(),
		Remaining:       remaining,
	})
}

// Run 执行迁移。ctx 取消（用户取消或失败快速中止）时尽快返回。
func (o *Orchestrator) Run(ctx context.Context) (*types.MigrationReport, error) {
	o.startTime = time.Now()
	report := &types.MigrationReport{
		StartTime: o.startTime.Format("2006-01-02 15:04:05"),
	}
	// SQL 文件模式尚未实现（解析器未接入编排器）：在启动阶段快速失败，
	// 避免先连接目标库、执行到取表结构阶段才中途报错。
	if o.config.SQLFilePath != "" {
		return report, fmt.Errorf("SQL 文件迁移模式暂未支持，请使用直连模式")
	}
	// runCtx：fail-fast 或用户取消均可中止整个流水线；
	// 父 ctx 仅在用户主动取消时关闭。
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	// 1. 连接源和目标
	if o.config.SQLFilePath == "" {
		// 直连模式
		o.log("INFO", "", "连接源数据库...")
		o.sourceAdapter = types.NewAdapter(o.config.Source.Type)
		if o.sourceAdapter == nil {
			return report, fmt.Errorf("不支持的源数据库类型: %s", o.config.Source.Type)
		}
		if err := o.sourceAdapter.Connect(runCtx, o.config.Source); err != nil {
			return report, fmt.Errorf("连接源数据库失败: %w", err)
		}
		defer o.sourceAdapter.Close()

		version, _ := o.sourceAdapter.GetVersion(runCtx)
		o.log("INFO", "", fmt.Sprintf("源数据库: %s", version))
	}

	// 连接目标库
	o.log("INFO", "", "连接目标数据库...")
	o.targetAdapter = types.NewAdapter(o.config.Target.Type)
	if o.targetAdapter == nil {
		return report, fmt.Errorf("不支持的目标数据库类型: %s", o.config.Target.Type)
	}
	if err := o.targetAdapter.Connect(runCtx, o.config.Target); err != nil {
		return report, fmt.Errorf("连接目标数据库失败: %w", err)
	}
	defer o.targetAdapter.Close()

	version, _ := o.targetAdapter.GetVersion(runCtx)
	o.log("INFO", "", fmt.Sprintf("目标数据库: %s", version))

	// 2. 获取表列表
	var tables []string
	if len(o.config.Tables) > 0 {
		tables = o.config.Tables
	} else if o.sourceAdapter != nil {
		tableMetas, err := o.sourceAdapter.GetTables(runCtx)
		if err != nil {
			return report, fmt.Errorf("获取源库表列表失败: %w", err)
		}
		for _, t := range tableMetas {
			tables = append(tables, t.Name)
		}
	}

	o.mu.Lock()
	o.tablesTotal = len(tables)
	o.mu.Unlock()
	o.log("INFO", "", fmt.Sprintf("待迁移表数: %d, 并发数: %d", len(tables), o.workerCount()))

	// 3. 并发迁移（worker pool）
	workers := o.workerCount()
	tableCh := make(chan indexedTable)
	var wg sync.WaitGroup
	var firstErr error // 触发快速中止的首个错误（fail-fast 模式下）

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tableCh {
				// 检查取消状态
				if runCtx.Err() != nil {
					return
				}
				tStart := time.Now()
				rows, err := o.migrateTable(runCtx, t.name)
				tReport := types.TableReport{
					TableName: t.name,
					Duration:  time.Since(tStart).Round(time.Millisecond).String(),
				}
				if err != nil {
					// 用户主动取消与真实失败区分开
					if runCtx.Err() != nil {
						tReport.Status = "cancelled"
						tReport.Error = "迁移已取消"
					} else {
						o.log("ERROR", t.name, err.Error())
						tReport.Status = "failed"
						tReport.Error = err.Error()

						// 尝试回滚
						if o.config.BackupBefore && o.config.AutoRollback {
							if o.tryRollback(runCtx, t.name) {
								tReport.RolledBack = true
								tReport.Status = "rolled_back"
								o.log("WARN", t.name, "已回滚到迁移前状态")
							}
						}
					}
				} else {
					tReport.Status = "success"
					tReport.Rows = rows
				}

				o.mu.Lock()
				o.tableReports[t.index] = tReport
				o.tablesDone++
				failed := err != nil && runCtx.Err() == nil
				o.mu.Unlock()

				if failed && !o.config.IgnoreErrors {
					// fail-fast：记录首个错误，取消其余任务（解除派发阻塞，避免死锁）
					o.mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("迁移表 %s 失败: %w", t.name, err)
					}
					o.mu.Unlock()
					runCancel()
					return
				}
			}
		}()
	}

	// 派发任务（在独立 goroutine 中，取消时退出派发，解除 worker 阻塞）
	go func() {
		defer close(tableCh)
		for i, name := range tables {
			select {
			case tableCh <- indexedTable{index: i, name: name}:
			case <-runCtx.Done():
				return
			}
		}
	}()

	// 等待所有 worker 结束（fail-fast 或取消时 runCtx 已关闭，worker 快速退出）
	wg.Wait()

	// 4. 汇总报告（按原始表顺序）
	o.mu.Lock()
	report.TablesTotal = len(tables)
	report.Backups = o.backups
	for i := 0; i < len(tables); i++ {
		tr, ok := o.tableReports[i]
		if !ok {
			continue
		}
		report.TableDetails = append(report.TableDetails, tr)
		switch tr.Status {
		case "success":
			report.TablesSuccess++
			report.TotalRows += tr.Rows
		case "failed":
			report.TablesFailed++
			report.FailedTables = append(report.FailedTables, tr.TableName)
		case "rolled_back":
			report.TablesFailed++
			report.FailedTables = append(report.FailedTables, tr.TableName)
		}
	}
	report.RollbackCount = o.countRollbacks(report.TableDetails)
	o.mu.Unlock()

	report.EndTime = time.Now().Format("2006-01-02 15:04:05")
	report.Duration = time.Since(o.startTime).Round(time.Second).String()

	// 判定最终状态
	o.mu.Lock()
	fErr := firstErr
	o.mu.Unlock()
	if fErr != nil {
		report.Error = fErr.Error()
		o.log("ERROR", "", fmt.Sprintf("迁移中止: %v", fErr))
		return report, fErr
	}
	if runCtx.Err() != nil && ctx.Err() == nil && fErr == nil {
		// runCtx 被取消但既非用户取消也非 fail-fast：防御性分支
		report.Error = "迁移已中止"
		return report, fmt.Errorf("迁移已中止")
	}
	if ctx.Err() != nil {
		report.Error = "迁移已取消"
		return report, fmt.Errorf("迁移已取消")
	}

	// done 事件必须携带真实累计行数（report.TotalRows 已在上方汇总），否则前端进度条归零
	o.reportProgress(ctx, "done", "", report.TotalRows, report.TotalRows)
	o.log("INFO", "", fmt.Sprintf("迁移完成！成功: %d, 失败: %d, 耗时: %s",
		report.TablesSuccess, report.TablesFailed, report.Duration))

	if len(report.Backups) > 0 {
		o.log("INFO", "", fmt.Sprintf("已备份 %d 张表，备份表名格式: _bak_原表名_时间戳", len(report.Backups)))
	}

	return report, nil
}

// indexedTable 带原始序号的表任务（保证报告顺序稳定）
type indexedTable struct {
	index int
	name  string
}

// migrateTable 迁移单张表，返回迁移的行数
func (o *Orchestrator) migrateTable(ctx context.Context, tableName string) (int64, error) {
	var processed int64

	// 获取源表结构
	if o.sourceAdapter == nil {
		// SQL 文件模式：从解析器获取（Phase 0 暂只支持直连模式）
		return 0, fmt.Errorf("SQL 文件模式尚未实现")
	}
	schema, err := o.sourceAdapter.GetTableSchema(ctx, tableName)
	if err != nil {
		return 0, fmt.Errorf("获取表结构失败: %w", err)
	}

	o.log("INFO", tableName, fmt.Sprintf("表结构: %d 列, %d 索引", len(schema.Columns), len(schema.Indexes)))

	// 迁移结构
	if !o.config.DataOnly {
		// 处理目标库已存在的同名表
		targetExists, _ := o.targetAdapter.TableExists(ctx, tableName)
		if targetExists {
			if o.config.BackupBefore {
				// 备份模式：将目标表重命名为备份表名
				o.log("INFO", tableName, "目标库存在同名表，正在备份...")
				backupName, err := o.targetAdapter.BackupTable(ctx, tableName)
				if err != nil {
					return 0, fmt.Errorf("备份目标表失败: %w", err)
				}
				o.log("INFO", tableName, fmt.Sprintf("已备份为 %s", backupName))
				o.mu.Lock()
				o.backups = append(o.backups, types.BackupInfo{
					OriginalTable: tableName,
					BackupTable:   backupName,
					CreatedAt:     time.Now().Format("2006-01-02 15:04:05"),
				})
				o.mu.Unlock()
			} else if o.config.DropIfExists {
				// 直接删除模式（错误显式传播）
				dropDDL, _ := o.targetAdapter.GenerateDropTableDDL(tableName)
				o.log("INFO", tableName, "目标库存在同名表，正在删除...")
				if err := o.targetAdapter.ExecContext(ctx, dropDDL); err != nil {
					return 0, fmt.Errorf("删除目标同名表失败: %w", err)
				}
			}
			// 如果既不备份也不删除，且表已存在，建表会失败，让错误自然抛出
		}

		// 生成并执行建表 SQL（类型已由目标适配器 MapType 完成异构映射）
		createDDL, err := o.targetAdapter.GenerateCreateTableDDL(schema)
		if err != nil {
			return 0, fmt.Errorf("生成建表SQL失败: %w", err)
		}

		o.log("INFO", tableName, "创建表结构...")
		// 分割多条 SQL 语句并逐条执行（MySQL DDL 隐式提交，PG 逐语句原子）
		for _, stmt := range splitSQL(createDDL) {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if err := o.targetAdapter.ExecContext(ctx, stmt); err != nil {
				return 0, fmt.Errorf("执行建表SQL失败: %w (SQL: %s)", err, truncate(stmt, 120))
			}
		}
		o.log("INFO", tableName, "表结构创建完成")
	}

	// 迁移数据
	if !o.config.StructureOnly {
		// 如果是仅迁移数据且没有获取结构，则需要获取目标表结构
		if o.config.DataOnly && len(schema.Columns) == 0 {
			schema, err = o.targetAdapter.GetTableSchema(ctx, tableName)
			if err != nil {
				return 0, fmt.Errorf("获取目标表结构失败: %w", err)
			}
		}

		columns := make([]string, 0, len(schema.Columns))
		for _, c := range schema.Columns {
			columns = append(columns, c.Name)
		}

		// 获取总行数（失败不阻塞，仅影响进度百分比）
		totalRows, _ := o.sourceAdapter.GetRowCount(ctx, tableName)
		o.log("INFO", tableName, fmt.Sprintf("开始迁移数据, 总行数: %d", totalRows))

		batchSize := o.config.BatchSize
		if batchSize <= 0 {
			batchSize = 5000
		}

		// 主键游标分页：单列主键时使用 keyset（深翻页 O(1)），否则回退 OFFSET
		var pkCol string
		for _, c := range schema.Columns {
			if c.IsPrimaryKey {
				pkCol = c.Name
				break
			}
		}
		useKeyset := pkCol != ""

		var lastKey any
		offset := 0
		for {
			if ctx.Err() != nil {
				return processed, fmt.Errorf("迁移已取消")
			}

			var rows []types.Row
			var err error
			if useKeyset {
				rows, err = o.sourceAdapter.ReadDataKeyset(ctx, tableName, pkCol, lastKey, batchSize)
			} else {
				rows, err = o.sourceAdapter.ReadData(ctx, tableName, offset, batchSize)
			}
			if err != nil {
				return processed, fmt.Errorf("读取数据失败 (已处理 %d 行): %w", processed, err)
			}
			if len(rows) == 0 {
				break
			}

			if err := o.targetAdapter.WriteData(ctx, tableName, columns, rows); err != nil {
				return processed, fmt.Errorf("写入数据失败 (已处理 %d 行): %w", processed, err)
			}

			if useKeyset && len(rows) > 0 {
				lastKey = rows[len(rows)-1][pkCol]
			}
			processed += int64(len(rows))
			offset += len(rows)
			o.reportProgress(ctx, "data", tableName, processed, totalRows)

			// keyset 模式靠空批结束；OFFSET 模式可提前结束
			if !useKeyset && processed >= totalRows {
				break
			}
		}

		o.log("INFO", tableName, fmt.Sprintf("数据迁移完成, 已迁移 %d 行", processed))
	}

	return processed, nil
}

// tryRollback 尝试回滚单张表到迁移前状态
// 找到该表的备份记录，将备份表恢复为原表名
func (o *Orchestrator) tryRollback(ctx context.Context, tableName string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range o.backups {
		if o.backups[i].OriginalTable == tableName && !o.backups[i].Restored {
			err := o.targetAdapter.RestoreFromBackup(ctx, o.backups[i].BackupTable, tableName)
			if err != nil {
				o.log("ERROR", tableName, fmt.Sprintf("回滚失败: %v", err))
				return false
			}
			o.backups[i].Restored = true
			return true
		}
	}
	// 没有备份记录，无法回滚
	return false
}

// countRollbacks 统计已回滚的表数
func (o *Orchestrator) countRollbacks(reports []types.TableReport) int {
	count := 0
	for _, r := range reports {
		if r.RolledBack {
			count++
		}
	}
	return count
}

// splitSQL 分割多条 SQL 语句（忽略引号内的分号）
func splitSQL(sqlText string) []string {
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

// truncate 截断字符串用于错误信息展示
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
