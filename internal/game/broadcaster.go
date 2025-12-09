package game

import (
	"encoding/json"
	"sort"
	"take5/internal/model"

	"github.com/gorilla/websocket"
)

// BroadcastState sends the current room state to all players in the room.
func (m *Manager) BroadcastState(r *model.Room) {
	publicPlayers := make(map[string]interface{})
	for id, p := range r.Players {
		publicPlayers[id] = map[string]interface{}{
			"id": p.ID, "name": p.Name, "score": p.Score, "ready": p.Ready,
			"hasSelected": p.SelectedCard != nil, "handSize": len(p.Hand),
			"isOwner": (id == r.OwnerID),
			"isOnline": p.IsOnline,
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

// BroadcastInfo sends a text notification to all players in the room.
func BroadcastInfo(r *model.Room, text string) {
	for _, p := range r.Players {
		if p.Conn != nil {
			p.Conn.WriteJSON(model.Message{Type: "info", Payload: text})
		}
	}
}

// BroadcastStats sends the historical game statistics to all players in the room.
func (m *Manager) BroadcastStats(r *model.Room) {
	stats := m.Store.GetRoomStats(r.ID)
	for _, p := range r.Players {
		if p.Conn != nil {
			p.Conn.WriteJSON(model.Message{Type: "stats", Payload: stats})
		}
	}
}

// BroadcastRoomList sends the list of active rooms to all users in the lobby.
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
