package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	_ "github.com/mattn/go-sqlite3"
)

// --- 数据库相关 (保持不变，略微精简展示) ---
var db *sql.DB

func initDB() {
	var err error
	db, err = sql.Open("sqlite3", "./take5.db")
	if err != nil {
		log.Fatal(err)
	}
	// 建表语句保持不变...
	sqlStmt := `CREATE TABLE IF NOT EXISTS game_history (id INTEGER PRIMARY KEY AUTOINCREMENT, room_id TEXT, player_name TEXT, score INTEGER, played_at DATETIME DEFAULT CURRENT_TIMESTAMP);`
	db.Exec(sqlStmt)
}

func recordGameResult(roomID string, players map[string]*Player) {
	// 保持不变...
	tx, _ := db.Begin()
	stmt, _ := tx.Prepare("INSERT INTO game_history(room_id, player_name, score) VALUES(?, ?, ?)")
	defer stmt.Close()
	for _, p := range players {
		stmt.Exec(roomID, p.Name, p.Score)
	}
	tx.Commit()
}

type PlayerStat struct {
	Name       string `json:"name"`
	TotalGames int    `json:"totalGames"`
	TotalScore int    `json:"totalScore"`
}

func getRoomStats(roomID string) []PlayerStat {
	// 保持不变...
	rows, err := db.Query(`SELECT player_name, COUNT(*) as games, SUM(score) as total_score FROM game_history WHERE room_id = ? GROUP BY player_name ORDER BY total_score ASC`, roomID)
	if err != nil {
		return []PlayerStat{}
	}
	defer rows.Close()
	var stats []PlayerStat
	for rows.Next() {
		var s PlayerStat
		rows.Scan(&s.Name, &s.TotalGames, &s.TotalScore)
		stats = append(stats, s)
	}
	return stats
}

// --- 核心数据结构 ---

type Card struct {
	Value int `json:"value"`
	Score int `json:"score"`
	OwnerID string `json:"ownerId,omitempty"`
}

type Player struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Conn         *websocket.Conn `json:"-"`
	Hand         []Card          `json:"hand"`
	Score        int             `json:"score"`
	Ready        bool            `json:"ready"`
	SelectedCard *Card           `json:"selectedCard"`
}

type Row struct {
	Cards []Card `json:"cards"`
}

type PlayAction struct {
	PlayerID string
	Card     Card
}

type Room struct {
	ID          string
	OwnerID     string // 房主ID
	Players     map[string]*Player
	Rows        [4]Row
	Status      string
	Deck        []Card
	TurnQueue   []PlayAction
	PendingPlay *PlayAction
	Mutex       sync.Mutex
}

// 用于前端大厅展示的房间摘要
type RoomSummary struct {
	ID          string `json:"id"`
	OwnerName   string `json:"ownerName"`
	PlayerCount int    `json:"playerCount"`
	Status      string `json:"status"`
}

type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

type Action struct {
	Type    string `json:"type"`
	Value   int    `json:"value"`
	Payload string `json:"payload"`
	ID      string `json:"id"`
	RoomID  string `json:"roomId"`
}

// --- 全局管理器 ---

var (
	upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	// 游戏房间管理
	rooms     = make(map[string]*Room)
	roomsLock sync.Mutex

	// 大厅连接管理 (用于广播房间列表)
	lobbyConns = make(map[*websocket.Conn]bool)
	lobbyLock  sync.Mutex
)

// --- 大厅广播逻辑 ---

func broadcastRoomList() {
	list := make([]RoomSummary, 0)
	roomsLock.Lock()
	for id, r := range rooms {
		r.Mutex.Lock()
		ownerName := "未知"
		if owner, ok := r.Players[r.OwnerID]; ok {
			ownerName = owner.Name
		} else if len(r.Players) > 0 {
			// 如果房主跑了，取第一个人显示
			for _, p := range r.Players {
				ownerName = p.Name
				break
			}
		}

		list = append(list, RoomSummary{
			ID:          id,
			OwnerName:   ownerName,
			PlayerCount: len(r.Players),
			Status:      r.Status,
		})
		r.Mutex.Unlock()
	}
	roomsLock.Unlock()

	// 按房间号排序
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })

	msg := Message{Type: "room_list", Payload: list}
	msgBytes, _ := json.Marshal(msg)

	lobbyLock.Lock()
	for conn := range lobbyConns {
		conn.WriteMessage(websocket.TextMessage, msgBytes)
	}
	lobbyLock.Unlock()
}

// --- 游戏逻辑辅助 (精简版，核心逻辑同上一次) ---

func getScore(val int) int {
	if val == 55 {
		return 7
	}
	if val%11 == 0 {
		return 5
	}
	if val%10 == 0 {
		return 3
	}
	if val%5 == 0 {
		return 2
	}
	return 1
}

func (r *Room) initDeck() {
	r.Deck = make([]Card, 0, 104)
	for i := 1; i <= 104; i++ {
		r.Deck = append(r.Deck, Card{Value: i, Score: getScore(i)})
	}
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(r.Deck), func(i, j int) { r.Deck[i], r.Deck[j] = r.Deck[j], r.Deck[i] })
}

func (r *Room) broadcastState() {
	// 构建公共状态
	publicPlayers := make(map[string]interface{})
	for id, p := range r.Players {
		publicPlayers[id] = map[string]interface{}{
			"id": p.ID, "name": p.Name, "score": p.Score, "ready": p.Ready,
			"hasSelected": p.SelectedCard != nil, "handSize": len(p.Hand),
			"isOwner": (id == r.OwnerID), // 告诉前端谁是房主
		}
	}
	stateMap := map[string]interface{}{
		"rows": r.Rows, "status": r.Status, "players": publicPlayers,
		"pendingPlayerId": "", "pendingCard": nil, "ownerId": r.OwnerID,
	}
	if r.PendingPlay != nil {
		stateMap["pendingPlayerId"] = r.PendingPlay.PlayerID
		stateMap["pendingCard"] = r.PendingPlay.Card
	}

	for _, p := range r.Players {
		if p.Conn != nil {
			p.Conn.WriteJSON(Message{Type: "state", Payload: map[string]interface{}{
				"publicState": stateMap, "myHand": p.Hand, "roomId": r.ID,
			}})
		}
	}
	// 每次状态改变（比如人数变化），也广播给大厅更新列表
	go broadcastRoomList()
}

func (r *Room) broadcastInfo(text string) {
	for _, p := range r.Players {
		if p.Conn != nil {
			p.Conn.WriteJSON(Message{Type: "info", Payload: text})
		}
	}
}

func (r *Room) broadcastStats() {
	stats := getRoomStats(r.ID)
	for _, p := range r.Players {
		if p.Conn != nil {
			p.Conn.WriteJSON(Message{Type: "stats", Payload: stats})
		}
	}
}

// --- 游戏流程控制 (保持不变，省略具体实现以节省篇幅，逻辑同上一次回答) ---
// 包含 startGame, prepareTurnResolution, processTurnQueue, handleRowChoice 等方法
// 请确保将上一次回答中的这些方法完整保留在最终代码中。
// 这里只列出 startGame 示意，其他方法必须存在。
func (r *Room) startGame() {
	r.initDeck()
	r.Status = "playing"
	r.TurnQueue = make([]PlayAction, 0)
	r.PendingPlay = nil
	idx := 0
	playingCount := 0
	for _, p := range r.Players {
		if p.Ready {
			p.Hand = r.Deck[idx : idx+10]
			sort.Slice(p.Hand, func(i, j int) bool { return p.Hand[i].Value < p.Hand[j].Value })
			p.Score = 0
			p.SelectedCard = nil
			idx += 10
			playingCount++
		}
	}
	if playingCount < 2 {
		r.Status = "waiting"
		r.broadcastInfo("人数不足，无法开始")
		return
	}
	for i := 0; i < 4; i++ {
		r.Rows[i].Cards = []Card{r.Deck[idx]}
		idx++
	}
	r.broadcastState()
}

// 必须包含 processTurnQueue, handleRowChoice 等方法...
// (为节省 Token，此处假设你已经合并了上一个版本的游戏逻辑代码)
// ... [Insert Game Logic Methods Here] ...
// 为了代码完整性，我将在最后提供一个简化的占位符，实际使用请合并。
func (r *Room) prepareTurnResolution() { /* 同上个版本 */
	r.TurnQueue = make([]PlayAction, 0)
	for id, p := range r.Players {
		if p.SelectedCard != nil {
			card := *p.SelectedCard
			card.OwnerID = id 
			r.TurnQueue = append(r.TurnQueue, PlayAction{PlayerID: id, Card: card})
			p.SelectedCard = nil
		}
	}
	sort.Slice(r.TurnQueue, func(i, j int) bool { return r.TurnQueue[i].Card.Value < r.TurnQueue[j].Card.Value })
	r.processTurnQueue()
}
func (r *Room) processTurnQueue() {
	// 如果处理队列为空，说明本轮出牌/结算全部完成
	if len(r.TurnQueue) == 0 {
		r.PendingPlay = nil

		// 检查所有玩家手牌是否为空
		allEmpty := true
		for _, p := range r.Players {
			if len(p.Hand) > 0 {
				allEmpty = false
				break
			}
		}

		// --- 修复点开始 ---
		// 只要手牌全空，无论当前状态是什么（playing 或 choosing_row），都视为游戏结束
		if allEmpty {
			r.Status = "finished"
			r.broadcastInfo("游戏结束！正在结算积分...")
			recordGameResult(r.ID, r.Players)
			r.broadcastStats()
		} else {
			// 手牌没空，继续下一轮
			r.Status = "playing"
		}
		// --- 修复点结束 ---

		r.broadcastState()
		return
	}

	// 下面是正常的出牌判定逻辑 (保持不变)
	currentPlay := r.TurnQueue[0]
	card := currentPlay.Card
	
	player := r.Players[currentPlay.PlayerID]
	if player != nil {
		newHand := make([]Card, 0)
		for _, c := range player.Hand {
			if c.Value != card.Value {
				newHand = append(newHand, c)
			}
		}
		player.Hand = newHand
	}

	bestRowIdx := -1
	diff := 1000
	for i := 0; i < 4; i++ {
		lastCard := r.Rows[i].Cards[len(r.Rows[i].Cards)-1]
		if card.Value > lastCard.Value {
			d := card.Value - lastCard.Value
			if d < diff {
				diff = d
				bestRowIdx = i
			}
		}
	}

	if bestRowIdx != -1 {
		player := r.Players[currentPlay.PlayerID]
		// 如果该行已经有5张牌，触发爆行
		if len(r.Rows[bestRowIdx].Cards) >= 5 {
			rowScore := 0
			for _, c := range r.Rows[bestRowIdx].Cards {
				rowScore += c.Score
			}
			player.Score += rowScore
			r.Rows[bestRowIdx].Cards = []Card{card}
			r.broadcastInfo(fmt.Sprintf("%s 放置 %d，爆了第 %d 行！扣 %d 分", player.Name, card.Value, bestRowIdx+1, rowScore))
		} else {
			r.Rows[bestRowIdx].Cards = append(r.Rows[bestRowIdx].Cards, card)
		}
		r.TurnQueue = r.TurnQueue[1:]
		r.processTurnQueue()
	} else {
		// 牌太小，需要选行
		r.Status = "choosing_row"
		r.PendingPlay = &currentPlay
		pName := "未知玩家"
		if p, ok := r.Players[currentPlay.PlayerID]; ok {
			pName = p.Name
		}
		r.broadcastInfo(fmt.Sprintf("%s 的牌 %d 太小了，请选择一行收走", pName, card.Value))
		r.broadcastState()
	}
}
func (r *Room) handleRowChoice(playerID string, rowIdx int) { /* 同上个版本 */
	if r.Status != "choosing_row" || r.PendingPlay == nil || r.PendingPlay.PlayerID != playerID || rowIdx < 0 || rowIdx > 3 {
		return
	}
	player := r.Players[playerID]
	rowScore := 0
	for _, c := range r.Rows[rowIdx].Cards {
		rowScore += c.Score
	}
	player.Score += rowScore
	r.Rows[rowIdx].Cards = []Card{r.PendingPlay.Card}
	r.broadcastInfo(fmt.Sprintf("%s 收走第 %d 行，扣 %d 分", player.Name, rowIdx+1, rowScore))
	r.TurnQueue = r.TurnQueue[1:]
	r.PendingPlay = nil
	r.processTurnQueue()
}

// --- HTTP Handlers ---

// 检查房间是否存在
func checkRoomHandler(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("id")
	roomsLock.Lock()
	_, exists := rooms[roomID]
	roomsLock.Unlock()

	json.NewEncoder(w).Encode(map[string]bool{"exists": exists})
}

// 大厅 WebSocket
func handleLobbyWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	lobbyLock.Lock()
	lobbyConns[ws] = true
	lobbyLock.Unlock()

	// 连上立马发一次列表
	go broadcastRoomList()

	defer func() {
		lobbyLock.Lock()
		delete(lobbyConns, ws)
		lobbyLock.Unlock()
		ws.Close()
	}()

	for {
		// 大厅只发不收，或者接收一些简单的Ping
		if _, _, err := ws.ReadMessage(); err != nil {
			break
		}
	}
}

// 游戏 WebSocket
func handleGameWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	var currentRoom *Room
	var currentPlayerID string

	defer func() {
		if currentRoom != nil {
			currentRoom.Mutex.Lock()
			if p, ok := currentRoom.Players[currentPlayerID]; ok {
				// 玩家断开
				p.Conn = nil
				// 可以在这里做更复杂的逻辑：如果房间没人了，是否删除？
				// 简单起见：如果没人了，删除房间
				activeCount := 0
				for _, pl := range currentRoom.Players {
					if pl.Conn != nil {
						activeCount++
					}
				}

				// 真正移除玩家数据（可选，或者保留数据等待重连）
				// 这里我们选择：断开连接不移除数据，除非房主手动踢人或销毁
				// 但为了列表准确，我们可以标记离线

				log.Printf("Player %s disconnected from room %s", p.Name, currentRoom.ID)
			}
			currentRoom.Mutex.Unlock()
			// 广播人数变化
			go broadcastRoomList()
		}
		ws.Close()
	}()

	for {
		var action Action
		err := ws.ReadJSON(&action)
		if err != nil {
			break
		}

		if action.Type == "create_room" {
			// 创建房间逻辑
			uid := action.ID
			roomID := action.RoomID

			roomsLock.Lock()
			if _, exists := rooms[roomID]; exists {
				roomsLock.Unlock()
				ws.WriteJSON(Message{Type: "error", Payload: "房间号已存在"})
				continue
			}
			newRoom := &Room{
				ID: roomID, OwnerID: uid, Players: make(map[string]*Player), Status: "waiting",
			}
			rooms[roomID] = newRoom
			roomsLock.Unlock()

			// 自动执行登录逻辑
			action.Type = "login"
			// Fallthrough to login logic...
		}

		if action.Type == "login" {
			uid := action.ID
			name := action.Payload
			roomID := action.RoomID

			roomsLock.Lock()
			room, exists := rooms[roomID]
			roomsLock.Unlock()

			if !exists {
				ws.WriteJSON(Message{Type: "error", Payload: "房间不存在"})
				continue
			}

			currentRoom = room
			currentPlayerID = uid

			room.Mutex.Lock()

			// 如果房间空了，第一个进来的人变成房主
			activeCount := 0
			for _, p := range room.Players {
				if p.Conn != nil {
					activeCount++
				}
			}
			if activeCount == 0 && len(room.Players) > 0 {
				room.OwnerID = uid
			}

			if existingPlayer, ok := room.Players[uid]; ok {
				existingPlayer.Conn = ws
				existingPlayer.Name = name
				room.Mutex.Unlock()
				room.broadcastState()
				room.broadcastStats()
			} else {
				newPlayer := &Player{ID: uid, Name: name, Conn: ws, Score: 0, Ready: false}
				room.Players[uid] = newPlayer
				// 如果是第一个玩家，设为房主
				if len(room.Players) == 1 {
					room.OwnerID = uid
				}
				room.Mutex.Unlock()
				room.broadcastState()
				room.broadcastStats()
			}
			go broadcastRoomList() // 更新大厅列表人数

		} else if action.Type == "delete_room" {
			// 删除房间
			if currentRoom != nil {
				currentRoom.Mutex.Lock()
				if currentRoom.OwnerID == currentPlayerID {
					// 通知所有人房间被解散
					currentRoom.broadcastInfo("房主解散了房间")
					// 发送特殊消息让前端退回大厅
					for _, p := range currentRoom.Players {
						if p.Conn != nil {
							p.Conn.WriteJSON(Message{Type: "room_closed", Payload: ""})
							p.Conn.Close()
						}
					}
					currentRoom.Mutex.Unlock()

					roomsLock.Lock()
					delete(rooms, currentRoom.ID)
					roomsLock.Unlock()

					currentRoom = nil // 避免 defer 里的逻辑报错
					go broadcastRoomList()
					return // 结束连接循环
				} else {
					currentRoom.Mutex.Unlock()
					ws.WriteJSON(Message{Type: "info", Payload: "只有房主可以解散房间"})
				}
			}

		} else if action.Type == "leave_room" {
			// 玩家主动退出
			if currentRoom != nil {
				currentRoom.Mutex.Lock()
				delete(currentRoom.Players, currentPlayerID)

				// 如果房主走了，移交房权
				if currentRoom.OwnerID == currentPlayerID {
					if len(currentRoom.Players) > 0 {
						for pid := range currentRoom.Players {
							currentRoom.OwnerID = pid
							break
						}
						currentRoom.broadcastInfo("房主离开了，房主移交")
					} else {
						// 没人了，删房间
						currentRoom.Mutex.Unlock() // 先解锁
						roomsLock.Lock()
						delete(rooms, currentRoom.ID)
						roomsLock.Unlock()
						go broadcastRoomList()
						return
					}
				}
				currentRoom.Mutex.Unlock()
				currentRoom.broadcastState()
				go broadcastRoomList()

				currentRoom = nil // 避免 defer 逻辑
				return
			}
		} else {
			// 游戏逻辑
			if currentRoom != nil && currentPlayerID != "" {
				currentRoom.Mutex.Lock()
				player := currentRoom.Players[currentPlayerID]
				if player != nil {
					switch action.Type {
					case "ready":
						if currentRoom.Status == "waiting" {
							player.Ready = true
							// 检查开始
							readyCount := 0
							allReady := true
							for _, p := range currentRoom.Players {
								if p.Ready {
									readyCount++
								} else {
									allReady = false
								}
							}
							if allReady && readyCount >= 2 {
								currentRoom.startGame()
							} else {
								currentRoom.broadcastState()
							}
						}
					case "play_card":
						// ... (同上) ...
						if currentRoom.Status == "playing" && player.SelectedCard == nil {
							valid := false
							var selectC Card
							for _, c := range player.Hand {
								if c.Value == action.Value {
									valid = true
									selectC = c
									break
								}
							}
							if valid {
								player.SelectedCard = &selectC
								allSelected := true
								for _, p := range currentRoom.Players {
									if len(p.Hand) > 0 && p.SelectedCard == nil {
										allSelected = false
										break
									}
								}
								if allSelected {
									currentRoom.prepareTurnResolution()
								} else {
									currentRoom.broadcastState()
								}
							}
						}
					case "choose_row":
						if currentRoom.Status == "choosing_row" {
							currentRoom.handleRowChoice(currentPlayerID, action.Value)
						}
					case "restart":
						if currentRoom.Status == "finished" && currentRoom.OwnerID == currentPlayerID {
							currentRoom.Status = "waiting"
							for _, p := range currentRoom.Players {
								p.Ready = false
								p.Score = 0
								p.Hand = []Card{}
								p.SelectedCard = nil
							}
							currentRoom.broadcastState()
						}
					}
				}
				currentRoom.Mutex.Unlock()
			}
		}
	}
}

func main() {
	initDB()
	defer db.Close()

	http.HandleFunc("/check_room", checkRoomHandler)
	http.HandleFunc("/lobby_ws", handleLobbyWS)
	http.HandleFunc("/ws", handleGameWS)
	http.Handle("/", http.FileServer(http.Dir("./")))

	fmt.Println("Server started on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
