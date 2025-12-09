package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"take5/internal/database"
	"take5/internal/game"
	"take5/internal/model"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type Handler struct {
	Manager *game.Manager
	Store   *database.Store
}

func NewHandler(m *game.Manager, s *database.Store) *Handler {
	return &Handler{Manager: m, Store: s}
}

func (h *Handler) CheckRoomHandler(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("id")
	h.Manager.RoomsLock.Lock()
	_, exists := h.Manager.Rooms[roomID]
	h.Manager.RoomsLock.Unlock()
	json.NewEncoder(w).Encode(map[string]bool{"exists": exists})
}

func (h *Handler) HandleLobbyWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	h.Manager.LobbyLock.Lock()
	h.Manager.LobbyConns[ws] = true
	h.Manager.LobbyLock.Unlock()

	go h.Manager.BroadcastRoomList()

	defer func() {
		h.Manager.LobbyLock.Lock()
		delete(h.Manager.LobbyConns, ws)
		h.Manager.LobbyLock.Unlock()
		ws.Close()
	}()

	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			break
		}
	}
}

func (h *Handler) HandleGameWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	var currentRoom *model.Room
	var currentPlayerID string

	defer func() {
		if currentRoom != nil {
			currentRoom.Mutex.Lock()
			if p, ok := currentRoom.Players[currentPlayerID]; ok {
				p.Conn = nil
				p.IsOnline = false // Mark player as offline
				log.Printf("Player %s disconnected from room %s", p.Name, currentRoom.ID)
			}
			currentRoom.Mutex.Unlock()
			// State broadcast will trigger room list update if needed
			if currentRoom != nil && currentRoom.Players[currentPlayerID] != nil {
				h.Manager.BroadcastState(currentRoom)
			}
		}
		ws.Close()
	}()

	for {
		var action model.Action
		err := ws.ReadJSON(&action)
		if err != nil {
			break
		}

		if action.Type == "create_room" {
			name := action.Payload
			uid := h.Store.GetOrCreateUserID(name)
			roomID := action.RoomID

			h.Manager.RoomsLock.Lock()
			if _, exists := h.Manager.Rooms[roomID]; exists {
				h.Manager.RoomsLock.Unlock()
				ws.WriteJSON(model.Message{Type: "error", Payload: "房间号已存在"})
				continue
			}
			newRoom := &model.Room{
				ID: roomID, OwnerID: uid, Players: make(map[string]*model.Player), Status: "waiting",
			}
			for i := 0; i < 4; i++ {
				newRoom.Rows[i].Cards = make([]model.Card, 0)
			}
			h.Manager.Rooms[roomID] = newRoom
			h.Store.PersistRoom(newRoom)
			h.Manager.RoomsLock.Unlock()

			action.Type = "login"
			action.ID = uid
			action.Payload = name
		}

		if action.Type == "login" {
			name := action.Payload
			uid := h.Store.GetOrCreateUserID(name)
			roomID := action.RoomID

			ws.WriteJSON(model.Message{Type: "identity", Payload: map[string]string{"id": uid, "name": name}})

			h.Manager.RoomsLock.Lock()
			room, exists := h.Manager.Rooms[roomID]
			h.Manager.RoomsLock.Unlock()

			if !exists {
				ws.WriteJSON(model.Message{Type: "error", Payload: "房间不存在"})
				continue
			}

			currentRoom = room
			currentPlayerID = uid

			room.Mutex.Lock()

			if existingPlayer, ok := room.Players[uid]; ok {
				existingPlayer.Conn = ws
				existingPlayer.Name = name
				existingPlayer.IsOnline = true // Mark player as online
				room.Mutex.Unlock()
				h.Manager.BroadcastState(room)
				h.Manager.BroadcastStats(room)
			} else {
				newPlayer := &model.Player{ID: uid, Name: name, Conn: ws, Score: 0, Ready: false, IsOnline: true}
				room.Players[uid] = newPlayer
				// OwnerID is set only on room creation, not on first player join.
				room.Mutex.Unlock()
				h.Manager.BroadcastState(room)
				h.Manager.BroadcastStats(room)
			}
			go h.Manager.BroadcastRoomList() // Update lobby after login/reconnect

		} else if action.Type == "delete_room" {
			if currentRoom != nil {
				currentRoom.Mutex.Lock()
				if currentRoom.OwnerID == currentPlayerID {
					game.BroadcastInfo(currentRoom, "房主解散了房间")
					for _, p := range currentRoom.Players {
						if p.Conn != nil {
							p.Conn.WriteJSON(model.Message{Type: "room_closed", Payload: ""})
							p.Conn.Close()
						}
					}
					currentRoom.Mutex.Unlock()

					h.Manager.RoomsLock.Lock()
					delete(h.Manager.Rooms, currentRoom.ID)
					h.Manager.RoomsLock.Unlock()
					h.Store.DeleteRoom(currentRoom.ID)

					currentRoom = nil
					go h.Manager.BroadcastRoomList()
					return
				} else {
					currentRoom.Mutex.Unlock()
					ws.WriteJSON(model.Message{Type: "info", Payload: "只有房主可以解散房间"})
				}
			}

		} else if action.Type == "leave_room" {
			if currentRoom != nil {
				currentRoom.Mutex.Lock()
				if p, ok := currentRoom.Players[currentPlayerID]; ok {
					p.Conn = nil
					p.IsOnline = false // Mark player as offline, do not delete
					game.BroadcastInfo(currentRoom, fmt.Sprintf("%s 离开了房间 (手牌已保留)", p.Name))
				}
				h.Manager.BroadcastState(currentRoom) // Broadcast state to update online status
				currentRoom.Mutex.Unlock()

				currentRoom = nil // Avoid defer logic for this explicit leave
				return
			}
		} else {
			// Game logic
			if currentRoom != nil && currentPlayerID != "" {
				currentRoom.Mutex.Lock()
				player := currentRoom.Players[currentPlayerID]
				if player != nil && player.IsOnline { // Only process actions from online players
					switch action.Type {
					case "ready":
						if currentRoom.Status == "waiting" {
							player.Ready = true
							// Ready logic for initial game start remains
							readyCount := 0
							for _, p := range currentRoom.Players {
								if p.IsOnline && p.Ready {
									readyCount++
								}
							}
							// Only start if at least two online players are ready
							if readyCount >= 2 {
								h.Manager.StartGame(currentRoom)
							} else {
								h.Manager.BroadcastState(currentRoom)
							}
						}
					case "play_card":
						if currentRoom.Status == "playing" && player.SelectedCard == nil {
							valid := false
							var selectC model.Card
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
								// Only consider online players for allSelected check
								for _, p := range currentRoom.Players {
									if p.IsOnline && len(p.Hand) > 0 && p.SelectedCard == nil {
										allSelected = false
										break
									}
								}
								if allSelected {
									h.Manager.PrepareTurnResolution(currentRoom)
								} else {
									h.Manager.BroadcastState(currentRoom)
								}
							}
						}
					case "choose_row":
						if currentRoom.Status == "choosing_row" {
							h.Manager.HandleRowChoice(currentRoom, currentPlayerID, action.Value)
						}
					case "force_restart": // New action for owner to force restart
						if currentRoom.OwnerID == currentPlayerID {
							if !h.Manager.ForceRestart(currentRoom, currentPlayerID) {
								ws.WriteJSON(model.Message{Type: "info", Payload: "无法强制重开，可能人数不足或你不是房主"})
							}
						} else {
							ws.WriteJSON(model.Message{Type: "info", Payload: "只有房主可以强制重开"})
						}
					case "restart":
						if currentRoom.Status == "finished" && currentRoom.OwnerID == currentPlayerID {
							currentRoom.Status = "waiting"
							for _, p := range currentRoom.Players {
								p.Ready = false
								p.Score = 0
								p.Hand = []model.Card{}
								p.SelectedCard = nil
							}
							for i := 0; i < 4; i++ {
								currentRoom.Rows[i].Cards = make([]model.Card, 0)
							}
							h.Manager.BroadcastState(currentRoom)
						}
					}
				}
				currentRoom.Mutex.Unlock()
			}
		}
	}
}
