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

	"github.com/gorilla/websocket"
	_ "github.com/mattn/go-sqlite3"
)

// --- 数据库相关 ---
var db *sql.DB

func initDB() {
	var err error
	db, err = sql.Open("sqlite3", "./take5.db")
	if err != nil {
		log.Fatal(err)
	}
	sqlStmt := `CREATE TABLE IF NOT EXISTS game_history (id INTEGER PRIMARY KEY AUTOINCREMENT, room_id TEXT, player_name TEXT, score INTEGER, played_at DATETIME DEFAULT CURRENT_TIMESTAMP);`
	sqlStmt += `CREATE TABLE IF NOT EXISTS rooms (id TEXT PRIMARY KEY, owner_id TEXT, status TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP);`
	sqlStmt += `CREATE TABLE IF NOT EXISTS room_snapshots (room_id TEXT PRIMARY KEY, state_json TEXT);`
	sqlStmt += `CREATE TABLE IF NOT EXISTS users (name TEXT PRIMARY KEY, id TEXT);`
	db.Exec(sqlStmt)
}

func recordGameResult(roomID string, players map[string]*Player) {
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

func getOrCreateUserID(name string) string {
	var id string
	// 尝试查找现有用户
	err := db.QueryRow("SELECT id FROM users WHERE name = ?", name).Scan(&id)
	if err == nil {
		return id // 找到了，返回旧ID
	}

	// 没找到，生成新ID (简单的 UUID 模拟)
	id = fmt.Sprintf("user_%d_%d", rand.Int(), rand.Int())

	// 插入新用户
	_, err = db.Exec("INSERT INTO users (name, id) VALUES (?, ?)", name, id)
	if err != nil {
		// 如果并发插入导致冲突，重新查询一次
		db.QueryRow("SELECT id FROM users WHERE name = ?", name).Scan(&id)
	}
	return id
}

func getRoomStats(roomID string) []PlayerStat {
	stats := make([]PlayerStat, 0)

	rows, err := db.Query(`SELECT player_name, COUNT(*) as games, SUM(score) as total_score FROM game_history WHERE room_id = ? GROUP BY player_name ORDER BY total_score ASC`, roomID)
	if err != nil {
		return stats
	}
	defer rows.Close()

	for rows.Next() {
		var s PlayerStat
		rows.Scan(&s.Name, &s.TotalGames, &s.TotalScore)
		stats = append(stats, s)
	}
	return stats
}

// --- 核心数据结构 ---

type Card struct {
	Value   int    `json:"value"`
	Score   int    `json:"score"`
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
	Mutex       sync.Mutex `json:"-"`
}

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

	// 大厅连接管理
	lobbyConns = make(map[*websocket.Conn]bool)
	lobbyLock  sync.Mutex
)

func loadRoomsFromDB() {
	rows, err := db.Query("SELECT id, owner_id, status FROM rooms")
	if err != nil {
		log.Println("Error loading rooms:", err)
		return
	}
	defer rows.Close()

	roomsLock.Lock()
	defer roomsLock.Unlock()

	for rows.Next() {
		var id, ownerId, status string
		rows.Scan(&id, &ownerId, &status)

		var stateJSON string
		err := db.QueryRow("SELECT state_json FROM room_snapshots WHERE room_id = ?", id).Scan(&stateJSON)

		newRoom := &Room{}
		if err == nil && stateJSON != "" {
			if err := json.Unmarshal([]byte(stateJSON), newRoom); err != nil {
				log.Printf("Failed to unmarshal room %s: %v", id, err)
				continue
			}
			newRoom.OwnerID = ownerId
			newRoom.ID = id
		} else {
			newRoom = &Room{
				ID: id, OwnerID: ownerId, Status: status,
				Players: make(map[string]*Player),
			}
			// 初始化 Rows 防止 null
			for i := 0; i < 4; i++ {
				newRoom.Rows[i].Cards = make([]Card, 0)
			}
		}
		rooms[id] = newRoom
	}
	log.Printf("Loaded %d rooms from database", len(rooms))
}

func persistRoom(r *Room) {
	data, err := json.Marshal(r)
	if err != nil {
		log.Println("Error marshaling room:", err)
		return
	}
	db.Exec("INSERT OR REPLACE INTO rooms (id, owner_id, status) VALUES (?, ?, ?)", r.ID, r.OwnerID, r.Status)
	db.Exec("INSERT OR REPLACE INTO room_snapshots (room_id, state_json) VALUES (?, ?)", r.ID, string(data))
}

func deleteRoomDB(roomID string) {
	db.Exec("DELETE FROM rooms WHERE id = ?", roomID)
	db.Exec("DELETE FROM room_snapshots WHERE room_id = ?", roomID)
}

// --- 大厅广播逻辑 ---

func broadcastRoomList() {
	list := make([]RoomSummary, 0)
	roomsLock.Lock()
	for id, r := range rooms {
		r.Mutex.Lock()

		ownerName := r.OwnerID
		if owner, ok := r.Players[r.OwnerID]; ok {
			ownerName = owner.Name
		} else if r.OwnerID == "" {
			ownerName = "无房主"
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

	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })

	msg := Message{Type: "room_list", Payload: list}
	msgBytes, _ := json.Marshal(msg)

	lobbyLock.Lock()
	for conn := range lobbyConns {
		conn.WriteMessage(websocket.TextMessage, msgBytes)
	}
	lobbyLock.Unlock()
}

// --- 游戏逻辑辅助 ---

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
	rand.Shuffle(len(r.Deck), func(i, j int) { r.Deck[i], r.Deck[j] = r.Deck[j], r.Deck[i] })
}

func (r *Room) broadcastState() {
	publicPlayers := make(map[string]interface{})
	for id, p := range r.Players {
		publicPlayers[id] = map[string]interface{}{
			"id": p.ID, "name": p.Name, "score": p.Score, "ready": p.Ready,
			"hasSelected": p.SelectedCard != nil, "handSize": len(p.Hand),
			"isOwner": (id == r.OwnerID),
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
			payload := map[string]interface{}{
				"publicState": stateMap,
				"myHand":      p.Hand,
				"roomId":      r.ID,
			}
			// 如果玩家已选牌，告诉他选的是哪张，以便前端恢复状态
			if p.SelectedCard != nil {
				payload["mySelectedCard"] = p.SelectedCard.Value
			}

			p.Conn.WriteJSON(Message{Type: "state", Payload: payload})
		}
	}

	persistRoom(r)
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

func (r *Room) prepareTurnResolution() {
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
	if len(r.TurnQueue) == 0 {
		r.PendingPlay = nil
		allEmpty := true
		for _, p := range r.Players {
			if len(p.Hand) > 0 {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			r.Status = "finished"
			r.broadcastInfo("游戏结束！正在结算积分...")
			recordGameResult(r.ID, r.Players)
			r.broadcastStats()
		} else {
			r.Status = "playing"
		}
		r.broadcastState()
		return
	}

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

func (r *Room) handleRowChoice(playerID string, rowIdx int) {
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

func checkRoomHandler(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("id")
	roomsLock.Lock()
	_, exists := rooms[roomID]
	roomsLock.Unlock()
	json.NewEncoder(w).Encode(map[string]bool{"exists": exists})
}

func handleLobbyWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	lobbyLock.Lock()
	lobbyConns[ws] = true
	lobbyLock.Unlock()

	go broadcastRoomList()

	defer func() {
		lobbyLock.Lock()
		delete(lobbyConns, ws)
		lobbyLock.Unlock()
		ws.Close()
	}()

	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			break
		}
	}
}

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
				p.Conn = nil
				log.Printf("Player %s disconnected from room %s", p.Name, currentRoom.ID)
			}
			currentRoom.Mutex.Unlock()
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
			name := action.Payload
			uid := getOrCreateUserID(name) // 获取唯一绑定的 ID
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
			for i := 0; i < 4; i++ {
				newRoom.Rows[i].Cards = make([]Card, 0)
			}
			rooms[roomID] = newRoom
			persistRoom(newRoom)
			roomsLock.Unlock()

			// 自动转为登录流程，更新 action 数据
			action.Type = "login"
			action.ID = uid // 强制使用后端生成的 ID
			action.Payload = name
		}

		if action.Type == "login" {
			name := action.Payload
			uid := getOrCreateUserID(name) // 获取唯一绑定的 ID
			roomID := action.RoomID

			ws.WriteJSON(Message{Type: "identity", Payload: map[string]string{"id": uid, "name": name}})

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

			if existingPlayer, ok := room.Players[uid]; ok {
				existingPlayer.Conn = ws
				existingPlayer.Name = name
				room.Mutex.Unlock()
				room.broadcastState()
				room.broadcastStats()
			} else {
				newPlayer := &Player{ID: uid, Name: name, Conn: ws, Score: 0, Ready: false}
				room.Players[uid] = newPlayer
				if len(room.Players) == 1 && room.OwnerID == "" {
					room.OwnerID = uid
				}
				room.Mutex.Unlock()
				room.broadcastState()
				room.broadcastStats()
			}
			go broadcastRoomList()

		} else if action.Type == "delete_room" {
			if currentRoom != nil {
				currentRoom.Mutex.Lock()
				if currentRoom.OwnerID == currentPlayerID {
					currentRoom.broadcastInfo("房主解散了房间")
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
					deleteRoomDB(currentRoom.ID)

					currentRoom = nil
					go broadcastRoomList()
					return
				} else {
					currentRoom.Mutex.Unlock()
					ws.WriteJSON(Message{Type: "info", Payload: "只有房主可以解散房间"})
				}
			}

		} else if action.Type == "leave_room" {
			// 玩家主动退出
			if currentRoom != nil {
				currentRoom.Mutex.Lock()
				if currentRoom.Status == "waiting" || currentRoom.Status == "finished" {
					delete(currentRoom.Players, currentPlayerID)

					// 只有真正删除了玩家，才需要移交房主权限
					if currentRoom.OwnerID == currentPlayerID {
						if len(currentRoom.Players) > 0 {
							for pid, p := range currentRoom.Players {
								currentRoom.OwnerID = pid
								currentRoom.broadcastInfo(fmt.Sprintf("房主离开了，房主移交给 %s", p.Name))
								break
							}
						} else {
							// 房间空了，状态重置
							currentRoom.Status = "waiting"
						}
					}
				} else {
					// 游戏中退出：不删除数据，仅通知
					// defer 里的逻辑会将 p.Conn 设为 nil
					if p, ok := currentRoom.Players[currentPlayerID]; ok {
						currentRoom.broadcastInfo(fmt.Sprintf("%s 暂时离开了游戏 (手牌已保留)", p.Name))
					}
				}

				// 如果还有人，广播状态（这也会触发 broadcastRoomList）
				if len(currentRoom.Players) > 0 {
					currentRoom.broadcastState()
				} else {
					// 如果没人了，手动触发一次列表更新，确保大厅显示人数为0或处理空房间
					go broadcastRoomList()
				}

				currentRoom.Mutex.Unlock()

				currentRoom = nil // 避免 defer 里的逻辑处理
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
							// 重置时也需要清空并初始化 Rows
							for i := 0; i < 4; i++ {
								currentRoom.Rows[i].Cards = make([]Card, 0)
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
	loadRoomsFromDB()

	http.HandleFunc("/check_room", checkRoomHandler)
	http.HandleFunc("/lobby_ws", handleLobbyWS)
	http.HandleFunc("/ws", handleGameWS)
	// Serve static files (HTML, CSS, JS) from the "static" directory
	http.Handle("/", http.FileServer(http.Dir("./static")))

	fmt.Println("Server started on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
