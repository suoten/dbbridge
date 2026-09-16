// Package mongodb 注册 MongoDB 适配器（NoSQL 文档数据库）。
//
// MongoDB 是最流行的文档数据库。本适配器将 MongoDB 的 collection 映射为"表"，
// document 字段映射为"列"。使用官方 Go Driver（go.mongodb.org/mongo-driver）。
// 类型映射将 BSON 类型与其他数据库的关系型类型做转换。
package mongodb

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dbbridge/internal/typeconv"
	types "dbbridge/pkg"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Adapter MongoDB 适配器
type Adapter struct {
	client *mongo.Client
	db     *mongo.Database
	brand  string
}

func init() {
	types.RegisterAdapter(types.MongoDB, func() types.DatabaseAdapter {
		return &Adapter{brand: "MongoDB"}
	})
}

func (a *Adapter) Connect(ctx context.Context, config types.ConnectionConfig) error {
	port := config.Port
	if port == 0 {
		port = 27017
	}
	uri := fmt.Sprintf("mongodb://%s:%s@%s:%d/%s",
		config.Username, config.Password, config.Host, port, config.Database)
	if config.Username == "" {
		uri = fmt.Sprintf("mongodb://%s:%d/%s", config.Host, port, config.Database)
	}
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return fmt.Errorf("MongoDB: 连接失败: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("MongoDB: ping 失败: %w", err)
	}
	a.client = client
	a.db = client.Database(config.Database)
	return nil
}

func (a *Adapter) Close() error {
	if a.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return a.client.Disconnect(ctx)
	}
	return nil
}

func (a *Adapter) GetVersion(ctx context.Context) (string, error) {
	var result bson.M
	err := a.client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&result)
	if err != nil {
		return "", fmt.Errorf("MongoDB: 获取版本失败: %w", err)
	}
	version, _ := result["version"].(string)
	if version == "" {
		return "MongoDB (unknown version)", nil
	}
	return "MongoDB " + version, nil
}

func (a *Adapter) GetTables(ctx context.Context) ([]types.TableMeta, error) {
	collections, err := a.db.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("MongoDB: 查询集合列表失败: %w", err)
	}
	var tables []types.TableMeta
	for _, name := range collections {
		tables = append(tables, types.TableMeta{Name: name})
	}
	return tables, nil
}

func (a *Adapter) GetTableSchema(ctx context.Context, tableName string) (types.TableSchema, error) {
	schema := types.TableSchema{Name: tableName}

	// 采样前 100 个文档推断字段类型
	collection := a.db.Collection(tableName)
	opts := options.Find().SetLimit(100)
	cursor, err := collection.Find(ctx, bson.D{}, opts)
	if err != nil {
		return schema, fmt.Errorf("MongoDB: 查询集合失败: %w", err)
	}
	defer cursor.Close(ctx)

	fieldTypes := map[string]string{}
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			continue
		}
		for k, v := range doc {
			if k == "_id" {
				fieldTypes[k] = "OBJECTID"
				continue
			}
			if _, exists := fieldTypes[k]; !exists {
				fieldTypes[k] = bsonTypeToSQL(v)
			}
		}
	}

	// 转换为列信息
	idFirst := false
	for k, t := range fieldTypes {
		if k == "_id" {
			idFirst = true
			schema.Columns = append([]types.ColumnMeta{{
				Name: "_id", DataType: "OBJECTID", BaseType: "VARCHAR",
				IsPrimaryKey: true, Nullable: false,
			}}, schema.Columns...)
			continue
		}
		schema.Columns = append(schema.Columns, types.ColumnMeta{
			Name: k, DataType: t, BaseType: t, Nullable: true,
		})
	}
	if idFirst {
		schema.Indexes = append(schema.Indexes, types.IndexMeta{
			Name: "_id_", Columns: []string{"_id"}, IsUnique: true, IsPrimary: true,
		})
	}
	return schema, nil
}

func (a *Adapter) GetRowCount(ctx context.Context, tableName string) (int64, error) {
	count, err := a.db.Collection(tableName).EstimatedDocumentCount(ctx)
	if err != nil {
		return 0, fmt.Errorf("MongoDB: 获取行数失败: %w", err)
	}
	return count, nil
}

func (a *Adapter) TableExists(ctx context.Context, tableName string) (bool, error) {
	collections, err := a.db.ListCollectionNames(ctx, bson.D{{Key: "name", Value: tableName}})
	if err != nil {
		return false, fmt.Errorf("MongoDB: 检查集合存在失败: %w", err)
	}
	return len(collections) > 0, nil
}

func (a *Adapter) BackupTable(ctx context.Context, tableName string) (string, error) {
	backupName := fmt.Sprintf("_bak_%s_%s", tableName, time.Now().Format("20060102_150405"))
	// MongoDB 没有 RENAME，用聚合管道 $out 复制再删原集合
	collection := a.db.Collection(tableName)
	pipeline := []bson.D{{{Key: "$match", Value: bson.D{}}}, {{Key: "$out", Value: backupName}}}
	if _, err := collection.Aggregate(ctx, pipeline); err != nil {
		return "", fmt.Errorf("MongoDB: 备份集合失败: %w", err)
	}
	if err := collection.Drop(ctx); err != nil {
		return backupName, fmt.Errorf("MongoDB: 备份后删除原集合失败: %w", err)
	}
	return backupName, nil
}

func (a *Adapter) RestoreFromBackup(ctx context.Context, backupName, originalName string) error {
	backupCol := a.db.Collection(backupName)
	pipeline := []bson.D{{{Key: "$match", Value: bson.D{}}}, {{Key: "$out", Value: originalName}}}
	if _, err := backupCol.Aggregate(ctx, pipeline); err != nil {
		return fmt.Errorf("MongoDB: 恢复备份失败: %w", err)
	}
	if err := backupCol.Drop(ctx); err != nil {
		return fmt.Errorf("MongoDB: 恢复后删除备份集合失败: %w", err)
	}
	return nil
}

func (a *Adapter) DropBackup(ctx context.Context, backupName string) error {
	if err := a.db.Collection(backupName).Drop(ctx); err != nil {
		return fmt.Errorf("MongoDB: 删除备份集合失败: %w", err)
	}
	return nil
}

func (a *Adapter) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	// MongoDB 无需建表 DDL，集合在首次写入时自动创建
	return fmt.Sprintf("-- MongoDB: 集合 %s 在首次写入时自动创建", table.Name), nil
}

func (a *Adapter) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf(`db.%s.drop()`, tableName), nil
}

func (a *Adapter) ReadData(ctx context.Context, tableName string, offset, limit int) ([]types.Row, error) {
	collection := a.db.Collection(tableName)
	opts := options.Find().SetSkip(int64(offset)).SetLimit(int64(limit))
	cursor, err := collection.Find(ctx, bson.D{}, opts)
	if err != nil {
		return nil, fmt.Errorf("MongoDB: 查询数据失败: %w", err)
	}
	defer cursor.Close(ctx)

	var result []types.Row
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("MongoDB: 解码数据失败: %w", err)
		}
		row := make(types.Row)
		for k, v := range doc {
			row[k] = normalizeBSONValue(v)
		}
		result = append(result, row)
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("MongoDB: 遍历数据失败: %w", err)
	}
	return result, nil
}

func (a *Adapter) ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]types.Row, error) {
	collection := a.db.Collection(tableName)
	filter := bson.D{}
	if lastKey != nil {
		filter = bson.D{{Key: keyColumn, Value: bson.D{{Key: "$gt", Value: lastKey}}}}
	}
	opts := options.Find().SetSort(bson.D{{Key: keyColumn, Value: 1}}).SetLimit(int64(limit))
	cursor, err := collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("MongoDB: 查询数据失败: %w", err)
	}
	defer cursor.Close(ctx)

	var result []types.Row
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("MongoDB: 解码数据失败: %w", err)
		}
		row := make(types.Row)
		for k, v := range doc {
			row[k] = normalizeBSONValue(v)
		}
		result = append(result, row)
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("MongoDB: 遍历数据失败: %w", err)
	}
	return result, nil
}

func (a *Adapter) WriteData(ctx context.Context, tableName string, columns []string, rows []types.Row) error {
	if len(rows) == 0 {
		return nil
	}
	collection := a.db.Collection(tableName)
	docs := make([]any, 0, len(rows))
	for _, row := range rows {
		doc := make(bson.M)
		for _, col := range columns {
			if val, ok := row[col]; ok {
				// _id 列：如果是 24 位 hex 字符串，转回 ObjectID 以保持主键一致性
				if col == "_id" {
					if s, ok := val.(string); ok && len(s) == 24 {
						if oid, err := primitive.ObjectIDFromHex(s); err == nil {
							doc[col] = oid
							continue
						}
					}
				}
				// 尝试将 JSON 字符串还原为嵌套文档（关系型 → MongoDB 场景）
				if s, ok := val.(string); ok && len(s) > 1 && s[0] == '{' {
					var m map[string]any
					if err := json.Unmarshal([]byte(s), &m); err == nil {
						doc[col] = m
						continue
					}
				}
				// 还原 hex 编码的二进制数据
				if s, ok := val.(string); ok && strings.HasPrefix(s, "hex:") {
					if b, err := hex.DecodeString(s[4:]); err == nil {
						doc[col] = b
						continue
					}
				}
				doc[col] = val
			}
		}
		docs = append(docs, doc)
	}
	_, err := collection.InsertMany(ctx, docs)
	if err != nil {
		return fmt.Errorf("MongoDB: 写入数据失败: %w", err)
	}
	return nil
}

func (a *Adapter) ExecContext(ctx context.Context, sqlText string) error {
	// MongoDB 不执行 SQL，支持 db.runCommand() 风格的 JSON 命令
	var cmd bson.M
	if err := bson.UnmarshalExtJSON([]byte(sqlText), false, &cmd); err != nil {
		return fmt.Errorf("MongoDB: 无法解析命令: %w", err)
	}
	if _, err := a.db.RunCommand(ctx, cmd).DecodeBytes(); err != nil {
		return fmt.Errorf("MongoDB: 执行命令失败: %w", err)
	}
	return nil
}

func (a *Adapter) MapType(col types.ColumnMeta) string {
	return typeconv.ToMongoDB(typeconv.Normalize(col.BaseType), col)
}

func (a *Adapter) GetTriggers(ctx context.Context) ([]types.TriggerMeta, error) {
	return nil, nil
}

func (a *Adapter) GetRoutines(ctx context.Context) ([]types.RoutineMeta, error) {
	return nil, nil
}

func (a *Adapter) GenerateTriggerDDL(trigger types.TriggerMeta, targetDialect types.DatabaseType) (string, error) {
	return "", fmt.Errorf("MongoDB: 不支持触发器")
}

func (a *Adapter) GenerateRoutineDDL(routine types.RoutineMeta, targetDialect types.DatabaseType) (string, error) {
	return "", fmt.Errorf("MongoDB: 不支持存储过程")
}

// bsonTypeToSQL 将 BSON 值类型映射为 SQL 类型名
func bsonTypeToSQL(v any) string {
	switch v.(type) {
	case int32:
		return "INT"
	case int64:
		return "BIGINT"
	case float64:
		return "DOUBLE"
	case bool:
		return "BOOLEAN"
	case string:
		return "VARCHAR"
	case time.Time:
		return "DATETIME"
	case primitive.ObjectID:
		return "OBJECTID"
	case bson.M, bson.D:
		return "JSON"
	case []byte:
		return "BLOB"
	default:
		return "TEXT"
	}
}

// normalizeBSONValue 将 BSON 特有类型转为可迁移的通用值。
// 嵌套文档递归转为 map[string]any（Go 原生类型），保留结构化语义：
//   - MongoDB → MongoDB：WriteData 中的 map 可直接被 bson.Marshal 编码为嵌套文档
//   - MongoDB → 关系型：map 被转为 JSON 字符串存储（由 typeconv 处理）
//   - 关系型 → MongoDB：JSON 字符串在 WriteData 中被解析回 map
func normalizeBSONValue(v any) any {
	switch val := v.(type) {
	case primitive.ObjectID:
		return val.Hex()
	case bson.M:
		// 递归转换嵌套文档为 map[string]any
		result := make(map[string]any, len(val))
		for k, v := range val {
			result[k] = normalizeBSONValue(v)
		}
		return result
	case bson.D:
		// bson.D 是有序文档，转为 map（丢失顺序但保留数据）
		result := make(map[string]any, len(val))
		for _, e := range val {
			result[e.Key] = normalizeBSONValue(e.Value)
		}
		return result
	case bson.A:
		// BSON 数组转为 []any
		result := make([]any, len(val))
		for i, v := range val {
			result[i] = normalizeBSONValue(v)
		}
		return result
	case primitive.DateTime:
		return val.Time().Format("2006-01-02 15:04:05")
	case primitive.Timestamp:
		return fmt.Sprintf("%d:%d", val.T, val.I)
	case time.Time:
		return val.Format("2006-01-02 15:04:05")
	case []byte:
		// Binary data 转为 hex 字符串
		return fmt.Sprintf("hex:%x", val)
	default:
		return v
	}
}

func schemaColumnNames(schema types.TableSchema) []string {
	var cols []string
	for _, c := range schema.Columns {
		cols = append(cols, c.Name)
	}
	return cols
}

// 引用未使用的函数防止编译器警告
var _ = schemaColumnNames
var _ = strings.TrimSpace
var _ = time.Now
