// Package audittest records activity-log writes for tests.
package audittest

import (
	"context"
	"sync"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
)

// Recorder is an audit.Logger that keeps what it is given.
type Recorder struct {
	mu     sync.Mutex
	Events []audit.Event
}

// Log implements audit.Logger.
func (r *Recorder) Log(_ context.Context, e audit.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Events = append(r.Events, e)
	return nil
}
