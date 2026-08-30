// Package service 提供业务服务层，隔离 Wails 绑定层（app.go）与底层实现。
package service

import (
	"context"
	"fmt"

	types "dbbridge/pkg"
)

// ConnectionService 连接与元数据服务
type ConnectionService struct{}

// NewConnectionService 创建连接服务
func NewConnectionService() *ConnectionService {
	return &ConnectionService{}
}

// TestConnection 测试数据库连接，返回版本描述
func (s *ConnectionService) TestConnection(ctx context.Context, config types.ConnectionConfig) (string, error) {
	adapter := types.NewAdapter(config.Type)
	if adapter == nil {
		return "", fmt.Errorf("不支持的数据库类型: %s", config.Type)
	}
	defer adapter.Close()

	if err := adapter.Connect(ctx, config); err != nil {
		return "", err
	}

	version, err := adapter.GetVersion(ctx)
	if err != nil {
		return "", err
	}
	return version, nil
}

// GetTables 获取数据库的表列表
func (s *ConnectionService) GetTables(ctx context.Context, config types.ConnectionConfig) ([]types.TableMeta, error) {
	adapter := types.NewAdapter(config.Type)
	if adapter == nil {
		return nil, fmt.Errorf("不支持的数据库类型: %s", config.Type)
	}
	defer adapter.Close()

	if err := adapter.Connect(ctx, config); err != nil {
		return nil, err
	}
	return adapter.GetTables(ctx)
}

// GetTableSchema 获取单个表的结构
func (s *ConnectionService) GetTableSchema(ctx context.Context, config types.ConnectionConfig, tableName string) (*types.TableSchema, error) {
	adapter := types.NewAdapter(config.Type)
	if adapter == nil {
		return nil, fmt.Errorf("不支持的数据库类型: %s", config.Type)
	}
	defer adapter.Close()

	if err := adapter.Connect(ctx, config); err != nil {
		return nil, err
	}
	schema, err := adapter.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, err
	}
	return &schema, nil
}

// GetSupportedDatabases 获取已支持的数据库类型列表
func (s *ConnectionService) GetSupportedDatabases() []string {
	dbs := types.SupportedDatabases()
	result := make([]string, len(dbs))
	for i, d := range dbs {
		result[i] = string(d)
	}
	return result
}
