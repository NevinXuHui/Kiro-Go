# Systemd 部署指南

## 快速部署

### 1. 一键编译并安装 systemd 服务
```bash
cd /root/Kiro-Go
sudo bash install-systemd.sh
```

安装脚本会自动：
- 在项目目录执行 `go build -o kiro-go .`
- 停止现有服务（如果存在）
- 将服务文件复制到 `/etc/systemd/system/`
- 启用服务（开机自启动）
- 启动服务
- 显示服务状态

> 说明：`install-systemd.sh` 按 `kiro-go.service` 使用仓库路径（默认 `/root/Kiro-Go`）。
> 若生产部署在 `/opt/kiro-go`，请用 `sudo bash install-service.sh`（脚本内也会先编译再安装）。

### 3. 验证服务状态
```bash
systemctl status kiro-go
```

## 常用命令

```bash
# 启动服务
systemctl start kiro-go

# 停止服务
systemctl stop kiro-go

# 重启服务
systemctl restart kiro-go

# 查看服务状态
systemctl status kiro-go

# 查看实时日志
journalctl -u kiro-go -f

# 查看最近的日志
journalctl -u kiro-go -n 100

# 启用开机自启
systemctl enable kiro-go

# 禁用开机自启
systemctl disable kiro-go
```

## 卸载服务

```bash
sudo bash uninstall-systemd.sh
```

## 服务配置

服务配置文件位于 `kiro-go.service`，主要配置项：

- **User**: 运行用户（默认 root）
- **WorkingDirectory**: 工作目录（/root/Kiro-Go）
- **ExecStart**: 可执行文件路径
- **Restart**: 失败时自动重启
- **Environment**: 环境变量配置

### 修改配置

1. 编辑服务文件：
```bash
sudo vim /etc/systemd/system/kiro-go.service
```

2. 重新加载并重启：
```bash
sudo systemctl daemon-reload
sudo systemctl restart kiro-go
```

## 环境变量

在 `kiro-go.service` 中可以配置环境变量：

```ini
Environment="KIRO_PORT=8991"
Environment="KIRO_LOG_LEVEL=info"
Environment="KIRO_PASSWORD=your_admin_password"
```

## 日志管理

### 查看日志
```bash
# 实时日志
journalctl -u kiro-go -f

# 最近 100 条
journalctl -u kiro-go -n 100

# 今天的日志
journalctl -u kiro-go --since today

# 指定时间范围
journalctl -u kiro-go --since "2024-01-01 00:00:00" --until "2024-01-02 00:00:00"
```

### 清理日志
```bash
# 清理 7 天前的日志
sudo journalctl --vacuum-time=7d

# 限制日志大小为 100M
sudo journalctl --vacuum-size=100M
```

## 故障排查

### 服务无法启动
```bash
# 查看详细状态
systemctl status kiro-go -l

# 查看完整日志
journalctl -u kiro-go -xe
```

### 端口占用
```bash
# 检查 8991 端口
lsof -i:8991

# 杀掉占用进程
lsof -ti:8991 | xargs kill -9
```

### 权限问题
确保：
- 可执行文件有执行权限：`chmod +x /root/Kiro-Go/kiro-go`
- 工作目录可访问
- 配置文件可读

## 安全建议

### 1. 使用专用用户运行（推荐）
```bash
# 创建专用用户
useradd -r -s /bin/false kiro

# 修改文件所有权
chown -R kiro:kiro /root/Kiro-Go

# 修改服务文件中的 User
User=kiro
```

### 2. 启用安全加固
在 `kiro-go.service` 中取消注释：
```ini
NoNewPrivileges=true
PrivateTmp=true
```

### 3. 配置文件权限
```bash
chmod 600 /root/Kiro-Go/config.json
```

## 更新服务

```bash
# 1. 停止服务
systemctl stop kiro-go

# 2. 更新代码并编译
cd /root/Kiro-Go
git pull
go build -o kiro-go

# 3. 启动服务
systemctl start kiro-go

# 4. 验证
systemctl status kiro-go
```

## 备份与恢复

### 备份
```bash
# 备份配置和数据
tar -czf kiro-go-backup-$(date +%Y%m%d).tar.gz \
  /root/Kiro-Go/config.json \
  /root/Kiro-Go/accounts.json \
  /etc/systemd/system/kiro-go.service
```

### 恢复
```bash
# 解压备份
tar -xzf kiro-go-backup-YYYYMMDD.tar.gz

# 重新加载服务
systemctl daemon-reload
systemctl restart kiro-go
```