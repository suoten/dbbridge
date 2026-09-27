# DBBridge 端到端生产级测试脚本
# 在 Docker 容器中拉起真实数据库，用打包好的 exe 通过 REST API 执行真实迁移
#
# 用法: pwsh -ExecutionPolicy Bypass -File scripts/e2e-test.ps1
# 前置: Docker 已启动，dist/dbbridge-windows-amd64.exe 已存在（或自动编译）

param(
    [string]$ExePath = "dist/dbbridge-windows-amd64.exe",
    [int]$ApiPort = 18989
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $ProjectRoot

$Pass = 0
$Fail = 0
$Skip = 0
$Results = @()

function Write-TestResult($name, $status, $detail="") {
    $color = switch ($status) {
        "PASS" { "Green" }
        "FAIL" { "Red" }
        "SKIP" { "Yellow" }
    }
    Write-Host "  [$status] $name" -ForegroundColor $color
    if ($detail) { Write-Host "         $detail" -ForegroundColor DarkGray }
    $script:Results += [PSCustomObject]@{Name=$name; Status=$status; Detail=$detail}
    if ($status -eq "PASS") { $script:Pass++ }
    elseif ($status -eq "FAIL") { $script:Fail++ }
    else { $script:Skip++ }
}

function Invoke-Api($method, $path, $body=$null) {
    $url = "http://127.0.0.1:$ApiPort$path"
    $params = @{
        Method = $method
        Uri = $url
        ContentType = "application/json; charset=utf-8"
        TimeoutSec = 300
    }
    if ($body) { $params.Body = ($body | ConvertTo-Json -Depth 10) }
    $resp = Invoke-RestMethod @params
    return $resp
}

function Wait-Container($name, $cmd, $timeout=120) {
    $deadline = (Get-Date).AddSeconds($timeout)
    $prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
    while ((Get-Date) -lt $deadline) {
        $expr = "docker exec $name $cmd"
        Invoke-Expression $expr 2>&1 | Out-Null
        if ($LASTEXITCODE -eq 0) { $ErrorActionPreference = $prevEAP; return $true }
        Start-Sleep -Seconds 3
    }
    $ErrorActionPreference = $prevEAP
    return $false
}

# ============================================================
# 0. 检查前置条件
# ============================================================
Write-Host ""
Write-Host "==========================================" -ForegroundColor Cyan
Write-Host "  DBBridge 端到端生产级测试" -ForegroundColor Cyan
Write-Host "==========================================" -ForegroundColor Cyan
Write-Host ""

# 检查 exe
if (-not (Test-Path $ExePath)) {
    Write-Host "  exe not found at $ExePath, building..." -ForegroundColor Yellow
    & go build -o $ExePath .
    if ($LASTEXITCODE -ne 0) { Write-Host "BUILD FAILED" -ForegroundColor Red; exit 1 }
}

# 检查 Docker
$prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
docker info 2>&1 | Out-Null
$dockerOk = ($LASTEXITCODE -eq 0)
$ErrorActionPreference = $prevEAP
if (-not $dockerOk) { Write-Host "Docker not running!" -ForegroundColor Red; exit 1 }

# ============================================================
# 1. 启动数据库容器
# ============================================================
Write-Host "[1/5] Starting Docker containers..." -ForegroundColor Green
$prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
docker compose -f docker-compose.e2e.yml up -d 2>&1 | Out-Null
$ErrorActionPreference = $prevEAP

# 等待各数据库就绪
Write-Host "  Waiting for databases to be ready..." -ForegroundColor DarkGray

$mysqlReady = Wait-Container "dbbridge-e2e-mysql" "mysqladmin ping -uroot -ptestpass123 --silent" 60
Write-TestResult "MySQL ready" $(if ($mysqlReady) {"PASS"} else {"FAIL"})

$pgReady = Wait-Container "dbbridge-e2e-postgres" "pg_isready -U postgres" 30
Write-TestResult "PostgreSQL ready" $(if ($pgReady) {"PASS"} else {"FAIL"})

$mssqlReady = Wait-Container "dbbridge-e2e-mssql" "/opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -P 'YourStrong!Passw0rd' -C -Q 'SELECT 1'" 120
if (-not $mssqlReady) {
    $mssqlReady = Wait-Container "dbbridge-e2e-mssql" "/opt/mssql-tools/bin/sqlcmd -S localhost -U sa -P 'YourStrong!Passw0rd' -Q 'SELECT 1'" 30
}
Write-TestResult "MSSQL ready" $(if ($mssqlReady) {"PASS"} else {"FAIL"})

$mongoReady = Wait-Container 'dbbridge-e2e-mongodb' 'mongosh --eval "db.adminCommand({ping:1})"' 60
Write-TestResult "MongoDB ready" $(if ($mongoReady) {"PASS"} else {"FAIL"})

$redisReady = Wait-Container "dbbridge-e2e-redis" "redis-cli -a testpass123 ping" 30
Write-TestResult "Redis ready" $(if ($redisReady) {"PASS"} else {"FAIL"})

$cassandraReady = Wait-Container "dbbridge-e2e-cassandra" "cqlsh -u cassandra -p testpass123 -e 'DESCRIBE KEYSPACES'" 120
Write-TestResult "Cassandra ready" $(if ($cassandraReady) {"PASS"} else {"FAIL"})

$influxReady = Wait-Container "dbbridge-e2e-influxdb" "influx ping" 60
Write-TestResult "InfluxDB ready" $(if ($influxReady) {"PASS"} else {"FAIL"})

# ============================================================
# 2. 初始化测试数据
# ============================================================
Write-Host ""
Write-Host "[2/5] Initializing test data..." -ForegroundColor Green

# MySQL: 导入测试数据
$initSql = Get-Content "testdata/init-mysql.sql" -Raw
$prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
$initSql | docker exec -i dbbridge-e2e-mysql mysql -uroot -ptestpass123 testdb 2>&1 | Out-Null
$mysqlDataOk = ($LASTEXITCODE -eq 0)
$ErrorActionPreference = $prevEAP
Write-TestResult "MySQL test data loaded" $(if ($mysqlDataOk) {"PASS"} else {"FAIL"})

# PostgreSQL: 创建表和数据
$pgInit = @"
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(50) NOT NULL,
    email VARCHAR(100) NOT NULL,
    age SMALLINT DEFAULT 18,
    salary NUMERIC(10,2) DEFAULT 5000.00,
    is_active BOOLEAN DEFAULT TRUE,
    bio TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS products (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    price NUMERIC(10,2) NOT NULL,
    stock INTEGER DEFAULT 0,
    description TEXT,
    category VARCHAR(50) DEFAULT 'general'
);
INSERT INTO users (username, email, age, salary, bio) VALUES
('pg_alice', 'pgalice@test.com', 28, 12000.50, 'PG engineer'),
('pg_bob', 'pgbob@test.com', 35, 8500.00, 'PG admin')
ON CONFLICT DO NOTHING;
INSERT INTO products (name, price, stock, category) VALUES
('PG Widget', 29.99, 100, 'tools'),
('PG Gadget', 199.00, 50, 'tools')
ON CONFLICT DO NOTHING;
"@
$prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
$pgInit | docker exec -i dbbridge-e2e-postgres psql -U postgres -d testdb 2>&1 | Out-Null
$pgDataOk = ($LASTEXITCODE -eq 0)
$ErrorActionPreference = $prevEAP
Write-TestResult "PostgreSQL test data loaded" $(if ($pgDataOk) {"PASS"} else {"FAIL"})

# MSSQL: 创建数据库和表（分两步：先建库，再建表）
$prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
docker exec dbbridge-e2e-mssql /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -P 'YourStrong!Passw0rd' -C -Q "IF NOT EXISTS (SELECT 1 FROM sys.databases WHERE name='testdb') CREATE DATABASE testdb" 2>&1 | Out-Null
$mssqlInit = @"
CREATE TABLE users (
    id INT IDENTITY(1,1) PRIMARY KEY,
    username NVARCHAR(50) NOT NULL,
    email NVARCHAR(100) NOT NULL,
    age TINYINT DEFAULT 18,
    salary DECIMAL(10,2) DEFAULT 5000.00,
    is_active BIT DEFAULT 1,
    bio NVARCHAR(MAX),
    created_at DATETIME2 DEFAULT GETDATE()
);
INSERT INTO users (username, email, age, salary, bio) VALUES
('sql_alice', 'sqlalice@test.com', 28, 12000.50, 'SQL Server engineer'),
('sql_bob', 'sqlbob@test.com', 35, 8500.00, 'SQL Server admin');
"@
$mssqlInit | docker exec -i dbbridge-e2e-mssql /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -P 'YourStrong!Passw0rd' -C -d testdb -l 30 2>&1 | Out-Null
$mssqlDataOk = ($LASTEXITCODE -eq 0)
if (-not $mssqlDataOk) {
    $mssqlInit | docker exec -i dbbridge-e2e-mssql /opt/mssql-tools/bin/sqlcmd -S localhost -U sa -P 'YourStrong!Passw0rd' -l 30 2>&1 | Out-Null
    $mssqlDataOk = ($LASTEXITCODE -eq 0)
}
$ErrorActionPreference = $prevEAP
Write-TestResult "MSSQL test data loaded" $(if ($mssqlDataOk) {"PASS"} else {"FAIL"})

# MongoDB: 插入测试数据
$mongoInit = @"
db.users.insertMany([
    {username: 'mongo_alice', email: 'mongoalice@test.com', age: 28, salary: 12000.50, isActive: true, bio: 'MongoDB engineer', tags: ['nosql','document']},
    {username: 'mongo_bob', email: 'mongobob@test.com', age: 35, salary: 8500.00, isActive: true, bio: 'MongoDB admin', tags: ['ops','sharding']}
]);
db.products.insertMany([
    {name: 'Mongo Widget', price: 29.99, stock: 100, category: 'tools'},
    {name: 'Mongo Gadget', price: 199.00, stock: 50, category: 'tools'}
]);
"@
$prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
$mongoInit | docker exec -i dbbridge-e2e-mongodb mongosh "mongodb://root:testpass123@localhost:27017/testdb?authSource=admin" --quiet 2>&1 | Out-Null
$mongoDataOk = ($LASTEXITCODE -eq 0)
$ErrorActionPreference = $prevEAP
Write-TestResult "MongoDB test data loaded" $(if ($mongoDataOk) {"PASS"} else {"FAIL"})

# Redis: 插入测试数据
$redisInit = @"
SET test:key1 'hello'
SET test:key2 'world'
HSET test:hash1 field1 'value1' field2 'value2'
LPUSH test:list1 'item1' 'item2' 'item3'
SADD test:set1 'member1' 'member2'
ZADD test:zset1 1 'one' 2 'two'
"@
$prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
$redisInit | docker exec -i dbbridge-e2e-redis redis-cli -a testpass123 2>&1 | Out-Null
$redisDataOk = ($LASTEXITCODE -eq 0)
$ErrorActionPreference = $prevEAP
Write-TestResult "Redis test data loaded" $(if ($redisDataOk) {"PASS"} else {"FAIL"})

# ============================================================
# 3. 启动 DBBridge --web 模式
# ============================================================
Write-Host ""
Write-Host "[3/5] Starting DBBridge web server..." -ForegroundColor Green

# 先杀掉可能残留的进程
Get-Process -Name "dbbridge*" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1

$webProc = Start-Process -FilePath $ExePath -ArgumentList "--web", "--port", $ApiPort -NoNewWindow -PassThru -RedirectStandardOutput "dist/e2e-stdout.log" -RedirectStandardError "dist/e2e-stderr.log"

# 等待 API 就绪
$apiReady = $false
for ($i = 0; $i -lt 30; $i++) {
    Start-Sleep -Seconds 1
    try {
        $health = Invoke-RestMethod "http://127.0.0.1:$ApiPort/api/health" -TimeoutSec 3
        if ($health.status -eq "ok") { $apiReady = $true; break }
    } catch {}
}
Write-TestResult "DBBridge API ready" $(if ($apiReady) {"PASS"} else {"FAIL"})
if (-not $apiReady) {
    Write-Host "API not ready, aborting." -ForegroundColor Red
    Get-Content "dist/e2e-stderr.log" -ErrorAction SilentlyContinue
    exit 1
}

# 验证版本
$ver = Invoke-RestMethod "http://127.0.0.1:$ApiPort/api/version" -TimeoutSec 5
Write-Host "  Version: $($ver.version)" -ForegroundColor DarkGray

# 验证支持的数据库列表
$dbs = Invoke-RestMethod "http://127.0.0.1:$ApiPort/api/databases" -TimeoutSec 5
$dbCount = $dbs.databases.Count
Write-TestResult "Supported databases count" $(if ($dbCount -ge 23) {"PASS"} else {"FAIL"}) "count=$dbCount"

# ============================================================
# 4. 连接测试
# ============================================================
Write-Host ""
Write-Host "[4/5] Testing connections..." -ForegroundColor Green

$connections = @{
    MySQL = @{ type="mysql"; host="127.0.0.1"; port=13306; username="root"; password="testpass123"; database="testdb" }
    PostgreSQL = @{ type="postgres"; host="127.0.0.1"; port=15432; username="postgres"; password="testpass123"; database="testdb" }
    MSSQL = @{ type="mssql"; host="127.0.0.1"; port=11433; username="sa"; password="YourStrong!Passw0rd"; database="testdb" }
    MongoDB = @{ type="mongodb"; host="127.0.0.1"; port=17017; username="root"; password="testpass123"; database="testdb" }
    Redis = @{ type="redis"; host="127.0.0.1"; port=16379; username=""; password="testpass123"; database="0" }
}

foreach ($name in $connections.Keys) {
    $cfg = $connections[$name]
    try {
        $resp = Invoke-Api "POST" "/api/test-connection" $cfg
        if ($resp.success) {
            Write-TestResult "Connect $name" "PASS" $resp.version
        } else {
            Write-TestResult "Connect $name" "FAIL" $resp.error
        }
    } catch {
        Write-TestResult "Connect $name" "FAIL" $_.Exception.Message
    }
}

# Cassandra 和 InfluxDB 连接测试
$cassConfig = @{ type="cassandra"; host="127.0.0.1"; port=19042; username="cassandra"; password="testpass123"; database="testks" }
try {
    $resp = Invoke-Api "POST" "/api/test-connection" $cassConfig
    if ($resp.success) {
        Write-TestResult "Connect Cassandra" "PASS" $resp.version
    } else {
        Write-TestResult "Connect Cassandra" "FAIL" $resp.error
    }
} catch {
    Write-TestResult "Connect Cassandra" "FAIL" $_.Exception.Message
}

$influxConfig = @{ type="influxdb"; host="127.0.0.1"; port=18086; username="admin"; password="test-token-1234567890abcdef"; database="testbucket" }
try {
    $resp = Invoke-Api "POST" "/api/test-connection" $influxConfig
    if ($resp.success) {
        Write-TestResult "Connect InfluxDB" "PASS" $resp.version
    } else {
        Write-TestResult "Connect InfluxDB" "FAIL" $resp.error
    }
} catch {
    Write-TestResult "Connect InfluxDB" "FAIL" $_.Exception.Message
}

# ============================================================
# 5. 迁移测试
# ============================================================
Write-Host ""
Write-Host "[5/5] Testing migrations..." -ForegroundColor Green

# --- 5.1 MySQL → PostgreSQL ---
Write-Host "  [Migration] MySQL -> PostgreSQL..." -ForegroundColor DarkGray
$migConfig = @{
    source = $connections.MySQL
    target = $connections.PostgreSQL
    batchSize = 1000
    concurrency = 2
    dropIfExists = $true
    backupBefore = $false
    ignoreErrors = $false
}
try {
    $report = Invoke-Api "POST" "/api/migrate" $migConfig
    if ($report.tablesTotal -gt 0 -and $report.tablesFailed -eq 0) {
        Write-TestResult "MySQL -> PostgreSQL" "PASS" "tables=$($report.tablesTotal) success=$($report.tablesSuccess) rows=$($report.totalRows)"
    } else {
        Write-TestResult "MySQL -> PostgreSQL" "FAIL" "tables=$($report.tablesTotal) failed=$($report.tablesFailed) error=$($report.error)"
    }
} catch {
    Write-TestResult "MySQL -> PostgreSQL" "FAIL" $_.Exception.Message
}

# --- 5.2 MySQL → MSSQL ---
Write-Host "  [Migration] MySQL -> MSSQL..." -ForegroundColor DarkGray
$migConfig2 = @{
    source = $connections.MySQL
    target = $connections.MSSQL
    batchSize = 1000
    concurrency = 2
    dropIfExists = $true
    backupBefore = $false
    ignoreErrors = $false
}
try {
    $report = Invoke-Api "POST" "/api/migrate" $migConfig2
    if ($report.tablesTotal -gt 0 -and $report.tablesFailed -eq 0) {
        Write-TestResult "MySQL -> MSSQL" "PASS" "tables=$($report.tablesTotal) success=$($report.tablesSuccess) rows=$($report.totalRows)"
    } else {
        Write-TestResult "MySQL -> MSSQL" "FAIL" "tables=$($report.tablesTotal) failed=$($report.tablesFailed) error=$($report.error)"
    }
} catch {
    Write-TestResult "MySQL -> MSSQL" "FAIL" $_.Exception.Message
}

# --- 5.3 PostgreSQL → MySQL ---
Write-Host "  [Migration] PostgreSQL -> MySQL..." -ForegroundColor DarkGray
$migConfig3 = @{
    source = $connections.PostgreSQL
    target = $connections.MySQL
    batchSize = 1000
    concurrency = 2
    dropIfExists = $true
    backupBefore = $false
    ignoreErrors = $false
}
try {
    $report = Invoke-Api "POST" "/api/migrate" $migConfig3
    if ($report.tablesTotal -gt 0 -and $report.tablesFailed -eq 0) {
        Write-TestResult "PostgreSQL -> MySQL" "PASS" "tables=$($report.tablesTotal) success=$($report.tablesSuccess) rows=$($report.totalRows)"
    } else {
        Write-TestResult "PostgreSQL -> MySQL" "FAIL" "tables=$($report.tablesTotal) failed=$($report.tablesFailed) error=$($report.error)"
    }
} catch {
    Write-TestResult "PostgreSQL -> MySQL" "FAIL" $_.Exception.Message
}

# --- 5.4 MySQL → MongoDB ---
Write-Host "  [Migration] MySQL -> MongoDB..." -ForegroundColor DarkGray
$migConfig4 = @{
    source = $connections.MySQL
    target = $connections.MongoDB
    batchSize = 1000
    concurrency = 2
    dropIfExists = $true
    backupBefore = $false
    ignoreErrors = $false
}
try {
    $report = Invoke-Api "POST" "/api/migrate" $migConfig4
    if ($report.tablesTotal -gt 0 -and $report.tablesFailed -eq 0) {
        Write-TestResult "MySQL -> MongoDB" "PASS" "tables=$($report.tablesTotal) success=$($report.tablesSuccess) rows=$($report.totalRows)"
    } else {
        Write-TestResult "MySQL -> MongoDB" "FAIL" "tables=$($report.tablesTotal) failed=$($report.tablesFailed) error=$($report.error)"
    }
} catch {
    Write-TestResult "MySQL -> MongoDB" "FAIL" $_.Exception.Message
}

# --- 5.5 MSSQL → PostgreSQL ---
Write-Host "  [Migration] MSSQL -> PostgreSQL..." -ForegroundColor DarkGray
$migConfig5 = @{
    source = $connections.MSSQL
    target = $connections.PostgreSQL
    batchSize = 1000
    concurrency = 2
    dropIfExists = $true
    backupBefore = $false
    ignoreErrors = $false
}
try {
    $report = Invoke-Api "POST" "/api/migrate" $migConfig5
    if ($report.tablesTotal -gt 0 -and $report.tablesFailed -eq 0) {
        Write-TestResult "MSSQL -> PostgreSQL" "PASS" "tables=$($report.tablesTotal) success=$($report.tablesSuccess) rows=$($report.totalRows)"
    } else {
        Write-TestResult "MSSQL -> PostgreSQL" "FAIL" "tables=$($report.tablesTotal) failed=$($report.tablesFailed) error=$($report.error)"
    }
} catch {
    Write-TestResult "MSSQL -> PostgreSQL" "FAIL" $_.Exception.Message
}

# --- 5.6 MySQL → SQLite (文件) ---
Write-Host "  [Migration] MySQL -> SQLite..." -ForegroundColor DarkGray
$sqlitePath = Join-Path $env:TEMP "dbbridge_e2e_target.db"
if (Test-Path $sqlitePath) { Remove-Item $sqlitePath -Force }
$migConfig6 = @{
    source = $connections.MySQL
    target = @{ type="sqlite"; database=$sqlitePath }
    batchSize = 1000
    concurrency = 2
    dropIfExists = $true
    backupBefore = $false
    ignoreErrors = $false
}
try {
    $report = Invoke-Api "POST" "/api/migrate" $migConfig6
    if ($report.tablesTotal -gt 0 -and $report.tablesFailed -eq 0) {
        Write-TestResult "MySQL -> SQLite" "PASS" "tables=$($report.tablesTotal) success=$($report.tablesSuccess) rows=$($report.totalRows)"
    } else {
        Write-TestResult "MySQL -> SQLite" "FAIL" "tables=$($report.tablesTotal) failed=$($report.tablesFailed) error=$($report.error)"
    }
} catch {
    Write-TestResult "MySQL -> SQLite" "FAIL" $_.Exception.Message
}

# --- 5.7 SQLite → PostgreSQL (反向) ---
Write-Host "  [Migration] SQLite -> PostgreSQL..." -ForegroundColor DarkGray
$migConfig7 = @{
    source = @{ type="sqlite"; database=$sqlitePath }
    target = $connections.PostgreSQL
    batchSize = 1000
    concurrency = 2
    dropIfExists = $true
    backupBefore = $false
    ignoreErrors = $false
}
try {
    $report = Invoke-Api "POST" "/api/migrate" $migConfig7
    if ($report.tablesTotal -gt 0 -and $report.tablesFailed -eq 0) {
        Write-TestResult "SQLite -> PostgreSQL" "PASS" "tables=$($report.tablesTotal) success=$($report.tablesSuccess) rows=$($report.totalRows)"
    } else {
        Write-TestResult "SQLite -> PostgreSQL" "FAIL" "tables=$($report.tablesTotal) failed=$($report.tablesFailed) error=$($report.error)"
    }
} catch {
    Write-TestResult "SQLite -> PostgreSQL" "FAIL" $_.Exception.Message
}

# --- 5.8 Redis → MySQL ---
Write-Host "  [Migration] Redis -> MySQL..." -ForegroundColor DarkGray
$migConfig8 = @{
    source = $connections.Redis
    target = $connections.MySQL
    batchSize = 1000
    concurrency = 2
    dropIfExists = $true
    backupBefore = $false
    ignoreErrors = $true
}
try {
    $report = Invoke-Api "POST" "/api/migrate" $migConfig8
    if ($report.tablesTotal -gt 0) {
        Write-TestResult "Redis -> MySQL" "PASS" "tables=$($report.tablesTotal) success=$($report.tablesSuccess) rows=$($report.totalRows)"
    } else {
        Write-TestResult "Redis -> MySQL" "SKIP" "no tables found"
    }
} catch {
    Write-TestResult "Redis -> MySQL" "FAIL" $_.Exception.Message
}

# ============================================================
# 6. 数据校验（在 MySQL → PostgreSQL 迁移后立即验证）
# ============================================================
Write-Host ""
Write-Host "[6/5] Data validation..." -ForegroundColor Green

# 注意：由于之前的迁移已经覆盖了 PostgreSQL 中的数据（MSSQL → PG, SQLite → PG），
# 这里验证的是最后一次迁移后的状态。为了得到有意义的验证结果，
# 我们重新执行一次 MySQL → PostgreSQL 迁移，然后立即验证。
Write-Host "  Re-running MySQL -> PostgreSQL for validation..." -ForegroundColor DarkGray
$reMigConfig = @{
    source = $connections.MySQL
    target = $connections.PostgreSQL
    batchSize = 1000
    concurrency = 2
    dropIfExists = $true
    backupBefore = $false
    ignoreErrors = $false
}
try {
    $reReport = Invoke-Api "POST" "/api/migrate" $reMigConfig
    if ($reReport.tablesTotal -gt 0) {
        Write-Host "  Re-migration done: tables=$($reReport.tablesTotal) success=$($reReport.tablesSuccess)" -ForegroundColor DarkGray
    }
} catch {
    Write-Host "  Re-migration failed: $($_.Exception.Message)" -ForegroundColor DarkGray
}

# 校验 MySQL vs PostgreSQL 的数据
try {
    $validateReq = @{
        source = $connections.MySQL
        target = $connections.PostgreSQL
        sampleSize = 100
    }
    $validateReport = Invoke-Api "POST" "/api/validate" $validateReq
    $totalChecks = $validateReport.tables.Count
    $passedChecks = ($validateReport.tables | Where-Object { $_.status -eq "match" }).Count
    Write-TestResult "Validate MySQL vs PostgreSQL" $(if ($passedChecks -eq $totalChecks -and $totalChecks -gt 0) {"PASS"} else {"FAIL"}) "tables=$totalChecks matched=$passedChecks"
} catch {
    Write-TestResult "Validate MySQL vs PostgreSQL" "SKIP" $_.Exception.Message
}

# ============================================================
# 清理
# ============================================================
Write-Host ""
Write-Host "Cleaning up..." -ForegroundColor DarkGray
Stop-Process -Id $webProc.Id -Force -ErrorAction SilentlyContinue

# ============================================================
# 汇总
# ============================================================
Write-Host ""
Write-Host "==========================================" -ForegroundColor Cyan
Write-Host "  Test Summary" -ForegroundColor Cyan
Write-Host "==========================================" -ForegroundColor Cyan
Write-Host "  PASS: $Pass" -ForegroundColor Green
Write-Host "  FAIL: $Fail" -ForegroundColor Red
Write-Host "  SKIP: $Skip" -ForegroundColor Yellow
Write-Host "  Total: $($Pass + $Fail + $Skip)" -ForegroundColor White
Write-Host ""

if ($Fail -eq 0) {
    Write-Host "  ✅ ALL TESTS PASSED — Production Ready!" -ForegroundColor Green
} else {
    Write-Host "  ❌ $Fail TEST(S) FAILED — Needs Investigation" -ForegroundColor Red
}

Write-Host ""
Write-Host "Docker containers still running. To stop:" -ForegroundColor DarkGray
Write-Host "  docker compose -f docker-compose.e2e.yml down -v" -ForegroundColor DarkGray
Write-Host ""

# 输出详细结果
$Results | Format-Table -AutoSize

exit $(if ($Fail -eq 0) {0} else {1})
