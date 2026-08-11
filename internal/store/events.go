package store

import (
	"context"
	"fmt"
)

// NextEventGeneration increments the persisted generation counter and
// returns the new value. The SSE bus calls it once at startup and stamps
// every event id it hands out as `generation-seq` (design 4.2 item 4).
//
// The counter is persisted rather than derived from the boot time because
// a restart inside the same second, or a clock that moves backwards, would
// otherwise reissue ids a reconnecting client has already seen and its
// Last-Event-ID replay would skip the events in between. Increment and
// read are one statement on the single write executor, so two processes
// racing to start on one data directory cannot take the same generation.
func (s *Store) NextEventGeneration(ctx context.Context) (int64, error) {
	var generation int64
	err := s.write.QueryRowContext(ctx,
		`UPDATE event_generation SET generation = generation + 1 WHERE id = 1
		 RETURNING generation`).Scan(&generation)
	if err != nil {
		return 0, fmt.Errorf("store: next event generation: %w", err)
	}
	return generation, nil
}
