package smtp

import "github.com/ivermin1123/authstunt/internal/extract"

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
