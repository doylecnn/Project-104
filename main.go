package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// --- 数据结构 ---

type Card struct {
	Value int `json:"value"`
	Score int `json:"score"`
}

type Player struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"` // 新增：用户昵称
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

type GameState struct {
	Players         map[string]*Player `json:"players"`
	Rows            [4]Row             `json:"rows"`
	Status          string             `json:"status"`
	PendingPlayerID string             `json:"pendingPlayerId"`
	PendingCard     *Card              `json:"pendingCard"`
}

type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

type Action struct {
	Type    string `json:"type"`
	Value   int    `json:"value"`
	Payload string `json:"payload"` // 用于传递登录名等字符串信息
	ID      string `json:"id"`      // 用于传递登录ID
}

// --- 全局变量 ---

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	players = make(map[string]*Player)
	rows    [4]Row
	status  = "waiting"
	mutex   sync.Mutex
	deck    []Card

	turnQueue   []PlayAction
	pendingPlay *PlayAction
)

// --- 辅助函数 (保持不变) ---

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

func initDeck() {
	deck = make([]Card, 0, 104)
	for i := 1; i <= 104; i++ {
		deck = append(deck, Card{Value: i, Score: getScore(i)})
	}
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
}

func broadcastState() {
	publicPlayers := make(map[string]interface{})
	for id, p := range players {
		publicPlayers[id] = map[string]interface{}{
			"id":          p.ID,
			"name":        p.Name, // 广播名字
			"score":       p.Score,
			"ready":       p.Ready,
			"hasSelected": p.SelectedCard != nil,
			"handSize":    len(p.Hand),
		}
	}

	state := GameState{
		Rows:   rows,
		Status: status,
	}

	if pendingPlay != nil {
		state.PendingPlayerID = pendingPlay.PlayerID
		state.PendingCard = &pendingPlay.Card
	}

	baseState := map[string]interface{}{
		"rows":            state.Rows,
		"status":          state.Status,
		"pendingPlayerId": state.PendingPlayerID,
		"pendingCard":     state.PendingCard,
		"players":         publicPlayers,
	}

	for _, p := range players {
		// 检查连接是否存活
		if p.Conn != nil {
			msg := map[string]interface{}{
				"publicState": baseState,
				"myHand":      p.Hand,
				"myId":        p.ID,
			}
			p.Conn.WriteJSON(Message{Type: "state", Payload: msg})
		}
	}
}

func broadcastInfo(text string) {
	msg := Message{Type: "info", Payload: text}
	for _, p := range players {
		if p.Conn != nil {
			p.Conn.WriteJSON(msg)
		}
	}
}

func startGame() {
	initDeck()
	status = "playing"
	turnQueue = make([]PlayAction, 0)
	pendingPlay = nil

	idx := 0
	// 只给已准备的玩家发牌
	playingCount := 0
	for _, p := range players {
		if p.Ready {
			p.Hand = deck[idx : idx+10]
			sort.Slice(p.Hand, func(i, j int) bool { return p.Hand[i].Value < p.Hand[j].Value })
			p.Score = 0
			p.SelectedCard = nil
			idx += 10
			playingCount++
		}
	}

	// 至少2人才能开始，虽然前端做了限制，后端再兜底一下
	if playingCount < 2 {
		status = "waiting"
		broadcastInfo("人数不足，无法开始")
		return
	}

	for i := 0; i < 4; i++ {
		rows[i].Cards = []Card{deck[idx]}
		idx++
	}
	broadcastState()
}

func prepareTurnResolution() {
	turnQueue = make([]PlayAction, 0)

	for id, p := range players {
		// 只有参与游戏的玩家（有SelectedCard）才结算
		if p.SelectedCard != nil {
			card := *p.SelectedCard
			turnQueue = append(turnQueue, PlayAction{PlayerID: id, Card: card})

			newHand := make([]Card, 0)
			for _, c := range p.Hand {
				if c.Value != card.Value {
					newHand = append(newHand, c)
				}
			}
			p.Hand = newHand
			p.SelectedCard = nil
		}
	}

	sort.Slice(turnQueue, func(i, j int) bool {
		return turnQueue[i].Card.Value < turnQueue[j].Card.Value
	})

	processTurnQueue()
}

func processTurnQueue() {
	if len(turnQueue) == 0 {
		pendingPlay = nil

		// 检查是否结束：看还在玩的玩家手牌是否为空
		allEmpty := true
		//playingExists := false
		for _, p := range players {
			// 简单的判断：如果他在上一轮是Ready的，那他就是玩家
			// 这里由于状态比较简单，我们假设只要有手牌就是没结束
			if len(p.Hand) > 0 {
				allEmpty = false
				//playingExists = true
				break
			}
		}

		// 如果没有任何人有手牌了（且之前有人在玩），则结束
		if allEmpty && status == "playing" {
			status = "finished"
			broadcastInfo("游戏结束！")
		} else {
			status = "playing"
		}
		broadcastState()
		return
	}

	currentPlay := turnQueue[0]
	card := currentPlay.Card

	bestRowIdx := -1
	diff := 1000

	for i := 0; i < 4; i++ {
		lastCard := rows[i].Cards[len(rows[i].Cards)-1]
		if card.Value > lastCard.Value {
			d := card.Value - lastCard.Value
			if d < diff {
				diff = d
				bestRowIdx = i
			}
		}
	}

	if bestRowIdx != -1 {
		player := players[currentPlay.PlayerID]
		playerName := player.Name

		if len(rows[bestRowIdx].Cards) >= 5 {
			rowScore := 0
			for _, c := range rows[bestRowIdx].Cards {
				rowScore += c.Score
			}
			player.Score += rowScore
			rows[bestRowIdx].Cards = []Card{card}
			broadcastInfo(fmt.Sprintf("%s 放置 %d，接第 %d 行爆了！扣除 %d 分", playerName, card.Value, bestRowIdx+1, rowScore))
		} else {
			rows[bestRowIdx].Cards = append(rows[bestRowIdx].Cards, card)
		}

		turnQueue = turnQueue[1:]
		processTurnQueue()

	} else {
		status = "choosing_row"
		pendingPlay = &currentPlay
		pName := players[currentPlay.PlayerID].Name
		broadcastInfo(fmt.Sprintf("%s 的牌 %d 太小了，请选择一行收走", pName, card.Value))
		broadcastState()
	}
}

func handleRowChoice(playerID string, rowIdx int) {
	if status != "choosing_row" || pendingPlay == nil {
		return
	}
	if pendingPlay.PlayerID != playerID {
		return
	}
	if rowIdx < 0 || rowIdx > 3 {
		return
	}

	player := players[playerID]
	rowScore := 0
	for _, c := range rows[rowIdx].Cards {
		rowScore += c.Score
	}
	player.Score += rowScore
	rows[rowIdx].Cards = []Card{pendingPlay.Card}

	broadcastInfo(fmt.Sprintf("%s 主动收走第 %d 行，扣除 %d 分", player.Name, rowIdx+1, rowScore))

	turnQueue = turnQueue[1:]
	pendingPlay = nil
	processTurnQueue()
}

// --- 连接处理逻辑 ---

func handleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	// 注意：这里不再 defer ws.Close()，因为如果是重连，旧的goroutine退出会关闭socket，
	// 但新的goroutine需要保持socket。
	// 更好的做法是：每个连接对应一个 readLoop。
	// 下面的实现简化了：如果重连，旧的 readLoop 会因为 socket 被新连接替换或者旧 socket 断开而报错退出。

	// 实际上，为了简单起见，我们允许 socket 替换。

	var currentPlayerID string

	defer func() {
		// 连接断开时的清理逻辑
		// 只有当 map 中的连接确实是当前这个断开的连接时，才做处理（防止重连后把新连接清理了）
		mutex.Lock()
		if p, ok := players[currentPlayerID]; ok {
			if p.Conn == ws {
				// 真正的断开
				log.Printf("Player %s (%s) disconnected", p.Name, p.ID)
				// 这里可以选择不删除玩家，保留状态等待重连
				// delete(players, currentPlayerID)
				// 如果所有人都断开了，重置游戏？
				// 暂时保留玩家数据，不删除
			}
		}
		mutex.Unlock()
		ws.Close()
	}()

	for {
		var action Action
		err := ws.ReadJSON(&action)
		if err != nil {
			break
		}

		mutex.Lock()

		// 特殊处理登录消息
		if action.Type == "login" {
			uid := action.ID
			name := action.Payload

			if existingPlayer, ok := players[uid]; ok {
				// --- 重连逻辑 ---
				log.Printf("Player %s reconnected", name)
				existingPlayer.Conn = ws
				existingPlayer.Name = name // 更新名字（如果改了）
				currentPlayerID = uid

				// 立即发送当前状态
				mutex.Unlock() // 先解锁，broadcastState 内部不加锁，但为了安全最好在锁外或调整锁粒度
				broadcastState()
				mutex.Lock() // 重新加锁以备后续操作
			} else {
				// --- 新玩家逻辑 ---
				log.Printf("New Player %s joined", name)
				newPlayer := &Player{
					ID:    uid,
					Name:  name,
					Conn:  ws,
					Score: 0,
					Ready: false,
				}
				players[uid] = newPlayer
				currentPlayerID = uid

				// 如果游戏正在进行，新玩家只能旁观（或者下一局）
				if status != "waiting" {
					// 发送一条提示
					ws.WriteJSON(Message{Type: "info", Payload: "游戏正在进行中，请等待下一局"})
				}

				mutex.Unlock()
				broadcastState()
				mutex.Lock()
			}
		} else {
			// 处理游戏内逻辑，必须确保已登录
			if currentPlayerID == "" {
				// 未登录发送指令，忽略
				mutex.Unlock()
				continue
			}

			player := players[currentPlayerID]

			switch action.Type {
			case "ready":
				if status == "waiting" {
					player.Ready = true
					// 检查是否所有人都准备好了
					allReady := true
					readyCount := 0
					for _, p := range players {
						if p.Ready {
							readyCount++
						} else {
							// 只要有一个人没准备，就不开始？
							// 或者只统计在线的人？这里简化为所有注册的人
							allReady = false
						}
					}
					// 只要有 >=2 人准备好，且没有未准备的人，就开始
					// 实际逻辑可能需要更复杂，比如只看 readyCount >= 2 且 == len(players)
					if allReady && readyCount >= 2 {
						startGame()
					} else {
						broadcastState()
					}
				}

			case "play_card":
				if status == "playing" && player.SelectedCard == nil {
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
						// 检查是否所有 *参与本局* 的玩家都出牌了
						allSelected := true
						for _, p := range players {
							// 只有手牌数 > 0 的才是本局玩家 (或者用 Ready 标记辅助判断)
							if len(p.Hand) > 0 && p.SelectedCard == nil {
								allSelected = false
								break
							}
						}
						if allSelected {
							prepareTurnResolution()
						} else {
							broadcastState()
						}
					}
				}

			case "choose_row":
				if status == "choosing_row" {
					handleRowChoice(currentPlayerID, action.Value)
				}

			case "restart":
				if status == "finished" {
					status = "waiting"
					for _, p := range players {
						p.Ready = false
						p.Score = 0
						p.Hand = []Card{}
						p.SelectedCard = nil
					}
					broadcastState()
				}
			}
		}
		mutex.Unlock()
	}
}

func main() {
	http.HandleFunc("/ws", handleConnections)
	http.Handle("/", http.FileServer(http.Dir("./")))
	fmt.Println("Server started on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
