package background

import (
	"context"
	"fmt"
	"testing"
)

func reserveMove(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, _, err := s.ReserveMove(context.Background(), MoveBinding{JobID: id, DelegationID: "grant", Params: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
}

// Finished moves are history, not state: past the limit the oldest go, while a
// move that may still copy or still holds its staging source stays.
func TestFinishedMoveMirrorsAreLimited(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	reserveMove(t, s, "pending")
	if err := s.FinishMoveMirror(ctx, "pending-refused", false, "invalid-size"); err == nil {
		t.Fatal("finished a mirror that does not exist")
	}
	reserveMove(t, s, "refused")
	if err := s.FinishMoveMirror(ctx, "refused", false, "SCOPE_DENIED"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxFinishedMoves+5; i++ {
		id := fmt.Sprintf("done-%03d", i)
		reserveMove(t, s, id)
		if err := s.FinishMoveMirror(ctx, id, true, ""); err != nil {
			t.Fatal(err)
		}
	}
	reserveMove(t, s, "next")
	bindings, err := s.MoveBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	finished, kept := 0, map[string]bool{}
	for _, b := range bindings {
		kept[b.JobID] = true
		if b.CleanupDone {
			finished++
		}
	}
	if finished != maxFinishedMoves || !kept["pending"] || !kept["refused"] || !kept["next"] || !kept[fmt.Sprintf("done-%03d", maxFinishedMoves+4)] {
		t.Fatal(finished, kept)
	}
}
