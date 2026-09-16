# DBBridge 发布打包脚本
# 用法: powershell -ExecutionPolicy Bypass -File make-release.ps1
# 用法: powershell -ExecutionPolicy Bypass -File make-release.ps1 -Version 1.3.0
# 产出: release/dbbridge-<version>-<platform>.zip (每个包包含二进制+安装脚本+配置+文档)
#
# 流程:
#   1. 构建前端 (Vue3 → dist/)
#   2. 交叉编译多平台二进制 (CGO_ENABLED=0 纯 Go，无 C 依赖)
#   3. 打包发布 zip（二进制 + install.sh + baota-proxy.sh + README.md）
#   4. 生成 SHA256 校验文件

param(
    [string]$Version = "2.0.0"
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
    $npmInstall = Start-Process -FilePath "cmd.exe" -ArgumentList "/c npm install --legacy-peer-deps" -NoNewWindow -Wait -PassThru
    if ($npmInstall.ExitCode -ne 0) {
        # 重试不带 legacy-peer-deps，且必须检查退出码，
        # 否则依赖装不上后面 vite build 必然报隐晦错误
        Write-Host "  Retrying npm install without legacy-peer-deps..." -ForegroundColor DarkGray
        $npmInstall = Start-Process -FilePath "cmd.exe" -ArgumentList "/c npm install" -NoNewWindow -Wait -PassThru
        if ($npmInstall.ExitCode -ne 0) {
            Write-Host "ERROR: npm install failed (exit $($npmInstall.ExitCode))" -ForegroundColor Red
            exit 1
        }
    }
}

# 构建前端（必须检查退出码，否则失败后继续编译会打进旧 dist）
Write-Host "  Building Vue3 frontend..." -ForegroundColor DarkGray
$viteBuild = Start-Process -FilePath "cmd.exe" -ArgumentList "/c npx vite build" -NoNewWindow -Wait -PassThru
if ($viteBuild.ExitCode -ne 0 -or -not (Test-Path "dist/index.html")) {
    # vite build 可能只输出了到 dist（相对于 frontend 目录），也可能是 vue-tsc 报错
    Write-Host "  First vite build failed (exit $($viteBuild.ExitCode)), retrying..." -ForegroundColor DarkGray
    Start-Sleep -Seconds 1
    $viteBuild = Start-Process -FilePath "cmd.exe" -ArgumentList "/c npx vite build" -NoNewWindow -Wait -PassThru
}

if (-not (Test-Path "dist/index.html")) {
    Write-Host "ERROR: Frontend build failed - dist/index.html not found" -ForegroundColor Red
    Write-Host "  Try running manually: cd frontend && npm run build" -ForegroundColor Yellow
    Pop-Location
    exit 1
}
Pop-Location
Write-Host "  Frontend built OK" -ForegroundColor DarkGray

# ============================================================
# 2. 编译多平台二进制
#    - Linux: CGO_ENABLED=0 纯 Go 交叉编译（headless Web 模式）
#    - Windows: wails build（生成完整桌面应用，含 WebView2 前端资源）
#    DBBridge 使用 modernc.org/sqlite（纯 Go），Linux 无需 CGO
# ============================================================
Write-Host "[2/4] Building multi-platform binaries..." -ForegroundColor Green

$distDir = "dist"
# 不做递归删除清理（构建产物按固定文件名覆盖，残留不影响打包正确性）
New-Item -ItemType Directory -Path $distDir -Force | Out-Null

# 构建版本信息
$buildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$gitCommit = "unknown"
try {
    $gitCommit = (git rev-parse --short HEAD 2>$null).Trim()
} catch {}

$ldflags = "-s -w -X main.Version=$Version -X main.BuildTime=$buildTime -X main.GitCommit=$gitCommit"
$binaries = @{}

# --- Linux amd64 ---
$env:GOOS = "linux"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"; $env:GOPROXY = "https://goproxy.cn,direct"
$out = "$distDir/dbbridge-linux-amd64"
Write-Host "  Building linux/amd64..." -ForegroundColor DarkGray
$prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
& go build -ldflags="$ldflags" -o $out . 2>&1 | Out-Null
$buildExit = $LASTEXITCODE; $ErrorActionPreference = $prevEAP
if ($buildExit -ne 0 -or -not (Test-Path $out)) { Write-Host "ERROR: Build failed for linux-amd64 (exit $buildExit)" -ForegroundColor Red; exit 1 }
$binaries["dbbridge-linux-amd64"] = $out
Write-Host "    -> dbbridge-linux-amd64 ($([math]::Round((Get-Item $out).Length / 1MB, 1)) MB)" -ForegroundColor Yellow

# --- Linux arm64 ---
$env:GOOS = "linux"; $env:GOARCH = "arm64"; $env:CGO_ENABLED = "0"; $env:GOPROXY = "https://goproxy.cn,direct"
$out = "$distDir/dbbridge-linux-arm64"
Write-Host "  Building linux/arm64..." -ForegroundColor DarkGray
$prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
& go build -ldflags="$ldflags" -o $out . 2>&1 | Out-Null
$buildExit = $LASTEXITCODE; $ErrorActionPreference = $prevEAP
if ($buildExit -ne 0 -or -not (Test-Path $out)) { Write-Host "ERROR: Build failed for linux-arm64 (exit $buildExit)" -ForegroundColor Red; exit 1 }
$binaries["dbbridge-linux-arm64"] = $out
Write-Host "    -> dbbridge-linux-arm64 ($([math]::Round((Get-Item $out).Length / 1MB, 1)) MB)" -ForegroundColor Yellow

# 清理 Linux 交叉编译环境变量
$env:GOOS = ""; $env:GOARCH = ""; $env:CGO_ENABLED = ""

# --- Windows amd64 (wails build) ---
Write-Host "  Building windows/amd64 (wails build)..." -ForegroundColor DarkGray
$env:GOPROXY = "https://goproxy.cn,direct"

# wails CLI 可能安装在 $GOPATH/bin 但不在系统 PATH 中，先尝试补上
$goBin = "$(go env GOPATH)\bin"
if ((Test-Path "$goBin\wails.exe") -and ($env:PATH -notlike "*$goBin*")) {
    $env:PATH = "$goBin;$env:PATH"
    Write-Host "  Added $goBin to PATH for wails CLI" -ForegroundColor DarkGray
}

$prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
# wails build 会输出到 build/bin/DBBridge.exe
& wails build -platform windows/amd64 -ldflags $ldflags 2>&1 | Out-Null
$buildExit = $LASTEXITCODE; $ErrorActionPreference = $prevEAP
$wailsExe = "build/bin/DBBridge.exe"
if ($buildExit -ne 0 -or -not (Test-Path $wailsExe)) {
    # wails build 失败，回退到 go build
    # 注意：go build 的 exe 可以用 --web 模式（headless）正常运行，
    # 但 Wails 桌面 GUI 模式可能缺少运行时初始化
    Write-Host "  wails build failed, falling back to go build..." -ForegroundColor Yellow
    Write-Host "  WARNING: go build exe may not work as Wails desktop GUI." -ForegroundColor Yellow
    Write-Host "           Please install wails CLI: go install github.com/wailsapp/wails/v2/cmd/wails@latest" -ForegroundColor Yellow
    $env:GOOS = "windows"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
    $out = "$distDir/dbbridge-windows-amd64.exe"
    # 删除旧产物，确保不残留
    if (Test-Path $out) { Remove-Item $out -Force }
    $prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
    & go build -a -ldflags="$ldflags" -o $out . 2>&1 | Out-Null
    $buildExit = $LASTEXITCODE; $ErrorActionPreference = $prevEAP
    $env:GOOS = ""; $env:GOARCH = ""; $env:CGO_ENABLED = ""
    if ($buildExit -ne 0 -or -not (Test-Path $out)) { Write-Host "ERROR: Build failed for windows-amd64 (exit $buildExit)" -ForegroundColor Red; exit 1 }
    $binaries["dbbridge-windows-amd64.exe"] = $out
} else {
    # 复制到 dist 目录
    $out = "$distDir/dbbridge-windows-amd64.exe"
    Copy-Item $wailsExe $out -Force
    $binaries["dbbridge-windows-amd64.exe"] = $out
}
Write-Host "    -> dbbridge-windows-amd64.exe ($([math]::Round((Get-Item $out).Length / 1MB, 1)) MB)" -ForegroundColor Yellow
Write-Host "  All binaries built OK" -ForegroundColor DarkGray

# ============================================================
# 3. 打包发布 zip
# ============================================================
Write-Host "[3/4] Preparing release packages..." -ForegroundColor Green

$releaseDir = "release"
# 不做递归删除清理；打包时 zip 以 -Force 覆盖，旧 pkg 目录不残留到 zip 内
New-Item -ItemType Directory -Path $releaseDir -Force | Out-Null

# Linux 公共文件（install.sh 等仅 Linux 包需要）
$linuxFiles = @(
    @{ Src="deploy/install.sh";          Dst="install.sh" },
    @{ Src="deploy/uninstall.sh";        Dst="uninstall.sh" },
    @{ Src="deploy/baota-proxy.sh";      Dst="baota-proxy.sh" },
    @{ Src="deploy/baota-nginx.conf";    Dst="baota-nginx.conf" }
)

# 打包函数
function Package-Zip($tag, $binKey, $isWindows, $extraFiles) {
    $ext = if ($isWindows) { ".exe" } else { "" }
    $pkgDir = "release/dbbridge-$tag-pkg"
    New-Item -ItemType Directory -Path $pkgDir -Force | Out-Null

    # 复制二进制
    Copy-Item $binaries[$binKey] "$pkgDir/dbbridge$ext"

    # 复制 README
    if (Test-Path "README.md") { Copy-Item "README.md" "$pkgDir/README.md" }

    # 复制额外文件
    if ($extraFiles) {
        foreach ($f in $extraFiles) {
            if (Test-Path $f.Src) { Copy-Item $f.Src "$pkgDir/$($f.Dst)" }
        }
    }

    # Windows 包额外包含图标
    if ($isWindows -and (Test-Path "build/appicon.png")) {
        Copy-Item "build/appicon.png" "$pkgDir/"
    }

    # 打 zip 包
    $zipball = "release/dbbridge-$Version-$tag.zip"
    Write-Host "  Creating $zipball..." -ForegroundColor DarkGray
    Compress-Archive -Path "$pkgDir/*" -DestinationPath $zipball -Force

    $size = [math]::Round((Get-Item $zipball).Length / 1MB, 1)
    Write-Host "    -> $zipball ($size MB)" -ForegroundColor Yellow

    # 不递归删除 pkg 目录（安全策略禁止）；输出列表只列 zip，不受残留影响
}

# 打包 Linux amd64
Package-Zip -tag "linux-amd64" -binKey "dbbridge-linux-amd64" -isWindows $false -extraFiles $linuxFiles

# 打包 Linux arm64
Package-Zip -tag "linux-arm64" -binKey "dbbridge-linux-arm64" -isWindows $false -extraFiles $linuxFiles

# 打包 Windows amd64（不包含 Linux 脚本）
Package-Zip -tag "windows-amd64" -binKey "dbbridge-windows-amd64.exe" -isWindows $true -extraFiles $null

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
Get-ChildItem $releaseDir -Filter "*.zip" | ForEach-Object {
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
