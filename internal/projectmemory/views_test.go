package projectmemory

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestEventPagingAndReceiptReadback(t *testing.T) {
	s, scope := memoryFixture(t)
	initialize(t, s, scope)
	var facts []Fact
	for i := 0; i < 75; i++ {
		facts = append(facts, fact(scope, fmt.Sprintf("file-%03d.txt", i), fmt.Sprintf("key-%d", i), "contents"))
	}
	observe(t, s, observation(scope, 1, facts...), "observe-many")
	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < 4; pages++ {
		page, err := s.RecentEvents(context.Background(), 2, cursor)
		if err != nil || len(page.Items) > 30 {
			t.Fatal(page, err)
		}
		for _, e := range page.Items {
			if seen[e.ID] {
				t.Fatal("duplicate event", e.ID)
			}
			seen[e.ID] = true
		}
		cursor = page.Next
		if cursor == "" {
			break
		}
	}
	if len(seen) != 76 || cursor != "" {
		t.Fatal("lost events", len(seen), cursor)
	}
	if _, err := s.RecentEvents(context.Background(), 1, ""); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	for _, cursor := range []string{"garbage", "2:-1", "2:99999", "3:0"} {
		if _, err := s.RecentEvents(context.Background(), 2, cursor); err == nil {
			t.Fatal("invalid cursor", cursor)
		}
	}
	status, err := s.Operation(context.Background(), "observe-many")
	if err != nil || !status.Found || status.Record.Revision != 2 {
		t.Fatal(status, err)
	}
	if readMemory(t, s).State.Revision != 2 {
		t.Fatal("readback mutated journal")
	}
}
