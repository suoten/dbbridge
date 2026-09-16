// Package redis 注册 Redis 适配器（NoSQL 键值数据库）。
//
// Redis 是内存键值数据库，支持持久化。本适配器将 Redis 的 key 映射为"表名"，
// value 映射为数据。支持 string/hash/list/set/zset 类型的迁移。
// 使用 github.com/redis/go-redis/v9 驱动。
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dbbridge/internal/typeconv"
	types "dbbridge/pkg"

	"github.com/redis/go-redis/v9"
)

// Adapter Redis 适配器
type Adapter struct {
	client *redis.Client
	brand  string
}

func init() {
	types.RegisterAdapter(types.Redis, func() types.DatabaseAdapter {
		return &Adapter{brand: "Redis"}
	})
}

func (a *Adapter) Connect(ctx context.Context, config types.ConnectionConfig) error {
	port := config.Port
	if port == 0 {
		port = 6379
	}
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", config.Host, port),
		Password: config.Password,
		DB:       0, // Redis 逻辑库编号，默认 0
	})
	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("Redis: 连接失败: %w", err)
	}
	a.client = client
	return nil
}

func (a *Adapter) Close() error {
	if a.client != nil {
		return a.client.Close()
	}
	return nil
}

func (a *Adapter) GetVersion(ctx context.Context) (string, error) {
	info, err := a.client.Info(ctx, "server").Result()
	if err != nil {
		return "", fmt.Errorf("Redis: 获取版本失败: %w", err)
	}
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "redis_version:") {
			return "Redis " + strings.TrimSpace(strings.TrimPrefix(line, "redis_version:")), nil
		}
	}
	return "Redis (unknown version)", nil
}

func (a *Adapter) GetTables(ctx context.Context) ([]types.TableMeta, error) {
	// Redis 的"表"概念：将 key 前缀模式作为表名
	// 使用 SCAN 遍历 key，按前缀分组
	var tables []types.TableMeta
	seen := map[string]bool{}
	iter := a.client.Scan(ctx, 0, "*", 1000).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		// 按 ":" 分割取第一段作为"表名"
		parts := strings.SplitN(key, ":", 2)
		tableName := parts[0]
		if len(parts) > 1 {
			tableName = parts[0]
		} else {
			tableName = "_default"
		}
		if !seen[tableName] {
			seen[tableName] = true
			tables = append(tables, types.TableMeta{Name: tableName})
		}
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("Redis: 扫描 key 失败: %w", err)
	}
	return tables, nil
}

func (a *Adapter) GetTableSchema(ctx context.Context, tableName string) (types.TableSchema, error) {
	// Redis 无固定 schema，返回通用 schema：key + value
	schema := types.TableSchema{
		Name: tableName,
		Columns: []types.ColumnMeta{
			{Name: "key", DataType: "VARCHAR", BaseType: "VARCHAR", Nullable: false, IsPrimaryKey: true},
			{Name: "type", DataType: "VARCHAR", BaseType: "VARCHAR", Nullable: false},
			{Name: "value", DataType: "TEXT", BaseType: "TEXT", Nullable: true},
		},
		Indexes: []types.IndexMeta{
			{Name: "PRIMARY", Columns: []string{"key"}, IsUnique: true, IsPrimary: true},
		},
	}
	return schema, nil
}

func (a *Adapter) GetRowCount(ctx context.Context, tableName string) (int64, error) {
	var count int64
	pattern := tableName + ":*"
	iter := a.client.Scan(ctx, 0, pattern, 1000).Iterator()
	for iter.Next(ctx) {
		count++
	}
	return count, iter.Err()
}

func (a *Adapter) TableExists(ctx context.Context, tableName string) (bool, error) {
	count, err := a.GetRowCount(ctx, tableName)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (a *Adapter) BackupTable(ctx context.Context, tableName string) (string, error) {
	backupName := fmt.Sprintf("_bak_%s_%s", tableName, time.Now().Format("20060102_150405"))
	// Redis 备份：将所有 key 前缀替换
	pattern := tableName + ":*"
	iter := a.client.Scan(ctx, 0, pattern, 1000).Iterator()
	for iter.Next(ctx) {
		oldKey := iter.Val()
		newKey := strings.Replace(oldKey, tableName+":", backupName+":", 1)
		// 用 DUMP + RESTORE 复制
		dump, err := a.client.Dump(ctx, oldKey).Result()
		if err != nil {
			continue
		}
		ttl, _ := a.client.PTTL(ctx, oldKey).Result()
		a.client.RestoreReplace(ctx, newKey, ttl, dump)
	}
	return backupName, iter.Err()
}

func (a *Adapter) RestoreFromBackup(ctx context.Context, backupName, originalName string) error {
	// 删除原 key，将备份 key 改回原名
	pattern := originalName + ":*"
	iter := a.client.Scan(ctx, 0, pattern, 1000).Iterator()
	var keysToDelete []string
	for iter.Next(ctx) {
		keysToDelete = append(keysToDelete, iter.Val())
	}
	if len(keysToDelete) > 0 {
		a.client.Del(ctx, keysToDelete...)
	}
	// 恢复
	bakPattern := backupName + ":*"
	bakIter := a.client.Scan(ctx, 0, bakPattern, 1000).Iterator()
	for bakIter.Next(ctx) {
		oldKey := bakIter.Val()
		newKey := strings.Replace(oldKey, backupName+":", originalName+":", 1)
		dump, err := a.client.Dump(ctx, oldKey).Result()
		if err != nil {
			continue
		}
		ttl, _ := a.client.PTTL(ctx, oldKey).Result()
		a.client.RestoreReplace(ctx, newKey, ttl, dump)
		a.client.Del(ctx, oldKey)
	}
	return bakIter.Err()
}

func (a *Adapter) DropBackup(ctx context.Context, backupName string) error {
	pattern := backupName + ":*"
	iter := a.client.Scan(ctx, 0, pattern, 1000).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if len(keys) > 0 {
		a.client.Del(ctx, keys...)
	}
	return iter.Err()
}

func (a *Adapter) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	return fmt.Sprintf("-- Redis: 键值存储无需建表 DDL（前缀 %s）", table.Name), nil
}

func (a *Adapter) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf("-- Redis: 删除前缀 %s:* 的所有键", tableName), nil
}

func (a *Adapter) ReadData(ctx context.Context, tableName string, offset, limit int) ([]types.Row, error) {
	pattern := tableName + ":*"
	iter := a.client.Scan(ctx, 0, pattern, 1000).Iterator()
	var result []types.Row
	skipped := 0
	for iter.Next(ctx) {
		if skipped < offset {
			skipped++
			continue
		}
		if len(result) >= limit {
			break
		}
		key := iter.Val()
		keyType, _ := a.client.Type(ctx, key).Result()
		var value string
		switch keyType {
		case "string":
			value, _ = a.client.Get(ctx, key).Result()
		case "hash":
			// 保留多字段结构：序列化为 JSON
			hashVal, _ := a.client.HGetAll(ctx, key).Result()
			if b, err := json.Marshal(hashVal); err == nil {
				value = string(b)
			} else {
				value = fmt.Sprintf("%v", hashVal)
			}
		case "list":
			listVal, _ := a.client.LRange(ctx, key, 0, -1).Result()
			if b, err := json.Marshal(listVal); err == nil {
				value = string(b)
			} else {
				value = strings.Join(listVal, ",")
			}
		case "set":
			setVal, _ := a.client.SMembers(ctx, key).Result()
			if b, err := json.Marshal(setVal); err == nil {
				value = string(b)
			} else {
				value = strings.Join(setVal, ",")
			}
		case "zset":
			zsetVal, _ := a.client.ZRangeWithScores(ctx, key, 0, -1).Result()
			if b, err := json.Marshal(zsetVal); err == nil {
				value = string(b)
			} else {
				zsetStr := make([]string, len(zsetVal))
				for i, z := range zsetVal {
					zsetStr[i] = fmt.Sprintf("%v:%v", z.Member, z.Score)
				}
				value = strings.Join(zsetStr, ",")
			}
		default:
			value = fmt.Sprintf("(type: %s)", keyType)
		}
		result = append(result, types.Row{
			"key":   key,
			"type":  string(keyType),
			"value": value,
		})
	}
	if err := iter.Err(); err != nil {
		return result, fmt.Errorf("Redis: 扫描数据失败: %w", err)
	}
	return result, nil
}

func (a *Adapter) ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]types.Row, error) {
	// Redis 没有有序主键概念，退化为 offset 分页
	return a.ReadData(ctx, tableName, 0, limit)
}

func (a *Adapter) WriteData(ctx context.Context, tableName string, columns []string, rows []types.Row) error {
	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("Redis: 写入被取消 (已处理 %d/%d 行): %w", i, len(rows), err)
		}
		key, _ := row["key"].(string)
		if key == "" {
			continue
		}
		// 前缀替换为目标表名
		parts := strings.SplitN(key, ":", 2)
		if len(parts) > 1 {
			key = tableName + ":" + parts[1]
		} else {
			key = tableName + ":" + key
		}
		value, _ := row["value"].(string)
		keyType, _ := row["type"].(string)
		var cmd redis.Cmder
		switch keyType {
		case "string":
			cmd = a.client.Set(ctx, key, value, 0)
		case "hash":
			// 反序列化 JSON 还原多字段 hash
			var hashFields map[string]string
			if err := json.Unmarshal([]byte(value), &hashFields); err == nil && len(hashFields) > 0 {
				args := make([]any, 0, len(hashFields)*2)
				for k, v := range hashFields {
					args = append(args, k, v)
				}
				cmd = a.client.HSet(ctx, key, args...)
			} else {
				cmd = a.client.HSet(ctx, key, "value", value)
			}
		case "list":
			// 反序列化 JSON 还原多元素 list
			var listVals []string
			if err := json.Unmarshal([]byte(value), &listVals); err == nil && len(listVals) > 0 {
				args := make([]any, len(listVals))
				for i, v := range listVals {
					args[i] = v
				}
				cmd = a.client.RPush(ctx, key, args...)
			} else {
				cmd = a.client.RPush(ctx, key, value)
			}
		case "set":
			// 反序列化 JSON 还原多元素 set
			var setVals []string
			if err := json.Unmarshal([]byte(value), &setVals); err == nil && len(setVals) > 0 {
				args := make([]any, len(setVals))
				for i, v := range setVals {
					args[i] = v
				}
				cmd = a.client.SAdd(ctx, key, args...)
			} else {
				cmd = a.client.SAdd(ctx, key, value)
			}
		case "zset":
			// 反序列化 JSON 还原带分数的 zset
			var zsetVals []redis.Z
			if err := json.Unmarshal([]byte(value), &zsetVals); err == nil && len(zsetVals) > 0 {
				cmd = a.client.ZAdd(ctx, key, zsetVals...)
			} else {
				cmd = a.client.Set(ctx, key, value, 0)
			}
		default:
			cmd = a.client.Set(ctx, key, value, 0)
		}
		if cmd != nil && cmd.Err() != nil {
			return fmt.Errorf("Redis: 写入 key=%s 失败 (已处理 %d 行): %w", key, i, cmd.Err())
		}
	}
	return nil
}

func (a *Adapter) ExecContext(ctx context.Context, sqlText string) error {
	// Redis 不执行 SQL，支持 Redis 命令
	cmd := a.client.Do(ctx, sqlText)
	return cmd.Err()
}

func (a *Adapter) MapType(col types.ColumnMeta) string {
	return typeconv.ToRedis(typeconv.Normalize(col.BaseType), col)
}

func (a *Adapter) GetTriggers(ctx context.Context) ([]types.TriggerMeta, error) {
	return nil, nil
}

func (a *Adapter) GetRoutines(ctx context.Context) ([]types.RoutineMeta, error) {
	return nil, nil
}

func (a *Adapter) GenerateTriggerDDL(trigger types.TriggerMeta, targetDialect types.DatabaseType) (string, error) {
	return "", fmt.Errorf("Redis: 不支持触发器")
}

func (a *Adapter) GenerateRoutineDDL(routine types.RoutineMeta, targetDialect types.DatabaseType) (string, error) {
	return "", fmt.Errorf("Redis: 不支持存储过程")
}
