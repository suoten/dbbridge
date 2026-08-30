<script lang="ts" setup>
import { ref, computed } from 'vue'
import ConnectionForm, { type ConnectionConfig } from './components/ConnectionForm.vue'
import ProgressPanel from './components/ProgressPanel.vue'
import MigrationReport from './components/MigrationReport.vue'

type Tab = 'source' | 'tables' | 'migrate'

const activeTab = ref<Tab>('source')

const sourceConfig = ref<ConnectionConfig>({
  type: 'mysql',
  host: '127.0.0.1',
  port: 3306,
  username: 'root',
  password: '',
  database: '',
  charset: 'utf8mb4',
})

const targetConfig = ref<ConnectionConfig>({
  type: 'sqlite',
  host: '',
  port: 0,
  username: '',
  password: '',
  database: ':memory:',
})

const sourceTested = ref(false)
const targetTested = ref(false)

const tables = ref<{ name: string; comment?: string; selected: boolean }[]>([])
const loadingTables = ref(false)

const migrationConfig = ref({
  structureOnly: false,
  dataOnly: false,
  batchSize: 5000,
  dropIfExists: true,
  ignoreErrors: false,
})

const migrating = ref(false)
const report = ref<any>(null)

const selectedTables = computed(() => tables.value.filter(t => t.selected).map(t => t.name))

async function loadTables() {
  loadingTables.value = true
  try {
    // @ts-ignore - Wails binding
    const result = await window.go.main.App.GetTables(sourceConfig.value)
    if (result.success) {
      tables.value = result.tables.map((t: any) => ({
        name: t.name,
        comment: t.comment,
        selected: true,
      }))
    }
  } catch (e) {
    console.error(e)
  } finally {
    loadingTables.value = false
  }
}

function selectAll() {
  tables.value.forEach(t => (t.selected = true))
}

function deselectAll() {
  tables.value.forEach(t => (t.selected = false))
}

async function startMigration() {
  migrating.value = true
  report.value = null
  activeTab.value = 'migrate'
  try {
    const req = {
      source: sourceConfig.value,
      target: targetConfig.value,
      tables: selectedTables.value,
      ...migrationConfig.value,
    }
    // @ts-ignore - Wails binding
    report.value = await window.go.main.App.StartMigration(req)
  } catch (e) {
    console.error(e)
  } finally {
    migrating.value = false
  }
}

const canStartMigration = computed(() => {
  return sourceTested.value && targetTested.value && selectedTables.value.length > 0 && !migrating.value
})

const tabs = [
  { id: 'source' as Tab, label: '1. 配置连接', icon: '🔗' },
  { id: 'tables' as Tab, label: '2. 选择表', icon: '📋' },
  { id: 'migrate' as Tab, label: '3. 执行迁移', icon: '🚀' },
]
</script>

<template>
  <div class="app">
    <!-- 顶部标题栏 -->
    <header class="app-header">
      <div class="header-brand">
        <span class="logo">🌉</span>
        <span class="brand-name">DBBridge</span>
        <span class="brand-subtitle">数据库迁移工具</span>
      </div>
    </header>

    <!-- Tab 导航 -->
    <nav class="tab-nav">
      <button
        v-for="tab in tabs"
        :key="tab.id"
        class="tab-btn"
        :class="{ active: activeTab === tab.id }"
        @click="activeTab = tab.id"
        :disabled="migrating"
      >
        <span class="tab-icon">{{ tab.icon }}</span>
        <span class="tab-label">{{ tab.label }}</span>
      </button>
    </nav>

    <!-- 内容区域 -->
    <main class="app-content">
      <!-- Tab 1: 配置连接 -->
      <div v-show="activeTab === 'source'" class="tab-content">
        <div class="connection-sections">
          <ConnectionForm
            title="源数据库"
            v-model="sourceConfig"
            @test="sourceTested = true"
          />
          <div class="arrow-icon">→</div>
          <ConnectionForm
            title="目标数据库"
            v-model="targetConfig"
            @test="targetTested = true"
          />
        </div>
        <div class="tab-actions">
          <button
            class="btn btn-primary"
            @click="activeTab = 'tables'"
            :disabled="!sourceTested || !targetTested"
          >
            下一步 →
          </button>
        </div>
      </div>

      <!-- Tab 2: 选择表 -->
      <div v-show="activeTab === 'tables'" class="tab-content">
        <div class="tables-panel">
          <div class="tables-toolbar">
            <button class="btn btn-secondary btn-sm" @click="loadTables" :disabled="loadingTables">
              {{ loadingTables ? '加载中...' : '刷新表列表' }}
            </button>
            <button class="btn btn-secondary btn-sm" @click="selectAll" :disabled="tables.length === 0">全选</button>
            <button class="btn btn-secondary btn-sm" @click="deselectAll" :disabled="tables.length === 0">取消全选</button>
            <span class="tables-count">已选 {{ selectedTables.length }} / {{ tables.length }} 张表</span>
          </div>

          <div class="table-list" v-if="tables.length > 0">
            <div
              v-for="t in tables"
              :key="t.name"
              class="table-item"
              :class="{ selected: t.selected }"
              @click="t.selected = !t.selected"
            >
              <input type="checkbox" v-model="t.selected" @click.stop />
              <span class="table-name">{{ t.name }}</span>
              <span class="table-comment" v-if="t.comment">{{ t.comment }}</span>
            </div>
          </div>
          <div v-else class="empty-state">
            点击"刷新表列表"加载源数据库的表
          </div>
        </div>

        <div class="migration-options">
          <label class="checkbox-label">
            <input type="checkbox" v-model="migrationConfig.dropIfExists" />
            目标表已存在时先删除
          </label>
          <label class="checkbox-label">
            <input type="checkbox" v-model="migrationConfig.structureOnly" />
            仅迁移表结构
          </label>
          <label class="checkbox-label">
            <input type="checkbox" v-model="migrationConfig.dataOnly" />
            仅迁移数据
          </label>
          <label class="checkbox-label">
            <input type="checkbox" v-model="migrationConfig.ignoreErrors" />
            忽略单表错误继续迁移
          </label>
          <div class="form-group">
            <label class="label">批量大小</label>
            <input class="input" type="number" v-model.number="migrationConfig.batchSize" style="width: 120px" />
          </div>
        </div>

        <div class="tab-actions">
          <button class="btn btn-secondary" @click="activeTab = 'source'">← 上一步</button>
          <button
            class="btn btn-primary"
            @click="startMigration"
            :disabled="!canStartMigration"
          >
            🚀 开始迁移
          </button>
        </div>
      </div>

      <!-- Tab 3: 执行迁移 -->
      <div v-show="activeTab === 'migrate'" class="tab-content migrate-tab">
        <ProgressPanel />
        <div class="report-wrapper" v-if="report && !migrating">
          <h3>迁移报告</h3>
          <MigrationReport :report="report" />
        </div>
      </div>
    </main>
  </div>
</template>

<style scoped>
.app {
  display: flex;
  flex-direction: column;
  height: 100vh;
  overflow: hidden;
}

/* Header */
.app-header {
  height: 52px;
  background: var(--color-surface);
  border-bottom: 1px solid var(--color-border);
  display: flex;
  align-items: center;
  padding: 0 20px;
}

.header-brand {
  display: flex;
  align-items: center;
  gap: 8px;
}

.logo {
  font-size: 22px;
}

.brand-name {
  font-size: 18px;
  font-weight: 700;
  color: var(--color-primary);
}

.brand-subtitle {
  font-size: 13px;
  color: var(--color-text-secondary);
}

/* Tabs */
.tab-nav {
  display: flex;
  background: var(--color-surface);
  border-bottom: 1px solid var(--color-border);
  padding: 0 20px;
  gap: 4px;
}

.tab-btn {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 10px 16px;
  border: none;
  background: transparent;
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  color: var(--color-text-secondary);
  border-bottom: 2px solid transparent;
  transition: all 0.2s;
  font-family: var(--font-sans);
}

.tab-btn:hover:not(:disabled) {
  color: var(--color-primary);
}

.tab-btn.active {
  color: var(--color-primary);
  border-bottom-color: var(--color-primary);
}

.tab-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* Content */
.app-content {
  flex: 1;
  overflow: auto;
  padding: 20px;
}

.tab-content {
  max-width: 1100px;
  margin: 0 auto;
}

/* Connection sections */
.connection-sections {
  display: flex;
  align-items: flex-start;
  gap: 16px;
}

.connection-sections > * {
  flex: 1;
}

.arrow-icon {
  font-size: 28px;
  color: var(--color-text-secondary);
  align-self: center;
  flex: 0 0 auto !important;
  padding-top: 20px;
}

/* Tab actions */
.tab-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid var(--color-border);
}

/* Tables panel */
.tables-panel {
  background: var(--color-surface);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border);
  overflow: hidden;
}

.tables-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--color-border);
}

.tables-count {
  margin-left: auto;
  font-size: 12px;
  color: var(--color-text-secondary);
}

.table-list {
  max-height: 400px;
  overflow-y: auto;
}

.table-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 16px;
  cursor: pointer;
  border-bottom: 1px solid #f0f0f0;
  transition: background 0.15s;
}

.table-item:hover {
  background: var(--color-surface-hover);
}

.table-item.selected {
  background: var(--color-primary-light);
}

.table-item .table-name {
  font-size: 13px;
  font-weight: 500;
}

.table-item .table-comment {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.empty-state {
  padding: 40px;
  text-align: center;
  color: var(--color-text-secondary);
  font-style: italic;
}

/* Migration options */
.migration-options {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  align-items: center;
  margin-top: 16px;
  padding: 16px;
  background: var(--color-surface);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border);
}

.checkbox-label {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  cursor: pointer;
}

.checkbox-label input {
  cursor: pointer;
}

/* Migrate tab */
.migrate-tab {
  display: flex;
  flex-direction: column;
  gap: 16px;
  height: 100%;
}

.report-wrapper {
  background: var(--color-surface);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border);
  padding: 16px;
}

.report-wrapper h3 {
  font-size: 15px;
  margin-bottom: 12px;
}
</style>
