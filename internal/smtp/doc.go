// Package smtp receives mail and turns it into a stored, extracted message.
//
// It is split in two. Server owns the protocol: it wraps emersion/go-smtp,
// decides every reply code, and knows nothing about storage. Ingest owns the
// pipeline behind it - MIME parsing, blobs, the message row, extraction, the
// ledger, the event bus - and knows nothing about SMTP. They meet at
// Deliverer, an interface with one method, which is also where the ack
// contract lives.
//
// That contract is the reason the split is worth having. A 250 is a promise
// that losing the process cannot lose the mail, so Deliver returns nil only
// after the blobs are synced and the row is committed with a pending
// extraction state. It is a promise about the process and not about the
// machine: the row commits under WAL with synchronous=NORMAL, so power loss
// is covered only as far as the last checkpoint. Everything after that
// point - the ledger event, the
// handoff to a worker, extraction itself - cannot un-store the message and
// therefore cannot turn into a refusal. Extraction then settles exactly one
// terminal state and the bus event follows that commit, so anything waiting
// on a message always sees its outcome rather than a row still in flight.
//
// A crash between the commit and the worker is the case Recover exists for:
// on start it scans pending rows only, so acked mail is always completed and
// a message that reliably kills extraction is settled failed once instead of
// being retried forever.
//
// Nothing here sends mail. There is no client, no dialer and no relay path,
// and a test parses this package to keep it that way.
package smtp
