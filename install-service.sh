#!/bin/bash

# Kiro-Go 服务安装脚本
# 用法: sudo ./install-service.sh [安装路径]
# 默认安装路径: /opt/kiro-go

set -e

INSTALL_DIR="${1:-/opt/kiro-go}"
SERVICE_NAME="kiro-go"
CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "========================================"
echo "  Kiro-Go 系统服务安装"
echo "========================================"
echo ""

# 检查是否 root
if [[ $EUID -ne 0 ]]; then
   echo "错误: 此脚本需要 root 权限运行"
   echo "请使用: sudo $0"
   exit 1
fi

# 检查二进制文件
if [[ ! -f "$CURRENT_DIR/kiro-go" ]]; then
    echo "错误: 未找到 kiro-go 可执行文件"
    echo "请先运行 'go build -o kiro-go' 编译项目"
    exit 1
fi

echo "→ 安装路径: $INSTALL_DIR"
echo ""

# 创建安装目录
echo "[1/6] 创建安装目录..."
mkdir -p "$INSTALL_DIR"
mkdir -p "$INSTALL_DIR/data"
mkdir -p "$INSTALL_DIR/web"

# 复制文件
echo "[2/6] 复制程序文件..."
cp "$CURRENT_DIR/kiro-go" "$INSTALL_DIR/"
chmod +x "$INSTALL_DIR/kiro-go"

# 复制 web 资源
if [[ -d "$CURRENT_DIR/web" ]]; then
    cp -r "$CURRENT_DIR/web"/* "$INSTALL_DIR/web/"
    echo "  ✓ Web 资源已复制"
fi

# 创建系统用户
echo "[3/6] 创建系统用户..."
if ! id -u kiro &>/dev/null; then
    useradd -r -s /bin/false -d "$INSTALL_DIR" kiro
    echo "  ✓ 已创建用户 kiro"
else
    echo "  ⓘ 用户 kiro 已存在"
fi

# 设置权限
echo "[4/6] 设置文件权限..."
chown -R kiro:kiro "$INSTALL_DIR"
chmod 755 "$INSTALL_DIR"
chmod 755 "$INSTALL_DIR/kiro-go"
chmod 700 "$INSTALL_DIR/data"

# 生成并安装 systemd 服务文件
echo "[5/6] 安装 systemd 服务..."
cat > /etc/systemd/system/${SERVICE_NAME}.service <<EOF
[Unit]
Description=Kiro-Go API Service
After=network.target

[Service]
Type=simple
User=kiro
Group=kiro
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/kiro-go
Restart=always
RestartSec=5
Environment="CONFIG_PATH=$INSTALL_DIR/data/config.json"

# 资源限制
LimitNOFILE=65536

# 安全加固
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=$INSTALL_DIR/data

# 日志
StandardOutput=journal
StandardError=journal
SyslogIdentifier=kiro-go

[Install]
WantedBy=multi-user.target
EOF

# 重载并启动服务
echo "[6/6] 启动服务..."
systemctl daemon-reload
systemctl enable ${SERVICE_NAME}
systemctl start ${SERVICE_NAME}

# 等待服务启动
sleep 2

echo ""
echo "========================================"
echo "  ✓ 安装完成！"
echo "========================================"
echo ""
systemctl status ${SERVICE_NAME} --no-pager || true
echo ""
echo "服务信息："
echo "  安装路径: $INSTALL_DIR"
echo "  配置文件: $INSTALL_DIR/data/config.json"
echo "  服务名称: ${SERVICE_NAME}"
echo ""
echo "常用命令："
echo "  查看状态: sudo systemctl status ${SERVICE_NAME}"
echo "  查看日志: sudo journalctl -u ${SERVICE_NAME} -f"
echo "  重启服务: sudo systemctl restart ${SERVICE_NAME}"
echo "  停止服务: sudo systemctl stop ${SERVICE_NAME}"
echo "  启动服务: sudo systemctl start ${SERVICE_NAME}"
echo "  禁用自启: sudo systemctl disable ${SERVICE_NAME}"
echo ""

# 尝试从配置文件读取端口，如果失败则使用默认值 8080
PORT=8080
if [[ -f "$INSTALL_DIR/data/config.json" ]]; then
    PORT=$(grep -oP '"port"\s*:\s*\K\d+' "$INSTALL_DIR/data/config.json" 2>/dev/null || echo "8080")
fi

echo "管理面板: http://localhost:${PORT}/admin"
echo "默认密码: changeme (首次登录后请立即修改)"
echo ""
