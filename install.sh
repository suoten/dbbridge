#!/usr/bin/env bash
# ============================================================================
#  DBBridge 一键安装部署脚本
#  数据库迁移与SQL转换工具
#  GitHub: https://github.com/suoten/dbbridge
# ============================================================================
set -euo pipefail

# ============================ 颜色定义 ============================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# ============================ 全局变量 ============================
APP_NAME="DBBridge"
APP_USER="dbbridge"
APP_DIR="/opt/dbbridge"
APP_BIN="${APP_DIR}/dbbridge"
PID_FILE="/var/run/dbbridge.pid"
LOG_DIR="/var/log/dbbridge"
LOG_FILE="${LOG_DIR}/dbbridge.log"
WORK_DIR="/opt/dbbridge"
DEFAULT_PORT=8989
WEB_PORT="${DBBRIDGE_PORT:-$DEFAULT_PORT}"
REPO_URL="${REPO_URL:-}"
BRANCH="${BRANCH:-master}"

# 下载地址（GitHub Release），可被环境变量覆盖
DOWNLOAD_URL="${DOWNLOAD_URL:-}"

# ============================ 工具函数 ============================
print_banner() {
    echo -e "${CYAN}"
    cat << 'EOF'
  ____  ____  ____  ___  _____ _____
 |  _ \|  _ \|  _ \|   \| ____|_   _|
 | | | | |_) | | | | |) |  _|   | |
 | |_| |  __/| |_| | _ <| |___  | |
 |____/|_|   |____/|_| \_\_____| |_|

    数据迁移，一键搞定
    让数据库之间的数据流通变得简单
EOF
    echo -e "${NC}"
}

info()    { echo -e "${GREEN}[信息]${NC} $*"; }
warn()    { echo -e "${YELLOW}[警告]${NC} $*"; }
error()   { echo -e "${RED}[错误]${NC} $*" >&2; }
fatal()   { echo -e "${RED}[致命]${NC} $*" >&2; exit 1; }
step()    { echo -e "\n${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"; echo -e "${BOLD}  步骤 $1: $2${NC}"; echo -e "${BLUE}  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"; }

check_root() {
    if [[ $EUID -ne 0 ]]; then
        error "此脚本需要 root 权限运行，请使用 sudo 或切换到 root 用户"
        error "  用法: sudo bash install.sh"
        exit 1
    fi
}

# 检测包管理器
detect_pkg_manager() {
    if command -v apt-get &>/dev/null; then
        echo "apt-get"
    elif command -v yum &>/dev/null; then
        echo "yum"
    elif command -v dnf &>/dev/null; then
        echo "dnf"
    elif command -v zypper &>/dev/null; then
        echo "zypper"
    elif command -v apk &>/dev/null; then
        echo "apk"
    else
        echo ""
    fi
}

# 安装系统依赖
install_dependencies() {
    local pm
    pm=$(detect_pkg_manager)
    if [[ -z "$pm" ]]; then
        fatal "无法识别包管理器，请手动安装 Go 1.21+ 和 Node.js 18+"
    fi
    info "检测到包管理器: $pm"

    local pkgs="curl wget git"
    info "安装基础依赖: $pkgs ..."
    case "$pm" in
        apt-get)
            export DEBIAN_FRONTEND=noninteractive
            apt-get update -qq
            apt-get install -y -qq $pkgs >/dev/null 2>&1 || true
            ;;
        yum|dnf)
            $pm install -y -q $pkgs >/dev/null 2>&1 || true
            ;;
        zypper)
            zypper --non-interactive --quiet refresh
            zypper --non-interactive --quiet install $pkgs 2>/dev/null || true
            ;;
        apk)
            apk add --no-cache $pkgs >/dev/null 2>&1 || true
            ;;
    esac
    info "基础依赖安装完成"
}

# 检查或安装 Go
ensure_go() {
    if command -v go &>/dev/null; then
        local ver
        ver=$(go version 2>/dev/null | grep -oP 'go\K[0-9]+\.[0-9]+')
        info "已安装 Go，版本: go${ver}"
        return 0
    fi
    warn "未检测到 Go，正在安装 Go 1.21..."
    local go_ver="1.21.6"
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64)  arch="amd64" ;;
        aarch64) arch="arm64" ;;
        *) fatal "不支持的架构: $arch" ;;
    esac
    local url="https://golang.google.cn/dl/go${go_ver}.linux-${arch}.tar.gz"
    info "下载 Go: $url"
    wget -q -O /tmp/go.tar.gz "$url"
    rm -rf /usr/local/go
    tar -C /usr/local -xzf /tmp/go.tar.gz
    rm -f /tmp/go.tar.gz
    export PATH=$PATH:/usr/local/go/bin
    # 写入 profile
    grep -q '/usr/local/go/bin' /etc/profile || echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
    info "Go 安装完成: $(go version)"
}

# 检查或安装 Node.js
ensure_node() {
    if command -v node &>/dev/null; then
        local ver
        ver=$(node --version 2>/dev/null)
        info "已安装 Node.js，版本: $ver"
        return 0
    fi
    warn "未检测到 Node.js，正在安装 Node.js 20.x..."
    local pm
    pm=$(detect_pkg_manager)
    case "$pm" in
        apt-get)
            curl -fsSL https://deb.nodesource.com/setup_20.x | bash - >/dev/null 2>&1
            apt-get install -y -qq nodejs >/dev/null 2>&1
            ;;
        yum|dnf)
            curl -fsSL https://rpm.nodesource.com/setup_20.x | bash - >/dev/null 2>&1
            $pm install -y -q nodejs >/dev/null 2>&1
            ;;
        *)
            warn "无法自动安装 Node.js，请手动安装后重试"
            fatal "参考: https://nodejs.org/en/download/package-manager/"
            ;;
    esac
    info "Node.js 安装完成: $(node --version)"
}

# 检查或安装 Wails CLI
ensure_wails() {
    if command -v wails &>/dev/null; then
        info "已安装 Wails CLI: $(wails version 2>/dev/null || echo 'installed')"
        return 0
    fi
    warn "未检测到 Wails CLI，正在安装..."
    export GOPROXY=https://goproxy.cn,direct
    go install github.com/wailsapp/wails/v2/cmd/wails@v2.10.1 2>/dev/null || \
        go install github.com/wailsapp/wails/v2/cmd/wails@latest 2>/dev/null
    export PATH=$PATH:$(go env GOPATH)/bin
    grep -q '$(go env GOPATH)/bin' /etc/profile || \
        echo 'export PATH=$PATH:$(go env GOPATH)/bin' >> /etc/profile 2>/dev/null || true
    info "Wails CLI 安装完成"
}

# 创建专用用户
create_user() {
    if id "$APP_USER" &>/dev/null; then
        info "用户 $APP_USER 已存在"
    else
        info "创建系统用户: $APP_USER"
        useradd --system --no-create-home --shell /usr/sbin/nologin "$APP_USER"
    fi
}

# 从源码编译构建
build_from_source() {
    local src_dir="/tmp/dbbridge-build"
    if [[ -n "$REPO_URL" ]]; then
        info "从 Git 仓库克隆源码: $REPO_URL (分支: $BRANCH)"
        rm -rf "$src_dir"
        git clone --depth 1 -b "$BRANCH" "$REPO_URL" "$src_dir" 2>/dev/null || \
            git clone --depth 1 "$REPO_URL" "$src_dir"
    elif [[ -d "$(pwd)/go.mod" ]]; then
        info "使用当前目录源码"
        src_dir="$(pwd)"
    else
        fatal "未找到源码。请设置 REPO_URL 环境变量或在项目根目录运行此脚本\n  示例: REPO_URL=https://github.com/suoten/dbbridge sudo bash install.sh"
    fi

    cd "$src_dir"
    info "安装前端依赖..."
    cd frontend && npm install --legacy-peer-deps 2>/dev/null || npm install 2>/dev/null || true
    cd "$src_dir"

    info "构建前端资源..."
    cd frontend && npm run build 2>/dev/null || true
    cd "$src_dir"

    info "编译 Go 后端（headless 模式）..."
    export GOPROXY=https://goproxy.cn,direct
    # 在 Linux 服务器上以 headless 模式编译（无 GUI 依赖）
    CGO_ENABLED=1 wails build -tags webserver -ldflags "-s -w" 2>/dev/null || \
        CGO_ENABLED=1 go build -tags webserver -ldflags "-s -w" -o "dbbridge" . 2>/dev/null || \
        CGO_ENABLED=0 go build -ldflags "-s -w" -o "dbbridge" .

    if [[ ! -f "$src_dir/build/bin/DBBridge" && ! -f "$src_dir/dbbridge" ]]; then
        fatal "构建失败，请检查日志"
    fi

    local built_bin
    if [[ -f "$src_dir/build/bin/DBBridge" ]]; then
        built_bin="$src_dir/build/bin/DBBridge"
    else
        built_bin="$src_dir/dbbridge"
    fi

    info "构建成功: $built_bin"

    # 安装到目标目录
    mkdir -p "$APP_DIR"
    cp "$built_bin" "$APP_BIN"
    chmod +x "$APP_BIN"
    info "二进制已安装到 $APP_BIN"

    # 复制前端静态资源（如果存在）
    if [[ -d "$src_dir/frontend/dist" ]]; then
        mkdir -p "$APP_DIR/web"
        cp -r "$src_dir/frontend/dist/"* "$APP_DIR/web/"
        info "前端资源已部署到 $APP_DIR/web/"
    fi
}

# 下载预编译二进制
download_binary() {
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64)  arch="amd64" ;;
        aarch64) arch="arm64" ;;
        *) fatal "不支持的架构: $arch" ;;
    esac

    if [[ -z "$DOWNLOAD_URL" ]]; then
        fatal "未设置下载地址。请设置 DOWNLOAD_URL 环境变量\n  示例: DOWNLOAD_URL=https://github.com/suoten/dbbridge/releases/download/v1.0.0/dbbridge-linux-amd64 sudo bash install.sh"
    fi

    info "下载 DBBridge 二进制: $DOWNLOAD_URL"
    mkdir -p "$APP_DIR"
    wget -q -O "$APP_BIN" "$DOWNLOAD_URL"
    chmod +x "$APP_BIN"
    info "二进制已安装到 $APP_BIN"
}

# 写入 systemd 服务
setup_systemd() {
    info "配置 systemd 服务..."

    local unit_file="/etc/systemd/system/dbbridge.service"
    cat > "$unit_file" << EOF
[Unit]
Description=DBBridge - 数据库迁移与SQL转换工具
Documentation=https://github.com/suoten/dbbridge
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${APP_USER}
Group=${APP_USER}
WorkingDirectory=${WORK_DIR}
ExecStart=${APP_BIN} --web --port ${WEB_PORT}
ExecStop=/bin/kill -TERM \$MAINPID
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

# 日志
StandardOutput=append:${LOG_FILE}
StandardError=append:${LOG_FILE}
SyslogIdentifier=dbbridge

# 安全加固
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

    mkdir -p "$LOG_DIR"
    chown -R "$APP_USER":"$APP_USER" "$LOG_DIR" "$APP_DIR" "$WORK_DIR"

    systemctl daemon-reload
    systemctl enable dbbridge
    info "systemd 服务已配置（开机自启已启用）"
}

# 启动服务
start_service() {
    info "启动 DBBridge 服务..."
    systemctl start dbbridge 2>/dev/null || {
        warn "systemd 启动失败，尝试直接运行..."
        nohup "$APP_BIN" --web --port "$WEB_PORT" >> "$LOG_FILE" 2>&1 &
        echo $! > "$PID_FILE"
    }
    sleep 2
    if systemctl is-active --quiet dbbridge 2>/dev/null || [[ -f "$PID_FILE" ]]; then
        info "DBBridge 服务已启动"
    else
        warn "服务可能未正常启动，请检查日志: $LOG_FILE"
    fi
}

# 配置防火墙
setup_firewall() {
    info "配置防火墙..."
    if command -v ufw &>/dev/null; then
        ufw allow ${WEB_PORT}/tcp >/dev/null 2>&1 || true
        info "ufw: 已放行端口 ${WEB_PORT}/tcp"
    elif command -v firewall-cmd &>/dev/null; then
        firewall-cmd --permanent --add-port=${WEB_PORT}/tcp >/dev/null 2>&1 || true
        firewall-cmd --reload >/dev/null 2>&1 || true
        info "firewalld: 已放行端口 ${WEB_PORT}/tcp"
    else
        warn "未检测到防火墙工具，请手动放行端口 ${WEB_PORT}/tcp"
    fi
}

# 打印安装结果
print_result() {
    local server_ip
    server_ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    [[ -z "$server_ip" ]] && server_ip="服务器IP"

    echo
    echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${GREEN}  ${BOLD}✅ DBBridge 安装部署完成！${NC}"
    echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo
    echo -e "  ${BOLD}访问地址:${NC}  http://${server_ip}:${WEB_PORT}"
    echo -e "  ${BOLD}安装目录:${NC}  ${APP_DIR}"
    echo -e "  ${BOLD}配置文件:${NC}  ${APP_DIR}/config.yaml"
    echo -e "  ${BOLD}日志文件:${NC}  ${LOG_FILE}"
    echo -e "  ${BOLD}服务端口:${NC}  ${WEB_PORT}"
    echo
    echo -e "  ${BOLD}常用命令:${NC}"
    echo -e "    systemctl start dbbridge      # 启动服务"
    echo -e "    systemctl stop dbbridge       # 停止服务"
    echo -e "    systemctl restart dbbridge    # 重启服务"
    echo -e "    systemctl status dbbridge     # 查看状态"
    echo -e "    journalctl -u dbbridge -f     # 查看日志"
    echo
    echo -e "  ${BOLD}宝塔面板反向代理:${NC}"
    echo -e "    1. 宝塔面板 → 网站 → 添加站点（绑定域名）"
    echo -e "    2. 站点设置 → 反向代理 → 添加反向代理"
    echo -e "    3. 代理名称: DBBridge"
    echo -e "    4. 目标URL:  http://127.0.0.1:${WEB_PORT}"
    echo -e "    5. 发送域名: \$host"
    echo
    echo -e "${CYAN}  数据迁移，一键搞定。开始你的数据库迁移之旅吧！${NC}"
    echo
}

# ============================ 主流程 ============================
main() {
    print_banner
    check_root

    step 1 "环境检查与依赖安装"
    install_dependencies
    ensure_go
    ensure_node
    ensure_wails

    step 2 "创建系统用户"
    create_user

    step 3 "编译构建 DBBridge"
    if [[ -n "$DOWNLOAD_URL" ]]; then
        download_binary
    else
        build_from_source
    fi

    step 4 "配置 systemd 服务"
    setup_systemd

    step 5 "配置防火墙"
    setup_firewall

    step 6 "启动服务"
    start_service

    print_result
}

# 命令行参数处理
case "${1:-}" in
    --uninstall|-u)
        check_root
        echo -e "${YELLOW}卸载 DBBridge...${NC}"
        systemctl stop dbbridge 2>/dev/null || true
        systemctl disable dbbridge 2>/dev/null || true
        rm -f /etc/systemd/system/dbbridge.service
        systemctl daemon-reload 2>/dev/null
        rm -rf "$APP_DIR" "$LOG_DIR"
        rm -f "$PID_FILE"
        userdel "$APP_USER" 2>/dev/null || true
        echo -e "${GREEN}DBBridge 已卸载${NC}"
        ;;
    --help|-h)
        echo "DBBridge 一键安装部署脚本"
        echo ""
        echo "用法:"
        echo "  sudo bash install.sh            # 从当前目录源码编译安装"
        echo "  sudo bash install.sh -u         # 卸载 DBBridge"
        echo ""
        echo "环境变量:"
        echo "  DOWNLOAD_URL=xxx  下载预编译二进制（跳过编译）"
        echo "  REPO_URL=xxx      从 Git 仓库克隆源码编译"
        echo "  BRANCH=xxx        指定 Git 分支（默认 master）"
        echo "  DBBRIDGE_PORT=xxx 指定服务端口（默认 8989）"
        ;;
    *)
        main "$@"
        ;;
esac
