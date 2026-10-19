package muse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"ucli.local/ucli/internal/process"
)

const maxFrame = 1024 * 1024
const maxTranscript = 16 * maxFrame
const maxEvents = 2048

type envelope struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
	Result json.RawMessage
	Error  json.RawMessage
}

// rpc has one consuming caller and one outstanding request. Its limits are
// deliberately finite for local echo; long model transcripts need a spool.
type rpc struct {
	child                      *process.Child
	buffer                     []byte
	events                     []envelope
	nextID                     int
	total, frames, stderrBytes int
}

func object(raw json.RawMessage) bool {
	trim := bytes.TrimSpace(raw)
	return len(trim) > 1 && trim[0] == '{' && json.Valid(trim)
}

func decode(raw []byte) (envelope, error) {
	var e envelope
	if !utf8.Valid(raw) || !object(raw) {
		return e, errors.New("malformed native UTF-8/JSON envelope")
	}
	// Reject duplicate/case-ambiguous identity fields at every nesting level;
	// encoding/json otherwise accepts the later value. Bound nesting as well as
	// bytes so an adversarial frame cannot grow an unbounded decoder stack.
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := uniqueMembers(d, 0); err != nil {
		return e, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return e, errors.New("malformed native envelope")
	}
	var version string
	if json.Unmarshal(fields["jsonrpc"], &version) != nil || version != "2.0" {
		return e, errors.New("unsupported native JSON-RPC version")
	}
	e.ID, e.Params, e.Result, e.Error = fields["id"], fields["params"], fields["result"], fields["error"]
	if e.ID != nil {
		var id any
		dec := json.NewDecoder(bytes.NewReader(e.ID))
		dec.UseNumber()
		if dec.Decode(&id) != nil {
			return e, errors.New("invalid native ID")
		}
		switch v := id.(type) {
		case string:
			if v == "" || len(v) > 256 {
				return e, errors.New("invalid native ID")
			}
		case json.Number:
			if _, err := strconv.ParseInt(string(v), 10, 64); err != nil {
				return e, errors.New("invalid native ID")
			}
		default:
			return e, errors.New("invalid native ID")
		}
	}
	if method, ok := fields["method"]; ok {
		if json.Unmarshal(method, &e.Method) != nil || e.Method == "" || e.Result != nil || e.Error != nil {
			return e, errors.New("invalid native method envelope")
		}
		if e.Params != nil && !object(e.Params) {
			return e, errors.New("invalid native method parameters")
		}
	} else if e.ID == nil || (e.Result == nil) == (e.Error == nil) {
		return e, errors.New("invalid native response envelope")
	}
	if e.Error != nil && !object(e.Error) {
		return e, errors.New("invalid native error")
	}
	return e, nil
}

func uniqueMembers(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("native JSON nesting exceeds limit")
	}
	t, err := d.Token()
	if err != nil {
		return errors.New("malformed native JSON")
	}
	opening, composite := t.(json.Delim)
	if !composite {
		return nil
	}
	keys := map[string]bool{}
	for d.More() {
		if opening == '{' {
			t, err := d.Token()
			if err != nil {
				return errors.New("malformed native JSON member")
			}
			key, ok := t.(string)
			if !ok {
				return errors.New("invalid native JSON member")
			}
			key = strings.ToLower(key)
			if keys[key] {
				return errors.New("duplicate or case-ambiguous native JSON member")
			}
			keys[key] = true
		}
		if err := uniqueMembers(d, depth+1); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

func (r *rpc) send(ctx context.Context, value any) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return false, errors.New("cannot encode native request")
	}
	raw = append(raw, '\n')
	if len(raw) > maxFrame {
		return false, errors.New("native request exceeds frame limit")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// Once Send begins, errors may describe a partially accepted request.
	return true, r.child.Send(ctx, raw)
}

func (r *rpc) read(ctx context.Context) (envelope, error) {
	for {
		if i := bytes.IndexByte(r.buffer, '\n'); i >= 0 {
			if i+1 > maxFrame {
				return envelope{}, errors.New("native frame exceeds limit")
			}
			raw := r.buffer[:i]
			e, err := decode(raw)
			r.buffer = r.buffer[i+1:]
			r.frames++
			if r.frames > maxEvents {
				return envelope{}, errors.New("native event count exceeds echo limit")
			}
			return e, err
		}
		if len(r.buffer) >= maxFrame {
			return envelope{}, errors.New("native frame exceeds limit")
		}
		select {
		case <-ctx.Done():
			return envelope{}, ctx.Err()
		case chunk, ok := <-r.child.Events():
			if !ok {
				_, err := r.child.Wait(ctx)
				if err != nil {
					return envelope{}, fmt.Errorf("native transport stopped: %w", err)
				}
				if len(r.buffer) != 0 {
					return envelope{}, errors.New("native stdout ended within a frame")
				}
				return envelope{}, io.EOF
			}
			if chunk.Stream == "stderr" {
				r.stderrBytes += len(chunk.Data)
				continue
			}
			r.total += len(chunk.Data)
			if r.total > maxTranscript {
				return envelope{}, errors.New("native transcript exceeds echo limit")
			}
			r.buffer = append(r.buffer, chunk.Data...)
		}
	}
}

func (r *rpc) receive(ctx context.Context) (envelope, error) {
	for {
		e, err := r.read(ctx)
		if err != nil {
			return e, err
		}
		if e.Method != "" && e.ID != nil {
			// No native approval, dialog, tool or other server request receives
			// authority from this experimental driver.
			_, err = r.send(ctx, map[string]any{"jsonrpc": "2.0", "id": e.ID, "error": map[string]any{"code": -32601, "message": "UCLI echo driver grants no authority"}})
			if err != nil {
				return envelope{}, err
			}
			continue
		}
		return e, nil
	}
}

func (r *rpc) call(ctx context.Context, method string, params any) (json.RawMessage, bool, error) {
	r.nextID++
	id := r.nextID
	sent, err := r.send(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, sent, err
	}
	for {
		e, err := r.receive(ctx)
		if err != nil {
			return nil, sent, err
		}
		if e.Method != "" {
			if len(r.events) >= maxEvents {
				return nil, sent, errors.New("native notification queue exceeds limit")
			}
			r.events = append(r.events, e)
			continue
		}
		if string(e.ID) != strconv.Itoa(id) {
			return nil, sent, errors.New("native response ID does not match request")
		}
		if e.Error != nil {
			var value struct {
				Code int `json:"code"`
			}
			if json.Unmarshal(e.Error, &value) != nil {
				return nil, sent, errors.New("malformed native RPC error")
			}
			return nil, sent, fmt.Errorf("native %s rejected (RPC code %d)", method, value.Code)
		}
		if !object(e.Result) {
			return nil, sent, errors.New("native RPC result is not an object")
		}
		return e.Result, sent, nil
	}
}

func (r *rpc) event(ctx context.Context) (envelope, error) {
	if len(r.events) > 0 {
		e := r.events[0]
		r.events[0] = envelope{}
		r.events = r.events[1:]
		return e, nil
	}
	e, err := r.receive(ctx)
	if err == nil && e.Method == "" {
		return e, errors.New("unsolicited native response")
	}
	return e, err
}
