package cli

import "fmt"

func completionCanAccept(result completionResult, width, rows int) bool {
	return result.Visible && result.Focus >= 0 && result.Focus < len(result.Rows) && rows > 0 &&
		cellWidth("> "+result.Rows[result.Focus].Name) < width
}

// Completion belongs to the input, not the modal panel layer. It uses a bounded
// part of the transcript area while leaving notices, editor and footer fixed.
func completionLines(result completionResult, query string, width, rows int) []terminalLine {
	if !result.Visible || rows <= 0 {
		return nil
	}
	rows = min(rows, 8)
	title := "Commands"
	if query != "/" {
		title += " matching " + query
	}
	if len(result.Rows) == 0 {
		lines := []terminalLine{{Text: "No commands match " + query, Dim: true}}
		if rows > 1 {
			lines = append(lines, terminalLine{Text: "Keep typing · Esc close", Dim: true})
		}
		return lines
	}
	focus := min(max(0, result.Focus), len(result.Rows)-1)
	if !completionCanAccept(result, width, rows) {
		return []terminalLine{{Text: "Resize for commands · Esc close", Dim: true}}
	}
	title += fmt.Sprintf(" · %d/%d", focus+1, len(result.Rows))
	count := max(1, rows-2)
	start := min(max(0, focus-count+1), max(0, len(result.Rows)-count))
	end := min(len(result.Rows), start+count)
	var lines []terminalLine
	if rows >= 3 {
		lines = append(lines, terminalLine{Text: title, Tone: toneInfo})
	}
	for index := start; index < end; index++ {
		marker := "  "
		if index == focus {
			marker = "> "
		}
		command := result.Rows[index]
		lines = append(lines, terminalLine{Text: marker + command.Usage + "  " + command.Description, Focused: index == focus})
	}
	if rows > 1 {
		exact := query == result.Rows[focus].Name
		lines = append(lines, terminalLine{Text: completionHint(width, exact), Dim: true})
	}
	return lines
}

func completionHint(width int, exact bool) string {
	enter := "Enter/Tab complete"
	if exact {
		enter = "Enter run · Tab complete"
	}
	for _, hint := range []string{
		"Up/Down select · " + enter + " · Esc close",
		"Up/Down · " + enter + " · Esc close",
		"Up/Down · Tab complete · Esc close",
		"Tab complete · Esc close",
		"Tab · Esc",
	} {
		if cellWidth(hint) < width {
			return hint
		}
	}
	return ""
}
