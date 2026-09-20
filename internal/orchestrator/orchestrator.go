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

	"dbbridge/internal/migrationlog"
	"dbbridge/internal/typeconv"
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
	pendingFKs   []pendingFK               // 延后添加的外键（数据迁完后统一补建）
logFile      *migrationlog.Writer      // 本地日志文件（可能为 nil：创建失败时降级）
}

// pendingFK 待补建的外键
type pendingFK struct {
	table string
	fk    types.ForeignKeyMeta
}

// isFKDDLGenerator 判断适配器是否支持延迟外键 DDL 生成
func isFKDDLGenerator(a types.DatabaseAdapter) bool {
	_, ok := a.(types.ForeignKeyDDLGenerator)
	return ok
}

// applyFKs 数据迁移完成后统一补建延后的外键约束。
// 单独报告错误（FKErrors）而不让整个迁移失败：外键缺失影响一致性但不丢数据，
// 用户可依据报告手动修复（如孤儿行导致约束失败）。
func (o *Orchestrator) applyFKs(ctx context.Context, report *types.MigrationReport) {
	o.mu.Lock()
	fks := o.pendingFKs
	o.pendingFKs = nil
	o.mu.Unlock()

	if len(fks) == 0 {
		return
	}
	o.log("INFO", "", fmt.Sprintf("开始补建外键约束（共 %d 条）...", len(fks)))

	gen, ok := o.targetAdapter.(types.ForeignKeyDDLGenerator)
	if !ok {
		return
	}
	for _, p := range fks {
		if ctx.Err() != nil {
			return
		}
		ddl, err := gen.GenerateAddForeignKeyDDL(p.table, p.fk)
		if err != nil {
			msg := fmt.Sprintf("外键 %s.%s: 生成 DDL 失败 - %v", p.table, p.fk.Name, err)
			o.log("ERROR", p.table, msg)
			report.FKErrors = append(report.FKErrors, msg)
			continue
		}
		if err := o.targetAdapter.ExecContext(ctx, ddl); err != nil {
			msg := fmt.Sprintf("外键 %s.%s: 创建失败 - %v（常见原因：子表存在引用列上不存在的父表值）", p.table, p.fk.Name, err)
			o.log("ERROR", p.table, msg)
			report.FKErrors = append(report.FKErrors, msg)
			continue
		}
		o.log("INFO", p.table, fmt.Sprintf("外键 %s 创建成功", p.fk.Name))
	}
}

// execDDL 按目标方言执行可能包含多条语句/过程体的 DDL：
//   - PG 系目标：触发器/函数体用美元引号包裹，splitSQL 能正确切分（先建函数再建触发器）；
//   - MySQL 系/SQLite 目标：过程体（BEGIN...END）内含分号，按分号切分会切碎语句，
//     依赖 MultiStatements=true 整体执行。
func (o *Orchestrator) execDDL(ctx context.Context, ddl string) error {
	switch o.config.Target.Type {
	case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB:
		for _, stmt := range splitSQL(ddl) {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if err := o.targetAdapter.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	default:
		return o.targetAdapter.ExecContext(ctx, ddl)
	}
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

// log 记录日志（同时写入回调与本地日志文件，两者内容完全一致）
func (o *Orchestrator) log(level, table, message string) {
	entry := types.LogEntry{
		Time:    time.Now().Format("2006-01-02 15:04:05"),
		Level:   level,
		Table:   table,
		Message: message,
	}
	if o.onLog != nil {
		o.onLog(entry)
	}
	o.logFile.Write(entry)
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

	// 本地日志文件：完整记录本次迁移所有日志（含失败详情），便于事后查看与复制。
	// 创建失败仅告警降级，不阻塞迁移。
	logWriter, logErr := migrationlog.NewWriter()
	if logErr == nil {
		defer logWriter.Close()
		o.logFile = logWriter
		report.LogFile = logWriter.Path()
	} else {
		o.log("WARN", "", "本地日志文件创建失败（不影响迁移，仅保留界面日志）: "+logErr.Error())
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
		case "cancelled":
			report.TablesCancelled++
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

	// 4.5 补建延后的外键约束（StructureOnly 模式也需要：建表时刻意未带外键）
	if !o.config.DataOnly {
		o.applyFKs(runCtx, report)
	}

	// 5. 迁移触发器
	if o.config.MigrateTriggers && o.sourceAdapter != nil {
		o.migrateTriggers(runCtx, report)
	}

	// 6. 迁移存储过程/函数
	if o.config.MigrateRoutines && o.sourceAdapter != nil {
		o.migrateRoutines(runCtx, report)
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

	// DataOnly 模式直接向已存在的表追加数据，备份/回滚（基于表重命名）不适用，
	// 显式告警避免用户误以为有备份保护
	if o.config.DataOnly && o.config.BackupBefore {
		o.log("WARN", tableName, "仅迁数据模式不支持迁移前备份与自动回滚（该机制依赖表重命名）")
	}

	// 迁移结构
	if !o.config.DataOnly {
		// 处理目标库已存在的同名表。
		// 防回归：TableExists 的错误禁止吞掉——一旦检查失败而按"不存在"处理，
		// 备份/删除选项会静默失效，建表又被 CREATE TABLE 静默跳过（已禁用
		// IF NOT EXISTS）或数据直接追加，造成数据重复且用户毫无感知。
		targetExists, err := o.targetAdapter.TableExists(ctx, tableName)
		if err != nil {
			return 0, fmt.Errorf("检查目标表是否存在失败: %w", err)
		}
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

		// 外键延后添加：多表并发迁移时若内联外键，被引用表可能尚未创建而报错。
		// 支持延迟 DDL 的目标库先建无外键的表，数据全部迁完后统一 ALTER TABLE 补建。
		deferFK := len(schema.ForeignKeys) > 0 &&
			o.config.Target.Type != types.SQLite && // SQLite 无法 ALTER ADD CONSTRAINT，只能内联
			isFKDDLGenerator(o.targetAdapter)
		if deferFK {
			o.mu.Lock()
			for _, fk := range schema.ForeignKeys {
				o.pendingFKs = append(o.pendingFKs, pendingFK{table: tableName, fk: fk})
			}
			o.mu.Unlock()
			schema.ForeignKeys = nil
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
				return 0, fmt.Errorf("执行建表SQL失败: %w (SQL: %s)", err, truncate(stmt, 500))
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

		// 主键游标分页：单列主键时使用 keyset（深翻页 O(1)）；
		// 无主键但有单列非空唯一索引时也用 keyset；都无则回退 OFFSET
		var pkCol string
		for _, c := range schema.Columns {
			if c.IsPrimaryKey {
				pkCol = c.Name
				break
			}
		}
		if pkCol == "" {
			for _, idx := range schema.Indexes {
				if !idx.IsUnique || len(idx.Columns) != 1 {
					continue
				}
				for _, c := range schema.Columns {
					if c.Name == idx.Columns[0] && !c.Nullable {
						pkCol = c.Name
						break
					}
				}
				if pkCol != "" {
					o.log("INFO", tableName, fmt.Sprintf("无主键，使用唯一索引列 %s 做游标分页", pkCol))
					break
				}
			}
		}
		useKeyset := pkCol != ""
		// 无主键/唯一索引时，检查适配器是否支持物理行ID游标分页（避免 OFFSET 深翻页 O(N)）
		usePhysRowID := !useKeyset
		if usePhysRowID {
			if _, ok := o.sourceAdapter.(types.PhysicalRowIDReader); !ok {
				usePhysRowID = false
			}
		}
		if !useKeyset && !usePhysRowID {
			o.log("WARN", tableName, "表无主键/可用唯一索引，回退 OFFSET 分页：源库存在并发写入或执行计划变化时可能漏行/重复行，建议迁完后核对行数")
		}

		var lastKey any
		var lastPhysRowID any
		offset := 0
		for {
			if ctx.Err() != nil {
				return processed, fmt.Errorf("迁移已取消")
			}

			var rows []types.Row
			var err error
			if useKeyset {
				rows, err = o.sourceAdapter.ReadDataKeyset(ctx, tableName, pkCol, lastKey, batchSize)
			} else if usePhysRowID {
				physReader := o.sourceAdapter.(types.PhysicalRowIDReader)
				rows, err = physReader.ReadDataByPhysicalRowID(ctx, tableName, lastPhysRowID, batchSize)
			} else {
				rows, err = o.sourceAdapter.ReadData(ctx, tableName, offset, batchSize)
			}
			if err != nil {
				return processed, fmt.Errorf("读取数据失败 (已处理 %d 行): %w", processed, err)
			}
			if len(rows) == 0 {
				break
			}

			// 规范化时间值：SQLite 等库可能把 time.Time 以 Go 字符串形式存入 TEXT 列，
			// 直接写入目标库会报 1292/22007，这里统一还原为标准 datetime 字符串
			for _, row := range rows {
				for k, v := range row {
					if s, ok := v.(string); ok {
						if nv, changed := typeconv.NormalizeTimeValue(s); changed {
							row[k] = nv
						}
					}
				}
			}

			if err := o.targetAdapter.WriteData(ctx, tableName, columns, rows); err != nil {
				return processed, fmt.Errorf("写入数据失败 (已处理 %d 行): %w", processed, err)
			}

			if useKeyset && len(rows) > 0 {
				lastKey = rows[len(rows)-1][pkCol]
				// 主键列含 NULL 时游标无法推进，SQL 会退化为无条件的全表首查导致死循环
				if lastKey == nil {
					return processed, fmt.Errorf("主键列 %s 存在 NULL 值，无法使用游标分页，请为该列补充 NOT NULL 约束或去除 NULL 数据后重试", pkCol)
				}
			}
			if usePhysRowID && len(rows) > 0 {
				// 物理行ID存储在特殊列名 _physrowid 中
				lastPhysRowID = rows[len(rows)-1]["_physrowid"]
				// 从数据行中移除物理行ID列，不写入目标库
				for i := range rows {
					delete(rows[i], "_physrowid")
				}
			}
			processed += int64(len(rows))
			offset += len(rows)
			o.reportProgress(ctx, "data", tableName, processed, totalRows)

			// keyset/physRowID 模式靠空批结束；OFFSET 模式可提前结束
			if !useKeyset && !usePhysRowID && processed >= totalRows {
				break
			}
		}

		o.log("INFO", tableName, fmt.Sprintf("数据迁移完成, 已迁移 %d 行", processed))

		// 自增序列修复：显式插入自增列不会推进种子（MSSQL IDENTITY / PG SERIAL），
		// 不修复的话目标库下一条自动 INSERT 会主键冲突。失败只告警不阻断迁移结果。
		if fixer, ok := o.targetAdapter.(types.SequenceFixer); ok {
			if err := fixer.FixAutoIncrementSequences(ctx, tableName, schema.Columns); err != nil {
				o.log("WARN", tableName, fmt.Sprintf("自增序列修复失败（后续 INSERT 可能主键冲突）: %v", err))
			} else {
				o.log("INFO", tableName, "自增序列已重置到当前最大值")
			}
		}
	}

	return processed, nil
}

// tryRollback 尝试回滚单张表到迁移前状态
// 找到该表的备份记录，将备份表恢复为原表名。
// 注意：RestoreFromBackup 是网络 I/O，必须在锁外执行，否则会阻塞所有进度上报。
func (o *Orchestrator) tryRollback(ctx context.Context, tableName string) bool {
	o.mu.Lock()
	var backup *types.BackupInfo
	for i := range o.backups {
		if o.backups[i].OriginalTable == tableName && !o.backups[i].Restored {
			backup = &o.backups[i]
			break
		}
	}
	o.mu.Unlock()

	if backup == nil {
		// 没有备份记录，无法回滚
		return false
	}

	err := o.targetAdapter.RestoreFromBackup(ctx, backup.BackupTable, tableName)
	if err != nil {
		o.log("ERROR", tableName, fmt.Sprintf("回滚失败: %v", err))
		return false
	}
	o.mu.Lock()
	backup.Restored = true
	o.mu.Unlock()
	return true
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

// splitSQL 分割多条 SQL 语句（忽略引号内的分号与反斜杠转义；
// 识别 PG 美元引号 $$...$$ / $tag$...$tag$，函数体内部分号不再误切）
func splitSQL(sqlText string) []string {
	var statements []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false // 单引号内反斜杠转义（MySQL 语义）
	dollarTag := ""  // 非空表示处于 $tag$...$tag$ 美元引号块内

	for i := 0; i < len(sqlText); i++ {
		ch := sqlText[i]

		if dollarTag != "" {
			// 美元引号块内：寻找闭合标记 $tag$
			current.WriteByte(ch)
			if ch == '$' && strings.HasPrefix(sqlText[i:], dollarTag) {
				current.WriteString(dollarTag[1:])
				i += len(dollarTag) - 1
				dollarTag = ""
			}
			continue
		}

		if inSingleQuote && escaped {
			// 转义字符（\' 或 \\）随其后字符原样保留
			escaped = false
			current.WriteByte(ch)
			continue
		}

		// 美元引号开始（PG plpgsql 函数体）：$tag$ 开启，直到匹配的 $tag$
		if ch == '$' && !inSingleQuote && !inDoubleQuote {
			if end := strings.IndexByte(sqlText[i+1:], '$'); end >= 0 {
				tag := "$" + sqlText[i+1:i+1+end] + "$"
				// 空 tag（$$）或纯标识符 tag 才是美元引号；避免把普通 $ 字符误判
				if tag == "$$" || isDollarTagIdent(sqlText[i+1:i+1+end]) {
					dollarTag = tag
					current.WriteByte(ch)
					current.WriteString(tag[1:])
					i += len(tag) - 1
					continue
				}
			}
		}

		switch ch {
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
		case '\\':
			if inSingleQuote {
				escaped = true
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
		current.WriteByte(ch)
	}

	// 最后一条语句
	stmt := current.String()
	if strings.TrimSpace(stmt) != "" {
		statements = append(statements, stmt)
	}

	return statements
}

// isDollarTagIdent 判断美元引号标签是否为合法标识符（字母/数字/下划线）
func isDollarTagIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// truncate 截断字符串用于错误信息展示（按 rune 截断，避免切断多字节 UTF-8 产生乱码）
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	runes := []rune(s)
	// 按 rune 收集不超过 n 字节的前缀
	var sb strings.Builder
	used := 0
	for _, r := range runes {
		size := len(string(r))
		if used+size > n {
			break
		}
		sb.WriteRune(r)
		used += size
	}
	return sb.String() + "..."
}

// migrateTriggers 迁移触发器：从源库读取触发器定义，转换为目标方言，在目标库执行。
func (o *Orchestrator) migrateTriggers(ctx context.Context, report *types.MigrationReport) {
	o.log("INFO", "", "开始迁移触发器...")

	triggers, err := o.sourceAdapter.GetTriggers(ctx)
	if err != nil {
		o.log("WARN", "", fmt.Sprintf("读取源库触发器失败: %v", err))
		return
	}
	if len(triggers) == 0 {
		o.log("INFO", "", "源库无触发器")
		return
	}

	o.log("INFO", "", fmt.Sprintf("发现 %d 个触发器，开始转换并执行...", len(triggers)))

	targetDialect := o.config.Target.Type
	for _, trigger := range triggers {
		if ctx.Err() != nil {
			return
		}

		ddl, err := o.sourceAdapter.GenerateTriggerDDL(trigger, targetDialect)
		if err != nil {
			msg := fmt.Sprintf("触发器 %s: 转换失败 - %v", trigger.Name, err)
			o.log("ERROR", trigger.Table, msg)
			report.TriggerErrors = append(report.TriggerErrors, msg)
			continue
		}

		// 按目标方言执行（PG 系逐语句，MySQL 系整体执行避免过程体被分号切碎）
		if err := o.execDDL(ctx, ddl); err != nil {
			msg := fmt.Sprintf("触发器 %s: 执行失败 - %v (SQL: %s)", trigger.Name, err, truncate(ddl, 120))
			o.log("ERROR", trigger.Table, msg)
			report.TriggerErrors = append(report.TriggerErrors, msg)
			continue
		}

		report.TriggersMigrated++
		o.log("INFO", trigger.Table, fmt.Sprintf("触发器 %s 迁移成功", trigger.Name))
	}

	o.log("INFO", "", fmt.Sprintf("触发器迁移完成: 成功 %d, 失败 %d", report.TriggersMigrated, len(report.TriggerErrors)))
}

// migrateRoutines 迁移存储过程/函数：从源库读取定义，转换为目标方言，在目标库执行。
func (o *Orchestrator) migrateRoutines(ctx context.Context, report *types.MigrationReport) {
	o.log("INFO", "", "开始迁移存储过程/函数...")

	routines, err := o.sourceAdapter.GetRoutines(ctx)
	if err != nil {
		o.log("WARN", "", fmt.Sprintf("读取源库存储过程失败: %v", err))
		return
	}
	if len(routines) == 0 {
		o.log("INFO", "", "源库无存储过程/函数")
		return
	}

	o.log("INFO", "", fmt.Sprintf("发现 %d 个存储过程/函数，开始转换并执行...", len(routines)))

	targetDialect := o.config.Target.Type
	for _, routine := range routines {
		if ctx.Err() != nil {
			return
		}

		ddl, err := o.sourceAdapter.GenerateRoutineDDL(routine, targetDialect)
		if err != nil {
			msg := fmt.Sprintf("存储过程 %s: 转换失败 - %v", routine.Name, err)
			o.log("ERROR", "", msg)
			report.RoutineErrors = append(report.RoutineErrors, msg)
			continue
		}

		// 按目标方言执行（同触发器：PG 系逐语句，MySQL 系整体执行）
		if err := o.execDDL(ctx, ddl); err != nil {
			msg := fmt.Sprintf("存储过程 %s: 执行失败 - %v (SQL: %s)", routine.Name, err, truncate(ddl, 120))
			o.log("ERROR", "", msg)
			report.RoutineErrors = append(report.RoutineErrors, msg)
			continue
		}

		report.RoutinesMigrated++
		o.log("INFO", "", fmt.Sprintf("存储过程 %s 迁移成功", routine.Name))
	}

	o.log("INFO", "", fmt.Sprintf("存储过程迁移完成: 成功 %d, 失败 %d", report.RoutinesMigrated, len(report.RoutineErrors)))
}
