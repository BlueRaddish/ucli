package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"ucli.local/ucli/internal/process"
)

const maxFrame = 1024 * 1024
const maxTranscript = 16 * maxFrame
const maxEvents = 4096
const maxQueued = 128

type envelope struct {
	ID, Params, Result, Error json.RawMessage
	Method                    string
}

// One consuming caller and one outstanding request. This is a finite transcript
// experiment; long-running conversations need a reviewed spool/resume boundary.
type rpc struct {
	child         *process.Child
	buffer        []byte
	events        []envelope
	nextID        int
	total, frames int
}

func objectFields(raw json.RawMessage) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	return fields
}

func textField(fields map[string]json.RawMessage, key string, limit int) (string, bool) {
	var value string
	err := json.Unmarshal(fields[key], &value)
	return value, err == nil && len(value) <= limit && !isNull(fields[key]) && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func equals(fields map[string]json.RawMessage, key, want string) bool {
	got, ok := textField(fields, key, maxFrame)
	return ok && got == want
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func arrayField(fields map[string]json.RawMessage, key string, target *[]json.RawMessage) bool {
	raw := bytes.TrimSpace(fields[key])
	return len(raw) > 1 && raw[0] == '[' && json.Unmarshal(raw, target) == nil
}

func decode(raw []byte) (envelope, error) {
	var e envelope
	if len(raw) >= maxFrame || !utf8.Valid(raw) || !validUnicodeEscapes(raw) || !json.Valid(raw) {
		return e, errors.New("Codex frame is malformed or exceeds its limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := uniqueMembers(decoder, 0); err != nil {
		return e, err
	}
	fields := objectFields(raw)
	if fields == nil {
		return e, errors.New("Codex envelope must be an object")
	}
	for key := range fields {
		switch key {
		case "id", "method", "params", "result", "error":
		case "emittedAtMs":
			var milliseconds int64
			if fields["method"] == nil || json.Unmarshal(fields[key], &milliseconds) != nil || milliseconds < 0 || isNull(fields[key]) {
				return e, errors.New("Codex notification timestamp is invalid")
			}
		default:
			return e, errors.New("Codex envelope has an unsupported field or dialect")
		}
	}
	e.ID, e.Params, e.Result, e.Error = fields["id"], fields["params"], fields["result"], fields["error"]
	if e.ID != nil {
		var value any
		d := json.NewDecoder(bytes.NewReader(e.ID))
		d.UseNumber()
		if d.Decode(&value) != nil {
			return e, errors.New("Codex request ID is invalid")
		}
		switch id := value.(type) {
		case string:
			if !validID(id) {
				return e, errors.New("Codex request ID is invalid")
			}
		case json.Number:
			if _, err := strconv.ParseInt(string(id), 10, 64); err != nil {
				return e, errors.New("Codex request ID is invalid")
			}
		default:
			return e, errors.New("Codex request ID is invalid")
		}
	}
	if _, method := fields["method"]; method {
		var ok bool
		e.Method, ok = textField(fields, "method", 256)
		if !ok || e.Method == "" || e.Result != nil || e.Error != nil || objectFields(e.Params) == nil {
			return e, errors.New("Codex method envelope is invalid")
		}
	} else if e.ID == nil || (e.Result == nil) == (e.Error == nil) || e.Params != nil {
		return e, errors.New("Codex response envelope is invalid")
	}
	if e.Error != nil && objectFields(e.Error) == nil {
		return e, errors.New("Codex RPC error is malformed")
	}
	return e, nil
}

func uniqueMembers(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("Codex JSON nesting exceeds its limit")
	}
	token, err := d.Token()
	if err != nil {
		return errors.New("Codex JSON is malformed")
	}
	opening, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	seen := map[string]bool{}
	for d.More() {
		if opening == '{' {
			t, err := d.Token()
			key, ok := t.(string)
			if err != nil || !ok {
				return errors.New("Codex JSON member is malformed")
			}
			key = strings.ToLower(key)
			if seen[key] {
				return errors.New("Codex JSON contains duplicate or ambiguous members")
			}
			seen[key] = true
		}
		if err := uniqueMembers(d, depth+1); err != nil {
			return err
		}
	}
	if _, err := d.Token(); err != nil {
		return errors.New("Codex JSON is malformed")
	}
	return nil
}

// encoding/json substitutes U+FFFD for unpaired surrogate escapes. Reject these
// before decoding identity or text rather than silently changing native output.
func validUnicodeEscapes(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' || i+1 >= len(raw) {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		r, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil || r >= 0xdc00 && r <= 0xdfff {
			return false
		}
		i += 4
		if r >= 0xd800 && r <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

// Preserve only stable context causes; transport and callback errors may contain
// native paths, credentials or prompt text and must not enter durable receipts.
func transportError(err error) error {
	if errors.Is(err, context.Canceled) {
		return errors.Join(errors.New("Codex transport canceled"), context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(errors.New("Codex transport deadline exceeded"), context.DeadlineExceeded)
	}
	return errors.New("Codex owned transport stopped or failed")
}

func (r *rpc) send(ctx context.Context, value any) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw)+1 > maxFrame {
		return false, errors.New("Codex request cannot be encoded within its frame limit")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// Once writing starts even an error may describe a partially accepted request.
	if err := r.child.Send(ctx, append(raw, '\n')); err != nil {
		return true, transportError(err)
	}
	return true, nil
}

func (r *rpc) read(ctx context.Context) (envelope, error) {
	for {
		if err := ctx.Err(); err != nil {
			return envelope{}, err
		}
		if i := bytes.IndexByte(r.buffer, '\n'); i >= 0 {
			if i+1 > maxFrame {
				return envelope{}, errors.New("Codex frame exceeds its limit")
			}
			e, err := decode(r.buffer[:i])
			r.buffer = r.buffer[i+1:]
			r.frames++
			if r.frames > maxEvents {
				return envelope{}, errors.New("Codex event count exceeds its limit")
			}
			return e, err
		}
		if len(r.buffer) >= maxFrame {
			return envelope{}, errors.New("Codex frame exceeds its limit")
		}
		select {
		case <-ctx.Done():
			return envelope{}, ctx.Err()
		case chunk, ok := <-r.child.Events():
			if !ok {
				_, err := r.child.Wait(ctx)
				if err != nil {
					return envelope{}, transportError(err)
				}
				if len(r.buffer) != 0 {
					return envelope{}, errors.New("Codex stdout ended within a frame")
				}
				return envelope{}, io.EOF
			}
			// Count both streams. Discard stderr without retaining diagnostics.
			r.total += len(chunk.Data)
			if r.total > maxTranscript {
				return envelope{}, errors.New("Codex transcript exceeds its limit")
			}
			if chunk.Stream == "stdout" {
				r.buffer = append(r.buffer, chunk.Data...)
			}
		}
	}
}

func (r *rpc) receive(ctx context.Context) (envelope, error) {
	for {
		e, err := r.read(ctx)
		if err != nil || e.Method == "" || e.ID == nil {
			return e, err
		}
		// Reject every server request, including approvals/tools/token refresh.
		// No server request can broaden the already selected native permission.
		if _, err = r.send(ctx, map[string]any{"id": e.ID, "error": map[string]any{"code": -32601, "message": "UCLI grants no additional server-request authority"}}); err != nil {
			return envelope{}, err
		}
	}
}

func (r *rpc) call(ctx context.Context, method string, params any) (json.RawMessage, bool, error) {
	r.nextID++
	id := r.nextID
	sent, err := r.send(ctx, map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, sent, err
	}
	for {
		e, err := r.receive(ctx)
		if err != nil {
			return nil, sent, err
		}
		if e.Method != "" {
			if len(r.events) >= maxQueued {
				return nil, sent, errors.New("Codex pending notification queue exceeds its limit")
			}
			r.events = append(r.events, e)
			continue
		}
		if string(e.ID) != strconv.Itoa(id) {
			return nil, sent, errors.New("Codex response ID does not match the outstanding request")
		}
		if e.Error != nil {
			var code int64
			if json.Unmarshal(objectFields(e.Error)["code"], &code) == nil {
				return nil, sent, errors.New("Codex rejected RPC " + method + " (code " + strconv.FormatInt(code, 10) + "); native diagnostics were withheld")
			}
			return nil, sent, errors.New("Codex rejected an RPC request; native diagnostics were withheld")
		}
		if objectFields(e.Result) == nil {
			return nil, sent, errors.New("Codex RPC result is not an object")
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
		return e, errors.New("Codex sent an unsolicited response")
	}
	return e, err
}
