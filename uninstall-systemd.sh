#!/bin/bash

# Kiro-Go systemd 服务卸载脚本

set -e

SERVICE_NAME="kiro-go.service"
SYSTEMD_DIR="/etc/systemd/system"

echo "========================================"
echo "  Kiro-Go systemd 服务卸载"
echo "========================================"
echo ""

# 检查是否以 root 权限运行
if [ "$EUID" -ne 0 ]; then 
    echo "错误: 请使用 root 权限运行此脚本"
    echo "使用: sudo bash uninstall-systemd.sh"
    exit 1
fi

# 检查服务是否存在
if [ ! -f "$SYSTEMD_DIR/$SERVICE_NAME" ]; then
    echo "服务未安装，无需卸载"
    exit 0
fi

# 停止服务
if systemctl is-active --quiet $SERVICE_NAME; then
    echo "→ 停止服务..."
    systemctl stop $SERVICE_NAME
fi

# 禁用服务
if systemctl is-enabled --quiet $SERVICE_NAME; then
    echo "→ 禁用服务..."
    systemctl disable $SERVICE_NAME
fi

# 删除服务文件
echo "→ 删除服务文件..."
rm -f "$SYSTEMD_DIR/$SERVICE_NAME"

# 重新加载 systemd
echo "→ 重新加载 systemd..."
systemctl daemon-reload

echo ""
echo "========================================"
echo "  卸载完成"
echo "========================================"
echo ""