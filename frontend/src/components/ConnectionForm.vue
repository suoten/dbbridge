<script lang="ts" setup>
import { ref, computed } from 'vue'

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
  { value: 'mysql', label: 'MySQL' },
  { value: 'mariadb', label: 'MariaDB' },
  { value: 'postgres', label: 'PostgreSQL' },
  { value: 'sqlite', label: 'SQLite' },
]

const isSQLite = computed(() => props.modelValue.type === 'sqlite')
const isPostgres = computed(() => props.modelValue.type === 'postgres')

const localConfig = computed({
  get: () => props.modelValue,
  set: (val: ConnectionConfig) => emit('update:modelValue', val)
})

function update(field: keyof ConnectionConfig, value: any) {
  const newConfig = { ...props.modelValue, [field]: value }
  
  // 切换数据库类型时设置默认端口
  if (field === 'type') {
    if (value === 'mysql' || value === 'mariadb') {
      newConfig.port = 3306
    } else if (value === 'postgres') {
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
  <div class="connection-form card">
    <div class="form-header">
      <h3>{{ title }}</h3>
      <button
        class="btn btn-secondary btn-sm"
        @click="handleTest"
        :disabled="testing || disabled"
      >
        {{ testing ? '测试中...' : '测试连接' }}
      </button>
    </div>

    <div class="form-body">
      <div class="form-row">
        <div class="form-group">
          <label class="label">数据库类型</label>
          <select
            class="select"
            :value="modelValue.type"
            @change="update('type', ($event.target as HTMLSelectElement).value)"
            :disabled="disabled"
          >
            <option v-for="t in dbTypes" :key="t.value" :value="t.value">{{ t.label }}</option>
          </select>
        </div>
      </div>

      <template v-if="!isSQLite">
        <div class="form-row">
          <div class="form-group flex-2">
            <label class="label">主机地址</label>
            <input
              class="input"
              type="text"
              :value="modelValue.host"
              @input="update('host', ($event.target as HTMLInputElement).value)"
              placeholder="127.0.0.1"
              :disabled="disabled"
            />
          </div>
          <div class="form-group">
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

        <div class="form-row">
          <div class="form-group">
            <label class="label">用户名</label>
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
            <label class="label">密码</label>
            <input
              class="input"
              type="password"
              :value="modelValue.password"
              @input="update('password', ($event.target as HTMLInputElement).value)"
              :disabled="disabled"
            />
          </div>
        </div>

        <div class="form-row">
          <div class="form-group">
            <label class="label">数据库名</label>
            <input
              class="input"
              type="text"
              :value="modelValue.database"
              @input="update('database', ($event.target as HTMLInputElement).value)"
              :disabled="disabled"
            />
          </div>
          <div class="form-group" v-if="isPostgres">
            <label class="label">SSL Mode</label>
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

      <template v-else>
        <div class="form-row">
          <div class="form-group">
            <label class="label">数据库文件路径</label>
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

      <div v-if="testResult" class="test-result" :class="testResult.success ? 'success' : 'error'">
        <span v-if="testResult.success">✓ 连接成功 — {{ testResult.version }}</span>
        <span v-else>✗ {{ testResult.error }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.connection-form {
  padding: 16px 20px;
}

.form-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--color-border);
}

.form-header h3 {
  font-size: 15px;
  font-weight: 600;
  color: var(--color-text);
}

.form-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.form-row {
  display: flex;
  gap: 12px;
}

.form-group {
  flex: 1;
  display: flex;
  flex-direction: column;
}

.flex-2 {
  flex: 2;
}

.test-result {
  padding: 8px 12px;
  border-radius: var(--radius-sm);
  font-size: 12px;
  font-weight: 500;
}

.test-result.success {
  background: rgba(46, 204, 113, 0.1);
  color: var(--color-success);
}

.test-result.error {
  background: rgba(231, 76, 60, 0.1);
  color: var(--color-danger);
}
</style>
