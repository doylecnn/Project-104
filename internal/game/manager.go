package game

import (
	"fmt"
	"sync"
	"take5/internal/database"
	"take5/internal/model"

	"github.com/gorilla/websocket"
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
