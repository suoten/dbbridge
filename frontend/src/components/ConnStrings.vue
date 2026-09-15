<script lang="ts" setup>
import { ref, watch } from 'vue'
import type { ConnectionConfig } from './ConnectionForm.vue'
import {
  Link2,
  Loader2,
  Copy,
  Check,
  Info,
  AlertTriangle,
} from '@lucide/vue'

const props = defineProps<{
  sourceConfig: ConnectionConfig
}>()

const loading = ref(false)
const errorMsg = ref('')
const result = ref<any>(null)
const copiedKey = ref('')

async function generate() {
  loading.value = true
  errorMsg.value = ''
  result.value = null
  try {
    // @ts-ignore - Wails binding
    result.value = await window.go.main.App.GenerateConnStrings(props.sourceConfig)
  } catch (e: any) {
    errorMsg.value = e?.message || String(e)
  } finally {
    loading.value = false
  }
}

// 连接配置变化时自动重新生成
watch(() => props.sourceConfig, generate, { deep: true, immediate: true })

async function copy(key: string) {
  if (!result.value?.templates?.[key]) return
  try {
    await navigator.clipboard.writeText(result.value.templates[key])
    copiedKey.value = key
    setTimeout(() => (copiedKey.value = ''), 1500)
  } catch {
    // 剪贴板不可用时静默
  }
}
</script>

<template>
  <div class="connstr">
    <div class="connstr-intro card">
      <Link2 :size="16" />
      <span>
        按连接配置生成各语言/框架的连接串模板（Java JDBC、Python SQLAlchemy、Go、PHP PDO、Node.js）。
        <strong>纯本地生成，不连接数据库。</strong>
      </span>
    </div>

    <div class="conn-summary card">
      <span class="conn-value">当前配置：{{ sourceConfig.type }} / {{ sourceConfig.host || 'localhost' }} / {{ sourceConfig.database || '(未填库名)' }}</span>
      <button class="btn btn-primary" :disabled="loading" @click="generate">
        <Loader2 v-if="loading" :size="15" class="animate-spin" />
        <Link2 v-else :size="15" />
        重新生成
      </button>
    </div>

    <div v-if="errorMsg" class="error-card card">
      <AlertTriangle :size="16" />
      <span>{{ errorMsg }}</span>
    </div>

    <template v-if="result && result.success">
      <div v-for="(tpl, key) in result.templates" :key="key" class="tpl-card card">
        <div class="tpl-head">
          <span class="tpl-lang">{{ key }}</span>
          <button class="btn btn-ghost btn-sm" @click="copy(String(key))">
            <Check v-if="copiedKey === key" :size="14" />
            <Copy v-else :size="14" />
            {{ copiedKey === key ? '已复制' : '复制' }}
          </button>
        </div>
        <pre class="tpl-code">{{ tpl }}</pre>
      </div>

      <div v-if="result.notes?.length" class="notes-card card">
        <div class="notes-title">
          <Info :size="14" />
          驱动与注意事项
        </div>
        <ul class="notes-list">
          <li v-for="(n, i) in result.notes" :key="i">{{ n }}</li>
        </ul>
      </div>
    </template>
  </div>
</template>

<style scoped>
.connstr {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 960px;
}

.connstr-intro {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 18px;
  font-size: 13px;
  color: var(--color-text-secondary);
  background: var(--color-info-light);
  border-color: var(--color-info);
}

.connstr-intro strong {
  color: var(--color-success);
}

.conn-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 20px;
  flex-wrap: wrap;
}

.conn-value {
  font-size: 13px;
  color: var(--color-text-secondary);
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

.tpl-card {
  padding: 14px 20px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.tpl-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.tpl-lang {
  font-size: 13px;
  font-weight: 700;
  color: var(--color-text);
}

.tpl-code {
  margin: 0;
  padding: 12px 14px;
  background: var(--color-bg);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  font-family: 'Cascadia Code', Consolas, monospace;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--color-text);
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-all;
}

.notes-card {
  padding: 14px 20px;
}

.notes-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 700;
  color: var(--color-text);
}

.notes-list {
  margin: 8px 0 0;
  padding-left: 20px;
  font-size: 12.5px;
  color: var(--color-text-secondary);
  line-height: 1.8;
}

.animate-spin {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
