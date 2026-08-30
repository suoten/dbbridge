<script lang="ts" setup>
import { ref, computed, onMounted } from 'vue'
import {
  Archive,
  RotateCcw,
  Trash2,
  RefreshCw,
  CheckCircle2,
  XCircle,
  Loader2,
  Database,
  AlertTriangle,
} from '@lucide/vue'
import type { ConnectionConfig } from './ConnectionForm.vue'

const props = defineProps<{
  targetConfig: ConnectionConfig
  initialBackups?: { backupTable: string; originalTable: string }[]
}>()

const emit = defineEmits<{
  'restored': [table: string]
}>()

interface BackupItem {
  backupName: string
  originalName: string
  selected: boolean
}

const backups = ref<BackupItem[]>([])
const loading = ref(false)
const actionLoading = ref<string | null>(null) // 正在操作的备份表名
const message = ref<{ type: 'success' | 'error' | 'info'; text: string } | null>(null)

const hasBackups = computed(() => backups.value.length > 0)
const selectedBackups = computed(() => backups.value.filter(b => b.selected))

async function loadBackups() {
  loading.value = true
  message.value = null
  try {
    // @ts-ignore - Wails binding
    const result = await window.go.main.App.GetBackupTables(props.targetConfig)
    if (result.success) {
      backups.value = (result.tables || []).map((t: any) => ({
        backupName: t.backupName,
        originalName: t.originalName,
        selected: false,
      }))
    } else {
      message.value = { type: 'error', text: result.error }
    }
  } catch (e: any) {
    message.value = { type: 'error', text: e?.message || String(e) }
  } finally {
    loading.value = false
  }
}

function selectAll() {
  backups.value.forEach(b => (b.selected = true))
}

function deselectAll() {
  backups.value.forEach(b => (b.selected = false))
}

async function restoreOne(backup: BackupItem) {
  actionLoading.value = backup.backupName
  message.value = null
  try {
    const req = {
      connection: props.targetConfig,
      backupName: backup.backupName,
    }
    // @ts-ignore - Wails binding
    const result = await window.go.main.App.RestoreTable(req)
    if (result.success) {
      message.value = {
        type: 'success',
        text: `已回滚: ${backup.originalName}（从 ${backup.backupName} 恢复）`,
      }
      emit('restored', backup.originalName)
      await loadBackups()
    } else {
      message.value = { type: 'error', text: result.error || '回滚失败' }
    }
  } catch (e: any) {
    message.value = { type: 'error', text: e?.message || String(e) }
  } finally {
    actionLoading.value = null
  }
}

async function restoreSelected() {
  if (selectedBackups.value.length === 0) return
  actionLoading.value = '__batch__'
  message.value = null
  try {
    const req = {
      connection: props.targetConfig,
      backupNames: selectedBackups.value.map(b => b.backupName),
    }
    // @ts-ignore - Wails binding
    const result = await window.go.main.App.RestoreAllTables(req)
    const total = selectedBackups.value.length
    if (result.failedCount === 0) {
      message.value = {
        type: 'success',
        text: `全部回滚成功: ${result.successCount} 张表已恢复`,
      }
    } else {
      message.value = {
        type: 'error',
        text: `回滚 ${result.successCount}/${total} 成功，失败: ${result.failedItems?.join(', ')}`,
      }
    }
    for (const b of selectedBackups.value) {
      emit('restored', b.originalName)
    }
    await loadBackups()
  } catch (e: any) {
    message.value = { type: 'error', text: e?.message || String(e) }
  } finally {
    actionLoading.value = null
  }
}

async function deleteOne(backup: BackupItem) {
  actionLoading.value = backup.backupName
  message.value = null
  try {
    const req = {
      connection: props.targetConfig,
      backupName: backup.backupName,
    }
    // @ts-ignore - Wails binding
    const result = await window.go.main.App.DeleteBackup(req)
    if (result.success) {
      message.value = {
        type: 'success',
        text: `已删除备份表: ${backup.backupName}`,
      }
      await loadBackups()
    } else {
      message.value = { type: 'error', text: result.error || '删除失败' }
    }
  } catch (e: any) {
    message.value = { type: 'error', text: e?.message || String(e) }
  } finally {
    actionLoading.value = null
  }
}

onMounted(() => {
  // 如果有初始备份数据（来自迁移报告），直接展示
  if (props.initialBackups && props.initialBackups.length > 0) {
    backups.value = props.initialBackups.map(b => ({
      backupName: b.backupTable,
      originalName: b.originalTable,
      selected: false,
    }))
  } else {
    // 没有初始备份时自动加载
    loadBackups()
  }
})
</script>

<template>
  <div class="backup-manager">
    <div class="bm-header">
      <div class="bm-title">
        <Archive :size="18" />
        <span>备份与回滚</span>
        <span class="bm-count" v-if="backups.length > 0">{{ backups.length }} 个备份</span>
      </div>
      <button class="btn btn-ghost btn-sm" @click="loadBackups" :disabled="loading">
        <Loader2 v-if="loading" :size="14" class="animate-spin" />
        <RefreshCw v-else :size="14" />
        刷新
      </button>
    </div>

    <!-- 消息提示 -->
    <Transition name="fade">
      <div v-if="message" class="bm-message" :class="message.type">
        <component
          :is="message.type === 'success' ? CheckCircle2 : message.type === 'error' ? XCircle : AlertTriangle"
          :size="15"
        />
        <span>{{ message.text }}</span>
      </div>
    </Transition>

    <!-- 备份列表 -->
    <div class="bm-list" v-if="hasBackups">
      <div class="bm-toolbar">
        <button class="btn btn-ghost btn-sm" @click="selectAll">全选</button>
        <button class="btn btn-ghost btn-sm" @click="deselectAll">取消</button>
        <span class="bm-selected-count" v-if="selectedBackups.length > 0">
          已选 {{ selectedBackups.length }} 个
        </span>
        <button
          class="btn btn-secondary btn-sm bm-batch-btn"
          @click="restoreSelected"
          :disabled="selectedBackups.length === 0 || actionLoading === '__batch__'"
        >
          <Loader2 v-if="actionLoading === '__batch__'" :size="14" class="animate-spin" />
          <RotateCcw v-else :size="14" />
          批量回滚
        </button>
      </div>

      <div class="bm-items">
        <div
          v-for="b in backups"
          :key="b.backupName"
          class="bm-item"
          :class="{ selected: b.selected }"
        >
          <label class="bm-item-check">
            <input type="checkbox" v-model="b.selected" />
          </label>
          <div class="bm-item-icon"><Database :size="15" /></div>
          <div class="bm-item-body">
            <span class="bm-item-original">{{ b.originalName }}</span>
            <span class="bm-item-backup">← {{ b.backupName }}</span>
          </div>
          <div class="bm-item-actions">
            <button
              class="btn btn-secondary btn-sm"
              @click="restoreOne(b)"
              :disabled="actionLoading !== null"
            >
              <Loader2 v-if="actionLoading === b.backupName" :size="13" class="animate-spin" />
              <RotateCcw v-else :size="13" />
              回滚
            </button>
            <button
              class="btn btn-ghost btn-sm bm-delete-btn"
              @click="deleteOne(b)"
              :disabled="actionLoading !== null"
            >
              <Trash2 :size="13" />
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- 空状态 -->
    <div class="bm-empty" v-else-if="!loading">
      <Archive :size="36" class="bm-empty-icon" />
      <p>暂无备份表</p>
      <span>迁移时开启备份后，备份的表会显示在这里</span>
    </div>
  </div>
</template>

<style scoped>
.backup-manager {
  background: var(--color-surface);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border);
  overflow: hidden;
}

.bm-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 18px;
  border-bottom: 1px solid var(--color-border);
}

.bm-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text);
}

.bm-count {
  font-size: 11px;
  color: var(--color-text-tertiary);
  background: var(--color-bg);
  padding: 2px 8px;
  border-radius: 10px;
}

/* Message */
.bm-message {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 18px;
  font-size: 13px;
}

.bm-message.success {
  background: var(--color-success-light);
  color: var(--color-success);
}

.bm-message.error {
  background: var(--color-danger-light);
  color: var(--color-danger);
}

.bm-message.info {
  background: var(--color-info-light);
  color: var(--color-info);
}

/* List */
.bm-list {
  padding: 0;
}

.bm-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 18px;
  border-bottom: 1px solid var(--color-border-light);
  background: var(--color-bg);
}

.bm-selected-count {
  font-size: 12px;
  color: var(--color-primary);
  font-weight: 600;
}

.bm-batch-btn {
  margin-left: auto;
}

.bm-items {
  max-height: 320px;
  overflow-y: auto;
}

.bm-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 18px;
  border-bottom: 1px solid var(--color-border-light);
  transition: background var(--transition-fast);
}

.bm-item:hover {
  background: var(--color-surface-hover);
}

.bm-item.selected {
  background: var(--color-primary-light);
}

.bm-item-check input {
  width: 16px;
  height: 16px;
  cursor: pointer;
  accent-color: var(--color-primary);
}

.bm-item-icon {
  color: var(--color-text-tertiary);
  display: flex;
  flex-shrink: 0;
}

.bm-item.selected .bm-item-icon {
  color: var(--color-primary);
}

.bm-item-body {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}

.bm-item-original {
  font-size: 13px;
  font-weight: 600;
  color: var(--color-text);
  font-family: var(--font-mono);
}

.bm-item-backup {
  font-size: 11px;
  color: var(--color-text-tertiary);
  font-family: var(--font-mono);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.bm-item-actions {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}

.bm-delete-btn {
  color: var(--color-danger);
}

.bm-delete-btn:hover {
  background: var(--color-danger-light);
}

/* Empty */
.bm-empty {
  padding: 36px 20px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
}

.bm-empty-icon {
  color: var(--color-border);
}

.bm-empty p {
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text-secondary);
}

.bm-empty span {
  font-size: 12px;
  color: var(--color-text-tertiary);
}

/* Animations */
.animate-spin {
  animation: spin 1s linear infinite;
}
</style>
