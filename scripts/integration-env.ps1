# integration-env.ps1 — 启动真实数据库容器（集成测试环境）
# 用法: pwsh -File scripts/integration-env.ps1 [-Down]
param([switch]$Down)

if ($Down) {
    docker rm -f dbbridge-it-mysql dbbridge-it-pg dbbridge-it-mssql 2>$null | Out-Null
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

Write-Host "容器已启动：等待就绪..."
$deadline = (Get-Date).AddSeconds(120)
do {
    Start-Sleep -Seconds 3
    $mysqlReady = docker exec dbbridge-it-mysql mysqladmin ping -uroot -proot123 --silent 2>$null
    $pgReady = docker exec dbbridge-it-pg pg_isready -U postgres 2>$null
    Write-Host "MySQL: $(if ($mysqlReady) {'ready'} else {'waiting'})  PG: $(if ($pgReady) {'ready'} else {'waiting'})"
} while (((Get-Date) -lt $deadline) -and (-not ($mysqlReady -and $pgReady)))
Write-Host "MySQL=3307 PostgreSQL=5433 MSSQL=14333（MSSQL 启动较慢，约 30-60 秒后可用）"
