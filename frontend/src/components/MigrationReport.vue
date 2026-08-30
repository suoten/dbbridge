<script lang="ts" setup>
import {
  CheckCircle2,
  XCircle,
  Database,
  Clock,
  Hash,
  Layers,
  AlertCircle,
  Table2,
  Archive,
  RotateCcw,
  ShieldCheck,
} from '@lucide/vue'

interface TableReport {
  tableName: string
  rows: number
  status: string
  error?: string
  duration: string
  backupTable?: string
  rolledBack?: boolean
}

interface BackupInfo {
  originalTable: string
  backupTable: string
  createdAt: string
  restored?: boolean
}

interface MigrationReport {
  startTime: string
  endTime: string
  duration: string
  tablesTotal: number
  tablesSuccess: number
  tablesFailed: number
  totalRows: number
  failedTables?: string[]
  tableDetails: TableReport[]
  backups?: BackupInfo[]
  rollbackCount?: number
}

defineProps<{
  report: MigrationReport | null
}>()

function formatNumber(n: number): string {
  return n.toLocaleString()
}

const statusConfig: Record<string, { label: string; icon: any; color: string; bg: string }> = {
  success: { label: '成功', icon: CheckCircle2, color: 'var(--color-success)', bg: 'var(--color-success-light)' },
  failed: { label: '失败', icon: XCircle, color: 'var(--color-danger)', bg: 'var(--color-danger-light)' },
  rolled_back: { label: '已回滚', icon: RotateCcw, color: 'var(--color-warning)', bg: 'var(--color-warning-light)' },
  skipped: { label: '跳过', icon: AlertCircle, color: 'var(--color-text-secondary)', bg: 'var(--color-bg)' },
}

function getStatus(s: string) {
  return statusConfig[s] || statusConfig.skipped
}
</script>

<template>
  <div v-if="report" class="report-panel">
    <!-- ====== Summary Cards ====== -->
    <div class="summary-grid">
      <div class="summary-card success-card">
        <div class="summary-icon"><CheckCircle2 :size="24" /></div>
        <div class="summary-body">
          <span class="summary-value">{{ report.tablesSuccess }}</span>
          <span class="summary-label">成功表</span>
        </div>
      </div>

      <div class="summary-card" :class="{ 'danger-card': report.tablesFailed > 0 }">
        <div class="summary-icon"><XCircle :size="24" /></div>
        <div class="summary-body">
          <span class="summary-value">{{ report.tablesFailed }}</span>
          <span class="summary-label">失败表</span>
        </div>
      </div>

      <div class="summary-card info-card">
        <div class="summary-icon"><Hash :size="24" /></div>
        <div class="summary-body">
          <span class="summary-value">{{ formatNumber(report.totalRows) }}</span>
          <span class="summary-label">总行数</span>
        </div>
      </div>

      <div class="summary-card neutral-card">
        <div class="summary-icon"><Clock :size="24" /></div>
        <div class="summary-body">
          <span class="summary-value">{{ report.duration }}</span>
          <span class="summary-label">总耗时</span>
        </div>
      </div>

      <div class="summary-card warning-card" v-if="report.rollbackCount && report.rollbackCount > 0">
        <div class="summary-icon"><RotateCcw :size="24" /></div>
        <div class="summary-body">
          <span class="summary-value">{{ report.rollbackCount }}</span>
          <span class="summary-label">已回滚</span>
        </div>
      </div>
      <div class="summary-card info-card" v-else-if="report.backups && report.backups.length > 0">
        <div class="summary-icon"><Archive :size="24" /></div>
        <div class="summary-body">
          <span class="summary-value">{{ report.backups.length }}</span>
          <span class="summary-label">备份表</span>
        </div>
      </div>
    </div>

    <!-- ====== Table Details ====== -->
    <div class="details-section">
      <div class="details-header">
        <Layers :size="16" />
        <span>表迁移详情</span>
      </div>

      <div class="table-card-list">
        <div
          v-for="t in report.tableDetails"
          :key="t.tableName"
          class="table-card"
          :class="t.status"
        >
          <div class="table-card-main">
            <div class="table-card-icon">
              <Table2 :size="16" />
            </div>
            <span class="table-card-name">{{ t.tableName }}</span>
            <span class="table-card-status" :style="{ color: getStatus(t.status).color, background: getStatus(t.status).bg }">
              <component :is="getStatus(t.status).icon" :size="11" />
              {{ getStatus(t.status).label }}
            </span>
          </div>
          <div class="table-card-meta">
            <span class="meta-item">
              <Hash :size="12" />
              {{ formatNumber(t.rows) }} 行
            </span>
            <span class="meta-item">
              <Clock :size="12" />
              {{ t.duration }}
            </span>
            <span class="meta-item backup" v-if="t.backupTable" :class="{ restored: t.rolledBack }">
              <Archive :size="12" />
              {{ t.backupTable }}
            </span>
            <span class="meta-error" v-if="t.error && !t.rolledBack" :title="t.error">
              <AlertCircle :size="12" />
              {{ t.error }}
            </span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.report-panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* ====== Summary Cards ====== */
.summary-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
}

.summary-card {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 18px 16px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border);
  background: var(--color-surface);
  box-shadow: var(--shadow-sm);
  transition: transform var(--transition-fast);
}

.summary-card:hover {
  transform: translateY(-2px);
  box-shadow: var(--shadow-md);
}

.summary-icon {
  width: 44px;
  height: 44px;
  border-radius: var(--radius-md);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.success-card .summary-icon {
  background: var(--color-success-light);
  color: var(--color-success);
}

.danger-card .summary-icon {
  background: var(--color-danger-light);
  color: var(--color-danger);
}

.info-card .summary-icon {
  background: var(--color-info-light);
  color: var(--color-info);
}

.neutral-card .summary-icon {
  background: var(--color-bg);
  color: var(--color-text-secondary);
}

.summary-body {
  display: flex;
  flex-direction: column;
}

.summary-value {
  font-size: 22px;
  font-weight: 700;
  font-family: var(--font-mono);
  color: var(--color-text);
  line-height: 1.2;
}

.success-card .summary-value { color: var(--color-success); }
.danger-card .summary-value { color: var(--color-danger); }

.summary-label {
  font-size: 11px;
  color: var(--color-text-secondary);
  margin-top: 2px;
}

/* ====== Details ====== */
.details-section {
  background: var(--color-surface);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border);
  overflow: hidden;
}

.details-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--color-border);
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text);
}

.table-card-list {
  max-height: 300px;
  overflow-y: auto;
  padding: 8px;
}

.table-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 14px;
  border-radius: var(--radius-md);
  margin-bottom: 4px;
  transition: background var(--transition-fast);
}

.table-card:hover {
  background: var(--color-surface-hover);
}

.table-card.failed {
  background: rgba(239, 68, 68, 0.04);
}

.table-card-main {
  display: flex;
  align-items: center;
  gap: 8px;
}

.table-card-icon {
  color: var(--color-text-tertiary);
  display: flex;
}

.table-card.success .table-card-icon {
  color: var(--color-success);
}

.table-card.failed .table-card-icon {
  color: var(--color-danger);
}

.table-card-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--color-text);
  font-family: var(--font-mono);
}

.table-card-status {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 2px 8px;
  border-radius: 10px;
  font-size: 10px;
  font-weight: 700;
}

.table-card-meta {
  display: flex;
  align-items: center;
  gap: 14px;
}

.meta-item {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--color-text-secondary);
  font-family: var(--font-mono);
}

.meta-error {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  color: var(--color-danger);
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.meta-item.backup {
  color: var(--color-warning);
}

.meta-item.backup.restored {
  color: var(--color-info);
}

/* Warning card (rollback count) */
.warning-card .summary-icon {
  background: var(--color-warning-light);
  color: var(--color-warning);
}

/* ====== 响应式 ====== */

/* ≤1024px：统计卡片降为 2 列 */
@media (max-width: 1024px) {
  .summary-grid {
    grid-template-columns: repeat(2, 1fr);
  }
}

/* ≤560px：明细行允许换行，避免横向溢出 */
@media (max-width: 560px) {
  .table-card {
    flex-wrap: wrap;
    gap: 6px;
  }

  .table-card-meta {
    flex-basis: 100%;
    flex-wrap: wrap;
    gap: 8px;
  }

  .meta-error {
    max-width: 100%;
    white-space: normal;
  }
}

.warning-card .summary-value {
  color: var(--color-warning);
}
</style>
