#!/bin/bash
# install.sh - 通过 systemd 安装 Kiro-Go 为系统服务
set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

SERVICE_NAME="kiro-go"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
INSTALL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY="${INSTALL_DIR}/kiro-go"
CONFIG_FILE="${INSTALL_DIR}/data/config.json"

DEFAULT_PORT=8991
DEFAULT_PASSWORD="admin123"

echo "=== Kiro-Go systemd 安装 ==="
echo ""

# 1. 权限检查
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}错误: 需要 root 权限${NC}"
    echo "请使用: sudo $0"
    exit 1
fi

# 2. systemd 检查
if ! command -v systemctl &> /dev/null; then
    echo -e "${RED}错误: 系统不支持 systemd${NC}"
    exit 1
fi

# 3. 二进制文件检查
if [ ! -f "$BINARY" ]; then
    echo -e "${RED}错误: 未找到 kiro-go 二进制文件${NC}"
    echo "请先编译: ./build.sh"
    exit 1
fi
echo -e "${GREEN}✓${NC} 找到二进制: $BINARY"

# 4. 配置文件初始化
mkdir -p "${INSTALL_DIR}/data"
if [ ! -f "$CONFIG_FILE" ]; then
    echo -e "${YELLOW}→${NC} 生成默认配置"
    cat > "$CONFIG_FILE" <<EOF
{
  "password": "$DEFAULT_PASSWORD",
  "port": $DEFAULT_PORT,
  "host": "0.0.0.0",
  "requireApiKey": false,
  "accounts": []
}
EOF
    chmod 600 "$CONFIG_FILE"
    CONFIG_CREATED=true
else
    echo -e "${GREEN}✓${NC} 配置文件已存在: $CONFIG_FILE"
    CONFIG_CREATED=false
fi

# 5. 处理已存在的服务
if systemctl list-unit-files | grep -q "^${SERVICE_NAME}.service"; then
    echo -e "${YELLOW}→${NC} 检测到已安装的服务，停止旧服务"
    systemctl stop "$SERVICE_NAME" 2>/dev/null || true
fi

# 6. 创建 systemd service 文件
echo -e "${BLUE}→${NC} 创建 systemd 服务文件"
cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Kiro-Go API Proxy Service
Documentation=https://github.com/Quorinex/Kiro-Go
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=${INSTALL_DIR}
ExecStart=${BINARY}
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=${SERVICE_NAME}

# 资源限制
LimitNOFILE=65535

# 安全加固
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=${INSTALL_DIR}/data

[Install]
WantedBy=multi-user.target
EOF

chmod 644 "$SERVICE_FILE"
echo -e "${GREEN}✓${NC} 服务文件: $SERVICE_FILE"

# 7. 重载 systemd 并启动
echo -e "${BLUE}→${NC} 重载 systemd 配置"
systemctl daemon-reload

echo -e "${BLUE}→${NC} 启用开机自启"
systemctl enable "$SERVICE_NAME" >/dev/null 2>&1

echo -e "${BLUE}→${NC} 启动服务"
systemctl start "$SERVICE_NAME"

# 8. 等待启动并检查状态
sleep 2
if systemctl is-active --quiet "$SERVICE_NAME"; then
    echo -e "${GREEN}✓${NC} 服务运行中"
else
    echo -e "${RED}✗${NC} 服务启动失败"
    echo "查看日志: journalctl -u $SERVICE_NAME -n 50"
    exit 1
fi

# 9. 显示信息
PORT=$(grep -oP '"port"\s*:\s*\K[0-9]+' "$CONFIG_FILE" | head -1)
PORT=${PORT:-$DEFAULT_PORT}

echo ""
echo "╔════════════════════════════╗"
echo "║   安装完成                 ║"
echo "╚════════════════════════════╝"
echo ""
echo "服务管理:"
echo "  systemctl status ${SERVICE_NAME}      # 查看状态"
echo "  systemctl restart ${SERVICE_NAME}     # 重启"
echo "  systemctl stop ${SERVICE_NAME}        # 停止"
echo "  systemctl disable ${SERVICE_NAME}     # 禁用自启"
echo "  journalctl -u ${SERVICE_NAME} -f      # 查看日志"
echo ""
echo "访问地址:"
echo "  管理面板: http://localhost:${PORT}/admin"
echo "  Claude API: http://localhost:${PORT}/v1/messages"
echo "  OpenAI API: http://localhost:${PORT}/v1/chat/completions"
echo ""
if [ "$CONFIG_CREATED" = true ]; then
    echo -e "${YELLOW}默认密码: ${DEFAULT_PASSWORD}（建议首次登录后修改）${NC}"
fi
