package queue

import (
	"math"
	"math/rand"
	"slices"
	"time"

	"geoduels/internal/rating"
)

// Two players can meet when their ratings are within a window: 150 to start, widening by 37.5 for
// every second the longer-waiting of them has waited, without limit, so nobody waits forever. A
// rating that is still uncertain (a high deviation, mostly a new player's) is a guess, so the more
// uncertain of the two widens the window by how far their deviation is above a settled player's.
// Two players who just played each other wait rematchAfter before they meet again.
const (
	baseWindow     = 150.0
	widenPerSecond = 37.5
	rematchAfter   = 15 * time.Second
)

// Pair is two players who will play each other in a queue. First waited longer.
type Pair struct {
	Queue  string
	First  Ticket
	Second Ticket
}

// Pairings decides who plays whom among the waiting tickets. The longest-waiting player picks first:
// the closest rating among the players who share one of their queues and can meet them, the
// longer-waiting on a tie. They play one of the queues they share, chosen at random.
func Pairings(tickets []Ticket, now time.Time, rng *rand.Rand) []Pair {
	waiting := slices.Clone(tickets)
	slices.SortStableFunc(waiting, func(a, b Ticket) int {
		if c := a.JoinedAt.Compare(b.JoinedAt); c != 0 {
			return c
		}
		if a.UserID < b.UserID {
			return -1
		}
		if a.UserID > b.UserID {
			return 1
		}
		return 0
	})
	paired := map[string]bool{}
	var pairs []Pair
	for i, self := range waiting {
		if paired[self.UserID] {
			continue
		}
		best, bestDiff := -1, math.Inf(1)
		for j, other := range waiting {
			if j == i || paired[other.UserID] || !canMeet(self, other, now) {
				continue
			}
			if diff := math.Abs(float64(self.MMR - other.MMR)); diff < bestDiff {
				best, bestDiff = j, diff
			}
		}
		if best < 0 {
			continue
		}
		other := waiting[best]
		paired[self.UserID], paired[other.UserID] = true, true
		shared := sharedQueues(self, other)
		pairs = append(pairs, Pair{Queue: shared[rng.Intn(len(shared))], First: self, Second: other})
	}
	return pairs
}

// canMeet reports whether two waiting players may play each other now.
func canMeet(a, b Ticket, now time.Time) bool {
	if a.UserID == b.UserID || len(sharedQueues(a, b)) == 0 {
		return false
	}
	if math.Abs(float64(a.MMR-b.MMR)) > window(a, b, now) {
		return false
	}
	justPlayed := a.LastOpponentID == b.UserID || b.LastOpponentID == a.UserID
	return !justPlayed || min(waited(a, now), waited(b, now)) >= rematchAfter
}

// window is how far apart two players' ratings may be.
func window(a, b Ticket, now time.Time) float64 {
	uncertainty := max(max(a.RD, b.RD)-rating.MinimumRatingRD, 0)
	return baseWindow + uncertainty + max(waited(a, now), waited(b, now)).Seconds()*widenPerSecond
}

func waited(t Ticket, now time.Time) time.Duration {
	return max(now.Sub(t.JoinedAt), 0)
}

// sharedQueues is the queues both players wait in, in a's order.
func sharedQueues(a, b Ticket) []string {
	var out []string
	for _, q := range a.Queues {
		if slices.Contains(b.Queues, q) {
			out = append(out, q)
		}
	}
	return out
}
