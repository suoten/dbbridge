<script lang="ts" setup>
import { ref, onMounted } from 'vue'
import {
  FileCode2,
  Wand2,
  Copy,
  Check,
  Loader2,
  FileWarning,
  AlertTriangle,
  Info,
  Search,
} from '@lucide/vue'

// 支持的方言列表（与后端 GetSupportedDatabases 一致）
const dialects = ref<string[]>([])
onMounted(async () => {
  try {
    // @ts-ignore - Wails binding
    const list = await window.go.main.App.GetSupportedDatabases()
    if (Array.isArray(list)) dialects.value = list
  } catch {
    dialects.value = ['mysql', 'mariadb', 'tidb', 'oceanbase', 'dameng', 'postgres', 'opengauss', 'kingbase', 'cockroachdb', 'sqlite', 'mssql']
  }
})

// 工具模式：convert = 脚本转换，lint = 方言体检
const mode = ref<'convert' | 'lint'>('convert')

const sourceDialect = ref('mysql')
const targetDialect = ref('postgres')
const sqlText = ref('')

const loading = ref(false)
const errorMsg = ref('')

// 转换结果
const convertResult = ref<any>(null)
// 体检结果
const lintResult = ref<any>(null)
const copied = ref(false)

async function runConvert() {
  loading.value = true
  errorMsg.value = ''
  convertResult.value = null
  lintResult.value = null
  try {
    // @ts-ignore - Wails binding
    convertResult.value = await window.go.main.App.ConvertSQL({
      sourceDialect: sourceDialect.value,
      targetDialect: targetDialect.value,
      sql: sqlText.value,
    })
  } catch (e: any) {
    errorMsg.value = e?.message || String(e)
  } finally {
    loading.value = false
  }
}

async function runLint() {
  loading.value = true
  errorMsg.value = ''
  convertResult.value = null
  lintResult.value = null
  try {
    // @ts-ignore - Wails binding
    lintResult.value = await window.go.main.App.LintSQL({
      sourceDialect: sourceDialect.value,
      targetDialect: targetDialect.value,
      sql: sqlText.value,
    })
  } catch (e: any) {
    errorMsg.value = e?.message || String(e)
  } finally {
    loading.value = false
  }
}

async function copyConverted() {
  if (!convertResult.value?.converted) return
  try {
    await navigator.clipboard.writeText(convertResult.value.converted)
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  } catch {
    // 剪贴板不可用时静默，不阻塞主流程
  }
}

function severityClass(sev: string) {
  if (sev === 'error') return 'sev-error'
  if (sev === 'warning') return 'sev-warning'
  return 'sev-info'
}

function severityLabel(sev: string) {
  if (sev === 'error') return '错误'
  if (sev === 'warning') return '警告'
  return '建议'
}

function canRun() {
  return !!sqlText.value.trim() && sourceDialect.value && targetDialect.value && !loading.value
}
</script>

<template>
  <div class="sql-tools">
    <!-- 模式切换 -->
    <div class="mode-switch card">
      <button
        class="mode-btn"
        :class="{ active: mode === 'convert' }"
        @click="mode = 'convert'"
      >
        <Wand2 :size="16" />
        <span>SQL 脚本转换</span>
        <span class="mode-desc">把存量 .sql 脚本转成目标库方言</span>
      </button>
      <button
        class="mode-btn"
        :class="{ active: mode === 'lint' }"
        @click="mode = 'lint'"
      >
        <Search :size="16" />
        <span>SQL 方言体检</span>
        <span class="mode-desc">按行号报告不兼容写法与改法建议</span>
      </button>
    </div>

    <!-- 方言选择 -->
    <div class="dialect-card card">
      <div class="dialect-row">
        <div class="dialect-field">
          <label>源方言</label>
          <select v-model="sourceDialect" class="input">
            <option v-for="d in dialects" :key="d" :value="d">{{ d }}</option>
          </select>
        </div>
        <div class="dialect-arrow">→</div>
        <div class="dialect-field">
          <label>目标方言</label>
          <select v-model="targetDialect" class="input">
            <option v-for="d in dialects" :key="d" :value="d">{{ d }}</option>
          </select>
        </div>
      </div>
      <textarea
        v-model="sqlText"
        class="sql-input"
        rows="10"
        spellcheck="false"
        placeholder="粘贴 SQL 脚本内容（mysqldump 导出文件可直接粘贴）..."
      ></textarea>
      <div class="dialect-footer">
        <span class="hint-text">
          {{ mode === 'convert'
            ? '转换基于 mysqldump 风格解析，MySQL 系源最可靠；转换后自动附带体检提示'
            : '体检只报告问题不改写内容：错误 = 在目标库必然报错，警告 = 语义有差异需人工确认' }}
        </span>
        <button
          v-if="mode === 'convert'"
          class="btn btn-primary"
          :disabled="!canRun()"
          @click="runConvert"
        >
          <Loader2 v-if="loading" :size="15" class="animate-spin" />
          <Wand2 v-else :size="15" />
          开始转换
        </button>
        <button
          v-else
          class="btn btn-primary"
          :disabled="!canRun()"
          @click="runLint"
        >
          <Loader2 v-if="loading" :size="15" class="animate-spin" />
          <Search v-else :size="15" />
          开始体检
        </button>
      </div>
    </div>

    <!-- 错误 -->
    <div v-if="errorMsg" class="error-card card">
      <FileWarning :size="16" />
      <span>{{ errorMsg }}</span>
    </div>

    <!-- 转换结果 -->
    <div v-if="convertResult && convertResult.success" class="result-card card">
      <div class="result-header">
        <span class="result-title">转换结果</span>
        <span class="result-stats">
          共 {{ convertResult.totalStatements }} 条 ·
          转换 {{ convertResult.convertedCount }} ·
          原样透传 {{ convertResult.passedThrough }} ·
          跳过 {{ convertResult.skippedCount }}
        </span>
        <button class="btn btn-ghost btn-sm" @click="copyConverted">
          <Check v-if="copied" :size="14" />
          <Copy v-else :size="14" />
          {{ copied ? '已复制' : '复制' }}
        </button>
      </div>

      <!-- 表达式级改写清单 -->
      <div v-if="convertResult.changes?.length" class="warnings-box">
        <div class="warnings-title">
          <Wand2 :size="14" />
          表达式改写
        </div>
        <ul class="warnings-list">
          <li v-for="(c, i) in convertResult.changes" :key="i">{{ c }}</li>
        </ul>
      </div>

      <div v-if="convertResult.warnings?.length" class="warnings-box">
        <div class="warnings-title">
          <AlertTriangle :size="14" />
          需人工确认（{{ convertResult.warnings.length }}）
        </div>
        <ul class="warnings-list">
          <li v-for="(w, i) in convertResult.warnings" :key="i">{{ w }}</li>
        </ul>
      </div>

      <pre class="sql-output">{{ convertResult.converted }}</pre>
    </div>

    <!-- 体检结果 -->
    <div v-if="lintResult" class="result-card card">
      <div class="result-header">
        <span class="result-title">体检报告</span>
        <span class="result-stats">
          错误 {{ lintResult.errors }} · 警告 {{ lintResult.warnings }} · 建议 {{ lintResult.infos }}
          （扫描 {{ lintResult.totalLines }} 行）
        </span>
      </div>

      <div
        v-if="lintResult.errors === 0 && lintResult.warnings === 0"
        class="lint-clean"
      >
        <Check :size="18" />
        <span>未发现必然报错或不兼容的写法（{{ lintResult.infos > 0 ? `另有 ${lintResult.infos} 条建议见下` : '也没有改进建议' }}）</span>
      </div>

      <div class="findings-list">
        <div
          v-for="(f, i) in lintResult.findings"
          :key="i"
          class="finding-item"
          :class="severityClass(f.severity)"
        >
          <div class="finding-head">
            <span class="finding-sev">
              <AlertTriangle v-if="f.severity === 'error'" :size="13" />
              <FileWarning v-else-if="f.severity === 'warning'" :size="13" />
              <Info v-else :size="13" />
              {{ severityLabel(f.severity) }}
            </span>
            <span class="finding-loc">第 {{ f.line }} 行 第 {{ f.column }} 列</span>
            <span class="finding-cat">{{ f.category }}</span>
          </div>
          <div class="finding-msg">{{ f.message }}</div>
          <div v-if="f.suggestion" class="finding-suggestion">建议：{{ f.suggestion }}</div>
          <code v-if="f.snippet" class="finding-snippet">{{ f.snippet }}</code>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.sql-tools {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 960px;
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
  padding: 14px 16px;
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

/* 方言与输入 */
.dialect-card {
  padding: 16px 20px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.dialect-row {
  display: flex;
  align-items: center;
  gap: 16px;
}

.dialect-field {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.dialect-field label {
  font-size: 12px;
  font-weight: 600;
  color: var(--color-text-secondary);
}

.dialect-arrow {
  color: var(--color-text-tertiary);
  font-size: 18px;
  padding-top: 16px;
}

.sql-input {
  width: 100%;
  padding: 12px 14px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-bg);
  font-family: 'Cascadia Code', Consolas, monospace;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--color-text);
  resize: vertical;
  outline: none;
  transition: border-color var(--transition-fast);
}

.sql-input:focus {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px rgba(59, 130, 246, 0.12);
}

.dialect-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.hint-text {
  font-size: 12px;
  color: var(--color-text-tertiary);
}

/* 结果 */
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

.result-card {
  padding: 16px 20px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.result-header {
  display: flex;
  align-items: center;
  gap: 12px;
}

.result-title {
  font-size: 14px;
  font-weight: 700;
  color: var(--color-text);
}

.result-stats {
  font-size: 12px;
  color: var(--color-text-secondary);
  flex: 1;
}

.sql-output {
  margin: 0;
  padding: 14px;
  background: var(--color-bg);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  font-family: 'Cascadia Code', Consolas, monospace;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--color-text);
  overflow-x: auto;
  max-height: 480px;
  overflow-y: auto;
  white-space: pre-wrap;
  word-break: break-all;
}

.warnings-box {
  border: 1px solid #f59e0b;
  background: rgba(245, 158, 11, 0.08);
  border-radius: var(--radius-md);
  padding: 10px 14px;
}

.warnings-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 700;
  color: #b45309;
}

.warnings-list {
  margin: 6px 0 0;
  padding-left: 20px;
  font-size: 12px;
  color: var(--color-text-secondary);
  line-height: 1.7;
}

/* 体检 */
.lint-clean {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  border-radius: var(--radius-md);
  background: rgba(16, 185, 129, 0.1);
  color: var(--color-success);
  font-size: 13px;
  font-weight: 600;
}

.findings-list {
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

.finding-loc {
  font-size: 11px;
  color: var(--color-text-secondary);
  font-family: Consolas, monospace;
}

.finding-cat {
  margin-left: auto;
  font-size: 11px;
  color: var(--color-text-tertiary);
}

.finding-msg {
  font-size: 13px;
  color: var(--color-text);
}

.finding-suggestion {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.finding-snippet {
  font-size: 11.5px;
  font-family: Consolas, monospace;
  background: var(--color-bg);
  border: 1px solid var(--color-border-light);
  border-radius: var(--radius-sm);
  padding: 6px 10px;
  color: var(--color-text-secondary);
  word-break: break-all;
  white-space: pre-wrap;
}

.animate-spin {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

/* 响应式 */
@media (max-width: 768px) {
  .dialect-row {
    flex-direction: column;
    align-items: stretch;
  }

  .dialect-arrow {
    display: none;
  }

  .dialect-footer {
    flex-direction: column;
    align-items: stretch;
  }

  .mode-switch {
    grid-template-columns: 1fr;
  }
}
</style>
