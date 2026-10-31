package cli

import (
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

// These are display-only block hints. They never create roles, tool events or
// actions, and all source characters (including Markdown fences) stay visible.
type transcriptFormat struct {
	fence byte
	count int
}

func (f *transcriptFormat) line(source string, partial bool) (literal, heading bool, hanging int) {
	trimmed := strings.TrimLeft(source, " ")
	indent := len(source) - len(trimmed)
	if partial {
		return true, false, 0 // the retained tail may start inside an omitted fence
	}
	if f.fence != 0 {
		if indent <= 3 && fenceLength(trimmed, f.fence) >= f.count && strings.TrimSpace(trimmed[fenceLength(trimmed, f.fence):]) == "" {
			f.fence, f.count = 0, 0
		}
		return true, false, 0
	}
	if indent <= 3 && len(trimmed) > 0 && (trimmed[0] == '`' || trimmed[0] == '~') {
		if count := fenceLength(trimmed, trimmed[0]); count >= 3 {
			f.fence, f.count = trimmed[0], count
			return true, false, 0
		}
	}
	if indent >= 4 || strings.HasPrefix(source, "\t") {
		return true, false, 0
	}
	if indent <= 3 {
		marks := 0
		for marks < len(trimmed) && trimmed[marks] == '#' {
			marks++
		}
		heading = marks >= 1 && marks <= 6 && marks < len(trimmed) && trimmed[marks] == ' '
		if len(trimmed) >= 2 && strings.ContainsRune("-*+", rune(trimmed[0])) && trimmed[1] == ' ' {
			hanging = indent + 2
		} else {
			digits := 0
			for digits < len(trimmed) && digits < 9 && trimmed[digits] >= '0' && trimmed[digits] <= '9' {
				digits++
			}
			if digits > 0 && digits+1 < len(trimmed) && (trimmed[digits] == '.' || trimmed[digits] == ')') && trimmed[digits+1] == ' ' {
				hanging = indent + digits + 2
			}
		}
	}
	return false, heading, hanging
}

func fenceLength(text string, marker byte) int {
	count := 0
	for count < len(text) && text[count] == marker {
		count++
	}
	return count
}

type transcriptPart struct {
	text   string
	offset int
	cells  int
	space  bool
}

// Keep a line-sized buffer, not one allocation per source character. Wrapping
// prefers whitespace boundaries while retaining whitespace and original byte
// offsets. Only a token wider than the available row is split by grapheme.
func wrapTranscriptSource(source string, offset, available, hanging int, literal bool, emit func(string, int, bool)) {
	if source == "" {
		emit("", offset, false)
		return
	}
	available = max(1, available)
	hanging = min(hanging, max(0, available-1))
	parts := make([]transcriptPart, 0, min(available, 128, len(source)))
	used, boundary := 0, 0
	continued := false
	capacity := func() int {
		if continued {
			return max(1, available-hanging)
		}
		return available
	}
	flush := func(count int) {
		var text strings.Builder
		for _, part := range parts[:count] {
			text.WriteString(part.text)
		}
		emit(text.String(), parts[0].offset, continued)
		copy(parts, parts[count:])
		parts = parts[:len(parts)-count]
		used, boundary = 0, 0
		for i, part := range parts {
			used += part.cells
			if part.space {
				boundary = i + 1
			}
		}
		continued = true
	}
	g := uniseg.NewGraphemes(source)
	for g.Next() {
		position, _ := g.Positions()
		part, cells := g.Str(), g.Width()
		space := strings.IndexFunc(part, func(r rune) bool { return !unicode.IsSpace(r) }) < 0
		if part == "\t" {
			part, cells = "    ", 4
		}
		for used+cells > capacity() && len(parts) > 0 {
			count := len(parts)
			if !literal && boundary > 0 {
				count = boundary
			}
			flush(count)
		}
		if cells > capacity() {
			part, cells = "?", 1 // existing explicit tiny-cell fallback
		}
		parts = append(parts, transcriptPart{part, offset + position, cells, space})
		used += cells
		if space {
			boundary = len(parts)
		}
	}
	if len(parts) > 0 {
		flush(len(parts))
	}
}
