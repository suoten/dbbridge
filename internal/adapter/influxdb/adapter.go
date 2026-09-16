// Package influxdb 注册 InfluxDB 适配器（时序数据库）。
//
// InfluxDB 是最流行的开源时序数据库，使用 InfluxQL 或 Flux 查询语言。
// 本适配器将 measurement 映射为"表"，tag/field 映射为"列"。
// 使用 github.com/influxdata/influxdb-client-go/v2 驱动。
package influxdb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dbbridge/internal/typeconv"
	types "dbbridge/pkg"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api/write"
)

// Adapter InfluxDB 适配器
type Adapter struct {
	client influxdb2.Client
	org    string
	bucket string
	brand  string
}

func init() {
	types.RegisterAdapter(types.InfluxDB, func() types.DatabaseAdapter {
		return &Adapter{brand: "InfluxDB"}
	})
}

func (a *Adapter) Connect(ctx context.Context, config types.ConnectionConfig) error {
	port := config.Port
	if port == 0 {
		port = 8086
	}
	uri := fmt.Sprintf("http://%s:%d", config.Host, port)
	client := influxdb2.NewClient(uri, config.Password)
	a.client = client
	a.org = config.Username
	a.bucket = config.Database
	// 验证连接
	_, err := client.Ping(ctx)
	if err != nil {
		return fmt.Errorf("InfluxDB: 连接失败: %w", err)
	}
	return nil
}

func (a *Adapter) Close() error {
	if a.client != nil {
		a.client.Close()
	}
	return nil
}

func (a *Adapter) GetVersion(ctx context.Context) (string, error) {
	pingResult, err := a.client.Ping(ctx)
	if err != nil {
		return "", fmt.Errorf("InfluxDB: 获取版本失败: %w", err)
	}
	return fmt.Sprintf("InfluxDB (ping: %v)", pingResult), nil
}

// GetTables 获取所有 measurement（通过 Flux 查询）
func (a *Adapter) GetTables(ctx context.Context) ([]types.TableMeta, error) {
	query := fmt.Sprintf(`import "influxdata/influxdb/schema"
schema.measurements(bucket: "%s")`, a.bucket)
	result, err := a.client.QueryAPI(a.org).Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("InfluxDB: 查询 measurement 失败: %w", err)
	}

	var tables []types.TableMeta
	for result.Next() {
		name, _ := result.Record().Value().(string)
		tables = append(tables, types.TableMeta{Name: name})
	}
	return tables, result.Err()
}

// GetTableSchema 获取 measurement 的结构（tag keys + field keys）
func (a *Adapter) GetTableSchema(ctx context.Context, tableName string) (types.TableSchema, error) {
	schema := types.TableSchema{Name: tableName}

	// 获取 tag keys
	tagQuery := fmt.Sprintf(`import "influxdata/influxdb/schema"
schema.tagKeys(bucket: "%s", measurement: "%s")`, a.bucket, tableName)
	result, err := a.client.QueryAPI(a.org).Query(ctx, tagQuery)
	if err != nil {
		return schema, fmt.Errorf("InfluxDB: 查询 tag keys 失败: %w", err)
	}
	for result.Next() {
		name, _ := result.Record().Value().(string)
		schema.Columns = append(schema.Columns, types.ColumnMeta{
			Name: name, DataType: "TAG", BaseType: "VARCHAR", Nullable: false,
		})
	}

	// 获取 field keys
	fieldQuery := fmt.Sprintf(`import "influxdata/influxdb/schema"
schema.fieldKeys(bucket: "%s", measurement: "%s")`, a.bucket, tableName)
	result2, err := a.client.QueryAPI(a.org).Query(ctx, fieldQuery)
	if err != nil {
		return schema, fmt.Errorf("InfluxDB: 查询 field keys 失败: %w", err)
	}
	for result2.Next() {
		name := result2.Record().Field()
		valueType := result2.Record().Value()
		dataType := "VARCHAR"
		if t, ok := valueType.(string); ok {
			dataType = strings.ToUpper(t)
		}
		schema.Columns = append(schema.Columns, types.ColumnMeta{
			Name: name, DataType: dataType, BaseType: dataType, Nullable: true,
		})
	}

	// 时间列总是存在
	schema.Columns = append([]types.ColumnMeta{{
		Name: "time", DataType: "TIMESTAMP", BaseType: "TIMESTAMP",
		IsPrimaryKey: true, Nullable: false,
	}}, schema.Columns...)
	schema.Indexes = append(schema.Indexes, types.IndexMeta{
		Name: "PRIMARY", Columns: []string{"time"}, IsUnique: true, IsPrimary: true,
	})

	return schema, nil
}

func (a *Adapter) GetRowCount(ctx context.Context, tableName string) (int64, error) {
	// 使用 pivot 后的查询计数，避免每个 field 单独计数导致行数虚高
	query := fmt.Sprintf(`from(bucket: "%s")
  |> range(start: 0)
  |> filter(fn: (r) => r._measurement == "%s")
  |> group(columns: ["_time"])
  |> count()
  |> group()
  |> sum()`, a.bucket, tableName)
	result, err := a.client.QueryAPI(a.org).Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("InfluxDB: 获取行数失败: %w", err)
	}
	var count int64
	for result.Next() {
		if v, ok := result.Record().Value().(int64); ok {
			count += v
		}
	}
	return count, result.Err()
}

func (a *Adapter) TableExists(ctx context.Context, tableName string) (bool, error) {
	tables, err := a.GetTables(ctx)
	if err != nil {
		return false, err
	}
	for _, t := range tables {
		if t.Name == tableName {
			return true, nil
		}
	}
	return false, nil
}

func (a *Adapter) BackupTable(ctx context.Context, tableName string) (string, error) {
	// InfluxDB 不支持 RENAME，备份通过读取源数据写入备份 measurement 实现
	backupName := fmt.Sprintf("_bak_%s_%s", tableName, time.Now().Format("20060102_150405"))

	// 读取源 measurement 的所有数据
	rows, err := a.ReadData(ctx, tableName, 0, 100000)
	if err != nil {
		return "", fmt.Errorf("InfluxDB: 备份时读取数据失败: %w", err)
	}
	if len(rows) == 0 {
		return backupName, nil
	}

	// 获取 schema 确定 tag 列
	schema, _ := a.GetTableSchema(ctx, tableName)
	tagCols := make(map[string]bool)
	for _, c := range schema.Columns {
		if c.DataType == "TAG" {
			tagCols[c.Name] = true
		}
	}

	// 写入备份 measurement
	writeAPI := a.client.WriteAPIBlocking(a.org, a.bucket)
	for _, row := range rows {
		tags := map[string]string{}
		fields := map[string]any{}
		var ts time.Time
		for k, v := range row {
			if k == "time" {
				if t, ok := v.(time.Time); ok {
					ts = t
				}
			} else if tagCols[k] || isTagColumn(k) {
				if s, ok := v.(string); ok {
					tags[k] = s
				}
			} else {
				fields[k] = v
			}
		}
		if ts.IsZero() {
			ts = time.Now()
		}
		p := write.NewPoint(backupName, tags, fields, ts)
		if err := writeAPI.WritePoint(ctx, p); err != nil {
			return "", fmt.Errorf("InfluxDB: 备份写入数据失败: %w", err)
		}
	}

	// 删除原 measurement 的数据（通过 DeleteAPI 按时间范围删除）
	start := time.Unix(0, 0)
	stop := time.Now().Add(time.Hour)
	deleteAPI := a.client.DeleteAPI()
	// 删除原 measurement 的数据
	predicate := fmt.Sprintf(`_measurement="%s"`, tableName)
	_ = deleteAPI.DeleteWithName(ctx, a.org, a.bucket, start, stop, predicate)

	return backupName, nil
}

func (a *Adapter) RestoreFromBackup(ctx context.Context, backupName, originalName string) error {
	// 从备份 measurement 读取数据，写回原 measurement
	rows, err := a.ReadData(ctx, backupName, 0, 100000)
	if err != nil {
		return fmt.Errorf("InfluxDB: 恢复时读取备份失败: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	// 获取备份 schema 确定 tag 列
	schema, _ := a.GetTableSchema(ctx, backupName)
	tagCols := make(map[string]bool)
	for _, c := range schema.Columns {
		if c.DataType == "TAG" {
			tagCols[c.Name] = true
		}
	}

	writeAPI := a.client.WriteAPIBlocking(a.org, a.bucket)
	for _, row := range rows {
		tags := map[string]string{}
		fields := map[string]any{}
		var ts time.Time
		for k, v := range row {
			if k == "time" {
				if t, ok := v.(time.Time); ok {
					ts = t
				}
			} else if tagCols[k] || isTagColumn(k) {
				if s, ok := v.(string); ok {
					tags[k] = s
				}
			} else {
				fields[k] = v
			}
		}
		if ts.IsZero() {
			ts = time.Now()
		}
		p := write.NewPoint(originalName, tags, fields, ts)
		if err := writeAPI.WritePoint(ctx, p); err != nil {
			return fmt.Errorf("InfluxDB: 恢复写入数据失败: %w", err)
		}
	}

	return nil
}

func (a *Adapter) DropBackup(ctx context.Context, backupName string) error {
	// 删除备份 measurement 的所有数据
	start := time.Unix(0, 0)
	stop := time.Now().Add(time.Hour)
	predicate := fmt.Sprintf(`_measurement="%s"`, backupName)
	return a.client.DeleteAPI().DeleteWithName(ctx, a.org, a.bucket, start, stop, predicate)
}

func (a *Adapter) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	// InfluxDB 无需建表 DDL，measurement 在写入时自动创建
	return fmt.Sprintf("-- InfluxDB: measurement %s 在写入时自动创建", table.Name), nil
}

func (a *Adapter) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf(`from(bucket: "%s") |> range(start: 0) |> filter(fn: (r) => r._measurement == "%s") |> delete()`, a.bucket, tableName), nil
}

// ReadData 读取数据。
// InfluxDB Flux 返回的是列式数据（每个 field 一条记录），
// 需要 pivot 为行式数据（每个时间点一行，field 作为列）。
func (a *Adapter) ReadData(ctx context.Context, tableName string, offset, limit int) ([]types.Row, error) {
	query := fmt.Sprintf(`from(bucket: "%s")
  |> range(start: 0)
  |> filter(fn: (r) => r._measurement == "%s")
  |> pivot(rowKey:["_time"], columnKey: ["_field"], valueColumn: "_value")
  |> drop(columns: ["_start", "_stop", "_measurement"])
  |> limit(n: %d, offset: %d)`, a.bucket, tableName, limit, offset)
	result, err := a.client.QueryAPI(a.org).Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("InfluxDB: 查询数据失败: %w", err)
	}

	var resultRows []types.Row
	for result.Next() {
		row := make(types.Row)
		row["time"] = result.Record().Time()
		// pivot 后，所有字段都是列
		for k, v := range result.Record().Values() {
			if k == "_time" || k == "_value" || k == "_field" || k == "_measurement" || k == "_start" || k == "_stop" {
				continue
			}
			row[k] = v
		}
		resultRows = append(resultRows, row)
	}
	if err := result.Err(); err != nil {
		return resultRows, fmt.Errorf("InfluxDB: 遍历数据失败: %w", err)
	}
	return resultRows, nil
}

func (a *Adapter) ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]types.Row, error) {
	startTime := "0"
	if lastKey != nil {
		if t, ok := lastKey.(time.Time); ok {
			startTime = t.Format(time.RFC3339Nano)
		}
	}
	query := fmt.Sprintf(`from(bucket: "%s")
  |> range(start: %s)
  |> filter(fn: (r) => r._measurement == "%s")
  |> pivot(rowKey:["_time"], columnKey: ["_field"], valueColumn: "_value")
  |> drop(columns: ["_start", "_stop", "_measurement"])
  |> limit(n: %d)`, a.bucket, startTime, tableName, limit)
	result, err := a.client.QueryAPI(a.org).Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("InfluxDB: 查询数据失败: %w", err)
	}

	var resultRows []types.Row
	for result.Next() {
		row := make(types.Row)
		row["time"] = result.Record().Time()
		for k, v := range result.Record().Values() {
			if k == "_time" || k == "_value" || k == "_field" || k == "_measurement" || k == "_start" || k == "_stop" {
				continue
			}
			row[k] = v
		}
		resultRows = append(resultRows, row)
	}
	if err := result.Err(); err != nil {
		return resultRows, fmt.Errorf("InfluxDB: 遍历数据失败: %w", err)
	}
	return resultRows, nil
}

func (a *Adapter) WriteData(ctx context.Context, tableName string, columns []string, rows []types.Row) error {
	writeAPI := a.client.WriteAPIBlocking(a.org, a.bucket)
	// 查询目标 measurement 的 schema，确定哪些列是 tag
	schema, err := a.GetTableSchema(ctx, tableName)
	tagCols := make(map[string]bool)
	if err == nil {
		for _, c := range schema.Columns {
			if c.DataType == "TAG" {
				tagCols[c.Name] = true
			}
		}
	}
	for _, row := range rows {
		tags := map[string]string{}
		fields := map[string]any{}
		var ts time.Time
		for _, col := range columns {
			val, ok := row[col]
			if !ok {
				continue
			}
			if col == "time" {
				if t, ok := val.(time.Time); ok {
					ts = t
				} else {
					ts = time.Now()
				}
			} else if tagCols[col] || isTagColumn(col) {
				if s, ok := val.(string); ok {
					tags[col] = s
				}
			} else {
				fields[col] = val
			}
		}
		if ts.IsZero() {
			ts = time.Now()
		}
		p := write.NewPoint(tableName, tags, fields, ts)
		if err := writeAPI.WritePoint(ctx, p); err != nil {
			return fmt.Errorf("InfluxDB: 写入数据失败: %w", err)
		}
	}
	return nil
}

func (a *Adapter) ExecContext(ctx context.Context, sqlText string) error {
	// InfluxDB 支持 Flux 查询语言
	result, err := a.client.QueryAPI(a.org).Query(ctx, sqlText)
	if err != nil {
		return fmt.Errorf("InfluxDB: 执行查询失败: %w", err)
	}
	for result.Next() {
		// 消费结果
	}
	return result.Err()
}

func (a *Adapter) MapType(col types.ColumnMeta) string {
	return typeconv.ToInfluxDB(typeconv.Normalize(col.BaseType), col)
}

func (a *Adapter) GetTriggers(ctx context.Context) ([]types.TriggerMeta, error) {
	return nil, nil
}

func (a *Adapter) GetRoutines(ctx context.Context) ([]types.RoutineMeta, error) {
	return nil, nil
}

func (a *Adapter) GenerateTriggerDDL(trigger types.TriggerMeta, targetDialect types.DatabaseType) (string, error) {
	return "", fmt.Errorf("InfluxDB: 不支持触发器")
}

func (a *Adapter) GenerateRoutineDDL(routine types.RoutineMeta, targetDialect types.DatabaseType) (string, error) {
	return "", fmt.Errorf("InfluxDB: 不支持存储过程")
}

// isTagColumn 判断列是否为 tag 类型（InfluxDB 中 tag 为字符串索引列）
func isTagColumn(colName string) bool {
	// 约定：列名以 _tag_ 开头或为常见 tag 名
	return strings.HasPrefix(colName, "tag_") || colName == "host" || colName == "region"
}
