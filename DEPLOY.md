# Kiro-Go 部署指南

本文档提供 Kiro-Go 的多种部署方式，包括 systemd 服务、Docker、源码编译等。

## 目录

- [systemd 服务部署](#systemd-服务部署)
- [Docker 部署](#docker-部署)
- [手动部署](#手动部署)
- [更新升级](#更新升级)
- [故障排查](#故障排查)

---

## systemd 服务部署

推荐用于 Linux 服务器的生产环境部署。

### 快速安装

```bash
# 1. 克隆项目
git clone https://github.com/Quorinex/Kiro-Go.git
cd Kiro-Go

# 2. 编译二进制
go build -o kiro-go

# 3. 运行安装脚本（默认安装到 /opt/kiro-go）
sudo ./install-service.sh

# 4. 自定义安装路径（可选）
sudo ./install-service.sh /usr/local/kiro-go
```

### 安装脚本功能

自动化安装脚本会完成以下操作：

1. ✓ 创建安装目录（默认 `/opt/kiro-go`）
2. ✓ 复制可执行文件和 web 资源
3. ✓ 创建系统用户 `kiro`（非 root 运行，更安全）
4. ✓ 设置正确的文件权限
5. ✓ 生成并安装 systemd 服务文件
6. ✓ 启用开机自启动
7. ✓ 启动服务

### 服务管理命令

```bash
# 查看服务状态
sudo systemctl status kiro-go

# 启动服务
sudo systemctl start kiro-go

# 停止服务
sudo systemctl stop kiro-go

# 重启服务
sudo systemctl restart kiro-go

# 查看实时日志
sudo journalctl -u kiro-go -f

# 查看最近日志（最后 100 行）
sudo journalctl -u kiro-go -n 100

# 禁用开机自启
sudo systemctl disable kiro-go

# 启用开机自启
sudo systemctl enable kiro-go
```

### 配置文件位置

- **程序目录**: `/opt/kiro-go/`
- **配置文件**: `/opt/kiro-go/data/config.json`
- **Web 资源**: `/opt/kiro-go/web/`
- **Systemd 服务文件**: `/etc/systemd/system/kiro-go.service`

### 手动配置 systemd（高级用户）

如果不想使用自动安装脚本，可以手动配置：

```bash
# 1. 创建安装目录
sudo mkdir -p /opt/kiro-go/data
sudo mkdir -p /opt/kiro-go/web

# 2. 复制文件
sudo cp kiro-go /opt/kiro-go/
sudo cp -r web/* /opt/kiro-go/web/

# 3. 创建系统用户
sudo useradd -r -s /bin/false -d /opt/kiro-go kiro

# 4. 设置权限
sudo chown -R kiro:kiro /opt/kiro-go
sudo chmod 755 /opt/kiro-go/kiro-go
sudo chmod 700 /opt/kiro-go/data

# 5. 安装服务文件
sudo cp kiro-go.service /etc/systemd/system/
sudo systemctl daemon-reload

# 6. 启动服务
sudo systemctl enable kiro-go
sudo systemctl start kiro-go
```

### 环境变量配置

编辑服务文件 `/etc/systemd/system/kiro-go.service`，在 `[Service]` 部分添加环境变量：

```ini
[Service]
Environment="CONFIG_PATH=/opt/kiro-go/data/config.json"
Environment="ADMIN_PASSWORD=your_secure_password"
Environment="LOG_LEVEL=info"
```

修改后需要重载配置：

```bash
sudo systemctl daemon-reload
sudo systemctl restart kiro-go
```

### 安全加固说明

systemd 服务文件包含以下安全措施：

- **非 root 运行**: 使用专用系统用户 `kiro` 运行
- **文件系统保护**: `ProtectSystem=strict` 只读挂载系统目录
- **临时目录隔离**: `PrivateTmp=true` 使用私有 /tmp
- **权限提升保护**: `NoNewPrivileges=true` 防止权限提升
- **资源限制**: `LimitNOFILE=65536` 限制文件描述符数量
- **最小写权限**: 仅 `/opt/kiro-go/data` 可写

---

## Docker 部署

### Docker Compose（推荐）

```bash
# 1. 克隆项目
git clone https://github.com/Quorinex/Kiro-Go.git
cd Kiro-Go

# 2. 创建数据目录
mkdir -p data

# 3. 启动服务
docker-compose up -d

# 4. 查看日志
docker-compose logs -f

# 5. 停止服务
docker-compose down
```

### Docker Run

```bash
docker run -d \
  --name kiro-go \
  -p 8080:8080 \
  -e ADMIN_PASSWORD=your_secure_password \
  -v $(pwd)/data:/app/data \
  --restart unless-stopped \
  ghcr.io/quorinex/kiro-go:latest
```

### 自定义构建

```bash
# 构建镜像
docker build -t kiro-go:custom .

# 运行容器
docker run -d \
  --name kiro-go \
  -p 8080:8080 \
  -v $(pwd)/data:/app/data \
  kiro-go:custom
```

---

## 手动部署

### 前置要求

- Go 1.21+
- Git

### 部署步骤

```bash
# 1. 克隆项目
git clone https://github.com/Quorinex/Kiro-Go.git
cd Kiro-Go

# 2. 编译
go build -o kiro-go

# 3. 创建数据目录
mkdir -p data

# 4. 运行（前台）
./kiro-go

# 5. 后台运行（使用 nohup）
nohup ./kiro-go > kiro-go.log 2>&1 &

# 6. 后台运行（使用 screen）
screen -S kiro-go
./kiro-go
# 按 Ctrl+A 然后按 D 分离会话
```

### 配置环境变量

```bash
# 方式一：导出环境变量
export CONFIG_PATH=./data/config.json
export ADMIN_PASSWORD=your_password
./kiro-go

# 方式二：临时设置
CONFIG_PATH=./data/config.json ADMIN_PASSWORD=your_password ./kiro-go

# 方式三：创建 .env 文件（需要使用 dotenv 工具）
echo "CONFIG_PATH=./data/config.json" > .env
echo "ADMIN_PASSWORD=your_password" >> .env
```

---

## 更新升级

### systemd 服务更新

```bash
# 1. 进入项目目录
cd Kiro-Go

# 2. 拉取最新代码
git pull

# 3. 重新编译
go build -o kiro-go

# 4. 停止服务
sudo systemctl stop kiro-go

# 5. 备份当前版本（可选）
sudo cp /opt/kiro-go/kiro-go /opt/kiro-go/kiro-go.backup

# 6. 复制新版本
sudo cp kiro-go /opt/kiro-go/

# 7. 更新 web 资源（如果有变化）
sudo cp -r web/* /opt/kiro-go/web/

# 8. 恢复权限
sudo chown kiro:kiro /opt/kiro-go/kiro-go
sudo chmod 755 /opt/kiro-go/kiro-go

# 9. 启动服务
sudo systemctl start kiro-go

# 10. 验证
sudo systemctl status kiro-go
```

### Docker 更新

```bash
# Docker Compose
docker-compose pull
docker-compose up -d

# Docker Run
docker pull ghcr.io/quorinex/kiro-go:latest
docker stop kiro-go
docker rm kiro-go
docker run -d --name kiro-go ... # 使用之前的运行命令
```

### 零停机更新（高级）

```bash
# 使用 systemd reload 实现平滑重启
sudo systemctl reload-or-restart kiro-go
```

---

## 故障排查

### 查看日志

```bash
# systemd 服务日志
sudo journalctl -u kiro-go -f

# 查看最近的错误
sudo journalctl -u kiro-go -p err -n 50

# 查看完整启动日志
sudo journalctl -u kiro-go -b

# Docker 日志
docker-compose logs -f
docker logs kiro-go -f
```

### 常见问题

#### 1. 服务无法启动

```bash
# 检查服务状态
sudo systemctl status kiro-go

# 检查配置文件
cat /opt/kiro-go/data/config.json

# 检查文件权限
ls -la /opt/kiro-go/

# 手动运行测试
sudo -u kiro /opt/kiro-go/kiro-go
```

#### 2. 端口被占用

```bash
# 查看端口占用
sudo lsof -i :8080
sudo netstat -tlnp | grep 8080

# 修改端口（编辑配置文件或环境变量）
```

#### 3. 权限问题

```bash
# 重新设置权限
sudo chown -R kiro:kiro /opt/kiro-go
sudo chmod 755 /opt/kiro-go/kiro-go
sudo chmod 700 /opt/kiro-go/data
```

#### 4. 无法访问管理面板

```bash
# 检查服务是否运行
sudo systemctl status kiro-go

# 检查防火墙
sudo ufw status
sudo firewall-cmd --list-all

# 开放端口（如需要）
sudo ufw allow 8080
sudo firewall-cmd --add-port=8080/tcp --permanent
sudo firewall-cmd --reload
```

#### 5. 卸载服务

```bash
# 停止并禁用服务
sudo systemctl stop kiro-go
sudo systemctl disable kiro-go

# 删除服务文件
sudo rm /etc/systemd/system/kiro-go.service
sudo systemctl daemon-reload

# 删除程序文件
sudo rm -rf /opt/kiro-go

# 删除用户（可选）
sudo userdel kiro
```

---

## 生产环境建议

### 1. 反向代理（Nginx）

```nginx
server {
    listen 80;
    server_name your-domain.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_cache_bypass $http_upgrade;
        
        # SSE 流式响应
        proxy_buffering off;
        proxy_read_timeout 3600s;
    }
}
```

### 2. SSL/TLS 加密

```bash
# 使用 Certbot 申请免费证书
sudo apt install certbot python3-certbot-nginx
sudo certbot --nginx -d your-domain.com
```

### 3. 日志轮转

```bash
# 创建 logrotate 配置
sudo nano /etc/logrotate.d/kiro-go
```

```
/var/log/kiro-go/*.log {
    daily
    rotate 7
    compress
    delaycompress
    missingok
    notifempty
    create 0640 kiro kiro
    sharedscripts
    postrotate
        systemctl reload kiro-go > /dev/null 2>&1 || true
    endscript
}
```

### 4. 监控告警

```bash
# 配置 systemd 邮件通知
sudo systemctl edit kiro-go

# 添加以下内容
[Service]
OnFailure=status-email@%n.service
```

### 5. 定期备份

```bash
# 创建备份脚本
sudo nano /usr/local/bin/backup-kiro-go.sh
```

```bash
#!/bin/bash
BACKUP_DIR="/backup/kiro-go"
DATE=$(date +%Y%m%d_%H%M%S)
mkdir -p $BACKUP_DIR
tar -czf $BACKUP_DIR/kiro-go-$DATE.tar.gz /opt/kiro-go/data/
find $BACKUP_DIR -name "kiro-go-*.tar.gz" -mtime +7 -delete
```

```bash
# 添加定时任务
sudo crontab -e
0 2 * * * /usr/local/bin/backup-kiro-go.sh
```

---

## 性能优化

### 1. 系统参数调优

```bash
# 编辑 /etc/sysctl.conf
net.core.somaxconn = 1024
net.ipv4.tcp_max_syn_backlog = 2048
net.ipv4.ip_local_port_range = 10000 65535
fs.file-max = 2097152

# 应用配置
sudo sysctl -p
```

### 2. 服务资源限制

编辑 `/etc/systemd/system/kiro-go.service`:

```ini
[Service]
LimitNOFILE=65536
LimitNPROC=4096
CPUQuota=200%
MemoryLimit=2G
```

---

## 支持

- GitHub Issues: https://github.com/Quorinex/Kiro-Go/issues
- 文档: https://github.com/Quorinex/Kiro-Go

---

## 许可证

[MIT](LICENSE)