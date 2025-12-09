package game

import (
	"fmt"
	"sort"
	"strings"
	"take5/internal/model"
	"time"
)

// StartGame initializes and starts a new game round.
func (m *Manager) StartGame(r *model.Room) {
	InitDeck(r)
	r.Status = "playing"
	r.TurnQueue = make([]model.PlayAction, 0)
	r.PendingPlay = nil

	// Count online players to verify we can start
	playingCount := 0
	for _, p := range r.Players {
		if p.IsOnline {
			playingCount++
		}
	}

	if playingCount < 2 {
		r.Status = "waiting"
		BroadcastInfo(r, "人数不足，无法开始")
		return
	}

	// Deal cards using rules.go helper
	DealCards(r)

	m.BroadcastState(r)
}

// PrepareTurnResolution collects selected cards and prepares the turn queue.
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

// ProcessTurnQueue resolves the actions in the turn queue.
// r 此时比在外部被锁
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

			// 广播结算状态，让客户端展示动画
			m.BroadcastState(r)

			// 延迟2秒，让玩家看到完整的上牌动画和准备进入结算
			time.Sleep(2 * time.Second)

			BroadcastInfo(r, "游戏结束！")
			m.Store.RecordGameResult(r.ID, r.Players)
			m.BroadcastStats(r)

			// 再次广播最终状态，确保客户端显示最新积分
			m.BroadcastState(r)

			// 再延迟2秒展示结算画面
			time.Sleep(2 * time.Second)

			onlinePlayersCount := 0
			for _, p := range r.Players {
				if p.IsOnline {
					onlinePlayersCount++
				}
			}

			scoreLines := []string{}
			for _, p := range r.Players {
				scoreLines = append(scoreLines, fmt.Sprintf("%s : %d 分", p.Name, p.Score))
			}
			BroadcastInfo(r, "本局得分："+strings.Join(scoreLines, " | "))

			if onlinePlayersCount >= 2 {
				// 开始倒计时，但保持状态为finished
				for i := 5; i > 0; i-- {
					for _, p := range r.Players {
						if p.Conn != nil && p.IsOnline {
							p.Conn.WriteJSON(model.Message{Type: "auto_restart_countdown", Payload: model.AutoRestartCountdownPayload{Count: i}})
						}
					}
					time.Sleep(1 * time.Second)
				}

				// 倒计时结束，开始新游戏
				m.StartGame(r)
			} else {
				BroadcastInfo(r, "在线人数不足，无法自动开始新一局。")
			}
		} else {
			r.Status = "playing"
			m.BroadcastState(r)
		}
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

	bestRowIdx, _ := FindBestRow(r, card.Value)

	if bestRowIdx != -1 {
		player := r.Players[currentPlay.PlayerID]
		// Check for row overflow (taking the row)
		if len(r.Rows[bestRowIdx].Cards) >= 5 {
			rowScore := CalculateRowScore(r.Rows[bestRowIdx])
			player.Score += rowScore
			r.Rows[bestRowIdx].Cards = []model.Card{card}
			BroadcastInfo(r, fmt.Sprintf("%s 放置 %d，爆了第 %d 行！扣 %d 分", player.Name, card.Value, bestRowIdx+1, rowScore))
		} else {
			r.Rows[bestRowIdx].Cards = append(r.Rows[bestRowIdx].Cards, card)
		}
		r.TurnQueue = r.TurnQueue[1:]
		m.ProcessTurnQueue(r)
	} else {
		// Card is smaller than all row ends, player must choose a row
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

// HandleRowChoice resolves a player's choice to take a specific row.
func (m *Manager) HandleRowChoice(r *model.Room, playerID string, rowIdx int) {
	if r.Status != "choosing_row" || r.PendingPlay == nil || r.PendingPlay.PlayerID != playerID || rowIdx < 0 || rowIdx > 3 {
		return
	}
	player := r.Players[playerID]
	rowScore := CalculateRowScore(r.Rows[rowIdx])

	player.Score += rowScore
	r.Rows[rowIdx].Cards = []model.Card{r.PendingPlay.Card}
	BroadcastInfo(r, fmt.Sprintf("%s 收走第 %d 行，扣 %d 分", player.Name, rowIdx+1, rowScore))
	r.TurnQueue = r.TurnQueue[1:]
	r.PendingPlay = nil
	m.ProcessTurnQueue(r)
}

// ForceRestart allows the owner to restart the game manually.
func (m *Manager) ForceRestart(r *model.Room, requesterID string) bool {
	if r.OwnerID != requesterID {
		return false
	}

	onlinePlayersCount := 0
	for _, p := range r.Players {
		if p.IsOnline {
			onlinePlayersCount++
		}
	}

	if onlinePlayersCount < 2 {
		BroadcastInfo(r, "人数不足，无法强制重开")
		return false
	}

	// Reset game state
	for _, p := range r.Players {
		p.Hand = []model.Card{}
		p.Score = 0
		p.Ready = false
		p.SelectedCard = nil
	}
	for i := 0; i < 4; i++ {
		r.Rows[i].Cards = make([]model.Card, 0)
	}
	r.TurnQueue = make([]model.PlayAction, 0)
	r.PendingPlay = nil
	r.Status = "waiting"

	BroadcastInfo(r, fmt.Sprintf("%s 强制重开了一局新游戏！", r.Players[requesterID].Name))
	m.StartGame(r)
	return true
}
