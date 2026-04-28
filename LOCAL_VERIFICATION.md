# 本地环境验证 Checklist

在本地开发环境中逐项执行，完成后打勾。

---

## 1. Go 后端编译验证

```bash
cd backend

# 1.1 生成依赖校验文件
go mod tidy
# ✅ 确认 go.sum 已生成

# 1.2 编译
go build -o tvplayer ./cmd/server
# ✅ 确认无编译错误，tvplayer 二进制文件生成

# 1.3 静态检查 (可选)
go vet ./...
# ✅ 确认无警告
```

## 2. Go 后端运行验证

```bash
# 2.1 启动服务
./tvplayer
# ✅ 确认输出: "🚀 TVPlayer Backend starting on 0.0.0.0:9527"

# 2.2 健康检查
curl http://localhost:9527/ping
# ✅ 期望: {"message":"pong"}

# 2.3 管理员登录
curl -X POST http://localhost:9527/api/v1/admin/login \
  -H "Content-Type: application/json" \
  -d '{"password":"admin123"}'
# ✅ 期望: {"code":0,"data":{"token":"eyJ...","message":"登录成功"}}

# 2.4 用返回的 token 查询统计
TOKEN="<上一步返回的token>"
curl http://localhost:9527/api/v1/stats \
  -H "Authorization: Bearer $TOKEN"
# ✅ 期望: {"code":0,"data":{"total_channels":0,...,"uptime_seconds":...,"memory_mb":...}}

# 2.5 客户端注册
curl -X POST http://localhost:9527/api/v1/client/register \
  -H "Content-Type: application/json" \
  -d '{"name":"测试设备","device_id":"test-001","device_model":"Test","device_os":"Android 14"}'
# ✅ 期望: {"code":0,"data":{"client_id":1,"status":"pending",...}}

# 2.6 审批客户端
curl -X POST http://localhost:9527/api/v1/admin/clients/1/approve \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"max_days":365,"max_streams":2}'
# ✅ 期望: {"code":0,"data":{"message":"已审批通过"}}

# 2.7 添加频道
curl -X POST http://localhost:9527/api/v1/channels \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"测试频道","stream_url":"https://example.com/stream.m3u8","stream_type":"hls","group_id":1}'
# ✅ 期望: {"code":0,"data":{"id":1,"name":"测试频道",...}}

# 2.8 查询频道列表
curl http://localhost:9527/api/v1/channels
# ✅ 期望: 包含上一步添加的频道

# 2.9 查询 EPG (空数据)
curl "http://localhost:9527/api/v1/epg?channel_id=test"
# ✅ 期望: {"code":0,"data":[]}

# 2.10 访问管理面板
curl -s http://localhost:9527/ -o /dev/null -w "%{http_code}"
# ✅ 期望: 302 (重定向到 /admin/)

curl -s http://localhost:9527/admin/ -o /dev/null -w "%{http_code}"
# ✅ 期望: 200

# 停止服务
# Ctrl+C
```

## 3. Android 编译验证

```bash
cd android

# 3.1 编译 Debug APK
./gradlew assembleDebug
# ✅ 确认 BUILD SUCCESSFUL
# ✅ 确认 app/build/outputs/apk/debug/app-debug.apk 生成

# 3.2 Lint 检查 (可选)
./gradlew lint
# ✅ 确认无 Error 级别问题
```

## 4. Android 安装验证

```bash
# 4.1 安装到设备
adb install app/build/outputs/apk/debug/app-debug.apk
# ✅ 确认安装成功

# 4.2 启动应用
adb shell am start -n com.tvplayer.app/.ui.home.MainActivity
# ✅ 确认应用启动，显示授权等待界面

# 4.3 修改服务器地址
# 在设置页面输入电脑的局域网 IP，如 http://192.168.1.100:9527
# ✅ 确认保存成功

# 4.4 设备注册
# 确认应用显示"等待管理员审批"
# 在 Web 面板审批该设备
# ✅ 确认审批后应用自动进入频道列表

# 4.5 频道播放
# 点击任意频道
# ✅ 确认播放器界面正常显示
# ✅ 确认频道信息覆盖层显示
```

## 5. Web 管理面板验证

```bash
# 在浏览器打开 http://localhost:9527/admin/

# 5.1 登录
# 输入密码 admin123
# ✅ 确认登录成功，显示仪表盘

# 5.2 仪表盘
# ✅ 确认 8 个统计卡片有数据
# ✅ 确认"运行时长"显示正确
# ✅ 确认"内存占用"显示正确

# 5.3 频道管理
# ✅ 确认频道列表分页正常
# ✅ 确认搜索功能正常
# ✅ 确认添加/编辑/删除频道正常

# 5.4 设备管理
# ✅ 确认设备列表分页正常
# ✅ 确认审批/拒绝/封禁操作正常
# ✅ 确认令牌管理正常

# 5.5 授权策略
# ✅ 确认设置加载正确 (require_note 已有默认值)
# ✅ 确认保存设置正常

# 5.6 退出登录
# 点击侧边栏"退出登录"
# ✅ 确认跳转到登录页面
# ✅ 确认刷新页面仍显示登录页面
```

## 6. 端到端流程验证

```
完整流程: 安装 → 注册 → 审批 → 播放 → 后台播放 → 设置修改

1. 安装 APK 到手机/TV
2. 首次打开 → 自动注册 → 显示等待审批
3. Web 面板审批设备
4. 手机自动进入频道列表
5. 点击频道播放 → 确认视频正常
6. 按 Home 键进入后台 → 确认通知栏显示"正在播放"
7. 点击通知栏返回应用 → 确认播放继续
8. 进入设置修改服务器地址 → 确认重新加载频道
9. 返回键退出播放器 → 确认双击退出机制
```

---

## 已知的非阻塞项

以下问题不影响核心功能，可后续优化：

- [ ] Android 无网络重试 interceptor
- [ ] ImageView 缺少 contentDescription
- [ ] 部分触控元素 < 48dp
- [ ] loadData() 无重入保护
- [ ] 默认 URL 在 4 处重复
- [ ] Go 默认 admin 密码应强制修改

---

**执行完毕后，项目即可交付。**
