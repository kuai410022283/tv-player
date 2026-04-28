# TVPlayer - Android TV 媒体播放器

模仿电视家风格的 Android TV 媒体播放器，前后端完全分离架构。

## 📐 架构

```
┌─────────────────┐     REST API      ┌──────────────────┐
│   Android TV    │ ◄──────────────► │   Go Backend     │
│   (Frontend)    │                   │   (API Server)   │
│                 │                   │                  │
│ • ExoPlayer     │                   │ • Gin HTTP       │
│ • HLS/FLV/RTMP  │                   │ • SQLite         │
│ • Leanback UI   │                   │ • Stream Proxy   │
│ • D-pad 遥控    │                   │ • M3U Importer   │
│ • 手势控制       │                   │ • Health Check   │
│ • 后台播放服务   │                   │ • Client Auth    │
└─────────────────┘                   └──────────────────┘
                                              │
                                       ┌──────┴──────┐
                                       │  Admin Web   │
                                       │  管理后台     │
                                       └─────────────┘
```

## 🚀 快速开始

### 后端 (Go)

```bash
cd backend

# 安装依赖
go mod tidy

# 编译
go build -o tvplayer ./cmd/server

# 运行
./tvplayer

# 或直接运行
go run ./cmd/server
```

服务启动后：
- API: `http://localhost:9527/api/v1/`
- 管理后台: `http://localhost:9527/admin/`
- 健康检查: `http://localhost:9527/ping`

> ⚠️ **首次部署请修改** `config.yaml` 中的 `auth.secret` 和 `auth.admin_password`

### 管理后台

浏览器打开 `http://localhost:9527` 即可使用 Web 管理面板：

- 🔑 **登录**：输入管理密码（默认 `admin123`，可通过 `config.yaml` 或环境变量 `ADMIN_PASSWORD` 修改）
- 📊 **仪表盘**：频道统计、活跃流监控、运行时长、内存占用
- 📺 **频道管理**：增删改查、收藏、分页、搜索
- 📁 **分组管理**：频道分类
- 📥 **M3U导入**：支持 URL 和粘贴导入
- 📡 **活跃流监控**：实时查看正在代理的流
- 👥 **设备管理**：审批、拒绝、封禁、令牌管理、批量操作
- 📋 **访问日志**：设备操作记录
- ⚙️ **授权策略**：自动审批、默认授权天数、并发流限制

### 前端 (Android TV / 手机)

使用 Android Studio 打开 `android/` 目录：

1. 打开 Android Studio
2. File → Open → 选择 `android/` 目录
3. 等待 Gradle 同步
4. 连接 Android TV 设备或手机（minSdk 21）
5. Run

**配置服务器地址：**
- 首次运行后进入设置页面
- 输入后端服务器地址（如 `http://192.168.1.100:9527`）

## 📡 API 接口

### 认证

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| POST | `/api/v1/admin/login` | 公开 | 管理员登录，返回 JWT Token |
| POST | `/api/v1/client/register` | 公开 | 客户端注册 |
| GET/POST | `/api/v1/client/verify` | 公开 | 验证客户端令牌 |
| GET | `/api/v1/client/me` | Client Token | 客户端查看自己状态 |

### 频道组

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/groups` | 获取所有分组 |
| POST | `/api/v1/groups` | 创建分组 |
| PUT | `/api/v1/groups/:id` | 更新分组 |
| DELETE | `/api/v1/groups/:id` | 删除分组 |

### 频道

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/channels` | 获取频道列表（支持分页、搜索、按分组/收藏过滤） |
| GET | `/api/v1/channels/:id` | 获取单个频道 |
| POST | `/api/v1/channels` | 创建频道 |
| PUT | `/api/v1/channels/:id` | 更新频道 |
| DELETE | `/api/v1/channels/:id` | 删除频道 |
| POST | `/api/v1/channels/:id/favorite` | 切换收藏状态 |

### 流媒体

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/stream/proxy/:id` | 代理流媒体（统一入口） |
| GET | `/api/v1/stream/check/:id` | 检测流状态 |
| GET | `/api/v1/stream/active` | 获取活跃流列表 |

### M3U 源

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/m3u` | 获取M3U源列表 |
| POST | `/api/v1/m3u` | 添加M3U源 |
| POST | `/api/v1/m3u/:id/import` | 从URL导入 |
| POST | `/api/v1/m3u/import-string` | 从字符串导入 |
| DELETE | `/api/v1/m3u/:id` | 删除M3U源 |

### EPG 电子节目单

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/epg?channel_id=xxx` | 查询指定频道的节目单 |

### 历史 & 设置 & 统计

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/history` | 播放历史 |
| POST | `/api/v1/history` | 记录播放 |
| GET | `/api/v1/settings` | 获取设置 |
| POST | `/api/v1/settings` | 设置项 |
| GET | `/api/v1/stats` | 服务器统计（含 uptime、内存） |

### 管理端 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/admin/clients` | 设备列表 |
| GET | `/api/v1/admin/clients/stats` | 设备统计 |
| GET | `/api/v1/admin/clients/:id` | 设备详情 |
| POST | `/api/v1/admin/clients/:id/approve` | 审批通过 |
| POST | `/api/v1/admin/clients/:id/reject` | 拒绝 |
| POST | `/api/v1/admin/clients/:id/ban` | 封禁 |
| POST | `/api/v1/admin/clients/:id/unban` | 解封 |
| POST | `/api/v1/admin/clients/:id/revoke` | 吊销令牌 |
| POST | `/api/v1/admin/clients/:id/regenerate` | 重新生成令牌 |
| POST | `/api/v1/admin/clients/batch` | 批量操作 |
| GET | `/api/v1/admin/clients/:id/logs` | 设备访问日志 |
| GET | `/api/v1/admin/clients/logs` | 全局访问日志 |

## 📺 支持的流媒体格式

| 格式 | 协议 | 说明 |
|------|------|------|
| HLS | `.m3u8` | HTTP Live Streaming，最常用 |
| FLV | `.flv` | Flash Video，低延迟 |
| RTMP | `rtmp://` | Real Time Messaging Protocol |
| RTSP | `rtsp://` | Real Time Streaming Protocol |
| DASH | `.mpd` | Dynamic Adaptive Streaming |
| MP4 | `.mp4` | 直接播放MP4文件 |

## 🎮 操作说明

### Android TV 遥控器

| 按键 | 功能 |
|------|------|
| ↑/↓ | 频道列表中选择频道 |
| ←/→ | 播放器中切换上/下一个频道（循环） |
| OK/确认 | 显示/隐藏频道信息 + EPG |
| 返回 | 双击退出播放 |
| CH+/CH- | 换台 |
| 音量+/音量- | 调节音量 |
| 菜单键 | 进入设置 |

### 手机手势

| 手势 | 功能 |
|------|------|
| 单击 | 显示/隐藏频道信息 |
| 双击 | 播放/暂停 |
| 左右滑动 | 切换频道 |
| 左半屏上下滑动 | 调节亮度 |
| 右半屏上下滑动 | 调节音量 |
| 长按 | 2倍速播放 |

## 🔐 客户端授权管理

### 工作流程

```
客户端启动 → 自动注册设备 → 等待管理员审批 → 获取Token → 正常使用
     │                                      │
     └── 每次连接自动携带Token ──────────────┘
```

### 客户端生命周期

| 状态 | 说明 |
|------|------|
| `pending` | 已注册，等待审批 |
| `approved` | 已授权，可正常使用 |
| `rejected` | 被拒绝，无法使用 |
| `banned` | 被封禁，令牌吊销 |
| `expired` | 授权过期 |

### 授权策略设置

| 设置项 | 说明 | 默认值 |
|--------|------|--------|
| `auto_approve` | 新设备自动审批 | `false` |
| `default_max_streams` | 默认最大并发流 | `2` |
| `default_expire_days` | 默认授权天数 | `365` |
| `require_note` | 注册时要求备注 | `false` |

### Token 认证方式

客户端请求时通过以下方式携带Token：
- Header: `X-Client-Token: <token>`
- Header: `Authorization: Bearer <token>`
- Query: `?token=<token>`（流媒体代理）

## 📱 Android 客户端特性

| 特性 | 说明 |
|------|------|
| 双模式 UI | 自动检测 TV/手机，分别使用 D-pad 和触控布局 |
| 后台播放 | 进入后台时前台服务保活，通知栏显示当前播放 |
| 播放重试 | 失败后指数退避重试（3s → 6s → 12s，最多3次） |
| EPG 节目单 | 播放时显示正在播放和下一节目 |
| 下拉刷新 | 手机模式支持下拉刷新频道列表 |
| 空状态提示 | 无频道/搜索无结果/加载失败有明确提示 |
| 双击退出 | 播放器返回键需双击确认，防误触 |
| 频道循环 | 首尾频道自动循环切换 |
| 手势控制 | 手机模式支持滑动切台、音量、亮度、倍速 |
| 首次引导 | 首次使用显示手势操作说明 |

## 🐳 Docker 部署

```bash
cd backend
docker build -t tvplayer .
docker run -p 9527:9527 -v tvplayer-data:/app/data tvplayer
```

### 生产环境建议

```yaml
# config.yaml - 务必修改以下配置
auth:
  secret: "使用随机生成的长字符串"    # JWT 密钥
  admin_password: "设置强密码"         # 管理员密码
  expire_hours: 720                    # Token 有效期

server:
  host: "0.0.0.0"
  port: 9527
```

建议在前端加 Nginx 反向代理并配置 HTTPS。

## ⚙️ 配置说明

`backend/config.yaml`:

```yaml
server:
  host: "0.0.0.0"           # 监听地址
  port: 9527                 # 监听端口

database:
  path: "./data/tvplayer.db" # SQLite 数据库路径

stream:
  cache_dir: "./data/cache"  # 缓存目录
  max_concurrent: 50         # 最大并发流数
  buffer_size: 4096          # 缓冲区大小(bytes)
  health_check_sec: 30       # 健康检查间隔(秒)

auth:
  secret: "tvplayer-change-this-secret-key"  # JWT 密钥（必须修改）
  expire_hours: 720                          # Token 有效期(小时)
  admin_password: "admin123"                 # 管理员密码（必须修改）
```

环境变量覆盖：
- `CONFIG_PATH` - 配置文件路径
- `ADMIN_PASSWORD` - 管理员密码（优先级高于配置文件）

## 📁 项目结构

```
tv-player/
├── backend/                         # Go 后端
│   ├── cmd/server/main.go          # 入口
│   ├── internal/
│   │   ├── api/
│   │   │   ├── handler.go          # 频道/分组/M3U/EPG/统计/登录 handler
│   │   │   ├── client_handler.go   # 客户端管理 handler
│   │   │   └── router.go           # 路由注册
│   │   ├── config/config.go        # 配置加载
│   │   ├── middleware/auth.go      # 认证中间件 (JWT + Client Token)
│   │   ├── models/models.go        # 数据模型
│   │   └── services/
│   │       ├── database.go         # 数据库初始化 + Schema
│   │       ├── channel_service.go  # 频道/分组/历史/设置/EPG 服务
│   │       ├── client_service.go   # 客户端注册/审批/令牌 服务
│   │       ├── stream_service.go   # 流代理 + 健康检查 + M3U 解析
│   │       └── m3u_importer.go     # M3U 导入
│   ├── web/index.html              # Web 管理后台 (SPA)
│   ├── config.yaml                 # 配置文件
│   ├── go.mod
│   └── Dockerfile
├── android/                         # Android 客户端
│   └── app/src/main/
│       ├── java/com/tvplayer/app/
│       │   ├── data/
│       │   │   ├── api/
│       │   │   │   ├── ApiClient.kt         # Retrofit 单例 + 拦截器
│       │   │   │   ├── TVPlayerApi.kt       # API 接口定义
│       │   │   │   └── ClientAuthManager.kt # 设备注册/验证/令牌管理
│       │   │   ├── model/Models.kt          # 数据模型
│       │   │   └── repository/ChannelRepository.kt  # 数据仓库
│       │   ├── ui/
│       │   │   ├── home/
│       │   │   │   ├── MainActivity.kt      # 首页 (频道列表)
│       │   │   │   ├── ChannelAdapter.kt    # 频道列表适配器
│       │   │   │   └── GroupAdapter.kt      # 分组列表适配器
│       │   │   ├── player/PlayerActivity.kt # 播放器
│       │   │   └── settings/SettingsActivity.kt # 设置页
│       │   ├── service/PlaybackService.kt   # 后台播放服务
│       │   ├── util/
│       │   │   ├── DeviceUtils.kt           # 设备类型检测
│       │   │   ├── FocusHelper.kt           # TV 焦点导航
│       │   │   └── PlayerGestureController.kt # 手势控制
│       │   ├── Prefs.kt                     # SharedPreferences 常量
│       │   └── TVPlayerApp.kt               # Application
│       ├── res/                    # 布局、drawable、values
│       └── AndroidManifest.xml
├── README.md
├── TEST_REPORT.md                  # 全生命周期测试报告
└── LOCAL_VERIFICATION.md           # 本地验证 Checklist
```

## 🧪 测试

项目经过两阶段共 75 项静态测试验证。详见：

- [TEST_REPORT.md](TEST_REPORT.md) - 测试报告
- [LOCAL_VERIFICATION.md](LOCAL_VERIFICATION.md) - 本地环境验证 Checklist

## License

MIT
