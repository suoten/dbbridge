<script lang="ts" setup>
interface TableReport {
  tableName: string
  rows: number
  status: string
  error?: string
  duration: string
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
}

defineProps<{
  report: MigrationReport | null
}>()

function formatNumber(n: number): string {
  return n.toLocaleString()
}

function statusLabel(status: string): string {
  return { success: '✓ 成功', failed: '✗ 失败', skipped: '⊘ 跳过' }[status] || status
}
</script>

<template>
  <div v-if="report" class="report-panel">
    <!-- 汇总 -->
    <div class="report-summary">
      <div class="summary-item">
        <span class="summary-value success">{{ report.tablesSuccess }}</span>
        <span class="summary-label">成功</span>
      </div>
      <div class="summary-item">
        <span class="summary-value danger" v-if="report.tablesFailed > 0">{{ report.tablesFailed }}</span>
        <span class="summary-value" v-else>0</span>
        <span class="summary-label">失败</span>
      </div>
      <div class="summary-item">
        <span class="summary-value">{{ formatNumber(report.totalRows) }}</span>
        <span class="summary-label">总行数</span>
      </div>
      <div class="summary-item">
        <span class="summary-value">{{ report.duration }}</span>
        <span class="summary-label">耗时</span>
      </div>
    </div>

    <!-- 表详情 -->
    <div class="table-details">
      <table>
        <thead>
          <tr>
            <th>表名</th>
            <th>行数</th>
            <th>状态</th>
            <th>耗时</th>
            <th>错误</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="t in report.tableDetails" :key="t.tableName" :class="t.status">
            <td class="table-name">{{ t.tableName }}</td>
            <td class="table-rows">{{ formatNumber(t.rows) }}</td>
            <td><span class="status-tag" :class="t.status">{{ statusLabel(t.status) }}</span></td>
            <td class="table-duration">{{ t.duration }}</td>
            <td class="table-error" v-if="t.error">{{ t.error }}</td>
            <td v-else>—</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.report-panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.report-summary {
  display: flex;
  gap: 16px;
  padding: 16px;
  background: var(--color-surface);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border);
}

.summary-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  flex: 1;
}

.summary-value {
  font-size: 24px;
  font-weight: 700;
  font-family: var(--font-mono);
  color: var(--color-text);
}

.summary-value.success {
  color: var(--color-success);
}

.summary-value.danger {
  color: var(--color-danger);
}

.summary-label {
  font-size: 12px;
  color: var(--color-text-secondary);
  margin-top: 4px;
}

.table-details {
  background: var(--color-surface);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border);
  overflow: auto;
  max-height: 300px;
}

table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

th {
  padding: 8px 12px;
  text-align: left;
  background: var(--color-surface-hover);
  font-weight: 600;
  color: var(--color-text-secondary);
  border-bottom: 1px solid var(--color-border);
  position: sticky;
  top: 0;
}

td {
  padding: 8px 12px;
  border-bottom: 1px solid var(--color-border);
}

tr:hover {
  background: var(--color-surface-hover);
}

.table-name {
  font-weight: 500;
}

.table-rows, .table-duration {
  font-family: var(--font-mono);
  color: var(--color-text-secondary);
}

.status-tag {
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 11px;
  font-weight: 600;
}

.status-tag.success {
  background: rgba(46, 204, 113, 0.1);
  color: var(--color-success);
}

.status-tag.failed {
  background: rgba(231, 76, 60, 0.1);
  color: var(--color-danger);
}

.status-tag.skipped {
  background: rgba(108, 117, 125, 0.1);
  color: var(--color-text-secondary);
}

.table-error {
  color: var(--color-danger);
  font-size: 12px;
}
</style>
