#!/bin/bash

# Kiro-Go 自动部署脚本
# 目标服务器: 8.215.92.113
# 认证方式: SSH 秘钥

set -e

# 配置变量
SERVER_IP="8.215.92.113"
SERVER_USER="root"
DEPLOY_DIR="/opt/kiro-go"
SERVICE_NAME="kiro-go"
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

echo_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

echo_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 检查 SSH 连接
echo_info "检查 SSH 连接..."
if ! ssh -o ConnectTimeout=5 -o BatchMode=yes ${SERVER_USER}@${SERVER_IP} "echo 'SSH 连接成功'" > /dev/null 2>&1; then
    echo_error "无法连接到服务器 ${SERVER_IP}"
    echo_error "请确认："
    echo "  1. 服务器 IP 是否正确"
    echo "  2. SSH 秘钥是否已配置 (~/.ssh/id_rsa)"
    echo "  3. 服务器防火墙是否开放 SSH 端口"
    exit 1
fi
echo_info "SSH 连接验证成功"

# 本地编译
echo_info "本地编译项目..."
cd "$PROJECT_DIR"
if ! command -v go >/dev/null 2>&1; then
    echo_error "未找到 go 命令，请先安装 Go 1.21+"
    exit 1
fi

go build -o kiro-go .
if [ ! -f "kiro-go" ]; then
    echo_error "编译失败"
    exit 1
fi
echo_info "编译完成: $(ls -lh kiro-go | awk '{print $5}')"

# 创建临时部署目录
TEMP_DEPLOY="/tmp/kiro-go-deploy-$$"
mkdir -p "$TEMP_DEPLOY"

# 复制文件到临时目录
echo_info "准备部署文件..."
cp kiro-go "$TEMP_DEPLOY/"
cp -r web "$TEMP_DEPLOY/" 2>/dev/null || echo_warn "web 目录不存在，跳过"
cp install-service.sh "$TEMP_DEPLOY/" 2>/dev/null || true
cp kiro-go.service "$TEMP_DEPLOY/" 2>/dev/null || true

# 上传文件到服务器
echo_info "上传文件到服务器..."
ssh ${SERVER_USER}@${SERVER_IP} "mkdir -p /tmp/kiro-go-upload"
scp -r "$TEMP_DEPLOY"/* ${SERVER_USER}@${SERVER_IP}:/tmp/kiro-go-upload/

# 在服务器上执行部署
echo_info "在服务器上执行部署..."
ssh ${SERVER_USER}@${SERVER_IP} bash << 'ENDSSH'
set -e

UPLOAD_DIR="/tmp/kiro-go-upload"
DEPLOY_DIR="/opt/kiro-go"
SERVICE_NAME="kiro-go"

echo "[服务器] 停止现有服务（如果存在）..."
if systemctl is-active --quiet $SERVICE_NAME 2>/dev/null; then
    systemctl stop $SERVICE_NAME
    echo "[服务器] 已停止现有服务"
fi

echo "[服务器] 创建部署目录..."
mkdir -p $DEPLOY_DIR/data
mkdir -p $DEPLOY_DIR/web

echo "[服务器] 复制文件..."
cp $UPLOAD_DIR/kiro-go $DEPLOY_DIR/
chmod +x $DEPLOY_DIR/kiro-go

# 复制 web 目录（如果存在）
if [ -d "$UPLOAD_DIR/web" ]; then
    cp -r $UPLOAD_DIR/web/* $DEPLOY_DIR/web/
fi

# 创建或更新 systemd 服务文件
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
sleep 2

echo "[服务器] 服务状态："
systemctl status $SERVICE_NAME --no-pager || true

# 清理临时文件
rm -rf $UPLOAD_DIR

echo ""
echo "[服务器] 部署完成！"
echo "  - 服务已启动并设置为开机自启"
echo "  - 部署目录: $DEPLOY_DIR"
echo "  - 配置文件: $DEPLOY_DIR/data/config.json"
echo "  - 数据库: $DEPLOY_DIR/data/kiro.db"
ENDSSH

# 本地清理
rm -rf "$TEMP_DEPLOY"

# 显示部署信息
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
echo "常用命令（在服务器上执行）："
echo "  查看状态: systemctl status ${SERVICE_NAME}"
echo "  查看日志: journalctl -u ${SERVICE_NAME} -f"
echo "  重启服务: systemctl restart ${SERVICE_NAME}"
echo "  停止服务: systemctl stop ${SERVICE_NAME}"
echo ""
echo_warn "注意事项："
echo "  1. 默认端口为 8991，请确保服务器防火墙已开放此端口"
echo "  2. 首次访问管理面板默认密码为 'changeme'，请及时修改"
echo "  3. 建议配置 Nginx 反向代理和 SSL 证书"
echo ""
