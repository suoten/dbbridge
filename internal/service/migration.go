package service

import (
	"context"
	"sync"

	"dbbridge/internal/orchestrator"
	types "dbbridge/pkg"
)

// MigrationService 迁移服务：管理迁移任务的执行与取消。
//
// 同一时刻只允许一个迁移任务运行；Cancel 会取消当前任务，
// 编排器通过 context 感知取消并尽快收尾返回。
type MigrationService struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
}

// NewMigrationService 创建迁移服务
func NewMigrationService() *MigrationService {
	return &MigrationService{}
}

// Run 执行迁移（阻塞直到完成或取消）。onProgress/onLog 回调由调用方提供。
// 若已有任务在执行，返回报告并填充 Error 字段。
func (m *MigrationService) Run(config types.MigrationConfig, onProgress orchestrator.ProgressCallback, onLog orchestrator.LogCallback) (*types.MigrationReport, error) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return &types.MigrationReport{Error: "已有迁移任务在执行中，请等待完成或先取消"}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.running = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.running = false
		m.cancel = nil
		m.mu.Unlock()
		cancel()
	}()

	orch := orchestrator.NewOrchestrator(config, onProgress, onLog)
	return orch.Run(ctx)
}

// Cancel 取消当前迁移任务，返回是否有任务被取消
func (m *MigrationService) Cancel() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running && m.cancel != nil {
		m.cancel()
		return true
	}
	return false
}

// IsRunning 返回是否有任务在执行
func (m *MigrationService) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}
