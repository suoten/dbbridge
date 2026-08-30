# DBBridge 发布打包脚本
# 用法: powershell -ExecutionPolicy Bypass -File make-release.ps1
# 用法: powershell -ExecutionPolicy Bypass -File make-release.ps1 -Version 1.0.0
# 产出: release/dbbridge-<version>-<platform>.zip (每个包包含二进制+安装脚本+配置+文档)
#
# 流程:
#   1. 构建前端 (Vue3 → dist/)
#   2. 交叉编译多平台二进制 (CGO_ENABLED=0 纯 Go，无 C 依赖)
#   3. 打包发布 zip（二进制 + install.sh + baota-proxy.sh + README.md）
#   4. 生成 SHA256 校验文件

param(
    [string]$Version = "1.0.0"
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $ProjectRoot

Write-Host ""
Write-Host "==========================================" -ForegroundColor Cyan
Write-Host "  DBBridge v$Version Release Builder" -ForegroundColor Cyan
Write-Host "  数据库迁移与SQL转换工具" -ForegroundColor Cyan
Write-Host "==========================================" -ForegroundColor Cyan
Write-Host ""

# ============================================================
# 1. 构建前端
# ============================================================
Write-Host "[1/4] Building frontend..." -ForegroundColor Green
Push-Location frontend

# 安装依赖（如果 node_modules 不存在）
if (-not (Test-Path "node_modules")) {
    Write-Host "  Installing npm dependencies..." -ForegroundColor DarkGray
    $npmInstall = Start-Process -FilePath "cmd.exe" -ArgumentList "/c npm install --legacy-peer-deps" -NoNewWindow -Wait -PassThru -RedirectStandardError "NUL"
    if ($npmInstall.ExitCode -ne 0) {
        # 重试不带 legacy-peer-deps
        Start-Process -FilePath "cmd.exe" -ArgumentList "/c npm install" -NoNewWindow -Wait -RedirectStandardError "NUL" | Out-Null
    }
}

# 构建前端
Write-Host "  Building Vue3 frontend..." -ForegroundColor DarkGray
$viteBuild = Start-Process -FilePath "cmd.exe" -ArgumentList "/c npx vite build" -NoNewWindow -Wait -PassThru -RedirectStandardError "NUL"
Pop-Location

if (-not (Test-Path "frontend/dist/index.html")) {
    # vite build 可能只输出了到 frontend/dist，也可能是 vue-tsc 报错
    # 尝试只跑 vite build（跳过 vue-tsc 类型检查）
    Write-Host "  Retrying with vite build only (skip type check)..." -ForegroundColor DarkGray
    Push-Location frontend
    Start-Process -FilePath "cmd.exe" -ArgumentList "/c npx vite build" -NoNewWindow -Wait -RedirectStandardError "NUL" | Out-Null
    Pop-Location
}

if (-not (Test-Path "frontend/dist/index.html")) {
    Write-Host "ERROR: Frontend build failed - frontend/dist/index.html not found" -ForegroundColor Red
    Write-Host "  Try running manually: cd frontend && npm run build" -ForegroundColor Yellow
    exit 1
}
Write-Host "  Frontend built OK" -ForegroundColor DarkGray

# ============================================================
# 2. 交叉编译多平台二进制
#    DBBridge 使用 modernc.org/sqlite（纯 Go），无需 CGO
# ============================================================
Write-Host "[2/4] Cross-compiling binaries (CGO_ENABLED=0)..." -ForegroundColor Green

$platforms = @(
    @{ GOOS="linux";   GOARCH="amd64" },
    @{ GOOS="linux";   GOARCH="arm64" },
    @{ GOOS="windows"; GOARCH="amd64" }
)

$distDir = "dist"
if (Test-Path $distDir) { Remove-Item -Recurse -Force $distDir -ErrorAction SilentlyContinue }
New-Item -ItemType Directory -Path $distDir -Force | Out-Null

# 构建版本信息
$buildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$gitCommit = "unknown"
try {
    $gitCommit = (git rev-parse --short HEAD 2>$null).Trim()
} catch {}

$ldflags = "-s -w -X main.Version=$Version -X main.BuildTime=$buildTime -X main.GitCommit=$gitCommit"

$binaries = @{}
foreach ($p in $platforms) {
    $env:GOOS = $p.GOOS
    $env:GOARCH = $p.GOARCH
    $env:CGO_ENABLED = "0"
    $env:GOPROXY = "https://goproxy.cn,direct"

    $ext = ""
    if ($p.GOOS -eq "windows") { $ext = ".exe" }

    $key = "dbbridge-$($p.GOOS)-$($p.GOARCH)$ext"
    $out = "$distDir/$key"

    Write-Host "  Building $($p.GOOS)/$($p.GOARCH)..." -ForegroundColor DarkGray

    # Go 会把诊断信息输出到 stderr，临时降低 ErrorActionPreference
    $prevEAP = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    & go build -ldflags="$ldflags" -o $out . 2>&1 | Out-Null
    $buildExit = $LASTEXITCODE
    $ErrorActionPreference = $prevEAP

    if ($buildExit -ne 0 -or -not (Test-Path $out)) {
        Write-Host "ERROR: Build failed for $key (exit $buildExit)" -ForegroundColor Red
        exit 1
    }

    $binaries[$key] = $out
    $sizeMB = [math]::Round((Get-Item $out).Length / 1MB, 1)
    Write-Host "    -> $key ($sizeMB MB)" -ForegroundColor Yellow
}

# 清理环境变量
$env:GOOS = ""
$env:GOARCH = ""
$env:CGO_ENABLED = ""
Write-Host "  All binaries built OK" -ForegroundColor DarkGray

# ============================================================
# 3. 打包发布 zip
# ============================================================
Write-Host "[3/4] Preparing release packages..." -ForegroundColor Green

$releaseDir = "release"
if (Test-Path $releaseDir) { Remove-Item -Recurse -Force $releaseDir -ErrorAction SilentlyContinue }
New-Item -ItemType Directory -Path $releaseDir -Force | Out-Null

# 公共文件（每个包都包含）
$commonFiles = @(
    @{ Src="deploy/install.sh";          Dst="install.sh" },
    @{ Src="deploy/uninstall.sh";        Dst="uninstall.sh" },
    @{ Src="deploy/baota-proxy.sh";      Dst="baota-proxy.sh" },
    @{ Src="deploy/baota-nginx.conf";    Dst="baota-nginx.conf" },
    @{ Src="README.md";                   Dst="README.md" }
)

foreach ($p in $platforms) {
    $tag = "$($p.GOOS)-$($p.GOARCH)"
    $ext = ""
    if ($p.GOOS -eq "windows") { $ext = ".exe" }

    $pkgDir = "release/dbbridge-$tag-pkg"
    New-Item -ItemType Directory -Path $pkgDir -Force | Out-Null

    # 复制二进制
    $binKey = "dbbridge-$tag$ext"
    Copy-Item $binaries[$binKey] "$pkgDir/dbbridge$ext"

    # 复制公共文件
    foreach ($f in $commonFiles) {
        if (Test-Path $f.Src) {
            Copy-Item $f.Src "$pkgDir/$($f.Dst)"
        }
    }

    # Windows 包额外包含 NSIS 安装程序说明
    if ($p.GOOS -eq "windows") {
        # 如果有 build/windows 目录的图标等资源，也打包进去
        if (Test-Path "build/appicon.png") {
            Copy-Item "build/appicon.png" "$pkgDir/"
        }
    }

    # 打 zip 包
    $zipball = "release/dbbridge-$Version-$tag.zip"
    Write-Host "  Creating $zipball..." -ForegroundColor DarkGray
    Compress-Archive -Path "$pkgDir/*" -DestinationPath $zipball -Force

    $size = [math]::Round((Get-Item $zipball).Length / 1MB, 1)
    Write-Host "    -> $zipball ($size MB)" -ForegroundColor Yellow

    # 清理临时目录
    if (Test-Path $pkgDir) {
        Remove-Item -Recurse -Force $pkgDir -ErrorAction SilentlyContinue
    }
}

# ============================================================
# 4. 生成 SHA256 校验文件
# ============================================================
Write-Host "[4/4] Generating checksums..." -ForegroundColor Green
Push-Location $releaseDir
$hashes = Get-ChildItem -Filter "*.zip" | ForEach-Object {
    $hash = (Get-FileHash $_.Name -Algorithm SHA256).Hash
    "$hash  $($_.Name)"
}
$hashes | Out-File -Encoding ASCII -FilePath "checksums.txt"
Pop-Location

# ============================================================
# 输出结果
# ============================================================
Write-Host ""
Write-Host "==========================================" -ForegroundColor Green
Write-Host "  Release packages created:" -ForegroundColor Green
Write-Host "==========================================" -ForegroundColor Green
Get-ChildItem $releaseDir | ForEach-Object {
    $size = if ($_.Length -gt 1MB) { "$([math]::Round($_.Length/1MB,1)) MB" } else { "$([math]::Round($_.Length/1KB,0)) KB" }
    Write-Host "  release/$($_.Name)  ($size)" -ForegroundColor White
}
Write-Host ""
Write-Host "Each package contains:" -ForegroundColor Cyan
Write-Host "  dbbridge                <- binary (数据库迁移工具)" -ForegroundColor DarkGray
Write-Host "  install.sh              <- one-click installer" -ForegroundColor DarkGray
Write-Host "  uninstall.sh             <- uninstaller" -ForegroundColor DarkGray
Write-Host "  baota-proxy.sh           <- 宝塔反向代理配置脚本" -ForegroundColor DarkGray
Write-Host "  baota-nginx.conf         <- 宝塔 Nginx 反向代理模板" -ForegroundColor DarkGray
Write-Host "  README.md               <- full documentation" -ForegroundColor DarkGray
Write-Host ""
Write-Host "Linux deploy:" -ForegroundColor Yellow
Write-Host "  1. Upload zip to server" -ForegroundColor Yellow
Write-Host "  2. unzip dbbridge-$Version-linux-amd64.zip" -ForegroundColor Yellow
Write-Host "  3. sudo bash install.sh" -ForegroundColor Yellow
Write-Host ""
Write-Host "  或者一行命令安装:" -ForegroundColor Yellow
Write-Host "  curl -fsSL <your-download-url>/install.sh | sudo bash" -ForegroundColor Yellow
Write-Host ""
