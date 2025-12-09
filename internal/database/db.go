package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"take5/internal/model"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	sqlStmt := `CREATE TABLE IF NOT EXISTS game_history (id INTEGER PRIMARY KEY AUTOINCREMENT, room_id TEXT, player_name TEXT, score INTEGER, played_at DATETIME DEFAULT CURRENT_TIMESTAMP);`
	sqlStmt += `CREATE TABLE IF NOT EXISTS rooms (id TEXT PRIMARY KEY, owner_id TEXT, status TEXT, state_json TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP);`
	sqlStmt += `CREATE TABLE IF NOT EXISTS users (name TEXT PRIMARY KEY, id TEXT);`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() {
	if s.db != nil {
		s.db.Close()
	}
}

func (s *Store) RecordGameResult(roomID string, players map[string]*model.Player) {
	tx, _ := s.db.Begin()
	stmt, _ := tx.Prepare("INSERT INTO game_history(room_id, player_name, score) VALUES(?, ?, ?)")
	defer stmt.Close()
	for _, p := range players {
		stmt.Exec(roomID, p.Name, p.Score)
	}
	tx.Commit()
}

func (s *Store) GetOrCreateUserID(name string) string {
	var id string
	err := s.db.QueryRow("SELECT id FROM users WHERE name = ?", name).Scan(&id)
	if err == nil {
		return id
	}

	id = fmt.Sprintf("user_%d_%d", rand.Int(), rand.Int())
	_, err = s.db.Exec("INSERT INTO users (name, id) VALUES (?, ?)", name, id)
	if err != nil {
		s.db.QueryRow("SELECT id FROM users WHERE name = ?", name).Scan(&id)
	}
	return id
}

func (s *Store) GetRoomStats(roomID string) []model.PlayerStat {
	stats := make([]model.PlayerStat, 0)

	rows, err := s.db.Query(`SELECT player_name, COUNT(*) as games, SUM(score) as total_score FROM game_history WHERE room_id = ? GROUP BY player_name ORDER BY total_score ASC`, roomID)
	if err != nil {
		return stats
	}
	defer rows.Close()

	for rows.Next() {
		var st model.PlayerStat
		rows.Scan(&st.Name, &st.TotalGames, &st.TotalScore)
		stats = append(stats, st)
	}
	return stats
}

func (s *Store) LoadRooms() (map[string]*model.Room, error) {
	rooms := make(map[string]*model.Room)
	rows, err := s.db.Query("SELECT id, owner_id, status, state_json FROM rooms")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id, ownerId, status string
		var stateJSON sql.NullString // Use NullString to handle potential NULLs
		rows.Scan(&id, &ownerId, &status, &stateJSON)

		newRoom := &model.Room{}
		if stateJSON.Valid && stateJSON.String != "" {
			if err := json.Unmarshal([]byte(stateJSON.String), newRoom); err != nil {
				log.Printf("Failed to unmarshal room %s: %v", id, err)
				continue
			}
			// Ensure essential fields are set even if JSON override them wrongly (though JSON usually has truth)
			newRoom.OwnerID = ownerId
			newRoom.ID = id
		} else {
			newRoom = &model.Room{
				ID: id, OwnerID: ownerId, Status: status,
				Players: make(map[string]*model.Player),
			}
			for i := 0; i < 4; i++ {
				newRoom.Rows[i].Cards = make([]model.Card, 0)
			}
		}
		rooms[id] = newRoom
	}
	return rooms, nil
}

func (s *Store) PersistRoom(r *model.Room) {
	data, err := json.Marshal(r)
	if err != nil {
		log.Println("Error marshaling room:", err)
		return
	}
	// Use UPDATE if exists, or INSERT OR REPLACE logic.
	// Since we always have ID, REPLACE INTO is fine.
	_, err = s.db.Exec("INSERT OR REPLACE INTO rooms (id, owner_id, status, state_json) VALUES (?, ?, ?, ?)", r.ID, r.OwnerID, r.Status, string(data))
	if err != nil {
		log.Println("Error persisting room:", err)
	}
}

func (s *Store) DeleteRoom(roomID string) {
	s.db.Exec("DELETE FROM rooms WHERE id = ?", roomID)
}
