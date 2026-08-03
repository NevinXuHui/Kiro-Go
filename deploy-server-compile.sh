#!/bin/bash

# Kiro-Go 服务器端编译部署脚本
# 解决 GLIBC 版本不兼容问题

set -e

SERVER_IP="8.215.92.113"
SERVER_USER="root"
DEPLOY_DIR="/opt/kiro-go"
SERVICE_NAME="kiro-go"
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
echo_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
echo_error() { echo -e "${RED}[ERROR]${NC} $1"; }

echo_info "检查 SSH 连接..."
if ! ssh -o ConnectTimeout=5 -o BatchMode=yes ${SERVER_USER}@${SERVER_IP} "echo '连接成功'" > /dev/null 2>&1; then
    echo_error "无法连接到服务器 ${SERVER_IP}"
    exit 1
fi
echo_info "SSH 连接验证成功"

# 上传源码到服务器
echo_info "上传源码到服务器..."
ssh ${SERVER_USER}@${SERVER_IP} "mkdir -p /tmp/kiro-go-src"
rsync -az --exclude='kiro-go' --exclude='.git' --exclude='data' \
    "${PROJECT_DIR}/" ${SERVER_USER}@${SERVER_IP}:/tmp/kiro-go-src/

# 在服务器上编译和部署
echo_info "在服务器上编译和部署..."
ssh ${SERVER_USER}@${SERVER_IP} bash << 'ENDSSH'
set -e

SRC_DIR="/tmp/kiro-go-src"
DEPLOY_DIR="/opt/kiro-go"
SERVICE_NAME="kiro-go"

echo "[服务器] 编译项目..."
cd $SRC_DIR
go build -o kiro-go .
if [ ! -f "kiro-go" ]; then
    echo "[服务器] 编译失败"
    exit 1
fi
echo "[服务器] 编译完成: $(ls -lh kiro-go | awk '{print $5}')"

echo "[服务器] 停止现有服务..."
systemctl stop $SERVICE_NAME 2>/dev/null || true

echo "[服务器] 创建部署目录..."
mkdir -p $DEPLOY_DIR/data
mkdir -p $DEPLOY_DIR/web

echo "[服务器] 复制文件..."
cp kiro-go $DEPLOY_DIR/
chmod +x $DEPLOY_DIR/kiro-go

# 复制 web 资源
if [ -d "web" ]; then
    cp -r web/* $DEPLOY_DIR/web/
    echo "[服务器] Web 资源已复制"
fi

echo "[服务器] 配置 systemd 服务..."
cat > /etc/systemd/system/$SERVICE_NAME.service << 'EOF'
[Unit]
Description=Kiro-Go API Service
After=network.target
StartLimitIntervalSec=0

[Service]
Type=simple
Restart=always
RestartSec=5
User=root
WorkingDirectory=/opt/kiro-go
ExecStart=/opt/kiro-go/kiro-go

# 环境变量
Environment="CONFIG_PATH=/opt/kiro-go/data/config.json"
Environment="DATABASE_PATH=/opt/kiro-go/data/kiro.db"
Environment="REQUEST_LOGS_MAX=10000"

# 资源限制
LimitNOFILE=65536

# 日志
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

echo "[服务器] 重新加载 systemd..."
systemctl daemon-reload

echo "[服务器] 启用服务（开机自启）..."
systemctl enable $SERVICE_NAME

echo "[服务器] 启动服务..."
systemctl start $SERVICE_NAME

# 等待服务启动
sleep 3

echo "[服务器] 检查服务状态..."
if systemctl is-active --quiet $SERVICE_NAME; then
    echo "[服务器] ✓ 服务运行正常"
    systemctl status $SERVICE_NAME --no-pager -l | head -15
else
    echo "[服务器] ✗ 服务启动失败"
    journalctl -u $SERVICE_NAME -n 20 --no-pager
    exit 1
fi

# 清理临时文件
rm -rf $SRC_DIR

echo ""
echo "[服务器] 部署完成！"
ENDSSH

if [ $? -eq 0 ]; then
    echo ""
    echo_info "=========================================="
    echo_info "  部署成功！"
    echo_info "=========================================="
    echo ""
    echo "服务器地址: ${SERVER_IP}"
    echo "部署目录: ${DEPLOY_DIR}"
    echo "管理面板: http://${SERVER_IP}:8991/admin"
    echo "API 端点: http://${SERVER_IP}:8991/v1/messages"
    echo ""
    echo "常用命令（SSH 到服务器后执行）："
    echo "  查看状态: systemctl status ${SERVICE_NAME}"
    echo "  查看日志: journalctl -u ${SERVICE_NAME} -f"
    echo "  重启服务: systemctl restart ${SERVICE_NAME}"
    echo "  停止服务: systemctl stop ${SERVICE_NAME}"
    echo ""
    echo_warn "重要提示："
    echo "  1. 默认端口 8991，请确保防火墙已开放"
    echo "  2. 首次登录密码 'changeme'，请及时修改"
    echo "  3. 建议配置 Nginx 反向代理 + SSL"
    echo ""
else
    echo_error "部署失败，请检查上面的错误信息"
    exit 1
fi
