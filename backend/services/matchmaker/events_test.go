package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"geoduels/pkg/live"
)

// The matchmaker's events are the API's: each must encode exactly as its example in the shared file,
// which the API's and the web client's tests check too.
func TestEventsMatchTheSharedExamples(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	match := activeMatch{MatchID: "m1", Kind: "ranked_duel", Mode: "duel", Status: "live"}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "shared", "live-events.json"))
	if err != nil {
		t.Fatal(err)
	}
	var examples []map[string]any
	if err := json.Unmarshal(data, &examples); err != nil {
		t.Fatal(err)
	}
	for _, event := range []live.Event{queueState{Queued: true, QueuedAt: at.UnixMilli()}, matchStarted{Match: match}} {
		payload, err := live.Encode(event)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, want := range examples {
			if want["type"] == event.EventType() {
				found = true
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s encodes as %v, the shared example is %v", event.EventType(), got, want)
				}
			}
		}
		if !found {
			t.Errorf("no shared example of %s", event.EventType())
		}
	}
}
