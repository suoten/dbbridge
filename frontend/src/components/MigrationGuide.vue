<script lang="ts" setup>
import { ref, onMounted, computed } from 'vue'
import type { ConnectionConfig } from './ConnectionForm.vue'
import {
  Compass,
  Database,
  Loader2,
  AlertTriangle,
  Info,
  FileWarning,
  CheckCircle2,
  RefreshCw,
} from '@lucide/vue'

const props = defineProps<{
  sourceConfig: ConnectionConfig
}>()

const dialects = ref<string[]>([])
const targetDialect = ref('postgres')
const loading = ref(false)
const errorMsg = ref('')
const report = ref<any>(null)

onMounted(async () => {
  try {
    // @ts-ignore - Wails binding
    const list = await window.go.main.App.GetSupportedDatabases()
    if (Array.isArray(list)) {
      dialects.value = list.filter((d: string) => d !== props.sourceConfig.type)
    }
  } catch {
    dialects.value = ['postgres', 'mysql', 'mssql', 'dameng']
  }
})

const errorItems = computed(() => (report.value?.items || []).filter((i: any) => i.severity === 'error'))
const warnItems = computed(() => (report.value?.items || []).filter((i: any) => i.severity === 'warning'))
const infoItems = computed(() => (report.value?.items || []).filter((i: any) => i.severity === 'info'))

async function run() {
  loading.value = true
  errorMsg.value = ''
  report.value = null
  try {
    // @ts-ignore - Wails binding
    report.value = await window.go.main.App.GenerateMigrationGuide(
      props.sourceConfig,
      targetDialect.value,
      []
    )
  } catch (e: any) {
    errorMsg.value = e?.message || String(e)
  } finally {
    loading.value = false
  }
}

function itemIcon(sev: string) {
  if (sev === 'error') return AlertTriangle
  if (sev === 'warning') return FileWarning
  return Info
}

function categoryLabel(c: string) {
  const map: Record<string, string> = {
    trigger: '触发器', routine: '存储过程', check: 'CHECK 约束',
    charset: '字符集', increment: '自增列', dialect: '方言语义',
    schema: '表结构',
  }
  return map[c] || c
}
</script>

<template>
  <div class="guide">
    <div class="guide-intro card">
      <Compass :size="16" />
      <span>
        迁移前评估"业务代码跟着改"的工作量：列类型映射差异、触发器/存储过程能否自动转换、
        字符集与方言语义提示。<strong>只读源库元数据，不动任何数据。</strong>
      </span>
    </div>

    <div class="guide-form card">
      <div class="form-row">
        <div class="conn-summary">
          <Database :size="14" />
          <span>源库：{{ sourceConfig.type }} / {{ sourceConfig.database || '(未配置，请先在"配置连接"页填写)' }}</span>
        </div>
        <div class="target-field">
          <label>目标方言</label>
          <select v-model="targetDialect" class="input">
            <option v-for="d in dialects" :key="d" :value="d">{{ d }}</option>
          </select>
        </div>
        <button class="btn btn-primary" :disabled="loading" @click="run">
          <Loader2 v-if="loading" :size="15" class="animate-spin" />
          <Compass v-else :size="15" />
          {{ loading ? '评估中...' : '生成适配指南' }}
        </button>
      </div>
    </div>

    <div v-if="errorMsg" class="error-card card">
      <AlertTriangle :size="16" />
      <span>{{ errorMsg }}</span>
    </div>

    <template v-if="report && report.success">
      <!-- 摘要 -->
      <div class="summary-card card">
        <div class="summary-line">{{ report.summary }}</div>
        <div class="summary-stats">
          <span class="stat" :class="{ zero: report.triggersFailed === 0 }">
            触发器需人工改写 {{ report.triggersFailed }}
          </span>
          <span class="stat" :class="{ zero: report.routinesFailed === 0 }">
            存储过程需人工改写 {{ report.routinesFailed }}
          </span>
          <span class="stat" :class="{ zero: errorItems.length === 0 }">
            必办事项 {{ errorItems.length }}
          </span>
        </div>
      </div>

      <!-- 类型映射差异 -->
      <div v-if="report.changes?.length" class="result-card card">
        <div class="result-header">
          <RefreshCw :size="15" />
          <span class="result-title">列类型映射差异（{{ report.changes.length }}）</span>
        </div>
        <div class="changes-table">
          <div class="chg-row chg-head">
            <span>表.列</span><span>源类型</span><span>目标类型</span><span>说明</span>
          </div>
          <div v-for="(c, i) in report.changes" :key="i" class="chg-row">
            <span class="chg-col">{{ c.table }}.{{ c.column }}</span>
            <code class="chg-type">{{ c.sourceType }}</code>
            <code class="chg-type tgt">{{ c.targetType }}</code>
            <span class="chg-note">{{ c.note || '—' }}</span>
          </div>
        </div>
      </div>

      <!-- 待办清单 -->
      <div v-if="report.items?.length" class="result-card card">
        <div class="result-header">
          <FileWarning :size="15" />
          <span class="result-title">人工核对清单（{{ report.items.length }}）</span>
        </div>
        <div class="items-list">
          <div v-for="(it, i) in report.items" :key="i" class="finding-item" :class="'sev-' + it.severity">
            <div class="finding-head">
              <span class="finding-sev">
                <component :is="itemIcon(it.severity)" :size="13" />
                {{ it.severity === 'error' ? '必须处理' : it.severity === 'warning' ? '需确认' : '提示' }}
              </span>
              <span class="finding-cat">{{ categoryLabel(it.category) }}</span>
              <span v-if="it.table || it.object" class="finding-loc">
                {{ [it.table, it.object].filter(Boolean).join(' · ') }}
              </span>
            </div>
            <div class="finding-msg">{{ it.message }}</div>
            <div v-if="it.suggestion" class="finding-suggestion">建议：{{ it.suggestion }}</div>
          </div>
        </div>
      </div>

      <div v-else class="lint-clean card">
        <CheckCircle2 :size="18" />
        <span>没有需要人工处理的事项</span>
      </div>
    </template>
  </div>
</template>

<style scoped>
.guide {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 960px;
}

.guide-intro {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 18px;
  font-size: 13px;
  color: var(--color-text-secondary);
  background: var(--color-info-light);
  border-color: var(--color-info);
}

.guide-intro strong {
  color: var(--color-success);
}

.guide-form {
  padding: 14px 20px;
}

.form-row {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}

.conn-summary {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--color-text-secondary);
  flex: 1;
  min-width: 200px;
}

.target-field {
  display: flex;
  align-items: center;
  gap: 8px;
}

.target-field label {
  font-size: 12px;
  font-weight: 600;
  color: var(--color-text-secondary);
}

.target-field select {
  width: 160px;
  padding: 6px 10px;
  font-size: 13px;
}

.error-card {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 18px;
  font-size: 13px;
  color: var(--color-danger);
  background: rgba(239, 68, 68, 0.08);
  border-color: var(--color-danger);
  word-break: break-all;
}

.summary-card {
  padding: 14px 20px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.summary-line {
  font-size: 14px;
  font-weight: 700;
  color: var(--color-text);
}

.summary-stats {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
}

.stat {
  font-size: 12px;
  padding: 3px 10px;
  border-radius: 12px;
  background: rgba(239, 68, 68, 0.1);
  color: var(--color-danger);
  font-weight: 600;
}

.stat.zero {
  background: rgba(16, 185, 129, 0.1);
  color: var(--color-success);
}

.result-card {
  padding: 16px 20px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.result-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.result-title {
  font-size: 14px;
  font-weight: 700;
  color: var(--color-text);
}

/* 类型差异表 */
.changes-table {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.chg-row {
  display: grid;
  grid-template-columns: minmax(140px, 1.2fr) minmax(100px, 0.8fr) minmax(100px, 0.8fr) 2fr;
  gap: 10px;
  padding: 8px 12px;
  font-size: 12.5px;
  border-bottom: 1px solid var(--color-border-light);
  align-items: baseline;
}

.chg-row:last-child {
  border-bottom: none;
}

.chg-head {
  background: var(--color-bg);
  font-weight: 700;
  color: var(--color-text-secondary);
  font-size: 11.5px;
}

.chg-col {
  font-weight: 600;
  color: var(--color-text);
  word-break: break-all;
}

.chg-type {
  font-family: Consolas, monospace;
  color: var(--color-text-secondary);
  word-break: break-all;
}

.chg-type.tgt {
  color: var(--color-primary);
}

.chg-note {
  color: var(--color-text-secondary);
  line-height: 1.5;
}

/* 清单 */
.items-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.finding-item {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 10px 14px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.finding-item.sev-error {
  border-left: 3px solid var(--color-danger);
}

.finding-item.sev-warning {
  border-left: 3px solid #f59e0b;
}

.finding-item.sev-info {
  border-left: 3px solid var(--color-primary);
}

.finding-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.finding-sev {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  font-weight: 700;
  padding: 2px 8px;
  border-radius: 12px;
}

.sev-error .finding-sev {
  color: var(--color-danger);
  background: rgba(239, 68, 68, 0.1);
}

.sev-warning .finding-sev {
  color: #b45309;
  background: rgba(245, 158, 11, 0.12);
}

.sev-info .finding-sev {
  color: var(--color-primary);
  background: var(--color-primary-light);
}

.finding-cat {
  font-size: 11px;
  color: var(--color-text-tertiary);
}

.finding-loc {
  font-size: 11px;
  color: var(--color-text-secondary);
}

.finding-msg {
  font-size: 13px;
  color: var(--color-text);
  word-break: break-all;
}

.finding-suggestion {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.lint-clean {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  background: rgba(16, 185, 129, 0.1);
  color: var(--color-success);
  font-size: 13px;
  font-weight: 600;
}

.animate-spin {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
