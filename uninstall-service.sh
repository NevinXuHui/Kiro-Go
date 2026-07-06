#!/bin/bash

# Kiro-Go 服务卸载脚本
# 用法: sudo ./uninstall-service.sh [安装路径]
# 默认路径: /opt/kiro-go

set -e

INSTALL_DIR="${1:-/opt/kiro-go}"
SERVICE_NAME="kiro-go"

echo "========================================"
echo "  Kiro-Go 服务卸载"
echo "========================================"
echo ""

# 检查是否 root
if [[ $EUID -ne 0 ]]; then
   echo "错误: 此脚本需要 root 权限运行"
   echo "请使用: sudo $0"
   exit 1
fi

# 确认卸载
read -p "确定要卸载 Kiro-Go 服务吗？这将删除所有程序文件（数据会保留）[y/N] " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "已取消卸载"
    exit 0
fi

echo ""
echo "[1/5] 停止服务..."
if systemctl is-active --quiet ${SERVICE_NAME}; then
    systemctl stop ${SERVICE_NAME}
    echo "  ✓ 服务已停止"
else
    echo "  ⓘ 服务未运行"
fi

echo "[2/5] 禁用服务..."
if systemctl is-enabled --quiet ${SERVICE_NAME} 2>/dev/null; then
    systemctl disable ${SERVICE_NAME}
    echo "  ✓ 已禁用开机自启"
else
    echo "  ⓘ 服务未启用"
fi

echo "[3/5] 删除服务文件..."
if [[ -f "/etc/systemd/system/${SERVICE_NAME}.service" ]]; then
    rm /etc/systemd/system/${SERVICE_NAME}.service
    systemctl daemon-reload
    echo "  ✓ 服务文件已删除"
else
    echo "  ⓘ 服务文件不存在"
fi

echo "[4/5] 删除程序文件..."
if [[ -d "$INSTALL_DIR" ]]; then
    # 备份数据目录
    if [[ -d "$INSTALL_DIR/data" ]]; then
        BACKUP_DIR="${INSTALL_DIR}-data-backup-$(date +%Y%m%d_%H%M%S)"
        mv "$INSTALL_DIR/data" "$BACKUP_DIR"
        echo "  ✓ 数据已备份到: $BACKUP_DIR"
    fi
    
    rm -rf "$INSTALL_DIR"
    echo "  ✓ 程序文件已删除"
else
    echo "  ⓘ 安装目录不存在: $INSTALL_DIR"
fi

echo "[5/5] 删除系统用户..."
if id -u kiro &>/dev/null; then
    read -p "是否删除系统用户 kiro？[y/N] " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        userdel kiro
        echo "  ✓ 用户 kiro 已删除"
    else
        echo "  ⓘ 保留用户 kiro"
    fi
else
    echo "  ⓘ 用户 kiro 不存在"
fi

echo ""
echo "========================================"
echo "  ✓ 卸载完成"
echo "========================================"
echo ""
echo "已删除的内容："
echo "  - 服务文件: /etc/systemd/system/${SERVICE_NAME}.service"
echo "  - 程序目录: $INSTALL_DIR"
echo ""
echo "保留的内容："
if [[ -d "${INSTALL_DIR}-data-backup"* ]]; then
    echo "  - 数据备份: ${INSTALL_DIR}-data-backup-*"
fi
echo ""