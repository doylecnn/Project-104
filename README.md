# Take 5 (谁是牛头王)

本项目是流行卡牌游戏 **Take 5** (又名 *6 Nimmt!* 或 *谁是牛头王*) 的网页版实现。它采用 WebSocket 实现实时多人游戏体验，后端使用模块化的 Go 语言，前端架构清晰且经过重构。

## 项目概览

本应用是一个 Web 服务器，负责提供游戏界面和处理所有游戏逻辑。玩家可以创建房间，通过大厅加入现有房间，并实时进行游戏。

### 主要功能
*   **实时多人游戏：** 使用 WebSocket 实现服务器和客户端之间的低延迟通信。
*   **大厅系统：** 玩家可以查看活跃房间列表及其状态（等待中/游戏中）。
*   **游戏逻辑：** 完整实现了“Take 5”游戏规则，包括同时选牌、自动放置牌到行以及惩罚计算（收牌）。
*   **数据持久化：** 游戏历史和房间状态保存到 SQLite 数据库 (`take5.db`)，支持崩溃恢复和统计跟踪。
*   **自动化游戏流程：** 如果有足够的玩家在线，游戏结束后会自动重新开始，并有清晰的倒计时。房主可以强制重新开始正在进行的游戏。
*   **玩家状态：** 玩家可以离开房间（断开连接）而不删除其数据，其在线/离线状态会被跟踪并可视化显示。
*   **增强型 UI 反馈：** UI 现在显示游戏面板上每行的总“牛头”数量，并为自动游戏重启提供显眼的倒计时。
*   **响应式 UI：** 移动友好的网页界面，HTML、CSS 和 JS 分离，具有动画交互、清晰的布局和收藏夹图标支持。

## 技术栈

*   **后端：** Go (Golang)
    *   `net/http` 用于 Web 服务器。
    *   `embed` 用于嵌入静态资源。
    *   `github.com/gorilla/websocket` 用于实时通信。
    *   `database/sql` + `github.com/mattn/go-sqlite3` 用于数据持久化。
*   **前端：** HTML5, CSS3, 原生 JavaScript (使用 ES 模块)。
*   **数据库：** SQLite3。

## 构建与运行

### 先决条件
*   Go 1.25.5 或更高版本。
*   GCC ( `go-sqlite3` CGO 编译所需)。

### 步骤
1.  **下载依赖：**
    ```bash
    go mod download
    ```

2.  **构建并运行服务器（单一二进制文件）：**
    ```bash
    go build -o take5.exe main.go
    ./take5.exe
    ```
    构建后，`static/` 目录在运行时不再需要。

3.  **访问游戏：**
    打开浏览器并导航到 `http://localhost:8080`。

## 架构与代码结构

### 后端 (`internal/`)
Go 后端已重构为模块化的 `internal` 包结构。它现在将所有前端静态资源直接嵌入到二进制文件中：

*   **`main.go`**：应用程序的入口点。它利用 `embed.FS` 提供静态内容。它初始化 `database.Store`、`game.Manager` 和 `server.Handler`，然后启动 HTTP 服务器。它现在负责协调各个模块的设置。
*   **`internal/model/`**：包含应用程序共享的数据结构：
    *   `types.go`：定义核心结构体，如 `Card`、`Player`（现在包含 `IsOnline` 状态）、`Room`、`Row` 和 WebSocket 消息格式（`Action`、`Message`、`AutoRestartCountdownPayload`）。
*   **`internal/database/`**：处理 SQLite 的所有数据持久化：
    *   `db.go`：管理 SQLite 连接（`Store` 结构体），并提供 `RecordGameResult`、`GetOrCreateUserID`、`GetRoomStats`、`LoadRooms`、`PersistRoom` 和 `DeleteRoom` 等方法。`rooms` 表现在直接包含 `state_json`。
*   **`internal/game/`**：包含核心游戏逻辑，现在为了更好的组织性而拆分为多个子包：
    *   `manager.go`：管理房间的全局状态、大厅连接和整体游戏环境。它处理从数据库加载房间和基本的房间生命周期。
    *   `rules.go`：封装纯游戏机制，例如 `GetScore`（计算牌点）、`InitDeck`（创建和洗牌）、`DealCards`、`FindBestRow` 和 `CalculateRowScore`。
    *   `room.go`：定义游戏房间内特定操作的方法，包括 `StartGame`、`PrepareTurnResolution`、`ProcessTurnQueue`（现在包含自动重启逻辑和倒计时）、`HandleRowChoice` 和 `ForceRestart`（仅限房主）。
    *   `broadcaster.go`：集中所有 WebSocket 通信逻辑，用于向玩家和大厅发送状态、信息消息和统计数据。
*   **`internal/server/`**：处理 HTTP 和 WebSocket 请求：
    *   `handlers.go`：包含 `check_room`、`lobby_ws` 和 `ws`（游戏 WebSocket）的 HTTP 处理程序。它与 `game.Manager` 和 `database.Store` 集成，以处理客户端操作和更新游戏状态，包括新的 `force_restart` 操作。

### 前端 (`static/`)
前端从 `static/` 目录提供服务，现在使用 ES 模块构建，以提高模块化程度：

*   **`index.html`**：主 HTML 文件。它提供 UI 骨架，现在将 `js/main.js` 作为 ES 模块导入。它包含大厅、游戏面板、游戏消息、控制和玩家手牌的容器。它现在包含用于收藏夹图标的 `<link>` 标签（`favicon.ico`、`favicon.png`）。
*   **`style.css`**：包含所有样式规则。它包括用于区分在线/离线玩家的新样式、用于“确认出牌”按钮禁用状态的新样式，以及用于显示游戏面板上牛头数量的新 `.row-score` 元素。
*   **`static/js/`**：此目录包含重构后的 JavaScript 模块：
    *   `network.js`：管理 WebSocket 连接（`connectLobby`、`connectGame`），处理来自服务器的传入消息，并提供 `sendAction` 用于传出消息。它现在将完整的消息对象传递给 `main.js`。
    *   `state.js`：客户端应用程序所有状态的集中存储（例如 `myId`、`myName`、`currentRoomId`、`currentGameState`、`mySelectedCardValue`）。它导出 getter 和 setter 函数。
    *   `ui.js`：处理所有 DOM 操作和渲染任务。`renderBoard`（现在显示行牛头数量）、`renderHand`（现在接受 `isLocked` 标志和 `onCardClick` 回调）、`renderPlayers`、`renderRoomList`、`updateInstructions`（现在处理倒计时消息）、`updateConfirmButton`、`renderPredictionMessage` 和 `processAnimations` 等函数都在此处。它从 `main.js` 接收数据以渲染 UI。
    *   `main.js`：应用程序的入口点和控制器。它初始化网络和 UI 模块，设置事件监听器（网络和 UI），并协调 `network`、`state` 和 `ui` 模块之间的数据流和操作。它现在正确处理来自 `network.js` 的不同消息类型（包括 `auto_restart_countdown`），并通过将回调传递给 `ui.js` 来管理游戏逻辑流程。

## 开发约定

*   **状态管理：** 服务器仍然是唯一的事实来源。它将完整的公共状态（`Room` 对象）广播给客户端。客户端严格根据此状态渲染，客户端的 `state.js` 模块保存当前视图状态。
*   **并发：**
    *   `Manager.RoomsLock` (sync.Mutex) 保护全局房间映射。
    *   每个 `Room.Mutex` 在并发玩家操作期间保护特定游戏状态。
*   **持久化策略：** 房间状态被序列化为 JSON，并在重要事件 (`PersistRoom`) 发生后直接保存到 `rooms` 表中，从而允许恢复 (`LoadRooms`)。
*   **前端模块化：** 前端现在使用 ES 模块，通过将关注点清晰地分离到不同的文件中，从而提高组织性、可重用性和可维护性。
*   **前端布局：** 游戏 UI 倾向于为动态内容（消息、按钮）使用固定高度的容器，以确保游戏阶段的稳定布局。
