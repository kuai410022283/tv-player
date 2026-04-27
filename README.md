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
│ • 后台播放服务   │                   │ • Health Check   │
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

### 管理后台

浏览器打开 `http://localhost:9527` 即可使用 Web 管理面板：
- 📊 仪表盘：频道统计、活跃流监控
- 📺 频道管理：增删改查、收藏
- 📁 分组管理：频道分类
- 📥 M3U导入：支持URL和粘贴导入
- 📡 实时流监控

### 前端 (Android TV)

使用 Android Studio 打开 `android/` 目录：

1. 打开 Android Studio
2. File → Open → 选择 `android/` 目录
3. 等待 Gradle 同步
4. 连接 Android TV 设备或模拟器
5. Run

**配置服务器地址：**
- 首次运行后进入设置页面
- 输入后端服务器地址（如 `http://192.168.1.100:9527`）

## 📡 API 接口

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

### 其他

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/history` | 播放历史 |
| POST | `/api/v1/history` | 记录播放 |
| GET | `/api/v1/settings` | 获取设置 |
| POST | `/api/v1/settings` | 设置项 |
| GET | `/api/v1/stats` | 服务器统计 |

## 📺 支持的流媒体格式

| 格式 | 协议 | 说明 |
|------|------|------|
| HLS | `.m3u8` | HTTP Live Streaming，最常用 |
| FLV | `.flv` | Flash Video，低延迟 |
| RTMP | `rtmp://` | Real Time Messaging Protocol |
| RTSP | `rtsp://` | Real Time Streaming Protocol |
| DASH | `.mpd` | Dynamic Adaptive Streaming |
| MP4 | `.mp4` | 直接播放MP4文件 |

## 🎮 Android TV 遥控器操作

| 按键 | 功能 |
|------|------|
| ↑/↓ | 频道列表中选择频道 |
| ←/→ | 播放器中切换上/下一个频道 |
| OK/确认 | 显示/隐藏频道信息 |
| 返回 | 退出播放 |
| CH+/CH- | 换台 |

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

### 管理端功能

Web 管理后台 (`/admin`) 提供完整的设备管理：

- **设备列表**：查看所有注册设备，按状态筛选，搜索
- **审批操作**：单个/批量通过、拒绝、封禁、解封
- **令牌管理**：查看令牌预览、重新生成、吊销
- **访问日志**：查看设备登录、播放、心跳等操作记录
- **授权策略**：配置自动审批、默认授权天数、最大并发流数

### 管理端 API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/client/register` | 客户端注册（公开） |
| GET | `/api/v1/client/verify` | 验证令牌（公开） |
| GET | `/api/v1/client/me` | 客户端查看自己状态 |
| GET | `/api/v1/admin/clients` | 设备列表（管理员） |
| GET | `/api/v1/admin/clients/stats` | 设备统计（管理员） |
| GET | `/api/v1/admin/clients/:id` | 设备详情（管理员） |
| POST | `/api/v1/admin/clients/:id/approve` | 审批通过（管理员） |
| POST | `/api/v1/admin/clients/:id/reject` | 拒绝（管理员） |
| POST | `/api/v1/admin/clients/:id/ban` | 封禁（管理员） |
| POST | `/api/v1/admin/clients/:id/unban` | 解封（管理员） |
| POST | `/api/v1/admin/clients/:id/revoke` | 吊销令牌（管理员） |
| POST | `/api/v1/admin/clients/:id/regenerate` | 重新生成令牌（管理员） |
| POST | `/api/v1/admin/clients/batch` | 批量操作（管理员） |
| GET | `/api/v1/admin/clients/:id/logs` | 设备访问日志（管理员） |
| GET | `/api/v1/admin/clients/logs` | 全局访问日志（管理员） |

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

## 🐳 Docker 部署

```bash
cd backend
docker build -t tvplayer .
docker run -p 9527:9527 -v tvplayer-data:/app/data tvplayer
```

## 📁 项目结构

```
tv-player/
├── backend/                    # Go 后端
│   ├── cmd/server/main.go     # 入口
│   ├── internal/
│   │   ├── api/               # HTTP handlers + 路由
│   │   ├── config/            # 配置
│   │   ├── middleware/        # 中间件（认证、日志）
│   │   ├── models/            # 数据模型
│   │   └── services/          # 业务逻辑
│   ├── web/index.html         # 管理后台
│   ├── config.yaml            # 配置文件
│   └── Dockerfile
├── android/                    # Android TV 前端
│   └── app/src/main/
│       ├── java/com/tvplayer/app/
│       │   ├── data/          # 数据层（API、模型、仓库）
│       │   ├── ui/            # 界面（首页、播放器、设置）
│       │   ├── service/       # 后台播放服务
│       │   └── TVPlayerApp.kt
│       ├── res/               # 资源文件
│       └── AndroidManifest.xml
└── README.md
```

## ⚙️ 配置说明

`backend/config.yaml`:

```yaml
server:
  host: "0.0.0.0"
  port: 9527

database:
  path: "./data/tvplayer.db"

stream:
  cache_dir: "./data/cache"
  max_concurrent: 50      # 最大并发流
  buffer_size: 4096       # 缓冲区大小(bytes)
  health_check_sec: 30    # 健康检查间隔(秒)

auth:
  secret: "change-me"     # JWT密钥
  expire_hours: 720       # Token有效期
```

## License

MIT
