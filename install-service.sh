#!/bin/bash

# Kiro-Go 服务安装脚本

set -e

echo "正在安装 Kiro-Go 系统服务..."

# 复制服务文件
sudo cp kiro-go.service /etc/systemd/system/

# 重载 systemd
sudo systemctl daemon-reload

# 启用开机自启
sudo systemctl enable kiro-go

# 启动服务
sudo systemctl start kiro-go

echo "✓ 服务安装完成"
echo ""
echo "常用命令："
echo "  启动服务: sudo systemctl start kiro-go"
echo "  停止服务: sudo systemctl stop kiro-go"
echo "  重启服务: sudo systemctl restart kiro-go"
echo "  查看状态: sudo systemctl status kiro-go"
echo "  查看日志: sudo journalctl -u kiro-go -f"
echo "  禁用自启: sudo systemctl disable kiro-go"
