package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// These roles belong to the display cache, never to parsed provider prose.
// Durable inputs/outputs and the native context contract remain in app/state.
type transcriptRole uint8

const (
	transcriptSystem transcriptRole = iota
	transcriptPrompt
	transcriptAssistant
)

type transcriptRecord struct {
	ID                uint64
	Role              transcriptRole
	Label             string
	Text              string
	Base              int    // bytes omitted from the beginning of this record
	TaskID, AttemptID string // display ownership, never a native session identity
}

type transcriptAnchor struct {
	ID     uint64
	Offset int // -2 is block spacing; -1 is the heading; nonnegative offsets are source bytes
}

type transcriptRow struct {
	terminalLine
	anchor transcriptAnchor
}

// Width-one and newline-heavy output must not allocate a row per source byte.
// Wrapping scans at most 64 KiB and retains 600 rows; durable /result is separate.
const transcriptDisplayRows = 600

type transcriptRowsBuilder struct {
	rows    []transcriptRow
	head    int
	dropped bool
}

func (b *transcriptRowsBuilder) add(row transcriptRow) {
	if len(b.rows) < transcriptDisplayRows {
		b.rows = append(b.rows, row)
		return
	}
	b.rows[b.head] = row
	b.head = (b.head + 1) % transcriptDisplayRows
	b.dropped = true
}

func (b transcriptRowsBuilder) finish() []transcriptRow {
	if !b.dropped {
		return b.rows
	}
	rows := append([]transcriptRow(nil), b.rows[b.head:]...)
	rows = append(rows, b.rows[:b.head]...)
	rows[0] = omittedTranscriptRow()
	return rows
}

func omittedTranscriptRow() transcriptRow {
	return transcriptRow{terminalLine: terminalLine{Text: "Earlier display omitted · /result opens saved output.", Dim: true}}
}

// Unscoped foreground/results retain FIFO/beginReply ordering. Scoped assistant
// deltas belong to one exact task/attempt pair; record IDs remain display anchors.
type terminalMessageMsg struct {
	Role              transcriptRole
	Label             string
	Text              string
	Start             bool // allocate a new unscoped message, including an initially empty reply
	TaskID, AttemptID string
}

func (s *terminalShell) append(text string) {
	s.message(terminalMessageMsg{Role: transcriptSystem, Text: text, Start: true})
}

func (s *terminalShell) message(message terminalMessageMsg) {
	scoped := message.TaskID != "" || message.AttemptID != ""
	if scoped && (!validTranscriptScopeID(message.TaskID) || !validTranscriptScopeID(message.AttemptID)) {
		return // an invalid partial scope must never fall back to the active reply
	}
	if message.Role == transcriptPrompt && message.Start {
		// An explicit submission/result inspection is navigation intent. Only
		// subsequent streaming output must preserve a manually paused reader.
		s.paused, s.anchor = false, transcriptAnchor{}
	}
	if !scoped && message.Role == transcriptAssistant && message.Start && message.Text == "" {
		s.replyID, s.replyLabel = 0, clipCells(inlineText(message.Label), 160)
		return
	}
	if !scoped && message.Role == transcriptAssistant && message.Label == "" {
		message.Label = s.replyLabel
	}
	text := safe(message.Text)
	index := -1
	if message.Role == transcriptAssistant && (scoped || !message.Start) {
		for i := range s.transcript {
			record := &s.transcript[i]
			match := !scoped && record.ID == s.replyID && record.TaskID == "" && record.AttemptID == ""
			if scoped {
				match = record.Role == transcriptAssistant && record.TaskID == message.TaskID && record.AttemptID == message.AttemptID
			}
			if match {
				index = i
				break
			}
		}
	}
	if index < 0 {
		s.nextID++
		// A scan of the bounded display cache avoids retaining identities after
		// eviction. Late output for an evicted attempt starts a new visible tail.
		s.transcript = append(s.transcript, transcriptRecord{ID: s.nextID, Role: message.Role, Label: clipCells(inlineText(message.Label), 160), TaskID: strings.Clone(message.TaskID), AttemptID: strings.Clone(message.AttemptID)})
		index = len(s.transcript) - 1
		if !scoped && message.Role == transcriptAssistant {
			s.replyID = s.nextID
		}
	}
	s.transcript[index].Text += text
	s.bytes += len(text)
	// Keep a bounded tail even for one enormous newline-free reply. IDs and
	// original byte offsets survive trimming; an evicted prompt is not jumpable.
	for len(s.transcript) > 1 && (s.bytes-len(s.transcript[0].Text) >= shellTranscriptBytes || len(s.transcript) > shellTranscriptLines) {
		s.bytes -= len(s.transcript[0].Text)
		s.transcript[0] = transcriptRecord{} // do not retain evicted strings in slice capacity
		s.transcript = s.transcript[1:]
		s.dropped = true
	}
	if s.bytes > shellTranscriptBytes {
		r := &s.transcript[0]
		cut := s.bytes - shellTranscriptBytes
		for cut < len(r.Text) && !utf8.RuneStart(r.Text[cut]) {
			cut++
		}
		r.Text, r.Base = strings.Clone(r.Text[cut:]), r.Base+cut
		s.bytes -= cut
		s.dropped = true
	}
}

func (s terminalShell) transcriptText() string {
	var text strings.Builder
	for i, record := range s.transcript {
		if i > 0 {
			text.WriteByte('\n')
		}
		text.WriteString(record.Text)
	}
	return text.String()
}

func (s terminalShell) latestPrompt() (transcriptRecord, bool) {
	for i := len(s.transcript) - 1; i >= 0; i-- {
		if s.transcript[i].Role == transcriptPrompt && s.transcript[i].Base == 0 {
			return s.transcript[i], true
		}
	}
	return transcriptRecord{}, false
}

// Wrapping carries source byte anchors, so appending output or changing terminal
// width does not turn the reader's current paragraph into a tail-relative offset.
// Markdown/code stay literal: indentation, fences, and newlines are not rewritten.
// Prose wraps at word boundaries; display gutters never enter stored content.
func appendRecordRows(record transcriptRecord, width int, rows *transcriptRowsBuilder) {
	width = max(1, width-1)
	prefix := "  "
	if width < 4 {
		prefix = "" // retain a content cell before decorative indentation
	}
	band := record.Role == transcriptPrompt
	if record.Role != transcriptSystem {
		label := record.Label
		if label == "" {
			label = "Assistant"
			if band {
				label = "Your prompt"
			}
		}
		captionPrefix := ""
		if band {
			captionPrefix = prefix
		}
		caption := terminalLine{Text: captionPrefix + label, Band: band, Bold: true}
		if record.Role == transcriptAssistant && validTranscriptScopeID(record.TaskID) && validTranscriptScopeID(record.AttemptID) {
			caption.Action = jobCaptionAction(record.TaskID, record.AttemptID)
		}
		rows.add(transcriptRow{caption, transcriptAnchor{record.ID, -1}})
	}
	available := max(1, width-cellWidth(prefix))
	offset := record.Base
	var format transcriptFormat
	for _, source := range strings.Split(record.Text, "\n") {
		literal, heading, hanging := format.line(source, record.Base > 0)
		if record.Role != transcriptAssistant {
			heading, hanging = false, 0
		}
		wrapTranscriptSource(source, offset, available, hanging, literal, func(text string, start int, continued bool) {
			marker := prefix
			if band && start == 0 && width >= 3 {
				marker = "> "
			}
			if continued && hanging > 0 {
				marker += strings.Repeat(" ", min(hanging, max(0, available-1)))
			}
			rows.add(transcriptRow{terminalLine{Text: marker + text, Band: band, Bold: heading}, transcriptAnchor{record.ID, start}})
		})
		offset += len(source) + 1
	}
}

func (b *transcriptRowsBuilder) separate(id uint64) {
	if len(b.rows) == 0 {
		return
	}
	last := len(b.rows) - 1
	if b.dropped {
		last = (b.head + len(b.rows) - 1) % len(b.rows)
	}
	if strings.TrimSpace(b.rows[last].Text) != "" {
		b.add(transcriptRow{anchor: transcriptAnchor{id, -2}})
	}
}

func (s terminalShell) transcriptRows(fragment string, width int) []transcriptRow {
	var all transcriptRowsBuilder
	if s.dropped {
		all.add(omittedTranscriptRow())
	}
	previous := transcriptSystem
	first := true
	appendRecord := func(record transcriptRecord) {
		if !first && (record.Role != transcriptSystem || previous != transcriptSystem) {
			all.separate(record.ID)
		}
		if record.Role == transcriptSystem && (first || previous != transcriptSystem) {
			// Writer.complete emits separate system records for each real line.
			// One visual caption groups those records without coalescing identities.
			all.add(transcriptRow{terminalLine{Text: "UCLI", Bold: true}, transcriptAnchor{record.ID, -1}})
		}
		appendRecordRows(record, width, &all)
		first, previous = false, record.Role
	}
	for _, record := range s.transcript {
		appendRecord(record)
	}
	if fragment != "" {
		appendRecord(transcriptRecord{ID: s.nextID + 1, Role: transcriptSystem, Text: fragment})
	}
	return all.finish()
}

func promptRowIndex(rows []transcriptRow, id uint64) int {
	for i, row := range rows {
		if row.anchor.ID == id && row.anchor.Offset == -1 {
			return i
		}
	}
	return -1
}

func anchorIndex(rows []transcriptRow, anchor transcriptAnchor) int {
	best := -1
	for i, row := range rows {
		if row.anchor.ID == anchor.ID {
			if anchor.Offset == -2 && row.anchor.Offset == -1 {
				return i // an explicit source blank may replace our display separator
			}
			if anchor.Offset >= 0 && row.anchor.Offset < 0 {
				continue
			}
			if row.anchor.Offset == anchor.Offset {
				return i
			}
			if row.anchor.Offset <= anchor.Offset {
				best = i
			}
		}
	}
	return max(0, best) // pruned content falls back to the explicit omission row
}

type transcriptWindow struct {
	rows           []transcriptRow
	start, end     int
	sticky, footer bool
}

func (s terminalShell) transcriptWindow(fragment string, width, height int) transcriptWindow {
	w := transcriptWindow{rows: s.transcriptRows(fragment, width)}
	if height <= 0 {
		return w
	}
	w.footer = height >= 3 && (len(w.rows) > height || s.paused)
	space := height
	if w.footer {
		space--
	}
	w.start = max(0, len(w.rows)-space)
	if s.paused {
		w.start = anchorIndex(w.rows, s.anchor)
	}
	if prompt, ok := s.latestPrompt(); ok && height >= 4 {
		index := promptRowIndex(w.rows, prompt.ID)
		w.sticky = index >= 0 && index < w.start
		if w.sticky {
			space--
			if !s.paused {
				w.start = max(0, len(w.rows)-space)
			}
		}
	}
	w.end = min(len(w.rows), w.start+space)
	return w
}

func (s terminalShell) history(fragment string, width, height int) []terminalLine {
	if height <= 0 {
		return nil
	}
	w := s.transcriptWindow(fragment, width, height)
	var visible []terminalLine
	if w.sticky {
		prompt, _ := s.latestPrompt()
		label := "Latest prompt · Ctrl+Up · " + strings.Join(strings.Fields(prompt.Text), " ")
		if width < 44 {
			label = "Prompt Ctrl+Up · " + strings.Join(strings.Fields(prompt.Text), " ")
		}
		if width < 18 {
			label = "Ctrl+Up prompt"
		}
		visible = append(visible, terminalLine{Text: label, Band: true, Action: "prompt"})
	}
	for _, row := range w.rows[w.start:w.end] {
		visible = append(visible, row.terminalLine)
	}
	if w.footer {
		for len(visible) < height-1 {
			visible = append(visible, terminalLine{})
		}
		label := fmt.Sprintf("PgUp/PgDn output · %d–%d of %d", w.start+1, w.end, len(w.rows))
		if s.paused {
			label = "Following paused · Latest output Ctrl+End"
			if width < 44 {
				label = "Paused · Ctrl+End latest"
			}
		}
		visible = append(visible, terminalLine{Text: label, Dim: true, Action: "latest"})
	}
	return visible
}

func (s *terminalShell) navigate(kind keyKind, fragment string, width, height int) {
	if kind == keyLatestOutput {
		s.paused, s.anchor = false, transcriptAnchor{}
		return
	}
	w := s.transcriptWindow(fragment, width, height)
	if len(w.rows) == 0 {
		return
	}
	if kind == keyLatestPrompt {
		if prompt, ok := s.latestPrompt(); ok && promptRowIndex(w.rows, prompt.ID) >= 0 {
			s.paused, s.anchor = true, transcriptAnchor{prompt.ID, -1}
		}
		return
	}
	step := max(1, w.end-w.start-1)
	start := max(0, w.start-step)
	if kind == keyPageDown {
		start = w.start + step
		if start+max(1, w.end-w.start) >= len(w.rows) {
			s.paused, s.anchor = false, transcriptAnchor{}
			return
		}
	}
	s.paused, s.anchor = true, w.rows[min(start, len(w.rows)-1)].anchor
}
