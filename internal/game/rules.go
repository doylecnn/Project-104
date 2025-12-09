package game

import (
	"math/rand"
	"sort"
	"take5/internal/model"
)

// GetScore calculates the penalty score (bullheads) for a given card value.
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

// InitDeck initializes and shuffles the deck for a new game.
func InitDeck(r *model.Room) {
	r.Deck = make([]model.Card, 0, 104)
	for i := 1; i <= 104; i++ {
		r.Deck = append(r.Deck, model.Card{Value: i, Score: GetScore(i)})
	}
	rand.Shuffle(len(r.Deck), func(i, j int) { r.Deck[i], r.Deck[j] = r.Deck[j], r.Deck[i] })
}

// DealCards distributes cards to players and sets up the initial rows.
// Returns the index in the deck where dealing stopped.
func DealCards(r *model.Room) int {
	idx := 0
	// Sort players by ID to ensure deterministic dealing order if needed, 
	// though map iteration order is random. Here we just iterate.
	// Filter for online players is done by the caller (StartGame).
	for _, p := range r.Players {
		if p.IsOnline {
			p.Hand = r.Deck[idx : idx+10]
			sort.Slice(p.Hand, func(i, j int) bool { return p.Hand[i].Value < p.Hand[j].Value })
			p.Score = 0
			p.SelectedCard = nil
			p.Ready = false
			idx += 10
		} else {
			p.Hand = []model.Card{}
			p.SelectedCard = nil
			p.Ready = false
		}
	}
	
	// Set up initial rows
	for i := 0; i < 4; i++ {
		r.Rows[i].Cards = []model.Card{r.Deck[idx]}
		idx++
	}
	return idx
}

// FindBestRow finds the optimal row index for a card placement.
// Returns -1 if no valid row is found (card is smaller than all row ends).
func FindBestRow(r *model.Room, cardValue int) (int, int) {
	bestRowIdx := -1
	diff := 1000
	for i := 0; i < 4; i++ {
		if len(r.Rows[i].Cards) == 0 {
			continue 
		}
		lastCard := r.Rows[i].Cards[len(r.Rows[i].Cards)-1]
		if cardValue > lastCard.Value {
			d := cardValue - lastCard.Value
			if d < diff {
				diff = d
				bestRowIdx = i
			}
		}
	}
	return bestRowIdx, diff
}

// CalculateRowScore computes the total penalty score of a row.
func CalculateRowScore(row model.Row) int {
	score := 0
	for _, c := range row.Cards {
		score += c.Score
	}
	return score
}
