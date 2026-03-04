# 望舒管理端（网页版）

独立的Web管理程序，提供Web界面和REST API。

## 功能特性

- 💬 实时聊天界面
- 🖥️ 望舒实例管理（启动/停止/重启）
- 📋 会话管理
- 📝 任务管理
- ⏰ 定时任务管理
- ⚙️ 配置管理
- 🔌 完整的REST API
- 🌐 支持多个Web Channel监听

## 更新日志

### v0.1.0 (多通道支持)

**新功能：**

- `agents`、`providers`、`channels` 全部改为 `map` 类型，支持自定义命名
- 支持配置多个 Web Channel，程序会自动监听所有启用的本地 Web Channel 端口
- Token 验证支持多个 Web Channel 的 token
- 前端配置页面支持动态添加/删除 Channel

**配置示例：**

```json
{
    "agents": {
        "myAgent": {
            "workspace": "~/.wangshu/workspace",
            "provider": "myProvider",
            "model": "qwen3-max",
            "temperature": 0.7
        }
    },
    "providers": {
        "myProvider": {
            "type": "openai",
            "api_key": "sk-your-api-key",
            "base_url": ""
        }
    },
    "channels": {
        "webLocal1": {
            "type": "web",
            "enabled": true,
            "agent": "myAgent",
            "host_address": "localhost:8080",
            "token": "token1"
        },
        "webLocal2": {
            "type": "web",
            "enabled": true,
            "agent": "myAgent",
            "host_address": "localhost:9090",
            "token": "token2"
        },
        "feishuBot": {
            "type": "feishu",
            "enabled": false,
            "agent": "myAgent",
            "app_id": "your-app-id",
            "app_secret": "your-app-secret"
        }
    },
    "skill": {
        "global_path": "~/.wangshu/skills",
        "builtin_path": "./skills"
    }
}
```

**注意事项：**
- Web Channel 只监听本地地址（`localhost:`、`127.0.0.1:` 或 `:` 开头）
- 多个 Web Channel 可以使用不同的 token
- 旧版配置文件需要手动迁移到新结构

## 快速开始

### 下载

从 [Releases](https://github.com/yockii/wangshu-manager/releases) 页面下载对应平台的可执行文件。

### 运行

```bash
# 使用默认配置路径（~/.wangshu/config.json）
./wangshu-web-admin

# 指定配置文件路径
./wangshu-web-admin /path/to/config.json
```

### 访问

打开浏览器访问 `http://localhost:8080?token=your-token`

注意：端口号和 token 取决于配置文件中 Web Channel 的设置。

## API文档

### 认证

所有API请求都需要在URL参数或HTTP Header中提供token：

```bash
# URL参数
curl http://localhost:8080/api/sessions?token=my-secret-token

# HTTP Header
curl -H "Authorization: my-secret-token" http://localhost:8080/api/sessions
```

### WebSocket

**连接：**

```javascript
const ws = new WebSocket('ws://localhost:8080/ws?token=my-secret-token');

ws.onmessage = function(event) {
    const data = JSON.parse(event.data);
    console.log(data);
};

ws.send(JSON.stringify({
    type: 'message',
    content: 'Hello, Wangshu!'
}));
```

### REST API

#### 1. 会话管理

**获取会话列表**

```bash
GET /api/sessions?agent=myAgent
```

**响应：**

```json
{
    "sessions": [
        {
            "chat_id": "web",
            "channel": "web",
            "messages": [
                {
                    "role": "user",
                    "content": "Hello",
                    "timestamp": "2024-01-01T00:00:00Z"
                }
            ]
        }
    ]
}
```

#### 2. 实例管理

**获取实例状态**

```bash
GET /api/instance
```

**响应：**

```json
{
    "status": {
        "running": true,
        "pid": 12345,
        "executable": "/path/to/wangshu",
        "config_path": "~/.wangshu/config.json",
        "start_time": "2024-01-01T00:00:00Z",
        "uptime": "1h30m",
        "auto_started": false
    }
}
```

**启动实例**

```bash
POST /api/instance?action=start
```

**响应：**

```json
{
    "success": true,
    "message": "Instance started successfully"
}
```

**停止实例**

```bash
POST /api/instance?action=stop
```

**响应：**

```json
{
    "success": true,
    "message": "Instance stopped successfully"
}
```

**重启实例**

```bash
POST /api/instance?action=restart
```

**响应：**

```json
{
    "success": true,
    "message": "Instance restarted successfully"
}
```

#### 3. 任务管理

**获取任务列表**

```bash
GET /api/tasks?agent=myAgent
```

**响应：**

```json
{
    "tasks": [
        {
            "id": "task-1",
            "name": "任务名称",
            "description": "任务描述",
            "priority": "high",
            "status": "running",
            "channel": "web",
            "chat_id": "web"
        }
    ]
}
```

#### 4. 定时任务管理

**获取定时任务列表**

```bash
GET /api/cron?agent=myAgent
```

**响应：**

```json
{
    "cronJobs": [
        {
            "id": "cron-1",
            "schedule": "0 9 * * *",
            "description": "任务描述",
            "status": "enabled",
            "channel": "web",
            "chat_id": "web"
        }
    ]
}
```

#### 5. 配置管理

**获取配置**

```bash
GET /api/config
```

**响应：**

```json
{
    "config": {
        "agents": {...},
        "providers": {...},
        "channels": {...}
    }
}
```

**更新配置**

```bash
PUT /api/config
Content-Type: application/json

{
    "agents": {...},
    "providers": {...},
    "channels": {...}
}
```

## 命令行参数

```
第一个参数（可选）
    配置文件路径（默认: ~/.wangshu/config.json）
```

示例：
```bash
# 使用默认配置路径
./wangshu-web-admin

# 指定配置文件路径
./wangshu-web-admin /path/to/config.json
```

监听地址和 token 现在从配置文件的 `channels` 中读取，每个启用的 Web Channel 都会启动一个监听服务。

## 架构说明

Web管理程序作为独立的服务运行：

1. **WebSocket服务器** - 接收来自望舒 Web Channel的连接
2. **HTTP API** - 提供管理功能
3. **静态文件服务** - 提供Web界面

与望舒主程序的交互：

```
望舒主程序 (Web Channel)
    ↓ WebSocket
Web管理程序
    ↓ HTTP/WebSocket
浏览器/第三方客户端
```

## 开发自己的界面

Web管理程序提供完整的REST API和WebSocket接口，你可以：

1. 使用REST API获取和管理数据
2. 使用WebSocket进行实时通信
3. 开发自己的前端界面（React、Vue、移动端等）

示例代码：

```javascript
// 连接WebSocket
const ws = new WebSocket('ws://localhost:8080/ws?token=my-token');

// 发送消息
ws.send(JSON.stringify({
    type: 'message',
    content: 'Hello'
}));

// 接收消息
ws.onmessage = (event) => {
    const data = JSON.parse(event.data);
    console.log(data);
};

// 获取会话列表
fetch('http://localhost:8080/api/sessions?agent=myAgent&token=my-token')
    .then(res => res.json())
    .then(data => console.log(data));
```

## 许可证

MIT License
