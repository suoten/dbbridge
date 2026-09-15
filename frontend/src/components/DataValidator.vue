<script lang="ts" setup>
import { ref, computed } from 'vue'
import type { ConnectionConfig } from './ConnectionForm.vue'
import {
  ClipboardCheck,
  Database,
  Table2,
  Loader2,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  ShieldCheck,
  CheckSquare,
  Square,
  Search,
} from '@lucide/vue'

const props = defineProps<{
  sourceConfig: ConnectionConfig
  targetConfig: ConnectionConfig
}>()

// 工具模式：validate = 数据校验，compat = 结构兼容性检查
const mode = ref<'validate' | 'compat'>('validate')

const tables = ref<{ name: string; selected: boolean }[]>([])
const loadingTables = ref(false)
const tablesError = ref('')
const tableSearch = ref('')

const sampleSize = ref(100)
const validating = ref(false)
const validateError = ref('')
const report = ref<any>(null)
const compatReport = ref<any>(null)

const filteredTables = computed(() => {
  if (!tableSearch.value) return tables.value
  const q = tableSearch.value.toLowerCase()
  return tables.value.filter(t => t.name.toLowerCase().includes(q))
})

const selectedTables = computed(() => tables.value.filter(t => t.selected).map(t => t.name))

async function loadTables() {
  loadingTables.value = true
  tablesError.value = ''
  try {
    // @ts-ignore - Wails binding
    const result = await window.go.main.App.GetTables(props.sourceConfig)
    if (result.success) {
      tables.value = (result.tables || []).map((t: any) => ({ name: t.name, selected: true }))
    } else {
      tablesError.value = result.error || '加载表列表失败，请检查源库连接'
      tables.value = []
    }
  } catch (e: any) {
    tablesError.value = e?.message || String(e)
    tables.value = []
  } finally {
    loadingTables.value = false
  }
}

function selectAll() {
  filteredTables.value.forEach(t => (t.selected = true))
}

function deselectAll() {
  filteredTables.value.forEach(t => (t.selected = false))
}

async function runValidate() {
  validating.value = true
  validateError.value = ''
  report.value = null
  compatReport.value = null
  if (mode.value === 'compat') {
    try {
      // @ts-ignore - Wails binding
      compatReport.value = await window.go.main.App.CheckCompatibility({
        source: props.sourceConfig,
        target: props.targetConfig,
        tables: selectedTables.value,
      })
    } catch (e: any) {
      validateError.value = e?.message || String(e)
    } finally {
      validating.value = false
    }
    return
  }
  try {
    // @ts-ignore - Wails binding
    report.value = await window.go.main.App.ValidateData({
      source: props.sourceConfig,
      target: props.targetConfig,
      tables: selectedTables.value,
      sampleSize: sampleSize.value,
    })
  } catch (e: any) {
    validateError.value = e?.message || String(e)
  } finally {
    validating.value = false
  }
}

function switchMode(m: 'validate' | 'compat') {
  mode.value = m
  report.value = null
  compatReport.value = null
  validateError.value = ''
}

function statusLabel(status: string) {
  switch (status) {
    case 'match': return '一致'
    case 'row_count_mismatch': return '行数不一致'
    case 'sample_mismatch': return '抽样比对不一致'
    case 'missing_target': return '目标表缺失'
    case 'error': return '校验出错'
    default: return status
  }
}

const allOk = computed(() => report.value?.success === true)
const compatAllOk = computed(() =>
  (compatReport.value?.tables || []).length > 0 &&
  (compatReport.value?.tables || []).every((t: any) => t.status === 'ok')
)
</script>

<template>
  <div class="validator">
    <div class="validator-hint card">
      <ShieldCheck :size="16" />
      <span>
        切换前数据校验：对比源库与目标库的行数，并按主键抽样逐字段比对（数值/时间/字符串规范化）。
        <strong>全程只读，不会修改任何数据。</strong>
      </span>
    </div>

    <!-- 模式切换 -->
    <div class="mode-switch card">
      <button class="mode-btn" :class="{ active: mode === 'validate' }" @click="switchMode('validate')">
        <span>数据校验</span>
        <span class="mode-desc">行数对比 + 主键抽样逐字段比对</span>
      </button>
      <button class="mode-btn" :class="{ active: mode === 'compat' }" @click="switchMode('compat')">
        <span>结构兼容性检查</span>
        <span class="mode-desc">列/索引/外键/自增/默认值差异扫描</span>
      </button>
    </div>

    <!-- 连接概览 -->
    <div class="conn-overview card">
      <div class="conn-item">
        <span class="conn-label">源库</span>
        <span class="conn-value">
          <Database :size="13" />
          {{ sourceConfig.type }} / {{ sourceConfig.database || '(未填写)' }}
        </span>
      </div>
      <div class="conn-arrow">→</div>
      <div class="conn-item">
        <span class="conn-label">目标库</span>
        <span class="conn-value">
          <Database :size="13" />
          {{ targetConfig.type }} / {{ targetConfig.database || '(未填写)' }}
        </span>
      </div>
      <div class="conn-sample" v-if="mode === 'validate'">
        <label>每表抽样行数</label>
        <input class="input" type="number" v-model.number="sampleSize" min="1" max="10000" />
      </div>
    </div>

    <!-- 表选择 -->
    <div class="tables-card card">
      <div class="tables-toolbar">
        <div class="search-box">
          <Search :size="15" class="search-icon" />
          <input v-model="tableSearch" class="search-input" placeholder="搜索表名..." />
        </div>
        <div class="toolbar-actions">
          <button class="btn btn-secondary btn-sm" @click="loadTables" :disabled="loadingTables">
            <Loader2 v-if="loadingTables" :size="14" class="animate-spin" />
            <Database v-else :size="14" />
            {{ loadingTables ? '加载中' : '加载源库表' }}
          </button>
          <button class="btn btn-ghost btn-sm" @click="selectAll" :disabled="tables.length === 0">
            <CheckSquare :size="14" />
            全选
          </button>
          <button class="btn btn-ghost btn-sm" @click="deselectAll" :disabled="tables.length === 0">
            <Square :size="14" />
            清空
          </button>
          <span class="tables-count">
            <strong>{{ selectedTables.length }}</strong> / {{ tables.length }} 张表
          </span>
        </div>
      </div>
      <div class="table-list" v-if="filteredTables.length > 0">
        <label
          v-for="t in filteredTables"
          :key="t.name"
          class="table-row"
          :class="{ selected: t.selected }"
        >
          <input type="checkbox" v-model="t.selected" class="table-checkbox" />
          <span class="table-icon"><Table2 :size="15" /></span>
          <span class="table-name">{{ t.name }}</span>
        </label>
      </div>
      <div v-else class="empty-state">
        <Table2 :size="36" class="empty-icon" />
        <p v-if="tablesError" class="empty-error">{{ tablesError }}</p>
        <p v-else>点击"加载源库表"选择要校验的表（不选任何表时需先加载）</p>
      </div>
    </div>

    <!-- 执行 -->
    <div class="run-row">
      <span v-if="validateError" class="run-error">
        <AlertTriangle :size="14" />
        {{ validateError }}
      </span>
      <button
        class="btn btn-primary"
        :disabled="validating || selectedTables.length === 0"
        @click="runValidate"
      >
        <Loader2 v-if="validating" :size="15" class="animate-spin" />
        <ClipboardCheck v-else :size="15" />
        {{ validating ? (mode === 'compat' ? '检查中...' : '校验中（大表行数统计可能较慢）...') : (mode === 'compat' ? '开始结构检查' : '开始校验') }}
      </button>
    </div>

    <!-- 兼容性检查报告 -->
    <div v-if="compatReport && compatReport.success" class="report-card card">
      <div class="report-head" :class="compatAllOk ? 'head-ok' : 'head-bad'">
        <CheckCircle2 v-if="compatAllOk" :size="20" />
        <XCircle v-else :size="20" />
        <span>{{ compatAllOk ? '结构兼容：所有表无缺失或错误' : '发现结构差异：详见下表，逐条核对' }}</span>
        <span class="report-count">{{ compatReport.summary }}</span>
      </div>
      <div class="report-table">
        <div v-for="t in compatReport.tables" :key="t.table" class="report-row" :class="{ 'row-bad': t.status !== 'ok' }">
          <div class="report-row-head">
            <Table2 :size="14" />
            <span class="report-table-name">{{ t.table }}</span>
            <span class="report-status" :class="t.status === 'ok' ? 'st-match' : 'st-error'">
              {{ t.status === 'ok' ? '一致' : t.status === 'warning' ? '有差异' : '有缺失/错误' }}
            </span>
          </div>
          <div v-for="(it, i) in t.items" :key="i" class="report-issue" :class="{ 'issue-error': it.severity === 'error' }">
            <strong>{{ it.message }}</strong>
            <div v-if="it.suggestion" class="issue-suggestion">建议：{{ it.suggestion }}</div>
          </div>
        </div>
      </div>
    </div>

    <!-- 校验报告 -->
    <div v-if="report" class="report-card card">
      <div class="report-head" :class="allOk ? 'head-ok' : 'head-bad'">
        <CheckCircle2 v-if="allOk" :size="20" />
        <XCircle v-else :size="20" />
        <span>{{ allOk ? '校验通过：所有表行数一致且抽样比对无差异' : '校验未通过：存在差异或错误，详见下表' }}</span>
        <span class="report-count">
          一致 {{ report.matchCount }} · 不一致 {{ report.mismatchCount }} · 出错 {{ report.errorCount }}
        </span>
      </div>

      <div class="report-table">
        <div v-for="t in report.tables" :key="t.table" class="report-row" :class="{ 'row-bad': t.status !== 'match' }">
          <div class="report-row-head">
            <Table2 :size="14" />
            <span class="report-table-name">{{ t.table }}</span>
            <span class="report-status" :class="'st-' + t.status">{{ statusLabel(t.status) }}</span>
          </div>
          <div class="report-row-detail">
            <span>源 {{ t.sourceRows }} 行</span>
            <span>目标 {{ t.targetRows }} 行</span>
            <span v-if="t.sampled > 0">抽样 {{ t.sampled }} 行</span>
            <span v-if="t.compared > 0">比对 {{ t.compared }} 行</span>
          </div>
          <div v-if="t.error" class="report-issue issue-error">{{ t.error }}</div>
          <div v-if="t.missingRows?.length" class="report-issue">
            目标库缺失主键：{{ t.missingRows.slice(0, 10).join('、') }}{{ t.missingRows.length > 10 ? ` 等 ${t.missingRows.length} 条` : '' }}
          </div>
          <div v-if="t.fieldMismatch?.length" class="report-issue">
            <div v-for="(m, i) in t.fieldMismatch.slice(0, 10)" :key="i">{{ m }}</div>
            <div v-if="t.fieldMismatch.length > 10">...等 {{ t.fieldMismatch.length }} 处差异</div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.validator {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 960px;
}

.validator-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 18px;
  font-size: 13px;
  color: var(--color-text-secondary);
  background: var(--color-info-light);
  border-color: var(--color-info);
}

.validator-hint strong {
  color: var(--color-success);
}

/* 连接概览 */
.conn-overview {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 14px 20px;
  flex-wrap: wrap;
}

.conn-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.conn-label {
  font-size: 11px;
  font-weight: 600;
  color: var(--color-text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.conn-value {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 600;
  color: var(--color-text);
}

.conn-arrow {
  color: var(--color-text-tertiary);
  font-size: 16px;
}

.conn-sample {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 8px;
}

.conn-sample label {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.conn-sample input {
  width: 90px;
  padding: 5px 8px;
  font-size: 12px;
}

/* 表选择 */
.tables-card {
  overflow: hidden;
}

.tables-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--color-border);
}

.search-box {
  position: relative;
  flex: 1;
  max-width: 280px;
}

.search-icon {
  position: absolute;
  left: 10px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--color-text-tertiary);
}

.search-input {
  width: 100%;
  padding: 7px 12px 7px 34px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  font-size: 13px;
  background: var(--color-bg);
  outline: none;
  font-family: var(--font-sans);
}

.toolbar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
}

.tables-count {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.tables-count strong {
  color: var(--color-primary);
  font-weight: 700;
}

.table-list {
  max-height: 280px;
  overflow-y: auto;
}

.table-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 16px;
  cursor: pointer;
  border-bottom: 1px solid var(--color-border-light);
  transition: background var(--transition-fast);
}

.table-row:hover {
  background: var(--color-surface-hover);
}

.table-row.selected {
  background: var(--color-primary-light);
}

.table-checkbox {
  width: 16px;
  height: 16px;
  cursor: pointer;
  accent-color: var(--color-primary);
}

.table-icon {
  color: var(--color-text-tertiary);
  display: flex;
}

.table-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--color-text);
}

.empty-state {
  padding: 36px 20px;
  text-align: center;
  color: var(--color-text-tertiary);
  font-size: 13px;
}

.empty-icon {
  color: var(--color-border);
  margin-bottom: 10px;
}

.empty-error {
  color: var(--color-danger);
  word-break: break-all;
}

/* 执行行 */
.run-row {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
}

.run-error {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--color-danger);
  margin-right: auto;
  word-break: break-all;
}

/* 报告 */
.report-card {
  padding: 16px 20px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.report-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 16px;
  border-radius: var(--radius-md);
  font-size: 13px;
  font-weight: 700;
}

.head-ok {
  background: rgba(16, 185, 129, 0.1);
  color: var(--color-success);
}

.head-bad {
  background: rgba(239, 68, 68, 0.08);
  color: var(--color-danger);
}

.report-count {
  margin-left: auto;
  font-size: 12px;
  font-weight: 500;
}

.report-table {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.report-row {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 10px 14px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.report-row.row-bad {
  border-left: 3px solid var(--color-danger);
}

.report-row-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.report-table-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--color-text);
}

.report-status {
  margin-left: auto;
  font-size: 11px;
  font-weight: 700;
  padding: 2px 8px;
  border-radius: 12px;
}

.st-match {
  color: var(--color-success);
  background: rgba(16, 185, 129, 0.1);
}

.st-row_count_mismatch,
.st-sample_mismatch,
.st-missing_target,
.st-error {
  color: var(--color-danger);
  background: rgba(239, 68, 68, 0.08);
}

.report-row-detail {
  display: flex;
  gap: 16px;
  font-size: 12px;
  color: var(--color-text-secondary);
}

.report-issue {
  font-size: 12px;
  color: var(--color-danger);
  background: rgba(239, 68, 68, 0.05);
  border-radius: var(--radius-sm);
  padding: 6px 10px;
  word-break: break-all;
  line-height: 1.7;
}

.report-issue strong {
  font-weight: 600;
}

.issue-suggestion {
  color: var(--color-text-secondary);
}

/* 模式切换 */
.mode-switch {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
  padding: 10px;
}

.mode-btn {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  padding: 12px 16px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-surface);
  cursor: pointer;
  transition: all var(--transition-fast);
  font-family: var(--font-sans);
  text-align: left;
}

.mode-btn:hover {
  border-color: #cbd5e1;
  background: var(--color-surface-hover);
}

.mode-btn.active {
  border-color: var(--color-primary);
  background: var(--color-primary-light);
  color: var(--color-primary);
}

.mode-btn span:first-of-type {
  font-size: 13px;
  font-weight: 600;
  color: var(--color-text);
}

.mode-btn.active span:first-of-type {
  color: var(--color-primary);
}

.mode-desc {
  font-size: 11px;
  color: var(--color-text-tertiary);
}

.issue-error {
  color: var(--color-text-secondary);
  background: var(--color-bg);
}

.animate-spin {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

@media (max-width: 768px) {
  .conn-sample {
    margin-left: 0;
  }

  .run-row {
    flex-direction: column;
    align-items: stretch;
  }

  .run-error {
    margin-right: 0;
  }
}
</style>
