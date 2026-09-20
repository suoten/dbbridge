# integration-env.ps1 — 启动真实数据库容器（集成测试环境）
# 用法: pwsh -File scripts/integration-env.ps1 [-Down]
param([switch]$Down)

if ($Down) {
    docker rm -f dbbridge-it-mysql dbbridge-it-pg dbbridge-it-mssql dbbridge-it-oracle 2>$null | Out-Null
    Write-Host "集成测试容器已移除"
    return
}

# MySQL 8（root/root123，预建 dbbridge 库）
docker rm -f dbbridge-it-mysql 2>$null | Out-Null
docker run -d --name dbbridge-it-mysql `
    -e MYSQL_ROOT_PASSWORD=root123 `
    -e MYSQL_DATABASE=dbbridge `
    -p 3307:3306 mysql:8.0

# PostgreSQL 16（postgres/postgres123，预建 /pgts 目录供 TABLESPACE 测试）
docker rm -f dbbridge-it-pg 2>$null | Out-Null
docker run -d --name dbbridge-it-pg `
    -e POSTGRES_PASSWORD=postgres123 `
    -p 5433:5432 postgres:16-alpine
docker exec dbbridge-it-pg sh -c "mkdir -p /pgts && chown postgres:postgres /pgts && chmod 700 /pgts"

# SQL Server 2022（sa/YourStrong!Passw0rd）
docker rm -f dbbridge-it-mssql 2>$null | Out-Null
docker run -d --name dbbridge-it-mssql `
    -e ACCEPT_EULA=Y `
    -e MSSQL_SA_PASSWORD=YourStrong!Passw0rd `
    -e MSSQL_MEMORY_LIMIT_MB=2048 `
    -p 14333:1433 mcr.microsoft.com/mssql/server:2022-latest

# Oracle Free 23ai（system/Oracle123，服务名 FREEPDB1；首次启动需 1-2 分钟）
docker rm -f dbbridge-it-oracle 2>$null | Out-Null
docker run -d --name dbbridge-it-oracle `
    -e ORACLE_PASSWORD=Oracle123 `
    -p 15321:1521 gvenzl/oracle-free:23.9-slim-faststart

Write-Host "容器已启动：等待就绪..."
$deadline = (Get-Date).AddSeconds(180)
$oraReady = $false
do {
    Start-Sleep -Seconds 3
    $mysqlReady = docker exec dbbridge-it-mysql mysqladmin ping -uroot -proot123 --silent 2>$null
    $pgReady = docker exec dbbridge-it-pg pg_isready -U postgres 2>$null
    if (-not $oraReady) {
        $oraReady = (docker logs dbbridge-it-oracle 2>&1 | Select-String "DATABASE IS READY TO USE" -Quiet)
    }
    Write-Host "MySQL: $(if ($mysqlReady) {'ready'} else {'waiting'})  PG: $(if ($pgReady) {'ready'} else {'waiting'})  Oracle: $(if ($oraReady) {'ready'} else {'waiting'})"
} while (((Get-Date) -lt $deadline) -and (-not ($mysqlReady -and $pgReady -and $oraReady)))
Write-Host "MySQL=3307 PostgreSQL=5433 MSSQL=14333 Oracle=15321（MSSQL/Oracle 启动较慢，日志确认就绪后可用）"
