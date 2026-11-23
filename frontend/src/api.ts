// api.ts — Wails 桌面 / Web 双模式 API 桥接层
//
// 桌面模式（Wails）：调用 window.go.main.App 绑定与 window.runtime 事件推送；
// Web 模式（--web 部署，浏览器直接访问）：改走 /api/* REST 端点，
// 进度/日志通过轮询 /api/progress 替代 Wails 事件。
//
// 两种模式返回的数据形状保持一致，组件无感知；
// Web 端点与绑定层形状有差异的地方在本层内做对齐。

export interface ConnectionConfig {
  type: string
  host: string
  port: number
  username: string
  password: string
  database: string
  sslMode?: string
  charset?: string
  instance?: string
}

export interface TestConnectionResult {
  success: boolean
  version?: string
  error?: string
}

export interface TableMeta {
  name: string
  comment?: string
}

export interface GetTablesResult {
  success: boolean
  tables?: TableMeta[]
  error?: string
}

export interface SimpleResult {
  success: boolean
  error?: string
}

export interface BackupItem {
  backupName: string
  originalName: string
}

export interface GetBackupTablesResult {
  success: boolean
  tables?: BackupItem[]
  error?: string
}

export interface RestoreRequest {
  connection: ConnectionConfig
  backupName?: string
  backupNames?: string[]
}

export interface RestoreTableResult {
  success: boolean
  backupName?: string
  original?: string
  error?: string
}

export interface RestoreAllResult {
  successCount: number
  failedCount: number
  failedItems?: string[]
}

export interface MigrationRecord {
  id: number
  startTime: string
  endTime: string
  duration: string
  sourceType: string
  sourceHost: string
  sourceDB: string
  targetType: string
  targetHost: string
  targetDB: string
  tablesTotal: number
  tablesSuccess: number
  tablesFailed: number
  totalRows: number
  status: string
  tableDetails?: any[]
  backups?: any[]
  createdAt: string
}

// 进度/日志载荷与后端 types.ProgressInfo / types.LogEntry 的 JSON 字段一致
export interface ProgressInfo {
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

export interface LogEntry {
  time: string
  level: string
  table?: string
  message: string
}

// ====================================================================
// 模式检测与 HTTP 工具
// ====================================================================

// 惰性检测：桌面模式由 Wails 注入 window.go.main.App，Web 模式下不存在
function wailsApp(): any {
  return (window as any).go?.main?.App ?? null
}

export function isWebMode(): boolean {
  return wailsApp() === null
}

// 非 2xx 时按服务端约定 {success:false, error} 抛错，保持组件既有 catch 分支可用
async function postJSON<T>(url: string, body?: unknown): Promise<T> {
  const resp = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  let data: any = null
  try {
    data = await resp.json()
  } catch {
    // 正文不是 JSON 时忽略，下面统一按状态码抛错
  }
  if (!resp.ok) {
    throw new Error(data?.error || `请求失败 (HTTP ${resp.status})`)
  }
  return data as T
}

async function getJSON<T>(url: string): Promise<T> {
  const resp = await fetch(url)
  let data: any = null
  try {
    data = await resp.json()
  } catch {
    // 同上
  }
  if (!resp.ok) {
    throw new Error(data?.error || `请求失败 (HTTP ${resp.status})`)
  }
  return data as T
}

// ====================================================================
// 连接与元数据
// ====================================================================

export async function TestConnection(config: ConnectionConfig): Promise<TestConnectionResult> {
  const app = wailsApp()
  if (app) return app.TestConnection(config)
  return postJSON('/api/test-connection', config)
}

export async function GetTables(config: ConnectionConfig): Promise<GetTablesResult> {
  const app = wailsApp()
  if (app) return app.GetTables(config)
  return postJSON('/api/tables', config)
}

export async function GetTableSchema(
  config: ConnectionConfig,
  tableName: string
): Promise<{ success: boolean; schema?: any; error?: string }> {
  const app = wailsApp()
  if (app) return app.GetTableSchema(config, tableName)
  return postJSON('/api/schema', { config, table: tableName })
}

// 绑定层返回裸数组，Web 端点包装为 {databases}，这里统一成裸数组
export async function GetSupportedDatabases(): Promise<string[]> {
  const app = wailsApp()
  if (app) return app.GetSupportedDatabases()
  const data = await getJSON<{ databases: string[] }>('/api/databases')
  return data.databases || []
}

// ====================================================================
// 迁移
// ====================================================================

// 两种模式都是阻塞调用：resolve 时迁移已结束，返回完整 MigrationReport；
// 进度通过 onMigrationProgress / onMigrationLog 获取
export async function StartMigration(req: any): Promise<any> {
  const app = wailsApp()
  if (app) return app.StartMigration(req)
  return postJSON('/api/migrate', req)
}

export async function CancelMigration(): Promise<boolean> {
  const app = wailsApp()
  if (app) return app.CancelMigration()
  const data = await postJSON<{ cancelled: boolean }>('/api/cancel')
  return !!data.cancelled
}

// ====================================================================
// 迁移历史（Web 端点为本版新增）
// ====================================================================

export async function GetMigrationHistory(): Promise<MigrationRecord[]> {
  const app = wailsApp()
  if (app) return app.GetMigrationHistory()
  const records = await getJSON<MigrationRecord[]>('/api/history')
  return records || []
}

export async function DeleteMigrationHistory(id: number): Promise<SimpleResult> {
  const app = wailsApp()
  if (app) return app.DeleteMigrationHistory(id)
  return postJSON<SimpleResult>('/api/history/delete', { id })
}

// 打开日志文件所在目录（桌面专用能力；Web 模式优雅降级并说明原因）
export async function OpenInFolder(path: string): Promise<SimpleResult> {
  const app = wailsApp()
  if (app) return app.OpenInFolder(path)
  return { success: false, error: 'Web 部署模式不支持在服务器上打开文件夹，请复制路径手动查看' }
}

// ====================================================================
// 备份与回滚
// ====================================================================

// Web 端点请求体为 {config}，响应为 {backups}；对齐成绑定层的 {success, tables}
export async function GetBackupTables(config: ConnectionConfig): Promise<GetBackupTablesResult> {
  const app = wailsApp()
  if (app) return app.GetBackupTables(config)
  const data = await postJSON<{ backups?: BackupItem[] }>('/api/backups', { config })
  return { success: true, tables: data.backups || [] }
}

export async function RestoreTable(req: RestoreRequest): Promise<RestoreTableResult> {
  const app = wailsApp()
  if (app) return app.RestoreTable(req)
  const data = await postJSON<{ success: boolean; restored?: string }>('/api/backups/restore', {
    config: req.connection,
    backupName: req.backupName,
  })
  return { success: !!data.success, backupName: req.backupName, original: data.restored }
}

// 绑定层与 Web 端点响应形状一致（{successCount, failedCount, failedItems}）
export async function RestoreAllTables(req: RestoreRequest): Promise<RestoreAllResult> {
  const app = wailsApp()
  if (app) return app.RestoreAllTables(req)
  return postJSON<RestoreAllResult>('/api/backups/restore', {
    config: req.connection,
    backupNames: req.backupNames,
  })
}

export async function DeleteBackup(req: RestoreRequest): Promise<SimpleResult> {
  const app = wailsApp()
  if (app) return app.DeleteBackup(req)
  return postJSON<SimpleResult>('/api/backups/delete', {
    config: req.connection,
    backupName: req.backupName,
  })
}

// ====================================================================
// SQL 工具与报告
// ====================================================================

// 请求/响应形状两种模式一致，直接透传
export async function ConvertSQL(req: { sourceDialect: string; targetDialect: string; sql: string }): Promise<any> {
  const app = wailsApp()
  if (app) return app.ConvertSQL(req)
  return postJSON('/api/convert-sql', req)
}

export async function LintSQL(req: { sourceDialect: string; targetDialect: string; sql: string }): Promise<any> {
  const app = wailsApp()
  if (app) return app.LintSQL(req)
  return postJSON('/api/lint-sql', req)
}

export async function ValidateData(req: {
  source: ConnectionConfig
  target: ConnectionConfig
  tables?: string[]
  sampleSize?: number
}): Promise<any> {
  const app = wailsApp()
  if (app) return app.ValidateData(req)
  return postJSON('/api/validate', req)
}

// 绑定层是 3 个位置参数，Web 端点收 {config, targetDialect, tables}
export async function GenerateMigrationGuide(
  source: ConnectionConfig,
  targetDialect: string,
  tables: string[]
): Promise<any> {
  const app = wailsApp()
  if (app) return app.GenerateMigrationGuide(source, targetDialect, tables)
  return postJSON('/api/guide', { config: source, targetDialect, tables })
}

export async function CheckCompatibility(req: {
  source: ConnectionConfig
  target: ConnectionConfig
  tables?: string[]
}): Promise<any> {
  const app = wailsApp()
  if (app) return app.CheckCompatibility(req)
  return postJSON('/api/compat', req)
}

export async function GenerateConnStrings(config: ConnectionConfig): Promise<any> {
  const app = wailsApp()
  if (app) return app.GenerateConnStrings(config)
  return postJSON('/api/connstr', config)
}

// ====================================================================
// 进度/日志事件桥接
// ====================================================================

// Web 模式轮询间隔：进度快照 payload 很小，1 秒足够流畅
const PROGRESS_POLL_MS = 1000

// 订阅迁移进度。返回取消订阅函数（组件 onUnmounted 时调用）。
// 桌面模式包装 Wails 'migration:progress' 事件；
// Web 模式由共享轮询器驱动（同一时刻只有一个轮询定时器）。
export function onMigrationProgress(cb: (info: ProgressInfo) => void): () => void {
  const runtime = (window as any).runtime
  if (runtime?.EventsOn) {
    // Wails v2 的 EventsOn 返回取消监听函数
    return runtime.EventsOn('migration:progress', cb)
  }
  return subscribePoller((snap) => {
    if (snap.hasProgress && snap.progress) cb(snap.progress)
  }, null)
}

// 订阅迁移日志。语义同上，对应 Wails 'migration:log' 事件。
export function onMigrationLog(cb: (entry: LogEntry) => void): () => void {
  const runtime = (window as any).runtime
  if (runtime?.EventsOn) {
    return runtime.EventsOn('migration:log', cb)
  }
  return subscribePoller(null, (logs) => {
    for (const entry of logs) cb(entry)
  })
}

interface ProgressSnapshot {
  running: boolean
  hasProgress: boolean
  progress?: ProgressInfo
  logs?: LogEntry[]
  lastSeq: number
}

// Web 模式共享轮询器状态：进度订阅与日志订阅共用一个定时器，
// 引用计数归零时停止，重新订阅时从头拉取
const poller = {
  refCount: 0,
  timer: null as ReturnType<typeof setInterval> | null,
  sinceSeq: 0,
  snapSubs: new Set<(snap: ProgressSnapshot) => void>(),
  logSubs: new Set<(logs: LogEntry[]) => void>(),
}

async function pollOnce(): Promise<void> {
  try {
    const snap = await getJSON<ProgressSnapshot>(`/api/progress?sinceSeq=${poller.sinceSeq}`)
    poller.sinceSeq = snap.lastSeq ?? poller.sinceSeq
    poller.snapSubs.forEach((cb) => cb(snap))
    if (snap.logs?.length) poller.logSubs.forEach((cb) => cb(snap.logs as LogEntry[]))
  } catch {
    // 轮询失败（网络抖动/服务重启）静默跳过，下个周期重试
  }
}

function acquirePoller(): void {
  poller.refCount++
  if (poller.timer === null) {
    poller.timer = setInterval(pollOnce, PROGRESS_POLL_MS)
    void pollOnce() // 立即拉一次，进度面板无需等待首个周期
  }
}

function releasePoller(): void {
  poller.refCount--
  if (poller.refCount <= 0 && poller.timer !== null) {
    clearInterval(poller.timer)
    poller.timer = null
    poller.sinceSeq = 0
    poller.snapSubs.clear()
    poller.logSubs.clear()
  }
}

function subscribePoller(
  onSnap: ((snap: ProgressSnapshot) => void) | null,
  onLogs: ((logs: LogEntry[]) => void) | null
): () => void {
  if (onSnap) poller.snapSubs.add(onSnap)
  if (onLogs) poller.logSubs.add(onLogs)
  acquirePoller()
  return () => {
    if (onSnap) poller.snapSubs.delete(onSnap)
    if (onLogs) poller.logSubs.delete(onLogs)
    releasePoller()
  }
}
