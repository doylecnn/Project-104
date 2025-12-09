package main

import (
	"fmt"
	"log"
	"net/http"
	"take5/internal/database"
	"take5/internal/game"
	"take5/internal/server"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	store, err := database.NewStore("./take5.db")
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	gameManager := game.NewManager(store)
	gameManager.LoadRooms()

	handler := server.NewHandler(gameManager, store)

	http.HandleFunc("/check_room", handler.CheckRoomHandler)
	http.HandleFunc("/lobby_ws", handler.HandleLobbyWS)
	http.HandleFunc("/ws", handler.HandleGameWS)
	http.Handle("/", http.FileServer(http.Dir("./static")))

	fmt.Println("Server started on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}