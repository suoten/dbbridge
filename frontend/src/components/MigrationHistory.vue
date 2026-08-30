<script lang="ts" setup>
import { ref, onMounted } from 'vue'
import {
  History,
  Database,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  ArrowRight,
  Clock,
  Hash,
  Trash2,
  RotateCcw,
  Loader2,
  Archive,
} from '@lucide/vue'
import type { ConnectionConfig } from './ConnectionForm.vue'

const emit = defineEmits<{
  'select-target': [config: ConnectionConfig]
}>()

interface MigrationRecord {
  id: number
  startTime: string
  endTime: string
  duration: string
  sourceType: string
  sourceHost: string
  sourceDB: string
  targetType: string
  targetHost: string
  targetDB: string
  tablesTotal: number
  tablesSuccess: number
  tablesFailed: number
  totalRows: number
  status: string
  backups?: any[]
  createdAt: string
}

const records = ref<MigrationRecord[]>([])
const loading = ref(false)

const statusConfig: Record<string, { label: string; icon: any; color: string; bg: string }> = {
  success: { label: '成功', icon: CheckCircle2, color: 'var(--color-success)', bg: 'var(--color-success-light)' },
  failed: { label: '失败', icon: XCircle, color: 'var(--color-danger)', bg: 'var(--color-danger-light)' },
  partial: { label: '部分成功', icon: AlertTriangle, color: 'var(--color-warning)', bg: 'var(--color-warning-light)' },
}

function getStatus(s: string) {
  return statusConfig[s] || { label: s, icon: AlertTriangle, color: 'var(--color-text-secondary)', bg: 'var(--color-bg)' }
}

async function loadHistory() {
  loading.value = true
  try {
    // @ts-ignore - Wails binding
    const result = await window.go.main.App.GetMigrationHistory()
    records.value = result || []
  } catch (e) {
    console.error('Failed to load history:', e)
  } finally {
    loading.value = false
  }
}

async function deleteRecord(id: number) {
  try {
    // @ts-ignore - Wails binding
    await window.go.main.App.DeleteMigrationHistory(id)
    records.value = records.value.filter(r => r.id !== id)
  } catch (e) {
    console.error('Failed to delete record:', e)
  }
}

function restoreToTarget(record: MigrationRecord) {
  // 将历史记录中的目标库信息填入连接表单
  emit('select-target', {
    type: record.targetType,
    host: record.targetHost === '(file)' ? '' : record.targetHost,
    port: getDefaultPort(record.targetType),
    username: '',
    password: '',
    database: record.targetDB,
  })
}

function getDefaultPort(type: string): number {
  const ports: Record<string, number> = {
    mysql: 3306, mariadb: 3306, tidb: 4000, oceanbase: 2881,
    postgres: 5432, opengauss: 5432, kingbase: 5432, cockroachdb: 5432,
    dameng: 5236, sqlite: 0,
  }
  return ports[type] || 0
}

function formatNumber(n: number): string {
  return n.toLocaleString()
}

onMounted(() => {
  loadHistory()
})
</script>

<template>
  <div class="history-panel">
    <div class="mh-header">
      <div class="mh-title">
        <History :size="18" />
        <span>迁移历史</span>
        <span class="mh-count" v-if="records.length > 0">{{ records.length }} 条记录</span>
      </div>
      <button class="btn btn-ghost btn-sm" @click="loadHistory" :disabled="loading">
        <Loader2 v-if="loading" :size="14" class="animate-spin" />
        <RotateCcw v-else :size="14" />
        刷新
      </button>
    </div>

    <!-- 记录列表 -->
    <div class="mh-list" v-if="records.length > 0">
      <div
        v-for="r in records"
        :key="r.id"
        class="mh-card"
      >
        <!-- 状态条 -->
        <div class="mh-card-status" :style="{ background: getStatus(r.status).bg, color: getStatus(r.status).color }">
          <component :is="getStatus(r.status).icon" :size="14" />
          <span>{{ getStatus(r.status).label }}</span>
          <span class="mh-card-time">{{ r.createdAt }}</span>
        </div>

        <!-- 内容 -->
        <div class="mh-card-body">
          <!-- 源 → 目标 -->
          <div class="mh-route">
            <div class="mh-endpoint">
              <Database :size="14" />
              <div class="mh-endpoint-body">
                <span class="mh-endpoint-type">{{ r.sourceType }}</span>
                <span class="mh-endpoint-detail">{{ r.sourceHost }} / {{ r.sourceDB }}</span>
              </div>
            </div>
            <ArrowRight :size="14" class="mh-arrow" />
            <div class="mh-endpoint">
              <Database :size="14" />
              <div class="mh-endpoint-body">
                <span class="mh-endpoint-type">{{ r.targetType }}</span>
                <span class="mh-endpoint-detail">{{ r.targetHost }} / {{ r.targetDB }}</span>
              </div>
            </div>
          </div>

          <!-- 统计 -->
          <div class="mh-stats">
            <span class="mh-stat">
              <CheckCircle2 :size="12" />
              {{ r.tablesSuccess }}/{{ r.tablesTotal }} 表
            </span>
            <span class="mh-stat" v-if="r.tablesFailed > 0" style="color: var(--color-danger)">
              <XCircle :size="12" />
              {{ r.tablesFailed }} 失败
            </span>
            <span class="mh-stat">
              <Hash :size="12" />
              {{ formatNumber(r.totalRows) }} 行
            </span>
            <span class="mh-stat">
              <Clock :size="12" />
              {{ r.duration }}
            </span>
            <span class="mh-stat" v-if="r.backups && r.backups.length > 0" style="color: var(--color-warning)">
              <Archive :size="12" />
              {{ r.backups.length }} 备份
            </span>
          </div>
        </div>

        <!-- 操作 -->
        <div class="mh-card-actions">
          <button
            class="btn btn-secondary btn-sm"
            @click="restoreToTarget(r)"
            title="将目标库信息填入连接表单，然后可去备份管理回滚"
          >
            <Database :size="13" />
            填入目标库
          </button>
          <button class="btn btn-ghost btn-sm mh-delete" @click="deleteRecord(r.id)">
            <Trash2 :size="13" />
          </button>
        </div>
      </div>
    </div>

    <!-- 空状态 -->
    <div class="mh-empty" v-else-if="!loading">
      <History :size="36" class="mh-empty-icon" />
      <p>暂无迁移记录</p>
      <span>每次迁移完成后都会自动记录在这里</span>
    </div>
  </div>
</template>

<style scoped>
.history-panel {
  background: var(--color-surface);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border);
  overflow: hidden;
}

.mh-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 18px;
  border-bottom: 1px solid var(--color-border);
}

.mh-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text);
}

.mh-count {
  font-size: 11px;
  color: var(--color-text-tertiary);
  background: var(--color-bg);
  padding: 2px 8px;
  border-radius: 10px;
}

.mh-list {
  max-height: 500px;
  overflow-y: auto;
  padding: 8px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.mh-card {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  overflow: hidden;
  transition: border-color var(--transition-fast);
}

.mh-card:hover {
  border-color: var(--color-primary);
}

.mh-card-status {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 14px;
  font-size: 12px;
  font-weight: 600;
}

.mh-card-time {
  margin-left: auto;
  font-size: 11px;
  font-weight: 400;
  opacity: 0.8;
}

.mh-card-body {
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.mh-route {
  display: flex;
  align-items: center;
  gap: 8px;
}

.mh-endpoint {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
}

.mh-endpoint-body {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.mh-endpoint-type {
  font-size: 12px;
  font-weight: 600;
  color: var(--color-text);
  text-transform: uppercase;
}

.mh-endpoint-detail {
  font-size: 11px;
  color: var(--color-text-tertiary);
  font-family: var(--font-mono);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mh-arrow {
  color: var(--color-text-tertiary);
  flex-shrink: 0;
}

.mh-stats {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
}

.mh-stat {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  color: var(--color-text-secondary);
  font-family: var(--font-mono);
}

.mh-card-actions {
  display: flex;
  justify-content: flex-end;
  gap: 4px;
  padding: 8px 14px;
  border-top: 1px solid var(--color-border-light);
  background: var(--color-bg);
}

.mh-delete {
  color: var(--color-danger);
}

.mh-delete:hover {
  background: var(--color-danger-light);
}

.mh-empty {
  padding: 36px 20px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
}

.mh-empty-icon {
  color: var(--color-border);
}

.mh-empty p {
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text-secondary);
}

.mh-empty span {
  font-size: 12px;
  color: var(--color-text-tertiary);
}

.animate-spin {
  animation: spin 1s linear infinite;
}
</style>
