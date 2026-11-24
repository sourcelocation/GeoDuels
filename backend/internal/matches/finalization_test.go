package matches

import (
	"testing"

	"geoduels/pkg/duel"
	"geoduels/pkg/gameplay"
)

// A duel's season result follows its HP: the side left with more wins, equal HP is a draw. A win
// is flawless only when the winner kept all of the duel's starting HP.
func TestDuelSeasonResult(t *testing.T) {
	cases := []struct {
		name              string
		hp1, hp2          int
		result1, result2  string
		flawless1, flaws2 bool
	}{
		{"flawless win", duel.StartingHP, 0, "win", "loss", true, false},
		{"win", 1200, 0, "win", "loss", false, false},
		{"loss", 0, duel.StartingHP, "loss", "win", false, true},
		{"draw", 3000, 3000, "draw", "draw", false, false},
		{"flawless draw is no win", duel.StartingHP, duel.StartingHP, "draw", "draw", false, false},
	}
	for _, c := range cases {
		snap := gameplay.MatchSnapshot{MatchID: "m", Players: map[string]gameplay.PlayerState{
			"00000000-0000-0000-0000-00000000000a": {UserID: "00000000-0000-0000-0000-00000000000a", HP: c.hp1},
			"00000000-0000-0000-0000-00000000000b": {UserID: "00000000-0000-0000-0000-00000000000b", HP: c.hp2},
		}}
		result, err := newDuelResult(&snap)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		p1, p2 := &result.participants[0], &result.participants[1]
		if p1.player.HP != c.hp1 {
			p1, p2 = p2, p1
		}
		if got := result.seasonResult(p1); got != c.result1 {
			t.Errorf("%s: player 1 result %q, want %q", c.name, got, c.result1)
		}
		if got := result.seasonResult(p2); got != c.result2 {
			t.Errorf("%s: player 2 result %q, want %q", c.name, got, c.result2)
		}
		if p1.flawless() != c.flawless1 || p2.flawless() != c.flaws2 {
			t.Errorf("%s: flawless %v/%v, want %v/%v", c.name, p1.flawless(), p2.flawless(), c.flawless1, c.flaws2)
		}
	}
}
