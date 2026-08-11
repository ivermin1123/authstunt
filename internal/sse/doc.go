// Package sse is the concurrency core: an in-process event bus owned by a
// single goroutine, the long-poll waiter registry, and the SSE framing with
// Last-Event-ID replay and reset semantics.
//
// It has no store dependency. The event generation is a number the caller
// reads once per start and passes to NewBus, and long-poll matching runs on
// the MessageRef carried by each event, so nothing here needs a database
// handle. Composing a waiter with a store query is the API layer's job, and
// the ordering it must follow is documented on Waiter.
package sse
