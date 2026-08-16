package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// JSON-RPC 2.0 error codes, plus the one code the 2026-07-28 revision
// added for version negotiation.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
	// codeUnsupportedProtocolVersion answers a request that declared a
	// protocol version this server does not implement. The data payload
	// lists what it does, which is how a client recovers.
	codeUnsupportedProtocolVersion = -32022
)

// maxMessageBytes caps one incoming line.
//
// Every message this server accepts is a small JSON object naming a tool
// and a few short ids. The cap exists so a peer that never sends a
// newline cannot grow the read buffer without bound.
const maxMessageBytes = 1 << 20

// request is an incoming JSON-RPC message.
//
// ID is kept as raw JSON because JSON-RPC allows a string or a number and
// a response must echo the value it was given, unchanged. Decoding it
// into an any would round-trip 1 as 1 and "1" as "1" correctly today and
// stop doing so the moment someone reaches for a numeric type.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether the peer expects no answer. A JSON-RPC
// notification is exactly a request with no id.
func (r request) isNotification() bool { return len(r.ID) == 0 }

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message) }

func newResult(id json.RawMessage, result any) response {
	return response{JSONRPC: "2.0", ID: id, Result: result}
}

func newError(id json.RawMessage, code int, message string, data any) response {
	return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message, Data: data}}
}

// errMessageTooLarge reports a line over the cap. It ends the session:
// the stream is newline framed, so a truncated read leaves the reader
// mid-message with no way to resynchronize.
var errMessageTooLarge = errors.New("mcp: message over the size cap")

// reader pulls newline delimited JSON off a stream.
type reader struct {
	buf *bufio.Reader
}

func newReader(r io.Reader) *reader {
	return &reader{buf: bufio.NewReaderSize(r, 64<<10)}
}

// next reads one message. It returns io.EOF when the peer closed the
// stream, which is how a stdio session ends.
//
// A blank line is skipped rather than treated as a parse error: it is not
// a message, and answering one would put a response on the wire that no
// request asked for.
func (r *reader) next() ([]byte, error) {
	for {
		line, err := r.readLine()
		if err != nil {
			return nil, err
		}
		if line = bytes.TrimSpace(line); len(line) > 0 {
			return line, nil
		}
	}
}

func (r *reader) readLine() ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.buf.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxMessageBytes {
			return nil, errMessageTooLarge
		}
		if err == nil {
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			// A final message with no trailing newline is still a
			// message.
			return line, nil
		}
		return nil, err
	}
}

// writer serializes outgoing messages.
//
// Responses are written from one goroutine per in-flight request, so the
// mutex is what keeps two encodings from interleaving into a line that is
// neither message.
type writer struct {
	mu  sync.Mutex
	out io.Writer
}

func newWriter(w io.Writer) *writer { return &writer{out: w} }

// write emits one message followed by a newline.
//
// json.Marshal escapes every newline inside a string, so the only newline
// in the output is the framing one - which is what the stdio transport
// requires.
func (w *writer) write(v any) error {
	encoded, err := json.Marshal(v)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.out.Write(encoded)
	return err
}
