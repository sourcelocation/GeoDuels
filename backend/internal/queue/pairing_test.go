package queue

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"

	"geoduels/internal/rating"
)

var pairingSeeds = []int64{1, 2, 3, 5, 8, 13, 21, 34, 55, 89}

var allQueues = []string{"moving", "no_move_hidden", "nmpz"}

func randomTickets(rng *rand.Rand, now time.Time) []Ticket {
	var tickets []Ticket
	for i := 0; i < 2+rng.Intn(40); i++ {
		var queues []string
		for _, q := range allQueues {
			if rng.Intn(3) > 0 {
				queues = append(queues, q)
			}
		}
		if len(queues) == 0 {
			queues = []string{"moving"}
		}
		ticket := Ticket{
			UserID: fmt.Sprintf("p%02d", i), Queues: queues, MMR: 500 + rng.Intn(1500),
			RD: rating.MinimumRatingRD + float64(rng.Intn(111)), JoinedAt: now.Add(-time.Duration(rng.Intn(40_000)) * time.Millisecond),
		}
		if i > 0 && rng.Intn(4) == 0 {
			ticket.LastOpponentID = fmt.Sprintf("p%02d", rng.Intn(i))
		}
		tickets = append(tickets, ticket)
	}
	return tickets
}

// Pairings never seats a player twice, only pairs players who share the queue they play and can meet,
// puts the longer-waiting first, and leaves no two players who could meet waiting.
func TestPairingsInvariants(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, seed := range pairingSeeds {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			tickets := randomTickets(rand.New(rand.NewSource(seed)), now)
			paired := map[string]bool{}
			for _, pair := range Pairings(tickets, now, rand.New(rand.NewSource(seed))) {
				for _, ticket := range []Ticket{pair.First, pair.Second} {
					if paired[ticket.UserID] {
						t.Fatalf("%s paired twice", ticket.UserID)
					}
					paired[ticket.UserID] = true
					if !contains(ticket.Queues, pair.Queue) {
						t.Fatalf("%s plays %s without waiting in it", ticket.UserID, pair.Queue)
					}
				}
				if !canMeet(pair.First, pair.Second, now) {
					t.Fatalf("pair %s/%s can't meet", pair.First.UserID, pair.Second.UserID)
				}
				if pair.Second.JoinedAt.Before(pair.First.JoinedAt) {
					t.Fatalf("pair %s/%s: the longer-waiting player must come first", pair.First.UserID, pair.Second.UserID)
				}
			}
			for _, a := range tickets {
				for _, b := range tickets {
					if a.UserID < b.UserID && !paired[a.UserID] && !paired[b.UserID] && canMeet(a, b, now) {
						t.Fatalf("%s and %s could play each other but were left waiting", a.UserID, b.UserID)
					}
				}
			}
		})
	}
}

func settled(id string, mmr int, joined time.Time) Ticket {
	return Ticket{UserID: id, Queues: []string{"moving"}, MMR: mmr, RD: rating.MinimumRatingRD, JoinedAt: joined}
}

// Two settled players meet within 150 at once, and 37.5 further for every second the longer-waiting
// of them has waited, without limit.
func TestPairingsWidenWithWaiting(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		wait time.Duration
		diff int
		want bool
	}{
		{0, 0, true},
		{0, 150, true},
		{0, 151, false},
		{6 * time.Second, 375, true},
		{6 * time.Second, 376, false},
		{time.Minute, 2400, true},
		{time.Minute, 2401, false},
	} {
		tickets := []Ticket{settled("a", 1000, now.Add(-tc.wait)), settled("b", 1000+tc.diff, now)}
		if got := len(Pairings(tickets, now, rand.New(rand.NewSource(1)))) == 1; got != tc.want {
			t.Errorf("waited %v, %d apart: paired=%v, want %v", tc.wait, tc.diff, got, tc.want)
		}
	}
}

// The more uncertain rating of the two widens the window by how far its deviation is above a settled one.
func TestPairingsWidenForUncertainRatings(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	fresh := settled("new", 1000, now)
	fresh.RD = rating.InitialRatingRD
	extra := int(rating.InitialRatingRD - rating.MinimumRatingRD)
	for diff, want := range map[int]bool{150 + extra: true, 151 + extra: false} {
		tickets := []Ticket{fresh, settled("b", 1000+diff, now)}
		if got := len(Pairings(tickets, now, rand.New(rand.NewSource(1)))) == 1; got != want {
			t.Errorf("%d apart: paired=%v, want %v", diff, got, want)
		}
	}
}

// Two players who just played each other wait 15 seconds, both of them, before they meet again.
func TestPairingsHoldRematches(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		waitA, waitB time.Duration
		want         bool
	}{
		{0, 0, false},
		{20 * time.Second, 5 * time.Second, false},
		{15 * time.Second, 15 * time.Second, true},
	} {
		a, b := settled("a", 1000, now.Add(-tc.waitA)), settled("b", 1000, now.Add(-tc.waitB))
		a.LastOpponentID = "b"
		if got := len(Pairings([]Ticket{a, b}, now, rand.New(rand.NewSource(1)))) == 1; got != tc.want {
			t.Errorf("waited %v and %v: paired=%v, want %v", tc.waitA, tc.waitB, got, tc.want)
		}
	}
}

// The longest-waiting player picks the closest rating among players sharing a queue with them, and
// plays a queue they share.
func TestPairingsOrder(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	old := settled("old", 1000, now.Add(-10*time.Second))
	old.Queues = []string{"moving", "no_move_hidden"}
	other := settled("other", 1000, now.Add(-5*time.Second))
	other.Queues = []string{"nmpz"}
	near := settled("near", 1050, now.Add(-2*time.Second))
	near.Queues = []string{"no_move_hidden"}
	tickets := []Ticket{old, settled("far", 1300, now.Add(-9*time.Second)), near, other}
	pairs := Pairings(tickets, now, rand.New(rand.NewSource(1)))
	if len(pairs) != 1 {
		t.Fatalf("pairs = %+v, want one", pairs)
	}
	if got := pairs[0]; got.Queue != "no_move_hidden" || got.First.UserID != "old" || got.Second.UserID != "near" {
		t.Fatalf("pair = %s %s/%s, want no_move_hidden old/near", got.Queue, got.First.UserID, got.Second.UserID)
	}
}

// Players sharing several queues play each of them in turn, depending on the draw.
func TestPairingsPickASharedQueueAtRandom(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	a, b := settled("a", 1000, now), settled("b", 1000, now)
	a.Queues, b.Queues = []string{"moving", "no_move_hidden"}, []string{"no_move_hidden", "moving"}
	played := map[string]bool{}
	rng := rand.New(rand.NewSource(7))
	for range 40 {
		played[Pairings([]Ticket{a, b}, now, rng)[0].Queue] = true
	}
	if !played["moving"] || !played["no_move_hidden"] {
		t.Fatalf("played %v, want both queues", played)
	}
}

func TestWindowNeverShrinks(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	last := math.Inf(-1)
	for wait := time.Duration(0); wait < 3*time.Minute; wait += 250 * time.Millisecond {
		w := window(settled("a", 0, now.Add(-wait)), settled("b", 0, now), now)
		if w < last {
			t.Fatalf("window shrank to %v after %v", w, wait)
		}
		last = w
	}
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}
