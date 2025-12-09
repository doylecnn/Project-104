package game

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"take5/internal/database"
	"take5/internal/model"

	"github.com/gorilla/websocket"

	"encoding/json"
)

type Manager struct {
	Rooms      map[string]*model.Room
	RoomsLock  sync.Mutex
	LobbyConns map[*websocket.Conn]bool
	LobbyLock  sync.Mutex
	Store      *database.Store
}

func NewManager(store *database.Store) *Manager {
	return &Manager{
		Rooms:      make(map[string]*model.Room),
		LobbyConns: make(map[*websocket.Conn]bool),
		Store:      store,
	}
}

func (m *Manager) LoadRooms() {
	rooms, err := m.Store.LoadRooms()
	if err != nil {
		fmt.Println("Error loading rooms:", err)
		return
	}
	m.RoomsLock.Lock()
	m.Rooms = rooms
	m.RoomsLock.Unlock()
	fmt.Printf("Loaded %d rooms from database\n", len(rooms))
}

func (m *Manager) BroadcastRoomList() {
	list := make([]model.RoomSummary, 0)
	m.RoomsLock.Lock()
	for id, r := range m.Rooms {
		r.Mutex.Lock()

		ownerName := r.OwnerID
		if owner, ok := r.Players[r.OwnerID]; ok {
			ownerName = owner.Name
		} else if r.OwnerID == "" {
			ownerName = "无房主"
		}

		list = append(list, model.RoomSummary{
			ID:          id,
			OwnerName:   ownerName,
			PlayerCount: len(r.Players),
			Status:      r.Status,
		})
		r.Mutex.Unlock()
	}
	m.RoomsLock.Unlock()

	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })

	msg := model.Message{Type: "room_list", Payload: list}
	msgBytes, _ := json.Marshal(msg)

	m.LobbyLock.Lock()
	for conn := range m.LobbyConns {
		conn.WriteMessage(websocket.TextMessage, msgBytes)
	}
	m.LobbyLock.Unlock()
}

// --- Game Logic Helpers ---

func GetScore(val int) int {
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

func InitDeck(r *model.Room) {
	r.Deck = make([]model.Card, 0, 104)
	for i := 1; i <= 104; i++ {
		r.Deck = append(r.Deck, model.Card{Value: i, Score: GetScore(i)})
	}
	rand.Shuffle(len(r.Deck), func(i, j int) { r.Deck[i], r.Deck[j] = r.Deck[j], r.Deck[i] })
}

func (m *Manager) BroadcastState(r *model.Room) {
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
			if p.SelectedCard != nil {
				payload["mySelectedCard"] = p.SelectedCard.Value
			}

			p.Conn.WriteJSON(model.Message{Type: "state", Payload: payload})
		}
	}

	m.Store.PersistRoom(r)
	go m.BroadcastRoomList()
}

func BroadcastInfo(r *model.Room, text string) {
	for _, p := range r.Players {
		if p.Conn != nil {
			p.Conn.WriteJSON(model.Message{Type: "info", Payload: text})
		}
	}
}

func (m *Manager) BroadcastStats(r *model.Room) {
	stats := m.Store.GetRoomStats(r.ID)
	for _, p := range r.Players {
		if p.Conn != nil {
			p.Conn.WriteJSON(model.Message{Type: "stats", Payload: stats})
		}
	}
}

func (m *Manager) StartGame(r *model.Room) {
	InitDeck(r)
	r.Status = "playing"
	r.TurnQueue = make([]model.PlayAction, 0)
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
		BroadcastInfo(r, "人数不足，无法开始")
		return
	}
	for i := 0; i < 4; i++ {
		r.Rows[i].Cards = []model.Card{r.Deck[idx]}
		idx++
	}
	m.BroadcastState(r)
}

func (m *Manager) PrepareTurnResolution(r *model.Room) {
	r.TurnQueue = make([]model.PlayAction, 0)
	for id, p := range r.Players {
		if p.SelectedCard != nil {
			card := *p.SelectedCard
			card.OwnerID = id
			r.TurnQueue = append(r.TurnQueue, model.PlayAction{PlayerID: id, Card: card})
			p.SelectedCard = nil
		}
	}
	sort.Slice(r.TurnQueue, func(i, j int) bool { return r.TurnQueue[i].Card.Value < r.TurnQueue[j].Card.Value })
	m.ProcessTurnQueue(r)
}

func (m *Manager) ProcessTurnQueue(r *model.Room) {
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
			BroadcastInfo(r, "游戏结束！正在结算积分...")
			m.Store.RecordGameResult(r.ID, r.Players)
			m.BroadcastStats(r)
		} else {
			r.Status = "playing"
		}
		m.BroadcastState(r)
		return
	}

	currentPlay := r.TurnQueue[0]
	card := currentPlay.Card

	player := r.Players[currentPlay.PlayerID]
	if player != nil {
		newHand := make([]model.Card, 0)
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
			r.Rows[bestRowIdx].Cards = []model.Card{card}
			BroadcastInfo(r, fmt.Sprintf("%s 放置 %d，爆了第 %d 行！扣 %d 分", player.Name, card.Value, bestRowIdx+1, rowScore))
		} else {
			r.Rows[bestRowIdx].Cards = append(r.Rows[bestRowIdx].Cards, card)
		}
		r.TurnQueue = r.TurnQueue[1:]
		m.ProcessTurnQueue(r)
	} else {
		// 牌太小，需要选行
		r.Status = "choosing_row"
		r.PendingPlay = &currentPlay
		pName := "未知玩家"
		if p, ok := r.Players[currentPlay.PlayerID]; ok {
			pName = p.Name
		}
		BroadcastInfo(r, fmt.Sprintf("%s 的牌 %d 太小了，请选择一行收走", pName, card.Value))
		m.BroadcastState(r)
	}
}

func (m *Manager) HandleRowChoice(r *model.Room, playerID string, rowIdx int) {
	if r.Status != "choosing_row" || r.PendingPlay == nil || r.PendingPlay.PlayerID != playerID || rowIdx < 0 || rowIdx > 3 {
		return
	}
	player := r.Players[playerID]
	rowScore := 0
	for _, c := range r.Rows[rowIdx].Cards {
		rowScore += c.Score
	}
	player.Score += rowScore
	r.Rows[rowIdx].Cards = []model.Card{r.PendingPlay.Card}
	BroadcastInfo(r, fmt.Sprintf("%s 收走第 %d 行，扣 %d 分", player.Name, rowIdx+1, rowScore))
	r.TurnQueue = r.TurnQueue[1:]
	r.PendingPlay = nil
	m.ProcessTurnQueue(r)
}
