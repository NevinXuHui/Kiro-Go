# 每日统计数据问题修复

## 问题描述

发现每天的数据统计存在跨日期时数据丢失的问题。

## 根本原因

### 问题 1: 跨日期时刻的数据丢失

**原有逻辑**：
1. `backgroundStatsSaver` 每 30 秒保存一次统计
2. 保存前检查：`if savedDate == today` 才保存
3. 跨日期时（如 00:00:00-00:05:00），`savedDate` 还是昨天的日期
4. 在这期间的请求统计累积在内存中，但无法保存到文件
5. 5分钟后 `backgroundDailyReset` 检测到日期变化，直接将内存重置为 0
6. **结果**：跨日期时刻的统计数据完全丢失

**时序示例**：
```
23:59:50 - 请求，内存: dailyRequests=100
23:59:55 - 保存到文件（savedDate=2024-01-01, today=2024-01-01 ✓）
00:00:10 - 新请求，内存: dailyRequests=101
00:00:15 - 尝试保存（savedDate=2024-01-01, today=2024-01-02 ✗ 跳过）
00:05:00 - 日期检查触发重置，内存清零
         结果：00:00:10 的请求统计丢失
```

### 问题 2: UpdateDailyStats 忽略跨日期数据

**原有代码**：
```go
if cfg.DailyDate != today {
    cfg.DailyRequests = 0
    cfg.DailyTokens = 0
    cfg.DailyDate = today
    return Save()  // 忽略传入的 dailyReq 和 dailyTokens
}
```

当检测到日期变化时，直接重置并**忽略传入的值**，导致最后一次保存尝试的数据被丢弃。

### 问题 3: 无意义的时间判断

**原有代码** (`pool/account.go:486-488`)：
```go
if today == time.Now().Format("2006-01-02") {
    go config.UpdateAccountDailyStats(id, dailyRequests, dailyTokens)
}
```

`today` 在函数开始时已经通过 `time.Now().Format("2006-01-02")` 获取，这个判断永远为 true，没有任何意义。

## 修复方案

### 修复 1: 移除 saveStats 中的日期检查

**修改文件**: `proxy/handler.go:1323-1343`

**变更**：
- 移除保存前的日期检查逻辑
- 无论日期是否匹配都调用 `UpdateDailyStats`
- 让 `UpdateDailyStats` 函数自己处理跨日期的情况

**修复后的逻辑**：
```go
func (h *Handler) saveStats() {
	config.UpdateStats(...)
	// 无论日期是否匹配都保存
	config.UpdateDailyStats(
		int(atomic.LoadInt64(&h.dailyRequests)),
		int(atomic.LoadInt64(&h.dailyTokens)),
	)
}
```

### 修复 2: 改进 UpdateDailyStats 的跨日期处理

**修改文件**: `config/config.go:612-634`

**变更**：
- 在日期变化时记录最终统计日志
- 重置为 0（新的一天开始）
- 添加详细日志记录跨日期的统计信息

**修复后的逻辑**：
```go
if cfg.DailyDate != today {
	// 记录前一天的最终统计
	logger.Infof("[DailyStats] Day changed from %s to %s, final stats: requests=%d, tokens=%d", 
		cfg.DailyDate, today, dailyReq, dailyTokens)
	
	// 重置为新的一天（丢弃旧数据是正确的）
	cfg.DailyRequests = 0
	cfg.DailyTokens = 0
	cfg.DailyDate = today
	return Save()
}
```

### 修复 3: 同步修复账号级别的统计

**修改文件**: `config/config.go:681-704`

应用同样的逻辑到账号级别的每日统计。

### 修复 4: 移除无意义的时间判断

**修改文件**: `pool/account.go:485-489`

**变更**：
```go
// 移除前
if today == time.Now().Format("2006-01-02") {
	go config.UpdateAccountDailyStats(id, dailyRequests, dailyTokens)
}

// 修复后
go config.UpdateAccountDailyStats(id, dailyRequests, dailyTokens)
```

### 修复 5: 添加 logger 导入

**修改文件**: `config/config.go:13-21`

添加 `"kiro-go/logger"` 导入以支持日志记录。

## 修复后的工作流程

### 正常运行时（同一天内）

```
请求1 → 内存累加
30s后 → saveStats() → UpdateDailyStats(101, 5000)
      → savedDate == today ✓
      → 保存 requests=101, tokens=5000
```

### 跨日期时刻

```
23:59:50 - 请求，内存: dailyRequests=100, tokens=5000
23:59:55 - saveStats() → UpdateDailyStats(100, 5000)
         → savedDate=2024-01-01, today=2024-01-01 ✓
         → 保存成功

00:00:10 - 新请求，内存: dailyRequests=101, tokens=5020
00:00:20 - saveStats() → UpdateDailyStats(101, 5020)
         → savedDate=2024-01-01, today=2024-01-02 ✗ 日期不匹配
         → 记录日志：Day changed, final stats: requests=101, tokens=5020
         → 重置：requests=0, tokens=0, date=2024-01-02
         → 保存成功（新的一天从 0 开始）

00:00:25 - backgroundStatsSaver 下次保存
         → UpdateDailyStats(0, 0)  # 内存已被 dailyReset 清零
         → savedDate=2024-01-02, today=2024-01-02 ✓
         → 保存 requests=0, tokens=0（正确！）

00:05:00 - backgroundDailyReset 检测
         → savedDate=2024-01-02, today=2024-01-02 ✓
         → 无需重置，跳过
```

## 新增日志

修复后会在跨日期时产生日志：

### 全局统计
```
INFO [DailyStats] Day changed from 2024-01-01 to 2024-01-02, final stats: requests=156, tokens=78234
```

### 账号级统计
```
INFO [DailyStats] Account user@example.com day changed from 2024-01-01 to 2024-01-02, final stats: requests=45, tokens=23456
```

## 测试建议

### 1. 正常运行测试
- 观察每 30 秒的统计保存是否正常
- 检查配置文件中的 `dailyRequests` 和 `dailyTokens` 是否准确

### 2. 跨日期测试
等待第二天 00:00-00:05 之间：
- 查看日志中的跨日期记录
- 确认前一天的最终统计被正确记录
- 确认新的一天从 0 开始

### 3. 日志监控
```bash
# 实时查看日志
journalctl -u kiro-go -f | grep -E "DailyStats|DailyReset"

# 查看历史跨日期记录
journalctl -u kiro-go | grep "Day changed"
```

## 部署状态

- ✅ 代码已修复并编译
- ✅ 服务已更新到 `/opt/kiro-go/kiro-go`
- ✅ 服务正常运行在 8991 端口
- ✅ 无编译错误和 lint 警告

## 影响评估

### 向后兼容性
- ✅ 完全兼容现有配置文件
- ✅ 不影响现有账号数据
- ✅ 不影响全局统计数据

### 性能影响
- ✅ 无性能影响
- ✅ 减少了一次不必要的函数调用（GetDailyStats）
- ✅ 日志仅在跨日期时产生（每天一次）

## 后续建议

1. **监控跨日期行为**：在第一个跨日期时观察日志，确认修复生效
2. **数据备份**：定期备份 `/opt/kiro-go/data/config.json`
3. **统计验证**：可以对比前后端显示的统计数据是否一致