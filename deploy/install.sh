#!/bin/bash
# ============================================================================
#  DBBridge 一键安装脚本（发布包模式）
#  数据库迁移与SQL转换工具
#  GitHub: https://github.com/suoten/dbbridge
# ============================================================================
#
# 用法:
#   curl -fsSL <url>/install.sh | sudo bash    # 管道模式
#   sudo bash install.sh                        # 本地模式（从同目录复制二进制）
#
# 本脚本从同目录或当前目录寻找 dbbridge 二进制并安装，不需要编译。
# 如需源码编译，请使用根目录的 install.sh。
#
set -e

# ============================ 颜色定义 ============================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

echo -e "${CYAN}"
echo "╔══════════════════════════════════════════════════╗"
echo "║        DBBridge 一键安装脚本                     ║"
echo "║        数据库迁移与SQL转换工具                   ║"
echo "║        数据迁移，一键搞定                        ║"
echo "╚══════════════════════════════════════════════════╝"
echo -e "${NC}"

# ============================ 全局变量 ============================
INSTALL_DIR="/opt/dbbridge"
LISTEN_PORT="${DBBRIDGE_PORT:-8989}"
LOG_DIR="/var/log/dbbridge"
DATA_DIR="/var/lib/dbbridge"

# 检查 root 权限
if [ "$EUID" -ne 0 ]; then
  echo -e "${RED}请使用 root 用户运行此脚本${NC}"
  echo -e "  sudo bash install.sh"
  exit 1
fi

# ── 宝塔面板环境检测 ──────────────────────────────────────────
BAOTA_PANEL_PATH="/www/server/panel"
IS_BAOTA=false
if [ -d "$BAOTA_PANEL_PATH" ] && [ -f "/etc/init.d/bt" ]; then
  IS_BAOTA=true
  echo -e "${YELLOW}[INFO]${NC} 检测到宝塔面板环境"
fi

# ── 检测架构 ──────────────────────────────────────────────────
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)
    ARCH_NAME="amd64"
    BINARY_NAMES=("dbbridge-linux-amd64" "dbbridge" "dbbridge-linux-x86_64")
    ;;
  aarch64)
    ARCH_NAME="arm64"
    BINARY_NAMES=("dbbridge-linux-arm64" "dbbridge" "dbbridge-linux-aarch64")
    ;;
  *)
    echo -e "${RED}不支持的架构: $ARCH${NC}"
    exit 1
    ;;
esac

# ── 端口冲突检测 ──────────────────────────────────────────────
if command -v ss &> /dev/null; then
  if ss -tlnp | grep -q ":${LISTEN_PORT} "; then
    echo -e "${YELLOW}[WARN]${NC} 端口 ${LISTEN_PORT} 已被占用"
    echo -e "  如果是旧版 DBBridge，请先卸载: sudo bash uninstall.sh"
    echo -e "  或修改端口: DBBRIDGE_PORT=8990 sudo bash install.sh"
  fi
fi

# ── 定位二进制文件 ────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
FOUND_BIN=""

echo -e "${GREEN}[1/7]${NC} 查找 DBBridge 二进制文件"

# 优先从脚本目录查找，再从当前目录查找
for search_dir in "$SCRIPT_DIR" "$(pwd)"; do
  for bin_name in "${BINARY_NAMES[@]}"; do
    if [ -f "${search_dir}/${bin_name}" ]; then
      FOUND_BIN="${search_dir}/${bin_name}"
      break 2
    fi
  done
done

if [ -z "$FOUND_BIN" ]; then
  echo -e "${RED}[错误]${NC} 未找到 DBBridge 二进制文件"
  echo -e "  请确保安装脚本和二进制文件在同一目录"
  echo -e "  当前目录: ${SCRIPT_DIR}"
  echo -e "  目录下文件:"
  ls -la "${SCRIPT_DIR}" 2>/dev/null || true
  echo ""
  echo -e "  如果你想从源码编译安装，请使用项目根目录的 install.sh"
  exit 1
fi

echo -e "  ${GREEN}找到二进制: ${FOUND_BIN}${NC}"

# ── 安装目录 ──────────────────────────────────────────────────
echo -e "${GREEN}[2/7]${NC} 安装目录: ${INSTALL_DIR}"
mkdir -p "${INSTALL_DIR}"

# ── 复制二进制文件 ────────────────────────────────────────────
echo -e "${GREEN}[3/7]${NC} 安装二进制文件"
cp "$FOUND_BIN" "${INSTALL_DIR}/dbbridge"
chmod +x "${INSTALL_DIR}/dbbridge"

# 复制宝塔反向代理脚本
if [ -f "${SCRIPT_DIR}/baota-proxy.sh" ]; then
  cp "${SCRIPT_DIR}/baota-proxy.sh" "${INSTALL_DIR}/"
  chmod +x "${INSTALL_DIR}/baota-proxy.sh"
  echo -e "  ${GREEN}✓${NC} 已安装宝塔反向代理配置脚本"
elif [ -f "baota-proxy.sh" ]; then
  cp "baota-proxy.sh" "${INSTALL_DIR}/"
  chmod +x "${INSTALL_DIR}/baota-proxy.sh"
  echo -e "  ${GREEN}✓${NC} 已安装宝塔反向代理配置脚本"
fi

# 复制 Nginx 配置模板
if [ -f "${SCRIPT_DIR}/baota-nginx.conf" ]; then
  cp "${SCRIPT_DIR}/baota-nginx.conf" "${INSTALL_DIR}/"
elif [ -f "baota-nginx.conf" ]; then
  cp "baota-nginx.conf" "${INSTALL_DIR}/"
fi

# 复制 README
if [ -f "${SCRIPT_DIR}/README.md" ]; then
  cp "${SCRIPT_DIR}/README.md" "${INSTALL_DIR}/"
elif [ -f "README.md" ]; then
  cp "README.md" "${INSTALL_DIR}/"
fi

# ── 创建数据目录 ──────────────────────────────────────────────
echo -e "${GREEN}[4/7]${NC} 创建数据目录"
mkdir -p "$DATA_DIR"
mkdir -p "$LOG_DIR"
chmod 755 "$DATA_DIR" "$LOG_DIR"

# ── 安装 systemd 服务 ─────────────────────────────────────────
echo -e "${GREEN}[5/7]${NC} 配置 systemd 服务"

# 自适应内存策略（参考 ServerGuard，按服务器实际配置设定资源上限）
TOTAL_MEM_KB=$(awk '/MemTotal/ {print $2}' /proc/meminfo 2>/dev/null || echo "0")
TOTAL_MEM_MB=$((TOTAL_MEM_KB / 1024))

if [ "$TOTAL_MEM_MB" -le 0 ]; then
  MEM_MAX="256M"
  MEM_HIGH="192M"
  GO_MEM_LIMIT="192MiB"
elif [ "$TOTAL_MEM_MB" -le 1024 ]; then
  MEM_MAX="256M"
  MEM_HIGH="192M"
  GO_MEM_LIMIT="192MiB"
elif [ "$TOTAL_MEM_MB" -le 2048 ]; then
  MEM_MAX="384M"
  MEM_HIGH="256M"
  GO_MEM_LIMIT="256MiB"
else
  MEM_MAX="512M"
  MEM_HIGH="384M"
  GO_MEM_LIMIT="384MiB"
fi

echo -e "  ${CYAN}系统总内存: ${TOTAL_MEM_MB}MB → MemoryMax=${MEM_MAX}, GOMEMLIMIT=${GO_MEM_LIMIT}${NC}"

cat > /etc/systemd/system/dbbridge.service << EOF
[Unit]
Description=DBBridge - 数据库迁移与SQL转换工具
Documentation=https://github.com/suoten/dbbridge
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/dbbridge --web --port ${LISTEN_PORT}
WorkingDirectory=${INSTALL_DIR}
Restart=on-failure
RestartSec=3

# 资源约束（自适应：按服务器总内存设定）
MemoryMax=${MEM_MAX}
MemoryHigh=${MEM_HIGH}
Environment=GOMEMLIMIT=${GO_MEM_LIMIT}
Environment=GOGC=50

# CPU：低优先级（空闲时可用满，繁忙时让路给数据库）
CPUWeight=50
Environment=GOMAXPROCS=4

# I/O：普通优先级（数据迁移需要读写）
IOWeight=50
LimitNOFILE=65536

# OOM：内存不足时可被杀（会自动重启）
OOMScoreAdjust=100

# 安全加固
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
PrivateTmp=true
ReadWritePaths=${DATA_DIR} ${LOG_DIR} ${INSTALL_DIR}

# 日志
StandardOutput=append:${LOG_DIR}/dbbridge.log
StandardError=append:${LOG_DIR}/dbbridge.log
SyslogIdentifier=dbbridge

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload

# ── 防火墙放行 ────────────────────────────────────────────────
echo -e "${GREEN}[6/7]${NC} 配置防火墙"
if [ "$IS_BAOTA" = true ]; then
  echo -e "  ${YELLOW}检测到宝塔面板，请在宝塔面板 → 安全 → 放行端口 ${LISTEN_PORT}${NC}"
  echo -e "  ${YELLOW}或通过宝塔反向代理使用域名访问（见下方提示）${NC}"
else
  if command -v firewall-cmd &> /dev/null; then
    firewall-cmd --permanent --add-port=${LISTEN_PORT}/tcp 2>/dev/null && firewall-cmd --reload 2>/dev/null
    echo -e "  ${YELLOW}已放行 ${LISTEN_PORT} 端口 (firewalld)${NC}"
  elif command -v ufw &> /dev/null; then
    ufw allow ${LISTEN_PORT}/tcp 2>/dev/null
    echo -e "  ${YELLOW}已放行 ${LISTEN_PORT} 端口 (ufw)${NC}"
  elif command -v iptables &> /dev/null; then
    iptables -I INPUT -p tcp --dport ${LISTEN_PORT} -j ACCEPT 2>/dev/null
    echo -e "  ${YELLOW}已放行 ${LISTEN_PORT} 端口 (iptables)${NC}"
  else
    echo -e "  ${YELLOW}未检测到防火墙，请手动放行 ${LISTEN_PORT} 端口${NC}"
  fi
fi

# ── 启动服务 ──────────────────────────────────────────────────
echo -e "${GREEN}[7/7]${NC} 启动服务"

# 先停止旧服务（如果正在运行），避免端口冲突
if systemctl is-active --quiet dbbridge 2>/dev/null; then
  echo -e "  ${YELLOW}停止旧服务...${NC}"
  systemctl stop dbbridge 2>/dev/null || true
  sleep 1
fi

systemctl enable dbbridge
systemctl restart dbbridge

# 等待启动
sleep 3

if systemctl is-active --quiet dbbridge; then
  SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
  if [ -z "$SERVER_IP" ]; then
    SERVER_IP="服务器IP"
  fi

  echo ""
  echo -e "${GREEN}╔════════════════════════════════════════════════════════════╗"
  echo -e "║                    ✅ 安装成功！                            ║"
  echo -e "╠════════════════════════════════════════════════════════════╣"
  echo -e "║                                                            ║"
  echo -e "║  访问地址: http://${SERVER_IP}:${LISTEN_PORT}                      "
  echo -e "║  安装目录: ${INSTALL_DIR}                       "
  echo -e "║  日志文件: ${LOG_DIR}/dbbridge.log                "
  echo -e "║  服务端口: ${LISTEN_PORT}                              "
  echo -e "║                                                            ║"
  echo -e "╚════════════════════════════════════════════════════════════╝${NC}"
  echo ""

  # 宝塔反向代理提示
  if [ "$IS_BAOTA" = true ]; then
    echo -e "${CYAN}${BOLD}─── 宝塔面板反向代理配置 ───${NC}"
    echo -e "  ${GREEN}已检测到宝塔面板！推荐配置反向代理，通过域名访问：${NC}"
    echo ""
    echo -e "  方式一（一键脚本，推荐）:"
    echo -e "    ${CYAN}sudo bash ${INSTALL_DIR}/baota-proxy.sh${NC}"
    echo ""
    echo -e "  方式二（宝塔面板手动操作）:"
    echo -e "    1. 宝塔面板 → 网站 → 添加站点（绑定域名）"
    echo -e "    2. 站点设置 → 反向代理 → 添加反向代理"
    echo -e "    3. 代理名称: DBBridge"
    echo -e "    4. 目标URL:  http://127.0.0.1:${LISTEN_PORT}"
    echo -e "    5. 发送域名: \$host"
    echo ""
  fi

  # 功能说明
  echo -e "${CYAN}${BOLD}─── DBBridge 已就绪，开始你的数据库迁移之旅 ───${NC}"
  echo ""
  echo -e "  ${GREEN}✓${NC} 支持 10 种数据库互转（MySQL/PG/SQLite/OceanBase/TiDB/...）"
  echo -e "  ${GREEN}✓${NC} 迁移前自动备份 + 一键回滚"
  echo -e "  ${GREEN}✓${NC} 实时进度展示 + 详细日志"
  echo -e "  ${GREEN}✓${NC} 迁移历史记录"
  echo ""

  # 常用命令
  echo -e "${CYAN}常用命令:${NC}"
  echo -e "  systemctl status dbbridge      # 查看状态"
  echo -e "  systemctl restart dbbridge     # 重启"
  echo -e "  systemctl stop dbbridge        # 停止"
  echo -e "  journalctl -u dbbridge -f     # 查看日志"
  echo -e "  DBBRIDGE_PORT=8990 bash install.sh  # 指定端口重装"
  echo ""
else
  echo -e "${RED}安装完成但服务启动失败，请检查:${NC}"
  echo -e "  ${YELLOW}journalctl -u dbbridge -n 50${NC}"
  echo ""
  echo -e "${CYAN}─── 自动诊断 ───${NC}"
  journalctl -u dbbridge -n 30 --no-pager 2>/dev/null || true
  echo ""
  echo -e "${CYAN}─── 手动测试 ───${NC}"
  echo -e "  ${YELLOW}手动运行查看详细错误:${NC}"
  echo -e "  ${INSTALL_DIR}/dbbridge --web --port ${LISTEN_PORT}"
fi
