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
				log.Printf("Player %s disconnected from room %s", p.Name, currentRoom.ID)
			}
			currentRoom.Mutex.Unlock()
			go h.Manager.BroadcastRoomList()
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
				room.Mutex.Unlock()
				h.Manager.BroadcastState(room)
				h.Manager.BroadcastStats(room)
			} else {
				newPlayer := &model.Player{ID: uid, Name: name, Conn: ws, Score: 0, Ready: false}
				room.Players[uid] = newPlayer
				if len(room.Players) == 1 && room.OwnerID == "" {
					room.OwnerID = uid
				}
				room.Mutex.Unlock()
				h.Manager.BroadcastState(room)
				h.Manager.BroadcastStats(room)
			}
			go h.Manager.BroadcastRoomList()

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
				if currentRoom.Status == "waiting" || currentRoom.Status == "finished" {
					delete(currentRoom.Players, currentPlayerID)

					if currentRoom.OwnerID == currentPlayerID {
						if len(currentRoom.Players) > 0 {
							for pid, p := range currentRoom.Players {
								currentRoom.OwnerID = pid
								game.BroadcastInfo(currentRoom, fmt.Sprintf("房主离开了，房主移交给 %s", p.Name))
								break
							}
						} else {
							currentRoom.Status = "waiting"
						}
					}
				} else {
					if p, ok := currentRoom.Players[currentPlayerID]; ok {
						game.BroadcastInfo(currentRoom, fmt.Sprintf("%s 暂时离开了游戏 (手牌已保留)", p.Name))
					}
				}

				if len(currentRoom.Players) > 0 {
					h.Manager.BroadcastState(currentRoom)
				} else {
					go h.Manager.BroadcastRoomList()
				}

				currentRoom.Mutex.Unlock()
				currentRoom = nil
				return
			}
		} else {
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
								for _, p := range currentRoom.Players {
									if len(p.Hand) > 0 && p.SelectedCard == nil {
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
