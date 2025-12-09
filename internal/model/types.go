package model

import (
	"sync"

	"github.com/gorilla/websocket"
)

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

type PlayerStat struct {
	Name       string `json:"name"`
	TotalGames int    `json:"totalGames"`
	TotalScore int    `json:"totalScore"`
}
