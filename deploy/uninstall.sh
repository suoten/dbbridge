#!/bin/bash
# ============================================================================
#  DBBridge 卸载脚本
#  数据库迁移与SQL转换工具
# ============================================================================
set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

if [ "$EUID" -ne 0 ]; then
  echo -e "${RED}请使用 root 用户运行: sudo bash uninstall.sh${NC}"
  exit 1
fi

# ── 宝塔环境检测 ──────────────────────────────────────────────
IS_BAOTA=false
if [ -d "/www/server/panel" ]; then
  IS_BAOTA=true
  echo -e "${YELLOW}[INFO]${NC} 检测到宝塔面板环境，卸载将保留所有宝塔相关配置"
fi

# ── 停止并禁用服务 ────────────────────────────────────────────
echo -e "${GREEN}[1/4]${NC} 停止服务..."
systemctl stop dbbridge 2>/dev/null || true
systemctl disable dbbridge 2>/dev/null || true

# ── 删除 systemd 服务文件 ─────────────────────────────────────
echo -e "${GREEN}[2/4]${NC} 删除 systemd 服务..."
rm -f /etc/systemd/system/dbbridge.service
systemctl daemon-reload

# ── 删除程序文件 ──────────────────────────────────────────────
echo -e "${GREEN}[3/4]${NC} 删除程序文件..."
rm -rf /opt/dbbridge

# ── 删除数据文件 ──────────────────────────────────────────────
echo -e "${GREEN}[4/4]${NC} 删除数据文件..."
rm -rf /var/lib/dbbridge
rm -rf /var/log/dbbridge

# ── 完成 ──────────────────────────────────────────────────────
echo ""
if [ "$IS_BAOTA" = true ]; then
  echo -e "${GREEN}DBBridge 已卸载完成${NC}"
  echo -e "${YELLOW}宝塔面板不受影响，所有宝塔配置均已保留${NC}"
else
  echo -e "${GREEN}DBBridge 已卸载完成${NC}"
fi
echo ""
