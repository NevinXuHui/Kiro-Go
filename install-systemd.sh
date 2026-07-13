#!/bin/bash

# Kiro-Go systemd 服务安装脚本

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_NAME="kiro-go.service"
SERVICE_FILE="$SCRIPT_DIR/$SERVICE_NAME"
SYSTEMD_DIR="/etc/systemd/system"

echo "========================================"
echo "  Kiro-Go systemd 服务安装"
echo "========================================"
echo ""

# 检查 service 文件是否存在
if [ ! -f "$SERVICE_FILE" ]; then
    echo "错误: 找不到 $SERVICE_FILE"
    exit 1
fi

# 检查是否以 root 权限运行
if [ "$EUID" -ne 0 ]; then 
    echo "错误: 请使用 root 权限运行此脚本"
    echo "使用: sudo bash install-systemd.sh"
    exit 1
fi

# 先编译，再安装（与 SYSTEMD_DEPLOY 文档一致）
echo "→ 编译项目..."
if ! command -v go >/dev/null 2>&1; then
    echo "错误: 未找到 go 命令，无法编译"
    exit 1
fi
(
    cd "$SCRIPT_DIR"
    go build -o kiro-go .
)
if [ ! -x "$SCRIPT_DIR/kiro-go" ]; then
    echo "错误: 编译失败，未生成可执行文件 $SCRIPT_DIR/kiro-go"
    exit 1
fi
echo "  ✓ 已生成 $SCRIPT_DIR/kiro-go"

# 停止现有服务（如果存在）
if systemctl is-active --quiet $SERVICE_NAME; then
    echo "→ 停止现有服务..."
    systemctl stop $SERVICE_NAME
fi

# 复制服务文件
echo "→ 安装服务文件到 $SYSTEMD_DIR..."
cp "$SERVICE_FILE" "$SYSTEMD_DIR/$SERVICE_NAME"

# 重新加载 systemd
echo "→ 重新加载 systemd..."
systemctl daemon-reload

# 启用服务（开机自启）
echo "→ 启用服务（开机自启）..."
systemctl enable $SERVICE_NAME

# 启动服务
echo "→ 启动服务..."
systemctl start $SERVICE_NAME

# 等待服务启动
sleep 2

# 显示服务状态
echo ""
echo "========================================"
echo "  安装完成"
echo "========================================"
echo ""
systemctl status $SERVICE_NAME --no-pager || true

echo ""
echo "常用命令:"
echo "  启动服务: systemctl start $SERVICE_NAME"
echo "  停止服务: systemctl stop $SERVICE_NAME"
echo "  重启服务: systemctl restart $SERVICE_NAME"
echo "  查看状态: systemctl status $SERVICE_NAME"
echo "  查看日志: journalctl -u $SERVICE_NAME -f"
echo "  禁用开机自启: systemctl disable $SERVICE_NAME"
echo ""