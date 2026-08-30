<script lang="ts" setup>
import { ref, computed } from 'vue'
import {
  Database,
  Server,
  Lock,
  User,
  KeyRound,
  FileText,
  Plug,
  CheckCircle2,
  XCircle,
  Loader2,
  ShieldCheck,
  HardDrive,
} from '@lucide/vue'

export interface ConnectionConfig {
  type: string
  host: string
  port: number
  username: string
  password: string
  database: string
  sslMode?: string
  charset?: string
}

const props = defineProps<{
  title: string
  role: 'source' | 'target'
  modelValue: ConnectionConfig
  disabled?: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: ConnectionConfig]
  'test': []
}>()

const testing = ref(false)
const testResult = ref<{ success: boolean; version?: string; error?: string } | null>(null)

const dbTypes = [
  // 开源主流
  { value: 'mysql', label: 'MySQL', icon: Database, group: '开源' },
  { value: 'mariadb', label: 'MariaDB', icon: Database, group: '开源' },
  { value: 'postgres', label: 'PostgreSQL', icon: Database, group: '开源' },
  { value: 'sqlite', label: 'SQLite', icon: HardDrive, group: '开源' },
  // 云原生 & 分布式
  { value: 'tidb', label: 'TiDB', icon: Database, group: '分布式' },
  { value: 'oceanbase', label: 'OceanBase', icon: Database, group: '分布式' },
  { value: 'cockroachdb', label: 'CockroachDB', icon: Database, group: '分布式' },
  // 国产数据库
  { value: 'opengauss', label: 'openGauss', icon: Database, group: '国产' },
  { value: 'dameng', label: '达梦 DM', icon: Database, group: '国产' },
  { value: 'kingbase', label: '金仓 KingbaseES', icon: Database, group: '国产' },
]

const isSQLite = computed(() => props.modelValue.type === 'sqlite')
const isPostgresLike = computed(() => ['postgres', 'opengauss', 'kingbase', 'cockroachdb'].includes(props.modelValue.type))
const isPostgres = isPostgresLike

const selectedDbType = computed(() => dbTypes.find(t => t.value === props.modelValue.type))

const groupedTypes = computed(() => {
  const groups: Record<string, typeof dbTypes> = {}
  for (const t of dbTypes) {
    const g = (t as any).group || '其他'
    if (!groups[g]) groups[g] = []
    groups[g].push(t)
  }
  return Object.entries(groups).map(([name, items]) => ({ name, items }))
})

function update(field: keyof ConnectionConfig, value: any) {
  const newConfig = { ...props.modelValue, [field]: value }
  if (field === 'type') {
    if (value === 'mysql' || value === 'mariadb') {
      newConfig.port = 3306
    } else if (value === 'tidb') {
      newConfig.port = 4000
    } else if (value === 'oceanbase') {
      newConfig.port = 2881
    } else if (value === 'dameng') {
      newConfig.port = 5236
    } else if (['postgres', 'opengauss', 'kingbase', 'cockroachdb'].includes(value)) {
      newConfig.port = 5432
    }
  }
  emit('update:modelValue', newConfig)
}

async function handleTest() {
  testing.value = true
  testResult.value = null
  try {
    // @ts-ignore - Wails binding
    const result = await window.go.main.App.TestConnection(props.modelValue)
    testResult.value = result
    if (result.success) {
      emit('test')
    }
  } catch (e: any) {
    testResult.value = { success: false, error: e?.message || String(e) }
  } finally {
    testing.value = false
  }
}
</script>

<template>
  <div class="conn-form card">
    <!-- Header -->
    <div class="form-header">
      <div class="header-left">
        <div class="header-icon" :class="role">
          <component v-if="selectedDbType" :is="selectedDbType.icon" :size="18" />
          <Database v-else :size="18" />
        </div>
        <div>
          <h3 class="form-title">{{ title }}</h3>
          <span class="form-subtitle">{{ selectedDbType?.label || '未选择' }}</span>
        </div>
      </div>
      <button
        class="btn btn-primary btn-sm"
        @click="handleTest"
        :disabled="testing || disabled"
      >
        <Loader2 v-if="testing" :size="14" class="animate-spin" />
        <Plug v-else :size="14" />
        {{ testing ? '测试中' : '测试连接' }}
      </button>
    </div>

    <!-- Body -->
    <div class="form-body">
      <!-- DB Type Selector -->
      <div class="db-type-selector">
        <div class="db-type-group" v-for="grp in groupedTypes" :key="grp.name">
          <div class="db-type-group-label">{{ grp.name }}</div>
          <div class="db-type-group-items">
            <button
              v-for="t in grp.items"
              :key="t.value"
              class="db-type-btn"
              :class="{ active: modelValue.type === t.value }"
              @click="update('type', t.value)"
              :disabled="disabled"
            >
              <component :is="t.icon" :size="16" />
              {{ t.label }}
            </button>
          </div>
        </div>
      </div>

      <template v-if="!isSQLite">
        <!-- Host & Port -->
        <div class="form-row">
          <div class="form-group flex-grow-2">
            <label class="label">
              <Server :size="12" />
              主机地址
            </label>
            <input
              class="input"
              type="text"
              :value="modelValue.host"
              @input="update('host', ($event.target as HTMLInputElement).value)"
              placeholder="127.0.0.1"
              :disabled="disabled"
            />
          </div>
          <div class="form-group port-group">
            <label class="label">端口</label>
            <input
              class="input"
              type="number"
              :value="modelValue.port"
              @input="update('port', parseInt(($event.target as HTMLInputElement).value) || 0)"
              :disabled="disabled"
            />
          </div>
        </div>

        <!-- Username & Password -->
        <div class="form-row">
          <div class="form-group">
            <label class="label">
              <User :size="12" />
              用户名
            </label>
            <input
              class="input"
              type="text"
              :value="modelValue.username"
              @input="update('username', ($event.target as HTMLInputElement).value)"
              placeholder="root"
              :disabled="disabled"
            />
          </div>
          <div class="form-group">
            <label class="label">
              <KeyRound :size="12" />
              密码
            </label>
            <input
              class="input"
              type="password"
              :value="modelValue.password"
              @input="update('password', ($event.target as HTMLInputElement).value)"
              :disabled="disabled"
            />
          </div>
        </div>

        <!-- Database & SSL -->
        <div class="form-row">
          <div class="form-group">
            <label class="label">
              <Database :size="12" />
              数据库名
            </label>
            <input
              class="input"
              type="text"
              :value="modelValue.database"
              @input="update('database', ($event.target as HTMLInputElement).value)"
              :disabled="disabled"
            />
          </div>
          <div class="form-group" v-if="isPostgres">
            <label class="label">
              <ShieldCheck :size="12" />
              SSL
            </label>
            <select
              class="select"
              :value="modelValue.sslMode || 'disable'"
              @change="update('sslMode', ($event.target as HTMLSelectElement).value)"
              :disabled="disabled"
            >
              <option value="disable">disable</option>
              <option value="require">require</option>
              <option value="verify-ca">verify-ca</option>
              <option value="verify-full">verify-full</option>
            </select>
          </div>
        </div>
      </template>

      <!-- SQLite: file path -->
      <template v-else>
        <div class="form-row">
          <div class="form-group">
            <label class="label">
              <FileText :size="12" />
              数据库文件路径
            </label>
            <input
              class="input"
              type="text"
              :value="modelValue.database"
              @input="update('database', ($event.target as HTMLInputElement).value)"
              placeholder="/path/to/database.db 或 :memory:"
              :disabled="disabled"
            />
          </div>
        </div>
      </template>

      <!-- Test Result -->
      <Transition name="fade">
        <div v-if="testResult" class="test-result" :class="testResult.success ? 'success' : 'error'">
          <component
            :is="testResult.success ? CheckCircle2 : XCircle"
            :size="16"
          />
          <div class="result-text">
            <span v-if="testResult.success" class="result-title">连接成功</span>
            <span v-else class="result-title">连接失败</span>
            <span v-if="testResult.version" class="result-detail">{{ testResult.version }}</span>
            <span v-if="testResult.error" class="result-detail">{{ testResult.error }}</span>
          </div>
        </div>
      </Transition>
    </div>
  </div>
</template>

<style scoped>
.conn-form {
  padding: 18px 20px;
}

/* Header */
.form-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 18px;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--color-border);
}

.header-left {
  display: flex;
  align-items: center;
  gap: 10px;
}

.header-icon {
  width: 36px;
  height: 36px;
  border-radius: var(--radius-md);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.header-icon.source {
  background: linear-gradient(135deg, #3b82f6, #1d4ed8);
  color: white;
  box-shadow: 0 2px 8px rgba(59, 130, 246, 0.25);
}

.header-icon.target {
  background: linear-gradient(135deg, #8b5cf6, #6d28d9);
  color: white;
  box-shadow: 0 2px 8px rgba(139, 92, 246, 0.25);
}

.form-title {
  font-size: 15px;
  font-weight: 700;
  color: var(--color-text);
}

.form-subtitle {
  font-size: 11px;
  color: var(--color-text-secondary);
}

/* Body */
.form-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* DB Type Selector */
.db-type-selector {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 10px;
  background: var(--color-bg);
  border-radius: var(--radius-md);
}

.db-type-group {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.db-type-group-label {
  font-size: 10px;
  font-weight: 600;
  color: var(--color-text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  padding: 0 2px;
}

.db-type-group-items {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.db-type-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  padding: 8px 6px;
  flex: 1 1 auto;
  min-width: 0;
  white-space: nowrap;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  cursor: pointer;
  font-size: 12px;
  font-weight: 500;
  color: var(--color-text-secondary);
  transition: all var(--transition-fast);
  font-family: var(--font-sans);
}

.db-type-btn:hover:not(:disabled) {
  background: var(--color-surface);
  color: var(--color-text);
}

.db-type-btn.active {
  background: var(--color-surface);
  color: var(--color-primary);
  box-shadow: var(--shadow-sm);
  font-weight: 600;
}

.db-type-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

/* Form rows */
.form-row {
  display: flex;
  gap: 12px;
}

.form-group {
  flex: 1;
  display: flex;
  flex-direction: column;
}

.flex-grow-2 {
  flex: 2;
}

.port-group {
  max-width: 100px;
}

/* Label with icon */
.label {
  display: flex;
  align-items: center;
  gap: 4px;
}

/* Test Result */
.test-result {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 14px;
  border-radius: var(--radius-md);
  font-size: 12px;
}

.test-result.success {
  background: var(--color-success-light);
  color: var(--color-success);
}

.test-result.error {
  background: var(--color-danger-light);
  color: var(--color-danger);
}

.result-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.result-title {
  font-weight: 600;
}

.result-detail {
  font-size: 11px;
  opacity: 0.85;
}

/* Animations */
.animate-spin {
  animation: spin 1s linear infinite;
}

/* ====== 响应式 ====== */

/* ≤680px：表单行上下堆叠，类型按钮自动换行 */
@media (max-width: 680px) {
  .form-row {
    flex-direction: column;
    gap: 10px;
  }

  .port-group {
    max-width: 100%;
  }
}
</style>
