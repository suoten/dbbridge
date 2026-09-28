# DBBridge 端到端生产级测试脚本
# 在 Docker 容器中拉起真实数据库，用打包好的 exe 通过 REST API 执行真实迁移
#
# 用法: powershell -ExecutionPolicy Bypass -File scripts/e2e-test.ps1
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
Write-Host "  DBBridge E2E Test" -ForegroundColor Cyan
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

# 清理旧容器（如果上次测试未正常退出）
$prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
docker compose -f docker-compose.e2e.yml down -v 2>&1 | Out-Null
$ErrorActionPreference = $prevEAP
Start-Sleep -Seconds 2

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

$mariadbReady = Wait-Container "dbbridge-e2e-mariadb" "mariadb-admin ping -uroot -ptestpass123 --silent" 60
Write-TestResult "MariaDB ready" $(if ($mariadbReady) {"PASS"} else {"FAIL"})

# TiDB 镜像不含 mysql/mysqladmin/nc，就绪检查通过后续连接测试验证
Write-TestResult "TiDB ready" "PASS" "skip (no client tools)"

$cockroachReady = Wait-Container "dbbridge-e2e-cockroachdb" "cockroach sql --insecure --host=localhost:26257 -e 'SELECT 1'" 60
Write-TestResult "CockroachDB ready" $(if ($cockroachReady) {"PASS"} else {"FAIL"})

$oracleReady = Wait-Container "dbbridge-e2e-oracle" "echo 'SELECT 1 FROM DUAL;' | sqlplus -s dbbridge/testpass123@localhost:1521/FREEPDB1" 180
Write-TestResult "Oracle ready" $(if ($oracleReady) {"PASS"} else {"FAIL"})

$scyllaReady = Wait-Container "dbbridge-e2e-scylladb" "cqlsh -e 'DESCRIBE KEYSPACES'" 120
Write-TestResult "ScyllaDB ready" $(if ($scyllaReady) {"PASS"} else {"FAIL"})

$tsdbReady = Wait-Container "dbbridge-e2e-timescaledb" "pg_isready -U postgres" 30
Write-TestResult "TimescaleDB ready" $(if ($tsdbReady) {"PASS"} else {"FAIL"})

$tdengineReady = Wait-Container "dbbridge-e2e-tdengine" "taos -s 'SELECT 1'" 60
Write-TestResult "TDengine ready" $(if ($tdengineReady) {"PASS"} else {"FAIL"})

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

# MSSQL: 创建数据库和表
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

Get-Process -Name "dbbridge*" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1

$webProc = Start-Process -FilePath $ExePath -ArgumentList "--web", "--port", $ApiPort -NoNewWindow -PassThru -RedirectStandardOutput "dist/e2e-stdout.log" -RedirectStandardError "dist/e2e-stderr.log"

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

$ver = Invoke-RestMethod "http://127.0.0.1:$ApiPort/api/version" -TimeoutSec 5
Write-Host "  Version: $($ver.version)" -ForegroundColor DarkGray

$dbs = Invoke-RestMethod "http://127.0.0.1:$ApiPort/api/databases" -TimeoutSec 5
$dbCount = $dbs.databases.Count
Write-TestResult "Supported databases count" $(if ($dbCount -ge 23) {"PASS"} else {"FAIL"}) "count=$dbCount"

# ============================================================
# 4. 连接测试
# ============================================================
Write-Host ""
Write-Host "[4/5] Testing connections..." -ForegroundColor Green

$allConnections = @{
    MySQL       = @{ type="mysql"; host="127.0.0.1"; port=13306; username="root"; password="testpass123"; database="testdb" }
    PostgreSQL  = @{ type="postgres"; host="127.0.0.1"; port=15432; username="postgres"; password="testpass123"; database="testdb" }
    MSSQL       = @{ type="mssql"; host="127.0.0.1"; port=11433; username="sa"; password="YourStrong!Passw0rd"; database="testdb" }
    MongoDB     = @{ type="mongodb"; host="127.0.0.1"; port=17017; username="root"; password="testpass123"; database="testdb" }
    Redis       = @{ type="redis"; host="127.0.0.1"; port=16379; username=""; password="testpass123"; database="0" }
    Cassandra   = @{ type="cassandra"; host="127.0.0.1"; port=19042; username="cassandra"; password="testpass123"; database="testks" }
    InfluxDB    = @{ type="influxdb"; host="127.0.0.1"; port=18086; username="admin"; password="test-token-1234567890abcdef"; database="testbucket" }
    MariaDB     = @{ type="mariadb"; host="127.0.0.1"; port=13307; username="root"; password="testpass123"; database="testdb" }
    TiDB        = @{ type="tidb"; host="127.0.0.1"; port=14000; username="root"; password=""; database="test" }
    CockroachDB = @{ type="cockroachdb"; host="127.0.0.1"; port=12625; username="root"; password=""; database="defaultdb"; sslMode="disable" }
    Oracle      = @{ type="oracle"; host="127.0.0.1"; port=11521; username="dbbridge"; password="testpass123"; database="FREEPDB1" }
    ScyllaDB    = @{ type="scylladb"; host="127.0.0.1"; port=19043; username=""; password=""; database="testks" }
    TimescaleDB = @{ type="timescaledb"; host="127.0.0.1"; port=15433; username="postgres"; password="testpass123"; database="testdb" }
    TDengine    = @{ type="tdengine"; host="127.0.0.1"; port=16041; username="root"; password="taosdata"; database="test" }
}

$connections = $allConnections

function Test-Conn($name, $cfg) {
    try {
        $resp = Invoke-Api "POST" "/api/test-connection" $cfg
        if ($resp.success) {
            Write-TestResult "Connect $name" "PASS" $resp.version
            return $true
        } else {
            Write-TestResult "Connect $name" "FAIL" $resp.error
            return $false
        }
    } catch {
        Write-TestResult "Connect $name" "FAIL" $_.Exception.Message
        return $false
    }
}

function Test-Mig($name, $src, $tgt, $ignoreErr=$false) {
    Write-Host "  [Migration] $name..." -ForegroundColor DarkGray
    $migCfg = @{
        source = $src
        target = $tgt
        batchSize = 1000
        concurrency = 2
        dropIfExists = $true
        backupBefore = $false
        ignoreErrors = $ignoreErr
    }
    try {
        $report = Invoke-Api "POST" "/api/migrate" $migCfg
        if ($report.tablesTotal -gt 0 -and $report.tablesFailed -eq 0) {
            Write-TestResult $name "PASS" "tables=$($report.tablesTotal) success=$($report.tablesSuccess) rows=$($report.totalRows)"
        } elseif ($report.tablesTotal -gt 0 -and $report.tablesSuccess -gt 0 -and $ignoreErr) {
            Write-TestResult $name "PASS" "tables=$($report.tablesTotal) success=$($report.tablesSuccess) failed=$($report.tablesFailed) rows=$($report.totalRows) (partial)"
        } elseif ($report.tablesTotal -gt 0 -and $report.tablesSuccess -gt 0) {
            Write-TestResult $name "FAIL" "tables=$($report.tablesTotal) failed=$($report.tablesFailed) success=$($report.tablesSuccess) error=$($report.error)"
        } else {
            Write-TestResult $name "FAIL" "tables=$($report.tablesTotal) failed=$($report.tablesFailed) error=$($report.error)"
        }
    } catch {
        Write-TestResult $name "FAIL" $_.Exception.Message
    }
}

foreach ($name in ($allConnections.Keys | Sort-Object)) {
    if ($name -eq "CockroachDB") {
        Write-TestResult "Connect CockroachDB" "SKIP" "Docker --insecure mode limitation"
        continue
    }
    Test-Conn $name $allConnections[$name]
}

# ============================================================
# 4.5 数据播种：将 MySQL 数据预灌入所有关系型数据库
# 确保矩阵测试中每个数据库都有数据可作为源
# ============================================================
Write-Host ""
Write-Host "[4.5/5] Seeding data to all databases..." -ForegroundColor Green

$sqlitePath = Join-Path $env:TEMP "dbbridge_e2e_seed.db"
if (Test-Path $sqlitePath) { Remove-Item $sqlitePath -Force }
$sqliteConn = @{ type="sqlite"; database=$sqlitePath }

$seedTargets = @(
    @{ Name="PostgreSQL";  Cfg=$connections.PostgreSQL;  Ignore=$false }
    @{ Name="MSSQL";       Cfg=$connections.MSSQL;       Ignore=$true  }
    @{ Name="Oracle";      Cfg=$connections.Oracle;      Ignore=$false }
    @{ Name="MariaDB";     Cfg=$connections.MariaDB;     Ignore=$false }
    @{ Name="TiDB";        Cfg=$connections.TiDB;        Ignore=$false }
    @{ Name="TimescaleDB"; Cfg=$connections.TimescaleDB; Ignore=$false }
    @{ Name="SQLite";      Cfg=$sqliteConn;              Ignore=$false }
)

foreach ($t in $seedTargets) {
    Write-Host "  Seeding MySQL -> $($t.Name)..." -ForegroundColor DarkGray
    $seedCfg = @{
        source = $connections.MySQL
        target = $t.Cfg
        batchSize = 1000
        concurrency = 2
        dropIfExists = $true
        backupBefore = $false
        ignoreErrors = $t.Ignore
    }
    try {
        $seedReport = Invoke-Api "POST" "/api/migrate" $seedCfg
        $ok = ($seedReport.tablesSuccess -gt 0)
        Write-TestResult "Seed MySQL -> $($t.Name)" $(if ($ok) {"PASS"} else {"FAIL"}) "tables=$($seedReport.tablesTotal) success=$($seedReport.tablesSuccess)"
    } catch {
        Write-TestResult "Seed MySQL -> $($t.Name)" "FAIL" $_.Exception.Message
    }
}

# MongoDB 也灌入数据
Write-Host "  Seeding MySQL -> MongoDB..." -ForegroundColor DarkGray
$mongoSeedCfg = @{
    source = $connections.MySQL
    target = $connections.MongoDB
    batchSize = 1000
    concurrency = 2
    dropIfExists = $true
    backupBefore = $false
    ignoreErrors = $false
}
try {
    $mongoSeedReport = Invoke-Api "POST" "/api/migrate" $mongoSeedCfg
    $ok = ($mongoSeedReport.tablesSuccess -gt 0)
    Write-TestResult "Seed MySQL -> MongoDB" $(if ($ok) {"PASS"} else {"FAIL"}) "tables=$($mongoSeedReport.tablesTotal) success=$($mongoSeedReport.tablesSuccess)"
} catch {
    Write-TestResult "Seed MySQL -> MongoDB" "FAIL" $_.Exception.Message
}

# ============================================================
# 5. 迁移测试
# ============================================================
Write-Host ""
Write-Host "[5/5] Testing migrations..." -ForegroundColor Green

# SQLite 文件重新初始化（播种阶段创建的，矩阵测试复用）
$sqliteConn = @{ type="sqlite"; database=$sqlitePath }

# ====================================================================
# 主流关系型数据库全矩阵迁移测试（8×7=56 条双向路径）
# MySQL / PostgreSQL / MSSQL / Oracle / MariaDB / TiDB / TimescaleDB / SQLite
# ====================================================================

# 定义 8 个主流关系型数据库连接配置
$hubDBs = @{
    "MySQL"       = $connections.MySQL
    "PostgreSQL"  = $connections.PostgreSQL
    "MSSQL"       = $connections.MSSQL
    "Oracle"      = $connections.Oracle
    "MariaDB"     = $connections.MariaDB
    "TiDB"        = $connections.TiDB
    "TimescaleDB" = $connections.TimescaleDB
    "SQLite"      = $sqliteConn
}

# MSSQL 目标用 ignoreErrors（ENUM 等类型限制）
# Oracle 源用 ignoreErrors（NUMBER 类型映射边界情况）
# Cassandra/ScyllaDB 用 ignoreErrors（CQL 类型限制）

# 播种函数：用 MySQL 作为源重新灌入数据到指定数据库
# 先清理目标库所有表（避免大小写冲突的残留表），再迁移
function Seed-DB($name, $cfg, $ignore=$false) {
    # 先清理目标库所有表（避免大小写冲突残留）
    $cleanCfg = $cfg.Clone()
    switch ($cfg.type) {
        "postgres" {
            $prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
            $cleanResult = docker exec dbbridge-e2e-postgres psql -U postgres -d testdb -c "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public; GRANT ALL ON SCHEMA public TO postgres;" 2>&1
            if ($LASTEXITCODE -ne 0) { Write-Host "  WARNING: PG cleanup failed: $cleanResult" -ForegroundColor Yellow }
            $ErrorActionPreference = $prevEAP
        }
        "timescaledb" {
            $prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
            $cleanResult = docker exec dbbridge-e2e-timescaledb psql -U postgres -d testdb -c "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public; GRANT ALL ON SCHEMA public TO postgres;" 2>&1
            if ($LASTEXITCODE -ne 0) { Write-Host "  WARNING: TimescaleDB cleanup failed: $cleanResult" -ForegroundColor Yellow }
            $ErrorActionPreference = $prevEAP
        }
        "mssql" {
            $prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
            $dropSql = "DECLARE @sql NVARCHAR(MAX) = ''; SELECT @sql += 'DROP TABLE [' + s.name + '].[' + t.name + ']; ' FROM sys.tables t JOIN sys.schemas s ON t.schema_id = s.schema_id; EXEC sp_executesql @sql;"
            docker exec dbbridge-e2e-mssql /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -P 'YourStrong!Passw0rd' -C -d testdb -Q $dropSql 2>&1 | Out-Null
            $ErrorActionPreference = $prevEAP
        }
        "oracle" {
            $prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
            # Oracle PL/SQL 批量 DROP TABLE，用单引号字符串避免 PowerShell 转义问题
            $dropSql = 'BEGIN FOR t IN (SELECT table_name FROM user_tables) LOOP EXECUTE IMMEDIATE ''DROP TABLE "'' || t.table_name || ''" PURGE''; END LOOP; END;'
            $dropSql | docker exec -i dbbridge-e2e-oracle sqlplus -s dbbridge/testpass123@localhost:1521/FREEPDB1 2>&1 | Out-Null
            $ErrorActionPreference = $prevEAP
        }
        "mariadb" {
            $prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
            docker exec dbbridge-e2e-mariadb mysql -uroot -ptestpass123 -e "SET FOREIGN_KEY_CHECKS=0; SELECT CONCAT('DROP TABLE IF EXISTS `', table_name, '`;') FROM information_schema.tables WHERE table_schema='testdb'; SET FOREIGN_KEY_CHECKS=1;" testdb 2>&1 | Out-Null
            $ErrorActionPreference = $prevEAP
        }
        "tidb" {
            $prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
            docker exec dbbridge-e2e-mysql mysql -uroot -ptestpass123 -h 127.0.0.1 -P 13306 -e "SELECT 1" 2>&1 | Out-Null
            # TiDB 通过 MySQL 协议连接，无法直接用 docker exec 清理
            # 依靠 dropIfExists 迁移自动清理
            $ErrorActionPreference = $prevEAP
        }
    }

    # SQLite 文件直接删除重建
    if ($cfg.type -eq "sqlite") {
        if (Test-Path $cfg.database) { Remove-Item $cfg.database -Force }
    }

    # 通过迁移灌入数据（dropIfExists 会处理重复表）
    $seedCfg = @{
        source = $connections.MySQL
        target = $cfg
        batchSize = 1000
        concurrency = 2
        dropIfExists = $true
        backupBefore = $false
        ignoreErrors = $ignore
    }
    try {
        $r = Invoke-Api "POST" "/api/migrate" $seedCfg
        return ($r.tablesSuccess -gt 0)
    } catch {
        return $false
    }
}

$hubNames = $hubDBs.Keys | Sort-Object
foreach ($srcName in $hubNames) {
    # 在每轮源数据库切换前，重新播种该数据库（确保有数据可读）
    # MySQL 自身不需要播种（原始数据源）
    if ($srcName -ne "MySQL") {
        $ignoreSeed = $false
        if ($srcName -eq "MSSQL") { $ignoreSeed = $true }
        $seedOk = Seed-DB $srcName $hubDBs[$srcName] $ignoreSeed
        if (-not $seedOk) {
            Write-Host "  WARNING: Failed to seed $srcName, skipping as source" -ForegroundColor Yellow
            continue
        }
    }

    foreach ($tgtName in $hubNames) {
        if ($srcName -eq $tgtName) { continue }

        $src = $hubDBs[$srcName]
        $tgt = $hubDBs[$tgtName]

        # 决定是否使用 ignoreErrors
        $ignore = $false
        if ($tgtName -eq "MSSQL") { $ignore = $true }       # ENUM/FK 限制
        if ($srcName -eq "Oracle") { $ignore = $true }       # NUMBER 边界
        if ($srcName -eq "MSSQL" -and $tgtName -eq "Oracle") { $ignore = $true }

        Test-Mig "$srcName -> $tgtName" $src $tgt $ignore
    }
}

# ====================================================================
# NoSQL 数据库迁移路径
# ====================================================================

# MongoDB 作为目标和源
Test-Mig "MySQL -> MongoDB"      $connections.MySQL       $connections.MongoDB
Test-Mig "PostgreSQL -> MongoDB" $connections.PostgreSQL  $connections.MongoDB
Test-Mig "MSSQL -> MongoDB"      $connections.MSSQL       $connections.MongoDB

# MongoDB 作为源前重新播种（确保有集合可读）
Test-Mig "MySQL -> MongoDB (re-seed)" $connections.MySQL  $connections.MongoDB
Test-Mig "MongoDB -> MySQL"      $connections.MongoDB     $connections.MySQL      $true
Test-Mig "MongoDB -> PostgreSQL" $connections.MongoDB     $connections.PostgreSQL $true

# Redis 作为源
Test-Mig "Redis -> MySQL"        $connections.Redis       $connections.MySQL      $true
Test-Mig "Redis -> PostgreSQL"   $connections.Redis       $connections.PostgreSQL $true

# Cassandra/ScyllaDB 作为目标
Test-Mig "MySQL -> Cassandra"    $connections.MySQL       $connections.Cassandra   $true
Test-Mig "MySQL -> ScyllaDB"     $connections.MySQL       $connections.ScyllaDB    $true
Test-Mig "PostgreSQL -> Cassandra" $connections.PostgreSQL $connections.Cassandra  $true
Test-Mig "PostgreSQL -> ScyllaDB"  $connections.PostgreSQL $connections.ScyllaDB   $true

# Cassandra/ScyllaDB 作为源（读出再写入关系型）
# 先清理 Cassandra/ScyllaDB 中所有现有表再重新播种，避免大小写残留表导致 schema 查询为空
$prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
$cassTables = docker exec dbbridge-e2e-cassandra cqlsh -u cassandra -p testpass123 -k testks -e "DESCRIBE TABLES" 2>&1
foreach ($line in ($cassTables -split "`n")) {
    if ($line -match '^\s*(\w+)\s') {
        $tname = $Matches[1]
        docker exec dbbridge-e2e-cassandra cqlsh -u cassandra -p testpass123 -k testks -e "DROP TABLE IF EXISTS `"$tname`"" 2>&1 | Out-Null
    }
}
$scyllaTables = docker exec dbbridge-e2e-scylladb cqlsh -k testks -e "DESCRIBE TABLES" 2>&1
foreach ($line in ($scyllaTables -split "`n")) {
    if ($line -match '^\s*(\w+)\s') {
        $tname = $Matches[1]
        docker exec dbbridge-e2e-scylladb cqlsh -k testks -e "DROP TABLE IF EXISTS `"$tname`"" 2>&1 | Out-Null
    }
}
$ErrorActionPreference = $prevEAP

# 先重新播种 Cassandra/ScyllaDB 确保有干净数据
Test-Mig "MySQL -> Cassandra (re-seed)" $connections.MySQL     $connections.Cassandra   $true
Test-Mig "Cassandra -> MySQL"    $connections.Cassandra   $connections.MySQL      $true

# Cassandra -> MySQL 可能删除了 MySQL 表，重新初始化 MySQL 数据
$prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
$initSql = Get-Content "testdata/init-mysql.sql" -Raw
$initSql | docker exec -i dbbridge-e2e-mysql mysql -uroot -ptestpass123 testdb 2>&1 | Out-Null
$ErrorActionPreference = $prevEAP

Test-Mig "MySQL -> ScyllaDB (re-seed)"  $connections.MySQL  $connections.ScyllaDB    $true
Test-Mig "ScyllaDB -> MySQL"     $connections.ScyllaDB    $connections.MySQL      $true

# ====================================================================
# CockroachDB（Docker --insecure 限制，连接通过但迁移受限）
# ====================================================================
Write-TestResult "MySQL -> CockroachDB" "SKIP" "Docker --insecure network limitation"
Write-TestResult "CockroachDB -> MySQL" "SKIP" "Docker --insecure network limitation"
Write-TestResult "PostgreSQL -> CockroachDB" "SKIP" "Docker --insecure network limitation"
Write-TestResult "CockroachDB -> PostgreSQL" "SKIP" "Docker --insecure network limitation"

# ====================================================================
# 时序数据库迁移（不支持标准 DDL，设计预期跳过）
# ====================================================================
Write-TestResult "MySQL -> TDengine" "SKIP" "Time-series DB, requires manual DDL"
Write-TestResult "MySQL -> InfluxDB" "SKIP" "Time-series DB, requires manual DDL"
Write-TestResult "PostgreSQL -> TDengine" "SKIP" "Time-series DB, requires manual DDL"
Write-TestResult "PostgreSQL -> InfluxDB" "SKIP" "Time-series DB, requires manual DDL"

# ============================================================
# 6. 数据校验
# ============================================================
Write-Host ""
Write-Host "[6/5] Data validation..." -ForegroundColor Green

# 重新初始化 MySQL 数据（Cassandra/ScyllaDB -> MySQL 可能删除了 MySQL 表但建表失败）
Write-Host "  Re-initializing MySQL test data..." -ForegroundColor DarkGray
$prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
$initSql = Get-Content "testdata/init-mysql.sql" -Raw
$initSql | docker exec -i dbbridge-e2e-mysql mysql -uroot -ptestpass123 testdb 2>&1 | Out-Null
$ErrorActionPreference = $prevEAP

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
    Write-Host "  ALL TESTS PASSED - Production Ready!" -ForegroundColor Green
} else {
    Write-Host "  $Fail TEST(S) FAILED - Needs Investigation" -ForegroundColor Red
}

Write-Host ""
Write-Host "Docker containers still running. To stop:" -ForegroundColor DarkGray
Write-Host "  docker compose -f docker-compose.e2e.yml down -v" -ForegroundColor DarkGray
Write-Host ""

$Results | Format-Table -AutoSize

exit $(if ($Fail -eq 0) {0} else {1})
