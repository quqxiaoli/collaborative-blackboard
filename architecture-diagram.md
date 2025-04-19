# 多人实时黑板系统架构图

```mermaid
graph TB
    %% 颜色定义
    classDef client fill:#D4F1F9,stroke:#05A4C3,color:#333
    classDef server fill:#E7F2FA,stroke:#6BAED6,color:#333
    classDef websocket fill:#D8E8D4,stroke:#78C679,color:#333
    classDef redis fill:#FED8B1,stroke:#FD8D3C,color:#333
    classDef database fill:#DBCCE0,stroke:#9E9AC8,color:#333
    classDef models fill:#FDF1DD,stroke:#FDAE61,color:#333

    %% 客户端层
    Client1[用户 1 浏览器<br>前端应用] :::client
    Client2[用户 2 浏览器<br>前端应用] :::client
    Client3[用户 3 浏览器<br>前端应用] :::client
    
    %% 服务器层
    GinServer[Go Gin 服务器<br>HTTP/WebSocket API] :::server
    
    %% WebSocket层
    WSHub[WebSocket Hub<br>连接管理器] :::websocket
    subgraph OTLogic ["操作转换 (OT) 逻辑"]
        direction TB
        OT[操作转换引擎] :::websocket
        ActionProcessor[操作处理器] :::websocket
    end
    
    %% Redis层
    subgraph RedisServices ["Redis实时数据服务"]
        direction TB
        RedisPubSub[发布订阅系统] :::redis
        RedisState[房间状态缓存] :::redis
        RedisOps[操作队列] :::redis
    end
    
    %% 数据库层
    MySQL[(MySQL数据库)] :::database
    
    %% 主要模型层
    subgraph Models ["数据模型"]
        direction LR
        User[用户模型] :::models
        Room[房间模型] :::models
        Action[操作模型] :::models
        Snapshot[快照模型] :::models
    end
    
    %% 系统流程关系
    Client1 & Client2 & Client3 -->|1. WebSocket 连接| GinServer
    GinServer -->|2. 升级连接| WSHub
    
    WSHub -->|3a. 注册客户端| WSHub
    WSHub -->|3b. 加载初始状态| RedisState
    RedisState -->|3c. 发送快照| WSHub
    
    Client1 -->|4a. 发送绘图操作| WSHub
    WSHub -->|4b. 操作转换| OT
    OT -->|4c. 解决冲突| ActionProcessor
    ActionProcessor -->|4d. 操作处理| RedisState
    ActionProcessor -->|4e. 批量存储| ActionProcessor
    ActionProcessor -->|5a. 发布操作| RedisPubSub
    
    RedisPubSub -->|5b. 操作广播| WSHub
    WSHub -->|5c. 分发消息| Client2 & Client3
    
    ActionProcessor -.->|6a. 定期批量保存| MySQL
    ActionProcessor -.->|6b. 快照生成| MySQL
    
    MySQL -.->|7a. 持久化| Models
    
    %% 后台快照处理
    subgraph SnapshotTask ["快照任务"]
        SnapshotGenerator[快照生成器] :::websocket
    end
    
    SnapshotGenerator -.->|定期检查活跃房间| RedisState
    SnapshotGenerator -.->|生成状态快照| MySQL
```

## 系统组件说明

### 客户端层
- **浏览器前端应用**: 多个用户通过浏览器访问协作黑板，可实时查看和编辑共享画板内容

### 服务器层
- **Go Gin 服务器**: 处理HTTP请求、用户认证、WebSocket连接升级
- **WebSocket Hub**: 核心实时通信组件，负责管理客户端连接、消息路由和分发
- **操作转换(OT)逻辑**: 处理并发操作冲突，确保所有客户端最终状态一致性

### 数据管理层
- **Redis服务**:
  - **发布订阅系统**: 向所有相关客户端广播画板操作
  - **房间状态缓存**: 维护每个房间的实时画板状态
  - **操作队列**: 存储最近操作，用于操作转换和冲突解决
- **MySQL数据库**: 持久化存储用户信息、房间数据、操作历史和画板快照

### 数据模型
- **用户模型(User)**: 存储用户信息和认证数据
- **房间模型(Room)**: 表示协作画板实例，包含元信息
- **操作模型(Action)**: 记录用户在画板上的每个操作（绘制、擦除等）
- **快照模型(Snapshot)**: 存储特定时间点的完整画板状态

## 数据流程

1. **连接建立**:
   - 用户通过浏览器连接WebSocket服务器
   - Gin服务器验证用户权限并升级连接
   - WebSocket Hub管理客户端连接

2. **初始状态同步**:
   - 新客户端连接后请求画板初始状态
   - 系统从Redis或MySQL加载最新快照
   - 客户端接收并渲染初始画板状态

3. **实时操作处理**:
   - 客户端发送绘图/擦除操作
   - WebSocket Hub接收操作并传递给OT引擎
   - OT引擎解决潜在冲突并确定最终操作
   - 操作应用到Redis中的实时状态
   - 通过Redis Pub/Sub广播操作到所有相关客户端
   - 其他客户端应用操作并更新本地视图

4. **持久化**:
   - 操作批量保存到MySQL数据库
   - 后台任务定期生成画板状态快照
   - 快照用于新用户加入时的高效状态恢复

5. **冲突解决**:
   - 使用基于版本号和时间戳的操作转换算法
   - 处理并发操作的一致性确保所有用户看到相同的最终状态

## 技术栈

- **前端**: HTML5 Canvas, WebSocket API
- **后端**: Go, Gin框架
- **实时通信**: WebSocket, Redis Pub/Sub
- **数据存储**: Redis(缓存), MySQL(持久化)
- **ORM**: GORM

