# 端口配置说明

## 问题说明

你可能注意到安装脚本提示的管理面板地址是 `http://localhost:8080/admin`，但实际服务运行在 `8991` 端口。这是因为：

1. **程序默认端口**：8080（硬编码在 `config/config.go` 中）
2. **实际运行端口**：由配置文件 `data/config.json` 中的 `port` 字段决定
3. **当前配置**：8991（你的配置文件中设置的值）

## 端口优先级

```
配置文件 (data/config.json) > 默认值 (8080)
```

## 查看当前端口

```bash
# 方式1: 从配置文件读取
cat data/config.json | jq '.port'

# 方式2: 查看服务日志
journalctl -u kiro-go | grep "starting on"

# 方式3: 检查监听端口
lsof -i -P -n | grep kiro-go
```

## 修改端口

### 方法1: 编辑配置文件（推荐）

```bash
# 编辑配置文件
vim /root/Kiro-Go/data/config.json

# 或 /opt/kiro-go/data/config.json（如果使用 install-service.sh 安装）
```

修改 `port` 字段：
```json
{
  "port": 8991,
  ...
}
```

重启服务：
```bash
systemctl restart kiro-go
```

### 方法2: 环境变量（不推荐）

虽然程序支持 `KIRO_PORT` 环境变量，但当前代码中未实现此功能。建议直接修改配置文件。

## 脚本修复

`install-service.sh` 脚本已修复，现在会自动读取配置文件中的端口号：

```bash
# 动态读取端口
PORT=8080
if [[ -f "$INSTALL_DIR/data/config.json" ]]; then
    PORT=$(grep -oP '"port"\s*:\s*\K\d+' "$INSTALL_DIR/data/config.json" 2>/dev/null || echo "8080")
fi

echo "管理面板: http://localhost:${PORT}/admin"
```

## 访问服务

根据你当前的配置（8991 端口）：

- 管理面板: `http://localhost:8991/admin`
- Claude API: `http://localhost:8991/v1/messages`
- OpenAI API: `http://localhost:8991/v1/chat/completions`

## 防火墙配置

如果需要外部访问，记得开放对应端口：

```bash
# UFW (Ubuntu/Debian)
sudo ufw allow 8991

# firewalld (CentOS/RHEL)
sudo firewall-cmd --add-port=8991/tcp --permanent
sudo firewall-cmd --reload

# iptables
sudo iptables -A INPUT -p tcp --dport 8991 -j ACCEPT
```

## 反向代理配置

### Nginx

```nginx
server {
    listen 80;
    server_name your-domain.com;

    location / {
        proxy_pass http://127.0.0.1:8991;  # 使用实际端口
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_cache_bypass $http_upgrade;
    }
}
```

### Caddy

```caddy
your-domain.com {
    reverse_proxy localhost:8991  # 使用实际端口
}
```

## 文档说明

README 和其他文档中的 8080 是**示例端口**（也是默认值），实际使用时请根据你的配置文件调整。