package cli

import (
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
	"ucli.local/ucli/internal/core"
)

// textEditor keeps its byte cursor on user-perceived character boundaries.
// Unicode15 segmentation/cell widths come from the pinned uniseg tables; actual
// terminal/font width conventions can differ, especially for newer Unicode.
type textEditor struct {
	text   string
	cursor int
}

func (e *textEditor) set(text string)     { e.text = text; e.cursor = len(text) }
func (e textEditor) beforeCursor() string { return e.text[:e.cursor] }
func (e textEditor) boundaries() []int {
	positions := []int{0}
	g := uniseg.NewGraphemes(e.text)
	for g.Next() {
		_, end := g.Positions()
		positions = append(positions, end)
	}
	return positions
}
func (e *textEditor) apply(event keyEvent) error {
	before, after := e.text[:e.cursor], e.text[e.cursor:]
	switch event.kind {
	case keyText, keyPaste, keyAltEnter:
		text := event.text
		if event.kind == keyAltEnter {
			text = "\n"
		}
		if event.kind == keyPaste {
			text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
		}
		if !utf8.ValidString(text) || strings.ContainsRune(text, 0) || len(text) > core.MaxInputBytes-len(e.text) {
			return core.Problem("invalid_input", "Input must be valid UTF-8 without NUL, at most 1 MiB.", 2)
		}
		e.text = before + text + after
		e.cursor += len(text)
		// Appending a combining mark/ZWJ can merge with adjacent text. Move the
		// cursor to the end of that cluster rather than splitting it next time.
		if e.cursor != len(e.text) {
			for _, position := range e.boundaries() {
				if position >= e.cursor {
					e.cursor = position
					break
				}
			}
		}
	case keyLeft, keyBackspace:
		previous := 0
		for _, position := range e.boundaries() {
			if position >= e.cursor {
				break
			}
			previous = position
		}
		if event.kind == keyBackspace {
			e.text = e.text[:previous] + after
		}
		e.cursor = previous
	case keyRight, keyDelete:
		next := e.cursor
		for _, position := range e.boundaries() {
			if position > e.cursor {
				next = position
				break
			}
		}
		if event.kind == keyDelete {
			e.text = before + e.text[next:]
		} else {
			e.cursor = next
		}
	case keyHome:
		e.cursor = strings.LastIndexByte(before, '\n') + 1
	case keyUp, keyDown:
		start := strings.LastIndexByte(before, '\n') + 1
		column := cellWidth(before[start:])
		var target string
		if event.kind == keyUp {
			if start == 0 {
				break
			}
			end := start - 1
			start = strings.LastIndexByte(e.text[:end], '\n') + 1
			target = e.text[start:end]
		} else {
			end := strings.IndexByte(after, '\n')
			if end < 0 {
				break
			}
			start = e.cursor + end + 1
			target, _, _ = strings.Cut(e.text[start:], "\n")
		}
		e.cursor = start + len(clipCells(target, column))
	case keyEnd:
		if next := strings.IndexByte(after, '\n'); next >= 0 {
			e.cursor += next
		} else {
			e.cursor = len(e.text)
		}
	case keyClear:
		e.text = ""
		e.cursor = 0
	case keyKillEnd:
		if next := strings.IndexByte(after, '\n'); next >= 0 {
			e.text = before + after[next:]
		} else {
			e.text = before
		}
	}
	return nil
}

func inlineText(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(safe(text), "\n", "↵"), "\t", "    ")
}
func cellWidth(text string) int { return uniseg.StringWidth(text) }
func clipCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	g := uniseg.NewGraphemes(text)
	end, used := 0, 0
	for g.Next() {
		if used+g.Width() > width {
			break
		}
		used += g.Width()
		_, end = g.Positions()
	}
	return text[:end]
}
