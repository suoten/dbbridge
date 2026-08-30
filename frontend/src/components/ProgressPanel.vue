<script lang="ts" setup>
import { ref, onMounted, onUnmounted, nextTick, computed } from 'vue'
import {
  Activity,
  CheckCircle2,
  Clock,
  Database,
  Hash,
  Loader2,
  Trash2,
  Terminal,
  Info,
  AlertTriangle,
  XCircle,
  Ban,
} from '@lucide/vue'

interface ProgressInfo {
  phase: string
  currentTable: string
  processedRows: number
  totalRows: number
  tablesCompleted: number
  tablesTotal: number
  percent: number
  elapsed: string
  remaining?: string
}

interface LogEntry {
  time: string
  level: string
  table?: string
  message: string
}

const progress = ref<ProgressInfo | null>(null)
const logs = ref<LogEntry[]>([])
const logContainer = ref<HTMLElement | null>(null)
const cancelling = ref(false)

let unlistenProgress: (() => void) | null = null
let unlistenLog: (() => void) | null = null

const isDone = computed(() => progress.value?.phase === 'done')
const isRunning = computed(() => progress.value && !isDone.value)

// 环形进度 SVG 计算
const circumference = 2 * Math.PI * 42
const strokeDashoffset = computed(() => {
  if (!progress.value) return circumference
  return circumference - (progress.value.percent / 100) * circumference
})

onMounted(async () => {
  // @ts-ignore - Wails runtime
  const runtime = window.runtime
  if (runtime) {
    unlistenProgress = runtime.EventsOn('migration:progress', (info: ProgressInfo) => {
      progress.value = info
    })
    unlistenLog = runtime.EventsOn('migration:log', (entry: LogEntry) => {
      logs.value.push(entry)
      nextTick(() => {
        if (logContainer.value) {
          logContainer.value.scrollTop = logContainer.value.scrollHeight
        }
      })
    })
  }
})

onUnmounted(() => {
  if (unlistenProgress) unlistenProgress()
  if (unlistenLog) unlistenLog()
})

function clearLogs() {
  logs.value = []
  progress.value = null
  cancelling.value = false
}

// 取消当前迁移任务（后端通过 context 中断所有进行中的查询）
async function cancelMigration() {
  if (cancelling.value) return
  cancelling.value = true
  try {
    // @ts-ignore - Wails bindings
    await window.go.main.App.CancelMigration()
  } finally {
    // 等待进度事件将 phase 置为 done 后自动复位
    setTimeout(() => { cancelling.value = false }, 3000)
  }
}

const phaseConfig: Record<string, { label: string; color: string; icon: any }> = {
  parsing: { label: '解析中', color: '#f59e0b', icon: Activity },
  structure: { label: '迁移结构', color: '#3b82f6', icon: Database },
  data: { label: '迁移数据', color: '#8b5cf6', icon: Database },
  verifying: { label: '校验中', color: '#06b6d4', icon: CheckCircle2 },
  done: { label: '完成', color: '#10b981', icon: CheckCircle2 },
}

function getPhaseInfo(phase: string) {
  return phaseConfig[phase] || { label: phase, color: '#64748b', icon: Info }
}

function formatNumber(n: number): string {
  return n.toLocaleString()
}

function logIcon(level: string) {
  switch (level.toLowerCase()) {
    case 'error': return XCircle
    case 'warn': return AlertTriangle
    default: return Info
  }
}

function logColor(level: string): string {
  switch (level.toLowerCase()) {
    case 'error': return 'var(--color-danger)'
    case 'warn': return 'var(--color-warning)'
    default: return 'var(--color-info)'
  }
}
</script>

<template>
  <div class="progress-panel">
    <!-- ====== Progress Section ====== -->
    <div class="progress-section card" v-if="progress">
      <div class="progress-layout">
        <!-- Circular Progress -->
        <div class="circular-progress">
          <svg width="100" height="100" viewBox="0 0 100 100">
            <!-- Track -->
            <circle
              cx="50" cy="50" r="42"
              fill="none"
              stroke="var(--color-border)"
              stroke-width="6"
            />
            <!-- Progress arc -->
            <circle
              cx="50" cy="50" r="42"
              fill="none"
              :stroke="isDone ? 'var(--color-success)' : 'var(--color-primary)'"
              stroke-width="6"
              stroke-linecap="round"
              :stroke-dasharray="circumference"
              :stroke-dashoffset="strokeDashoffset"
              transform="rotate(-90 50 50)"
              class="progress-arc"
            />
          </svg>
          <div class="circular-center">
            <span class="circular-percent">{{ progress.percent.toFixed(0) }}<small>%</small></span>
            <span class="circular-phase">{{ getPhaseInfo(progress.phase).label }}</span>
          </div>
        </div>

        <!-- Stats Grid -->
        <div class="progress-stats-grid">
          <div class="stat-item">
            <div class="stat-icon current"><Activity :size="16" /></div>
            <div class="stat-body">
              <span class="stat-value">{{ progress.currentTable || '—' }}</span>
              <span class="stat-label">当前表</span>
            </div>
          </div>
          <div class="stat-item">
            <div class="stat-icon table"><Database :size="16" /></div>
            <div class="stat-body">
              <span class="stat-value">{{ progress.tablesCompleted }} / {{ progress.tablesTotal }}</span>
              <span class="stat-label">表进度</span>
            </div>
          </div>
          <div class="stat-item">
            <div class="stat-icon row"><Hash :size="16" /></div>
            <div class="stat-body">
              <span class="stat-value">{{ formatNumber(progress.processedRows) }}</span>
              <span class="stat-label">已处理行</span>
            </div>
          </div>
          <div class="stat-item">
            <div class="stat-icon time"><Clock :size="16" /></div>
            <div class="stat-body">
              <span class="stat-value">{{ progress.elapsed }}</span>
              <span class="stat-label">耗时</span>
            </div>
          </div>
        </div>
      </div>

      <!-- Remaining time bar / Cancel -->
      <div class="remaining-bar" v-if="isRunning && progress.remaining">
        <Loader2 :size="13" class="animate-spin" />
        <span>预计剩余 {{ progress.remaining }}</span>
        <button
          class="btn btn-danger btn-sm cancel-btn"
          :disabled="cancelling"
          @click="cancelMigration"
          title="中断当前迁移任务"
        >
          <Ban :size="13" />
          {{ cancelling ? '正在取消...' : '取消迁移' }}
        </button>
      </div>
      <div class="done-bar" v-if="isDone">
        <CheckCircle2 :size="16" />
        <span>迁移完成</span>
      </div>
    </div>

    <!-- Waiting State -->
    <div class="waiting-state card" v-if="!progress">
      <Terminal :size="40" class="waiting-icon" />
      <p>等待开始迁移...</p>
      <span class="waiting-hint">切换到"选择表"页面配置并启动迁移</span>
    </div>

    <!-- ====== Log Section ====== -->
    <div class="log-section card">
      <div class="log-header">
        <div class="log-title">
          <Terminal :size="16" />
          <span>迁移日志</span>
          <span class="log-count" v-if="logs.length > 0">{{ logs.length }} 条</span>
        </div>
        <button class="btn btn-ghost btn-sm" @click="clearLogs" v-if="logs.length > 0">
          <Trash2 :size="13" />
          清空
        </button>
      </div>
      <div class="log-container" ref="logContainer">
        <div
          v-for="(entry, i) in logs"
          :key="i"
          class="log-entry"
          :class="entry.level.toLowerCase()"
        >
          <component :is="logIcon(entry.level)" :size="13" class="log-icon" :style="{ color: logColor(entry.level) }" />
          <span class="log-time">{{ entry.time }}</span>
          <span v-if="entry.table" class="log-table">[{{ entry.table }}]</span>
          <span class="log-message">{{ entry.message }}</span>
        </div>
        <div v-if="logs.length === 0" class="log-empty">
          <Terminal :size="28" class="empty-icon" />
          <p>暂无日志</p>
          <span>开始迁移后将显示实时日志</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.progress-panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
  height: 100%;
  min-height: 0;
}

/* ====== Progress Section ====== */
.progress-section {
  padding: 20px;
  flex-shrink: 0;
}

.progress-layout {
  display: flex;
  gap: 24px;
  align-items: center;
}

.circular-progress {
  position: relative;
  width: 100px;
  height: 100px;
  flex-shrink: 0;
}

.progress-arc {
  transition: stroke-dashoffset 0.4s ease, stroke 0.3s ease;
}

.circular-center {
  position: absolute;
  top: 0; left: 0; right: 0; bottom: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}

.circular-percent {
  font-size: 22px;
  font-weight: 700;
  color: var(--color-text);
  font-family: var(--font-mono);
  line-height: 1;
}

.circular-percent small {
  font-size: 12px;
  color: var(--color-text-secondary);
  font-weight: 500;
}

.circular-phase {
  font-size: 10px;
  color: var(--color-text-secondary);
  margin-top: 4px;
}

.progress-stats-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
  flex: 1;
}

.stat-item {
  display: flex;
  align-items: center;
  gap: 10px;
}

.stat-icon {
  width: 32px;
  height: 32px;
  border-radius: var(--radius-sm);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.stat-icon.current { background: var(--color-primary-light); color: var(--color-primary); }
.stat-icon.table { background: var(--color-info-light); color: var(--color-info); }
.stat-icon.row { background: var(--color-warning-light); color: var(--color-warning); }
.stat-icon.time { background: var(--color-success-light); color: var(--color-success); }

.stat-body {
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.stat-value {
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text);
  font-family: var(--font-mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 140px;
}

.stat-label {
  font-size: 11px;
  color: var(--color-text-secondary);
}

/* Remaining / Done bar */
.remaining-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 14px;
  padding: 8px 14px;
  background: var(--color-primary-light);
  color: var(--color-primary);
  border-radius: var(--radius-sm);
  font-size: 12px;
  font-weight: 500;
}

.cancel-btn {
  margin-left: auto;
  flex-shrink: 0;
}

.done-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 14px;
  padding: 8px 14px;
  background: var(--color-success-light);
  color: var(--color-success);
  border-radius: var(--radius-sm);
  font-size: 13px;
  font-weight: 600;
}

/* ====== Waiting State ====== */
.waiting-state {
  padding: 40px 20px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.waiting-icon {
  color: var(--color-border);
}

.waiting-state p {
  font-size: 15px;
  font-weight: 600;
  color: var(--color-text-secondary);
}

.waiting-hint {
  font-size: 12px;
  color: var(--color-text-tertiary);
}

/* ====== Log Section ====== */
.log-section {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}

.log-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 16px;
  border-bottom: 1px solid var(--color-border);
  flex-shrink: 0;
}

.log-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 600;
  color: var(--color-text);
}

.log-count {
  font-size: 11px;
  color: var(--color-text-tertiary);
  background: var(--color-bg);
  padding: 1px 8px;
  border-radius: 10px;
}

.log-container {
  flex: 1;
  overflow-y: auto;
  padding: 8px 16px;
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.9;
}

.log-entry {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  white-space: nowrap;
  padding: 1px 0;
}

.log-icon {
  flex-shrink: 0;
  margin-top: 3px;
}

.log-time {
  color: var(--color-text-tertiary);
  flex-shrink: 0;
}

.log-table {
  color: var(--color-text-secondary);
  flex-shrink: 0;
}

.log-message {
  color: var(--color-text);
  overflow: hidden;
  text-overflow: ellipsis;
}

.log-entry.error .log-message {
  color: var(--color-danger);
}

.log-entry.warn .log-message {
  color: var(--color-warning);
}

.log-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 32px;
  color: var(--color-text-tertiary);
}

.empty-icon {
  color: var(--color-border);
}

.log-empty p {
  font-size: 13px;
  font-weight: 500;
}

.log-empty span {
  font-size: 11px;
}

/* Animations */
.animate-spin {
  animation: spin 1s linear infinite;
}

/* ====== 响应式 ====== */

/* ≤560px：进度区纵向堆叠，日志允许横向滚动 */
@media (max-width: 560px) {
  .progress-layout {
    flex-direction: column;
    align-items: stretch;
  }

  .circular-progress {
    margin: 0 auto;
  }

  .log-container {
    overflow-x: auto;
  }
}
</style>
