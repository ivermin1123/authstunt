package smtp

import (
	"context"

	"github.com/ivermin1123/authstunt/internal/extract"
	"github.com/ivermin1123/authstunt/internal/store"
)

// SetExtractorForTest substitutes the extraction engine, which is how the
// panic path is exercised: no real corpus entry panics, and a recovery
// path that is never taken is not proven to work.
func (i *Ingest) SetExtractorForTest(fn func(extract.Input) extract.Result) {
	i.extractFn = fn
}

// QueueDepthForTest reports how many messages are waiting for a worker, so
// a backpressure test can assert the queue is genuinely full rather than
// guessing from timing.
func (i *Ingest) QueueDepthForTest() int { return len(i.queue) }

// PublishForTest announces a settled message the way a worker does.
//
// It exists because the interesting case is the announcement of a message
// whose terminal state has already committed, made with a context that has
// since been canceled. That state is a race in a running server - shutdown
// landing between the commit and the publish - and a test cannot hit the
// window by timing, but it is exactly the state a caller can be stranded by.
func (i *Ingest) PublishForTest(ctx context.Context, m store.Message) {
	i.publish(ctx, m)
}
