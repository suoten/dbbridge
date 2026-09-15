<script lang="ts" setup>
import { ref, computed } from 'vue'
import {
  Database,
  Plug,
  Table2,
  Rocket,
  CheckCircle2,
  ArrowRight,
  ArrowLeft,
  Search,
  CheckSquare,
  Square,
  Trash2,
  FileWarning,
  Settings2,
  Loader2,
  Building2,
  ShieldCheck,
  RotateCcw,
  Archive,
  History,
  Zap,
  Code2,
  Wand2,
  ClipboardCheck,
  Compass,
  Link2,
} from '@lucide/vue'
import ConnectionForm, { type ConnectionConfig } from './components/ConnectionForm.vue'
import ProgressPanel from './components/ProgressPanel.vue'
import MigrationReport from './components/MigrationReport.vue'
import BackupManager from './components/BackupManager.vue'
import MigrationHistory from './components/MigrationHistory.vue'
import SqlTools from './components/SqlTools.vue'
import DataValidator from './components/DataValidator.vue'
import MigrationGuide from './components/MigrationGuide.vue'
import ConnStrings from './components/ConnStrings.vue'

type Tab = 'source' | 'tables' | 'migrate' | 'backup' | 'sqltools' | 'validate' | 'guide' | 'connstr'

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
const tablesError = ref('')
const tableSearch = ref('')

const filteredTables = computed(() => {
  if (!tableSearch.value) return tables.value
  const q = tableSearch.value.toLowerCase()
  return tables.value.filter(t => t.name.toLowerCase().includes(q) || (t.comment || '').toLowerCase().includes(q))
})

const migrationConfig = ref({
  structureOnly: false,
  dataOnly: false,
  batchSize: 5000,
  dropIfExists: true,
  ignoreErrors: false,
  backupBefore: true,
  autoRollback: true,
  migrateTriggers: false,
  migrateRoutines: false,
})

const migrating = ref(false)
const report = ref<any>(null)
const migrationError = ref('')

const selectedTables = computed(() => tables.value.filter(t => t.selected).map(t => t.name))

async function loadTables() {
  loadingTables.value = true
  tablesError.value = ''
  try {
    // @ts-ignore - Wails binding
    const result = await window.go.main.App.GetTables(sourceConfig.value)
    if (result.success) {
      tables.value = (result.tables || []).map((t: any) => ({
        name: t.name,
        comment: t.comment,
        selected: true,
      }))
    } else {
      // 静默失败会让用户误以为库里没有表，必须展示错误
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

function onHistorySelectTarget(config: ConnectionConfig) {
  targetConfig.value = { ...config }
  targetTested.value = false
}

function goToTables() {
  activeTab.value = 'tables'
  if (tables.value.length === 0) {
    loadTables()
  }
}

async function startMigration() {
  migrating.value = true
  report.value = null
  migrationError.value = ''
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
  } catch (e: any) {
    // 迁移启动失败必须让用户看到，不能只留在控制台
    migrationError.value = e?.message || String(e)
  } finally {
    migrating.value = false
  }
}

const canStartMigration = computed(() => {
  return sourceTested.value && targetTested.value && selectedTables.value.length > 0 && !migrating.value
})

const steps = [
  { id: 'source' as Tab, label: '配置连接', desc: '设置源库与目标库', icon: Plug },
  { id: 'tables' as Tab, label: '选择表', desc: '选择迁移范围与选项', icon: Table2 },
  { id: 'migrate' as Tab, label: '执行迁移', desc: '启动并监控进度', icon: Rocket },
]

const currentStepIndex = computed(() => steps.findIndex(s => s.id === activeTab.value))

// 判断当前是否在迁移流程中（非备份页面）
const isMigrationFlow = computed(() => activeTab.value !== 'backup')
</script>

<template>
  <div class="app">
    <!-- ====== 侧边栏 ====== -->
    <aside class="sidebar">
      <!-- Brand -->
      <div class="sidebar-brand">
        <div class="brand-icon">
          <Building2 :size="22" />
        </div>
        <div class="brand-text">
          <span class="brand-name">DBBridge</span>
          <span class="brand-sub">数据库迁移工具</span>
        </div>
      </div>

      <!-- Step Navigation -->
      <nav class="sidebar-nav">
        <button
          v-for="(step, i) in steps"
          :key="step.id"
          class="nav-item"
          :class="{
            active: activeTab === step.id,
            done: currentStepIndex > i && isMigrationFlow,
            disabled: migrating && activeTab !== step.id,
          }"
          @click="!migrating || activeTab === step.id ? (activeTab = step.id) : null"
        >
          <div class="nav-indicator" v-if="activeTab === step.id"></div>
          <div class="nav-icon-wrapper">
            <component
              v-if="currentStepIndex > i && isMigrationFlow"
              :is="CheckCircle2"
              :size="18"
              class="nav-icon done"
            />
            <component v-else :is="step.icon" :size="18" class="nav-icon" />
          </div>
          <div class="nav-content">
            <span class="nav-label">{{ step.label }}</span>
            <span class="nav-desc">{{ step.desc }}</span>
          </div>
        </button>

        <!-- 分隔线 -->
        <div class="nav-divider"></div>

        <!-- 备份与回滚（独立菜单） -->
        <button
          class="nav-item"
          :class="{ active: activeTab === 'backup' }"
          @click="activeTab = 'backup'"
        >
          <div class="nav-indicator" v-if="activeTab === 'backup'"></div>
          <div class="nav-icon-wrapper">
            <RotateCcw :size="18" class="nav-icon" />
          </div>
          <div class="nav-content">
            <span class="nav-label">备份与回滚</span>
            <span class="nav-desc">随时恢复备份数据</span>
          </div>
        </button>

        <!-- SQL 工具（独立菜单） -->
        <button
          class="nav-item"
          :class="{ active: activeTab === 'sqltools' }"
          @click="activeTab = 'sqltools'"
        >
          <div class="nav-indicator" v-if="activeTab === 'sqltools'"></div>
          <div class="nav-icon-wrapper">
            <Wand2 :size="18" class="nav-icon" />
          </div>
          <div class="nav-content">
            <span class="nav-label">SQL 工具</span>
            <span class="nav-desc">脚本转换与方言体检</span>
          </div>
        </button>

        <!-- 数据校验（独立菜单） -->
        <button
          class="nav-item"
          :class="{ active: activeTab === 'validate' }"
          @click="activeTab = 'validate'"
        >
          <div class="nav-indicator" v-if="activeTab === 'validate'"></div>
          <div class="nav-icon-wrapper">
            <ClipboardCheck :size="18" class="nav-icon" />
          </div>
          <div class="nav-content">
            <span class="nav-label">数据校验</span>
            <span class="nav-desc">切换前核对迁移数据</span>
          </div>
        </button>

        <!-- 迁移指南（独立菜单） -->
        <button
          class="nav-item"
          :class="{ active: activeTab === 'guide' }"
          @click="activeTab = 'guide'"
        >
          <div class="nav-indicator" v-if="activeTab === 'guide'"></div>
          <div class="nav-icon-wrapper">
            <Compass :size="18" class="nav-icon" />
          </div>
          <div class="nav-content">
            <span class="nav-label">迁移指南</span>
            <span class="nav-desc">评估业务代码适配工作</span>
          </div>
        </button>

        <!-- 连接串（独立菜单） -->
        <button
          class="nav-item"
          :class="{ active: activeTab === 'connstr' }"
          @click="activeTab = 'connstr'"
        >
          <div class="nav-indicator" v-if="activeTab === 'connstr'"></div>
          <div class="nav-icon-wrapper">
            <Link2 :size="18" class="nav-icon" />
          </div>
          <div class="nav-content">
            <span class="nav-label">连接串</span>
            <span class="nav-desc">生成各语言连接模板</span>
          </div>
        </button>
      </nav>

      <!-- Sidebar Footer -->
      <div class="sidebar-footer">
        <div class="status-pill" v-if="sourceTested">
          <Database :size="12" />
          <span>源库已连接</span>
        </div>
        <div class="status-pill" v-if="targetTested">
          <Database :size="12" />
          <span>目标库已连接</span>
        </div>
      </div>
    </aside>

    <!-- ====== 主内容区 ====== -->
    <main class="main-content">
      <!-- Top Bar -->
      <header class="top-bar">
        <h1 class="top-title">{{
          activeTab === 'backup' ? '备份与回滚'
          : activeTab === 'sqltools' ? 'SQL 工具'
          : activeTab === 'validate' ? '数据校验'
          : activeTab === 'guide' ? '迁移指南'
          : activeTab === 'connstr' ? '连接串生成'
          : steps.find(s => s.id === activeTab)?.label
        }}</h1>
        <div class="top-actions">
          <span v-if="migrating" class="migrating-badge">
            <Loader2 :size="14" class="animate-spin" />
            迁移中...
          </span>
        </div>
      </header>

      <!-- Content Area -->
      <div class="content-area">
        <!-- ====== 备份与回滚页面（独立） ====== -->
        <Transition name="slide-up" mode="out-in">
          <div v-if="activeTab === 'backup'" key="backup" class="step-content backup-step">
            <div class="backup-hint card">
              <ShieldCheck :size="16" />
              <span>从目标数据库中查找备份表（以 _bak_ 开头），可随时回滚或清理。请确保下方目标库连接信息正确。</span>
            </div>
            <ConnectionForm
              title="目标数据库"
              role="target"
              v-model="targetConfig"
              @test="targetTested = true"
            />
<div class="backup-section">
<h3 class="backup-section-title">
<Archive :size="16" />
目标库备份表
</h3>
<BackupManager
:target-config="targetConfig"
/>
</div>
<div class="backup-section">
<h3 class="backup-section-title">
<History :size="16" />
迁移历史
</h3>
<MigrationHistory
@select-target="onHistorySelectTarget"
/>
</div>
</div>

        <!-- ====== SQL 工具页面（独立） ====== -->
        <div v-else-if="activeTab === 'sqltools'" key="sqltools" class="step-content tool-step">
          <SqlTools />
        </div>

        <!-- ====== 数据校验页面（独立） ====== -->
        <div v-else-if="activeTab === 'validate'" key="validate" class="step-content tool-step">
          <DataValidator :source-config="sourceConfig" :target-config="targetConfig" />
        </div>

        <!-- ====== 迁移指南页面（独立） ====== -->
        <div v-else-if="activeTab === 'guide'" key="guide" class="step-content tool-step">
          <MigrationGuide :source-config="sourceConfig" />
        </div>

        <!-- ====== 连接串页面（独立） ====== -->
        <div v-else-if="activeTab === 'connstr'" key="connstr" class="step-content tool-step">
          <ConnStrings :source-config="sourceConfig" />
        </div>

          <!-- ====== Step 1: 配置连接 ====== -->
          <div v-else-if="activeTab === 'source'" key="source" class="step-content">
            <div class="connection-layout">
              <ConnectionForm
                title="源数据库"
                role="source"
                v-model="sourceConfig"
                @test="sourceTested = true"
              />
              <div class="connection-arrow">
                <div class="arrow-line"></div>
                <div class="arrow-circle">
                  <ArrowRight :size="20" />
                </div>
                <div class="arrow-line"></div>
              </div>
              <ConnectionForm
                title="目标数据库"
                role="target"
                v-model="targetConfig"
                @test="targetTested = true"
              />
            </div>

            <div class="step-footer">
              <div class="footer-hint">
                <CheckCircle2 v-if="sourceTested && targetTested" :size="16" class="text-success" />
                <span v-if="sourceTested && targetTested">两端连接正常，可以继续下一步</span>
                <span v-else>请测试两端连接后继续</span>
              </div>
              <button
                class="btn btn-primary"
                @click="goToTables"
                :disabled="!sourceTested || !targetTested"
              >
                下一步
                <ArrowRight :size="16" />
              </button>
            </div>
          </div>

          <!-- ====== Step 2: 选择表 ====== -->
          <div v-else-if="activeTab === 'tables'" key="tables" class="step-content">
            <div class="tables-card card">
              <!-- Toolbar -->
              <div class="tables-toolbar">
                <div class="search-box">
                  <Search :size="15" class="search-icon" />
                  <input
                    v-model="tableSearch"
                    class="search-input"
                    placeholder="搜索表名..."
                  />
                </div>
                <div class="toolbar-actions">
                  <button class="btn btn-secondary btn-sm" @click="loadTables" :disabled="loadingTables">
                    <Loader2 v-if="loadingTables" :size="14" class="animate-spin" />
                    <Database v-else :size="14" />
                    {{ loadingTables ? '加载中' : '刷新' }}
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

              <!-- Table List -->
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
                  <span class="table-comment" v-if="t.comment">{{ t.comment }}</span>
                </label>
              </div>
              <div v-else class="empty-state">
                <Database :size="40" class="empty-icon" />
                <p v-if="tablesError" class="empty-error">{{ tablesError }}</p>
                <p v-else-if="tables.length === 0">点击"刷新"加载源数据库的表</p>
                <p v-else>没有匹配 "{{ tableSearch }}" 的表</p>
              </div>
            </div>

            <!-- Options -->
            <div class="options-card card">
              <div class="options-header">
                <Settings2 :size="16" />
                <span>迁移选项</span>
              </div>
              <div class="options-grid">
                <label class="option-item" :class="{ checked: migrationConfig.dropIfExists }">
                  <input type="checkbox" v-model="migrationConfig.dropIfExists" />
                  <div class="option-content">
                    <Trash2 :size="16" class="option-icon" />
                    <div>
                      <span class="option-label">先删后建</span>
                      <span class="option-hint">目标表存在时先删除</span>
                    </div>
                  </div>
                </label>

                <label class="option-item" :class="{ checked: migrationConfig.structureOnly }">
                  <input type="checkbox" v-model="migrationConfig.structureOnly" />
                  <div class="option-content">
                    <FileWarning :size="16" class="option-icon" />
                    <div>
                      <span class="option-label">仅结构</span>
                      <span class="option-hint">只迁移表结构不迁数据</span>
                    </div>
                  </div>
                </label>

                <label class="option-item" :class="{ checked: migrationConfig.dataOnly }">
                  <input type="checkbox" v-model="migrationConfig.dataOnly" />
                  <div class="option-content">
                    <Database :size="16" class="option-icon" />
                    <div>
                      <span class="option-label">仅数据</span>
                      <span class="option-hint">只迁数据不建表</span>
                    </div>
                  </div>
                </label>

                <label class="option-item" :class="{ checked: migrationConfig.ignoreErrors }">
                  <input type="checkbox" v-model="migrationConfig.ignoreErrors" />
                  <div class="option-content">
                    <FileWarning :size="16" class="option-icon" />
                    <div>
                      <span class="option-label">容错模式</span>
                      <span class="option-hint">单表失败不中断</span>
                    </div>
                  </div>
                </label>

                <label class="option-item" :class="{ checked: migrationConfig.backupBefore }">
                  <input type="checkbox" v-model="migrationConfig.backupBefore" />
                  <div class="option-content">
                    <Archive :size="16" class="option-icon" />
                    <div>
                      <span class="option-label">迁移前备份</span>
                      <span class="option-hint">目标同名表自动重命名</span>
                    </div>
                  </div>
                </label>

                <label class="option-item" :class="{ checked: migrationConfig.autoRollback, disabled: !migrationConfig.backupBefore }">
                  <input type="checkbox" v-model="migrationConfig.autoRollback" :disabled="!migrationConfig.backupBefore" />
                  <div class="option-content">
                    <RotateCcw :size="16" class="option-icon" />
                    <div>
                      <span class="option-label">失败自动回滚</span>
                      <span class="option-hint">迁移失败恢复备份</span>
                    </div>
                  </div>
                </label>

                <label class="option-item" :class="{ checked: migrationConfig.migrateTriggers }">
                  <input type="checkbox" v-model="migrationConfig.migrateTriggers" />
                  <div class="option-content">
                    <Zap :size="16" class="option-icon" />
                    <div>
                      <span class="option-label">迁移触发器</span>
                      <span class="option-hint">自动转换触发器语法</span>
                    </div>
                  </div>
                </label>

                <label class="option-item" :class="{ checked: migrationConfig.migrateRoutines }">
                  <input type="checkbox" v-model="migrationConfig.migrateRoutines" />
                  <div class="option-content">
                    <Code2 :size="16" class="option-icon" />
                    <div>
                      <span class="option-label">迁移存储过程</span>
                      <span class="option-hint">转换函数/过程语法</span>
                    </div>
                  </div>
                </label>

                <div class="option-item option-input">
                  <div class="option-content">
                    <Settings2 :size="16" class="option-icon" />
                    <div>
                      <span class="option-label">批量大小</span>
                      <span class="option-hint">每次写入行数</span>
                    </div>
                  </div>
                  <input class="input batch-input" type="number" v-model.number="migrationConfig.batchSize" />
                </div>
              </div>
            </div>

            <!-- Footer -->
            <div class="step-footer">
              <button class="btn btn-ghost" @click="activeTab = 'source'">
                <ArrowLeft :size="16" />
                上一步
              </button>
              <button
                class="btn btn-primary"
                @click="startMigration"
                :disabled="!canStartMigration"
              >
                <Rocket :size="16" />
                开始迁移
              </button>
            </div>
          </div>

          <!-- ====== Step 3: 执行迁移 ====== -->
          <div v-else key="migrate" class="step-content migrate-step">
            <div v-if="migrationError && !migrating" class="migration-error card">
              <FileWarning :size="16" />
              <span>迁移启动失败：{{ migrationError }}</span>
            </div>
            <ProgressPanel />
            <div v-if="report && !migrating" class="report-wrapper">
              <MigrationReport :report="report" />
            </div>
          </div>
        </Transition>
      </div>
    </main>
  </div>
</template>

<style scoped>
.app {
  display: flex;
  /* #app 是 flex 容器，根节点必须占满宽度，否则收缩为内容宽导致右侧留白 */
  width: 100%;
  height: 100vh;
  height: 100dvh;
  overflow: hidden;
}

/* ====== Sidebar ====== */
.sidebar {
  width: 260px;
  flex-shrink: 0;
  background: var(--color-sidebar);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.sidebar-brand {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 20px 20px 24px;
}

.brand-icon {
  width: 38px;
  height: 38px;
  background: linear-gradient(135deg, var(--color-primary), #8b5cf6);
  border-radius: var(--radius-md);
  display: flex;
  align-items: center;
  justify-content: center;
  color: white;
  flex-shrink: 0;
  box-shadow: 0 2px 8px rgba(59, 130, 246, 0.3);
}

.brand-text {
  display: flex;
  flex-direction: column;
}

.brand-name {
  font-size: 17px;
  font-weight: 700;
  color: var(--color-text-on-dark);
  letter-spacing: -0.02em;
}

.brand-sub {
  font-size: 11px;
  color: var(--color-text-on-dark-secondary);
  margin-top: 1px;
}

.sidebar-nav {
  flex: 1;
  padding: 8px 12px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border: none;
  background: transparent;
  cursor: pointer;
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
  position: relative;
  font-family: var(--font-sans);
  text-align: left;
  width: 100%;
}

.nav-item:hover:not(.disabled) {
  background: var(--color-sidebar-hover);
}

.nav-item.active {
  background: var(--color-sidebar-hover);
}

.nav-item.disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.nav-indicator {
  position: absolute;
  left: 0;
  top: 50%;
  transform: translateY(-50%);
  width: 3px;
  height: 24px;
  background: var(--color-primary);
  border-radius: 0 2px 2px 0;
}

.nav-icon-wrapper {
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  border-radius: var(--radius-sm);
  background: rgba(255, 255, 255, 0.06);
  transition: all var(--transition-fast);
}

.nav-item.active .nav-icon-wrapper {
  background: rgba(59, 130, 246, 0.15);
}

.nav-item.done .nav-icon-wrapper {
  background: rgba(16, 185, 129, 0.15);
}

.nav-icon {
  color: var(--color-text-on-dark-secondary);
  transition: color var(--transition-fast);
}

.nav-item.active .nav-icon {
  color: var(--color-primary);
}

.nav-icon.done {
  color: var(--color-success);
}

.nav-content {
  display: flex;
  flex-direction: column;
}

.nav-label {
  font-size: 13px;
  font-weight: 600;
  color: var(--color-text-on-dark);
}

.nav-desc {
  font-size: 11px;
  color: var(--color-text-on-dark-secondary);
  margin-top: 1px;
}

.nav-divider {
  height: 1px;
  background: rgba(255, 255, 255, 0.06);
  margin: 8px 12px;
}

.sidebar-footer {
  padding: 16px 20px;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 10px;
  background: rgba(16, 185, 129, 0.12);
  color: var(--color-success);
  border-radius: 20px;
  font-size: 11px;
  font-weight: 500;
  width: fit-content;
}

/* ====== Main Content ====== */
.main-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.top-bar {
  height: 56px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 24px;
  background: var(--color-surface);
  border-bottom: 1px solid var(--color-border);
}

.top-title {
  font-size: 16px;
  font-weight: 700;
  color: var(--color-text);
}

.migrating-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 12px;
  background: var(--color-primary-light);
  color: var(--color-primary);
  border-radius: 20px;
  font-size: 12px;
  font-weight: 600;
}

.content-area {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 24px;
  display: flex;
  justify-content: center;
}

.step-content {
  width: 100%;
  max-width: 1280px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* ====== Backup Page ====== */
.backup-step {
max-width: 860px;
}

/* ====== 独立工具页 ====== */
.tool-step {
  max-width: 960px;
}

.backup-section {
display: flex;
flex-direction: column;
gap: 8px;
}

.backup-section-title {
display: flex;
align-items: center;
gap: 6px;
font-size: 13px;
font-weight: 600;
color: var(--color-text-secondary);
text-transform: uppercase;
letter-spacing: 0.04em;
padding: 8px 4px 4px;
}

.backup-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 18px;
  font-size: 13px;
  color: var(--color-text-secondary);
  background: var(--color-info-light);
  border-color: var(--color-info);
}

/* ====== Connection Layout ====== */
.connection-layout {
  display: flex;
  align-items: stretch;
  gap: 0;
}

.connection-layout > :first-child,
.connection-layout > :last-child {
  flex: 1;
}

.connection-arrow {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 0 8px;
  flex-shrink: 0;
}

.arrow-line {
  width: 2px;
  flex: 1;
  background: linear-gradient(to bottom, transparent, var(--color-border), transparent);
}

.arrow-circle {
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: var(--color-surface);
  border: 2px solid var(--color-border);
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-secondary);
  flex-shrink: 0;
}

/* ====== Step Footer ====== */
.step-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding-top: 16px;
  margin-top: 8px;
  border-top: 1px solid var(--color-border);
}

.footer-hint {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--color-text-secondary);
}

.text-success {
  color: var(--color-success);
}

/* ====== Tables Card ====== */
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
  transition: all var(--transition-fast);
  font-family: var(--font-sans);
}

.search-input:focus {
  border-color: var(--color-primary);
  background: var(--color-surface);
  box-shadow: 0 0 0 3px rgba(59, 130, 246, 0.12);
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
  max-height: 360px;
  overflow-y: auto;
}

.table-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 16px;
  cursor: pointer;
  border-bottom: 1px solid var(--color-border-light);
  transition: all var(--transition-fast);
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

.table-row.selected .table-icon {
  color: var(--color-primary);
}

.table-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--color-text);
}

.table-comment {
  font-size: 12px;
  color: var(--color-text-tertiary);
  margin-left: auto;
}

.empty-state {
  padding: 48px 20px;
  text-align: center;
  color: var(--color-text-tertiary);
}

.empty-icon {
  color: var(--color-border);
  margin-bottom: 12px;
}

.empty-error {
  color: var(--color-danger);
  font-size: 13px;
  max-width: 480px;
  margin: 0 auto;
  word-break: break-all;
}

/* ====== Options Card ====== */
.options-card {
  padding: 16px 20px;
}

.options-header {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
  margin-bottom: 14px;
  color: var(--color-text);
}

.options-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 10px;
}

.option-item {
  display: block;
  padding: 12px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all var(--transition-fast);
  background: var(--color-surface);
}

.option-item:hover {
  border-color: #cbd5e1;
  background: var(--color-surface-hover);
}

.option-item.checked {
  border-color: var(--color-primary);
  background: var(--color-primary-light);
}

.option-item.disabled {
  opacity: 0.45;
  cursor: not-allowed;
  pointer-events: none;
}

.option-item input {
  display: none;
}

.option-content {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.option-icon {
  color: var(--color-text-tertiary);
  flex-shrink: 0;
  margin-top: 1px;
}

.option-item.checked .option-icon {
  color: var(--color-primary);
}

.option-label {
  display: block;
  font-size: 13px;
  font-weight: 600;
  color: var(--color-text);
}

.option-hint {
  display: block;
  font-size: 11px;
  color: var(--color-text-tertiary);
  margin-top: 1px;
}

.option-input {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.batch-input {
  width: 80px;
  padding: 5px 8px;
  font-size: 12px;
}

/* ====== Migrate Step ====== */
.migrate-step {
  display: flex;
  flex-direction: column;
  gap: 16px;
  /* content-area 是 row flex，默认 align-items:stretch 会把本容器锁死为一屏固定高度，
     报告出现后进度面板会被 flex 压缩（日志归零、完成横条被报告盖住）。
     改为脱离拉伸按内容增高，min-height 保底占满一屏，超出由 content-area 滚动 */
  align-self: flex-start;
  min-height: 100%;
}

.migration-error {
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

.report-wrapper {
  flex-shrink: 0;
}

/* ====== Animations ====== */
.animate-spin {
  animation: spin 1s linear infinite;
}

/* ====== 响应式：宽屏 → 平板 → 手机 ====== */

/* ≤1280px：内容区跟随窗口收缩，保持两侧留白均匀 */
@media (max-width: 1280px) {
  .content-area {
    padding: 20px;
  }

  .step-content {
    max-width: 100%;
  }
}

/* ≤1100px：双栏连接表单改为上下堆叠，箭头转为水平 */
@media (max-width: 1100px) {
  .connection-layout {
    flex-direction: column;
    gap: 12px;
  }

  .connection-arrow {
    flex-direction: row;
    padding: 4px 0;
  }

  .arrow-line {
    width: auto;
    height: 2px;
    flex: 1;
    background: linear-gradient(to right, transparent, var(--color-border), transparent);
  }
}

/* ≤1024px：侧边栏收窄为图标栏 */
@media (max-width: 1024px) {
  .sidebar {
    width: 68px;
  }

  .sidebar-brand {
    padding: 16px 0 20px;
    justify-content: center;
  }

  .brand-text {
    display: none;
  }

  .sidebar-nav {
    padding: 8px;
  }

  .nav-item {
    padding: 10px;
    justify-content: center;
  }

  .nav-content {
    display: none;
  }

  .sidebar-footer {
    padding: 12px 8px;
    align-items: center;
  }

  .status-pill span {
    display: none;
  }
}

/* ≤768px：侧边栏变为顶部导航栏，主内容占满剩余空间 */
@media (max-width: 768px) {
  .app {
    flex-direction: column;
  }

  .sidebar {
    width: 100%;
    flex-direction: row;
    align-items: center;
    min-height: 54px;
    padding: 0 12px;
    border-bottom: 1px solid rgba(255, 255, 255, 0.08);
    flex-shrink: 0;
  }

  .sidebar-brand {
    padding: 8px 0;
  }

  .brand-icon {
    width: 32px;
    height: 32px;
  }

  .brand-sub {
    display: none;
  }

  .sidebar-nav {
    flex-direction: row;
    align-items: center;
    padding: 0 0 0 8px;
    gap: 2px;
    overflow-x: auto;
  }

  .nav-item {
    padding: 8px 10px;
    width: auto;
    flex-shrink: 0;
  }

  .nav-content {
    display: none;
  }

  .nav-indicator {
    left: 50%;
    top: auto;
    bottom: 2px;
    transform: translateX(-50%);
    width: 18px;
    height: 3px;
    border-radius: 2px;
  }

  .nav-divider {
    width: 1px;
    height: 20px;
    margin: 0 6px;
    background: rgba(255, 255, 255, 0.12);
  }

  .sidebar-footer {
    display: none;
  }

  .main-content {
    min-height: 0;
  }

  .top-bar {
    padding: 0 14px;
  }

  .content-area {
    padding: 14px;
  }

  .tables-toolbar {
    flex-wrap: wrap;
  }

  .search-box {
    max-width: 100%;
    flex-basis: 100%;
  }

  .toolbar-actions {
    margin-left: 0;
  }

  .migrate-step {
    min-height: 0;
  }

  .table-list {
    max-height: 45vh;
  }
}

/* ≤560px：手机竖屏，进一步压缩密度 */
@media (max-width: 560px) {
  .options-grid {
    grid-template-columns: 1fr;
  }

  .step-footer {
    flex-wrap: wrap;
    gap: 10px;
  }

  .step-footer .btn {
    flex: 1;
  }

  .footer-hint {
    flex-basis: 100%;
  }
}
</style>
