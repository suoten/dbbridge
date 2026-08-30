<script lang="ts" setup>
import { ref, onMounted, onUnmounted, nextTick } from 'vue'

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

// 进度的事件监听
let unlistenProgress: (() => void) | null = null
let unlistenLog: (() => void) | null = null

onMounted(async () => {
  // @ts-ignore - Wails runtime
  const runtime = window.runtime
  if (runtime) {
    unlistenProgress = runtime.EventsOn('migration:progress', (info: ProgressInfo) => {
      progress.value = info
    })
    unlistenLog = runtime.EventsOn('migration:log', (entry: LogEntry) => {
      logs.value.push(entry)
      // 自动滚动到底部
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
}

const phaseLabels: Record<string, string> = {
  parsing: '解析中',
  structure: '迁移结构',
  data: '迁移数据',
  verifying: '校验中',
  done: '完成',
}

function getPhaseLabel(phase: string): string {
  return phaseLabels[phase] || phase
}

function formatNumber(n: number): string {
  return n.toLocaleString()
}
</script>

<template>
  <div class="progress-panel">
    <!-- 进度条 -->
    <div class="progress-section" v-if="progress">
      <div class="progress-header">
        <span class="phase-badge">{{ getPhaseLabel(progress.phase) }}</span>
        <span v-if="progress.currentTable" class="current-table">
          📋 {{ progress.currentTable }}
        </span>
        <span class="percent">{{ progress.percent.toFixed(1) }}%</span>
      </div>
      <div class="progress-bar-wrapper">
        <div class="progress-bar" :style="{ width: progress.percent + '%' }"></div>
      </div>
      <div class="progress-stats">
        <span>表: {{ progress.tablesCompleted }}/{{ progress.tablesTotal }}</span>
        <span>行: {{ formatNumber(progress.processedRows) }}/{{ formatNumber(progress.totalRows) }}</span>
        <span>耗时: {{ progress.elapsed }}</span>
        <span v-if="progress.remaining">预计剩余: {{ progress.remaining }}</span>
      </div>
    </div>

    <!-- 日志区域 -->
    <div class="log-section">
      <div class="log-header">
        <span>迁移日志</span>
        <button class="btn btn-secondary btn-sm" @click="clearLogs">清空</button>
      </div>
      <div class="log-container" ref="logContainer">
        <div
          v-for="(entry, i) in logs"
          :key="i"
          class="log-entry"
          :class="entry.level.toLowerCase()"
        >
          <span class="log-time">{{ entry.time }}</span>
          <span class="log-level" :class="entry.level.toLowerCase()">[{{ entry.level }}]</span>
          <span v-if="entry.table" class="log-table">[{{ entry.table }}]</span>
          <span class="log-message">{{ entry.message }}</span>
        </div>
        <div v-if="logs.length === 0" class="log-empty">
          暂无日志，开始迁移后将显示实时日志...
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.progress-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
  height: 100%;
  overflow: hidden;
}

.progress-section {
  padding: 12px 16px;
  background: var(--color-surface);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border);
}

.progress-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.phase-badge {
  padding: 2px 8px;
  border-radius: 4px;
  background: var(--color-primary-light);
  color: var(--color-primary);
  font-size: 11px;
  font-weight: 600;
}

.current-table {
  font-size: 13px;
  color: var(--color-text);
  font-weight: 500;
}

.percent {
  margin-left: auto;
  font-size: 16px;
  font-weight: 700;
  color: var(--color-primary);
  font-family: var(--font-mono);
}

.progress-bar-wrapper {
  height: 8px;
  background: var(--color-border);
  border-radius: 4px;
  overflow: hidden;
  margin-bottom: 8px;
}

.progress-bar {
  height: 100%;
  background: linear-gradient(90deg, var(--color-primary), #6c5ce7);
  border-radius: 4px;
  transition: width 0.3s ease;
}

.progress-stats {
  display: flex;
  gap: 16px;
  font-size: 12px;
  color: var(--color-text-secondary);
  font-family: var(--font-mono);
}

.log-section {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: var(--color-surface);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border);
  overflow: hidden;
}

.log-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 16px;
  border-bottom: 1px solid var(--color-border);
  font-size: 13px;
  font-weight: 600;
}

.log-container {
  flex: 1;
  overflow-y: auto;
  padding: 8px 16px;
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.8;
}

.log-entry {
  display: flex;
  gap: 4px;
  white-space: nowrap;
}

.log-time {
  color: #888;
}

.log-level {
  font-weight: 600;
}

.log-level.info {
  color: var(--color-info);
}

.log-level.warn {
  color: var(--color-warning);
}

.log-level.error {
  color: var(--color-danger);
}

.log-table {
  color: #666;
}

.log-message {
  color: var(--color-text);
}

.log-empty {
  color: var(--color-text-secondary);
  text-align: center;
  padding: 24px;
  font-style: italic;
}

.log-entry.error .log-message {
  color: var(--color-danger);
}
</style>
