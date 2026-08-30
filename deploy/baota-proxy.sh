#!/usr/bin/env bash
# ============================================================================
#  DBBridge 宝塔面板反向代理一键配置脚本
#  自动创建站点并配置 Nginx 反向代理
# ============================================================================
set -euo pipefail

# ============================ 颜色定义 ============================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
NC='\033[0m'

info()    { echo -e "${GREEN}[信息]${NC} $*"; }
warn()    { echo -e "${YELLOW}[警告]${NC} $*"; }
error()   { echo -e "${RED}[错误]${NC} $*" >&2; }
fatal()   { echo -e "${RED}[致命]${NC} $*" >&2; exit 1; }

# ============================ 配置 ============================
DBBRIDGE_PORT="${DBBRIDGE_PORT:-8989}"
BT_NGINX_CONF_DIR="/www/server/panel/vhost/nginx"
BT_SITE_ROOT="/www/wwwroot"
BT_LOG_DIR="/www/wwwlogs"

# ============================ 检查 ============================
check_root() {
    if [[ $EUID -ne 0 ]]; then
        fatal "请使用 root 权限运行: sudo bash baota-proxy.sh"
    fi
}

check_baota() {
    if [[ ! -d "/www/server/panel" ]]; then
        fatal "未检测到宝塔面板，请先安装宝塔面板: https://www.bt.cn"
    fi
    info "检测到宝塔面板 ✓"
}

# ============================ 主流程 ============================
main() {
    check_root
    check_baota

    echo
    echo -e "${BOLD}=== DBBridge 宝塔反向代理配置 ===${NC}"
    echo

    # 1. 输入域名
    read -rp "$(echo -e ${YELLOW}请输入绑定域名（如 db.example.com）: ${NC})" DOMAIN_NAME
    [[ -z "$DOMAIN_NAME" ]] && fatal "域名不能为空"

    # 2. 确认端口
    read -rp "$(echo -e ${YELLOW}DBBridge 服务端口（默认 ${DBBRIDGE_PORT}）: ${NC})" INPUT_PORT
    if [[ -n "$INPUT_PORT" ]]; then
        DBBRIDGE_PORT="$INPUT_PORT"
    fi

    # 3. 选择模式
    echo
    echo "请选择配置方式："
    echo "  1) 通过宝塔 API 创建站点（推荐，自动化程度高）"
    echo "  2) 手动创建站点后，自动写入 Nginx 反向代理配置"
    echo "  3) 仅输出配置文件内容，手动粘贴"
    read -rp "$(echo -e ${YELLOW}请选择 [1/2/3] (默认 2): ${NC})" MODE
    MODE="${MODE:-2}"

    case "$MODE" in
        1) setup_via_api ;;
        2) setup_manual ;;
        3) print_config_only ;;
        *) fatal "无效选择: $MODE" ;;
    esac
}

# ============================ 模式 1: 宝塔 API ============================
setup_via_api() {
    info "通过宝塔 API 创建站点..."

    # 获取宝塔面板 API Token
    read -rp "$(echo -e ${YELLOW}请输入宝塔面板 API Token（面板设置 → API接口）: ${NC})" BT_TOKEN
    [[ -z "$BT_TOKEN" ]] && fatal "Token 不能为空"

    # 获取面板地址
    read -rp "$(echo -e ${YELLOW}宝塔面板地址（默认 https://127.0.0.1:8888）: ${NC})" BT_URL
    BT_URL="${BT_URL:-https://127.0.0.1:8888}"

    # 创建站点
    info "正在创建站点: $DOMAIN_NAME ..."
    local site_root="${BT_SITE_ROOT}/${DOMAIN_NAME}"

    # 宝塔 API 创建站点
    curl -sk -X POST "${BT_URL}/site?action=AddSite" \
        -H "panel-token: ${BT_TOKEN}" \
        -d "webname=${DOMAIN_NAME}&type=PHP&version=0&port=80&path=${site_root}" \
        >/dev/null 2>&1 || true

    info "站点已创建，正在配置反向代理..."
    write_nginx_conf

    info "重载 Nginx 配置..."
    /www/server/nginx/sbin/nginx -t 2>/dev/null && \
        /www/server/nginx/sbin/nginx -s reload 2>/dev/null || \
        nginx -t 2>/dev/null && nginx -s reload 2>/dev/null

    success
}

# ============================ 模式 2: 手动站点 + 自动配置 ============================
setup_manual() {
    info "请先在宝塔面板中创建站点:"
    echo -e "  ${BOLD}宝塔面板 → 网站 → 添加站点${NC}"
    echo -e "  域名: ${BOLD}${DOMAIN_NAME}${NC}"
    echo -e "  类型: 纯静态即可"
    echo
    read -rsp "$(echo -e ${YELLOW}站点创建完成后按回车继续...${NC})"

    write_nginx_conf

    info "重载 Nginx 配置..."
    /www/server/nginx/sbin/nginx -t 2>/dev/null && \
        /www/server/nginx/sbin/nginx -s reload 2>/dev/null || \
        nginx -t 2>/dev/null && nginx -s reload 2>/dev/null

    success
}

# ============================ 写入 Nginx 配置 ============================
write_nginx_conf() {
    local conf_file="${BT_NGINX_CONF_DIR}/${DOMAIN_NAME}.conf"
    local site_root="${BT_SITE_ROOT}/${DOMAIN_NAME}"
    local log_dir="${BT_LOG_DIR}"

    info "写入 Nginx 配置: $conf_file"

    cat > "$conf_file" << EOF
# DBBridge 反向代理配置 - 由 baota-proxy.sh 自动生成
upstream dbbridge_backend {
    server 127.0.0.1:${DBBRIDGE_PORT};
    keepalive 32;
}

server {
    listen 80;
    # listen 443 ssl http2;

    server_name ${DOMAIN_NAME};
    root ${site_root};

    # SSL 证书（如有，取消注释并修改路径）
    # ssl_certificate     /www/server/ssl/${DOMAIN_NAME}/fullchain.pem;
    # ssl_certificate_key /www/server/ssl/${DOMAIN_NAME}/privkey.pem;

    # 安全
    location ~ /\. {
        deny all;
    }

    # DBBridge 反向代理
    location / {
        proxy_pass http://dbbridge_backend;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        # WebSocket 支持（迁移进度实时推送）
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";

        # 超时设置（大表迁移耗时较长）
        proxy_connect_timeout 60s;
        proxy_send_timeout 300s;
        proxy_read_timeout 300s;

        # 缓冲设置
        proxy_buffering off;
        proxy_cache off;
    }

    # 静态资源缓存
    location ~* \.(js|css|png|jpg|jpeg|gif|ico|svg|woff|woff2|ttf|eot)\$ {
        proxy_pass http://dbbridge_backend;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        expires 30d;
        add_header Cache-Control "public, immutable";
    }

    # 健康检查
    location = /health {
        proxy_pass http://dbbridge_backend/health;
        access_log off;
    }

    access_log ${log_dir}/${DOMAIN_NAME}.log;
    error_log  ${log_dir}/${DOMAIN_NAME}.error.log;
}
EOF

    info "Nginx 配置写入成功"
}

# ============================ 模式 3: 仅输出配置 ============================
print_config_only() {
    info "以下为 Nginx 配置文件内容，请手动粘贴到宝塔站点配置中:"
    echo
    echo -e "${BLUE}# ============================================================${NC}"
    cat << 'CONFIGEOF'
upstream dbbridge_backend {
    server 127.0.0.1:8989;
    keepalive 32;
}

server {
    listen 80;
    # listen 443 ssl http2;

    server_name 你的域名.com;
    root /www/wwwroot/你的域名.com;

    location ~ /\. {
        deny all;
    }

    location / {
        proxy_pass http://dbbridge_backend;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";

        proxy_connect_timeout 60s;
        proxy_send_timeout 300s;
        proxy_read_timeout 300s;
        proxy_buffering off;
        proxy_cache off;
    }

    location ~* \.(js|css|png|jpg|jpeg|gif|ico|svg|woff|woff2|ttf|eot)$ {
        proxy_pass http://dbbridge_backend;
        expires 30d;
        add_header Cache-Control "public, immutable";
    }

    location = /health {
        proxy_pass http://dbbridge_backend/health;
        access_log off;
    }

    access_log /www/wwwlogs/你的域名.log;
    error_log  /www/wwwlogs/你的域名.error.log;
}
CONFIGEOF
    echo -e "${BLUE}# ============================================================${NC}"
    echo
    info "请将上方配置中的「你的域名.com」替换为实际域名"
    echo
}

# ============================ 成功输出 ============================
success() {
    local server_ip
    server_ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    [[ -z "$server_ip" ]] && server_ip="服务器IP"

    echo
    echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${GREEN}  ${BOLD}✅ 宝塔反向代理配置完成！${NC}"
    echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo
    echo -e "  ${BOLD}域名访问:${NC}  http://${DOMAIN_NAME}"
    echo -e "  ${BOLD}本地服务:${NC}  http://127.0.0.1:${DBBRIDGE_PORT}"
    echo
    echo -e "  ${BOLD}SSL 证书（可选）:${NC}"
    echo -e "    宝塔面板 → 网站 → ${DOMAIN_NAME} → SSL → Let's Encrypt → 申请"
    echo
    echo -e "  ${BOLD}常用命令:${NC}"
    echo -e "    systemctl restart dbbridge    # 重启 DBBridge"
    echo -e "    systemctl status dbbridge     # 查看状态"
    echo -e "    nginx -t && nginx -s reload   # 重载 Nginx"
    echo
    echo -e "${CYAN}  反向代理已就绪，通过域名即可访问 DBBridge！${NC}"
    echo
}

main "$@"
