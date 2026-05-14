#!/bin/bash
# uninstall.sh - 卸载 Kiro-Go systemd 服务
set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

SERVICE_NAME="kiro-go"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

echo "=== Kiro-Go 卸载 ==="
echo ""

# 权限检查
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}错误: 需要 root 权限${NC}"
    echo "请使用: sudo $0"
    exit 1
fi

# 检查服务是否存在
if [ ! -f "$SERVICE_FILE" ]; then
    echo -e "${YELLOW}服务未安装${NC}"
    exit 0
fi

# 确认
echo -e "${YELLOW}将执行以下操作:${NC}"
echo "  1. 停止服务"
echo "  2. 禁用开机自启"
echo "  3. 删除服务文件: $SERVICE_FILE"
echo ""
echo -e "${YELLOW}注意: 不会删除二进制文件、配置文件和数据${NC}"
echo ""
read -p "确认卸载？[y/N] " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "已取消"
    exit 0
fi

# 停止服务
if systemctl is-active --quiet "$SERVICE_NAME"; then
    echo "→ 停止服务"
    systemctl stop "$SERVICE_NAME"
fi

# 禁用自启
if systemctl is-enabled --quiet "$SERVICE_NAME" 2>/dev/null; then
    echo "→ 禁用开机自启"
    systemctl disable "$SERVICE_NAME" >/dev/null 2>&1
fi

# 删除服务文件
echo "→ 删除服务文件"
rm -f "$SERVICE_FILE"

# 重载 systemd
systemctl daemon-reload
systemctl reset-failed 2>/dev/null || true

echo ""
echo -e "${GREEN}✓ 卸载完成${NC}"
echo ""
echo "如需彻底清理:"
echo "  rm -rf $(pwd)/data    # 删除配置和数据"
echo "  rm -f $(pwd)/kiro-go  # 删除二进制"
