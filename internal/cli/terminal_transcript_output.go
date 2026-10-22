package cli

import "io"

// Typed messages use the same bounded delivery worker as notices and normal
// output. No task callback calls the potentially blocking program.Send itself.
type terminalMessageWriter struct {
	terminal *terminalUI
	message  terminalMessageMsg
}

func (w terminalMessageWriter) Write(data []byte) (int, error) {
	writer := w.terminal.writer
	writer.mu.Lock()
	defer writer.mu.Unlock()
	select {
	case <-w.terminal.done:
		return 0, io.ErrClosedPipe
	default:
	}
	if writer.pending.Len() > 0 {
		writer.complete(writer.pending.String())
		writer.pending.Reset()
	}
	w.terminal.program.Send(terminalFragmentMsg(""))
	w.message.Text = string(data)
	w.terminal.program.Send(w.message)
	return len(data), nil
}

func (o *output) prompt(text string) error {
	if o.terminal == nil || o.json {
		return o.text("\nYou\n" + text + "\n\n")
	}
	return o.write(terminalMessageWriter{o.terminal, terminalMessageMsg{Role: transcriptPrompt, Start: true}}, text)
}

func (o *output) beginReply(model string) error {
	if o.terminal == nil || o.json {
		return nil
	}
	label := "Assistant"
	if model != "" {
		label += " · " + model
	}
	return o.write(terminalMessageWriter{o.terminal, terminalMessageMsg{Role: transcriptAssistant, Label: label, Start: true}}, "")
}

func (o *output) assistant(text string) error {
	if o.terminal == nil || o.json {
		return o.text(text)
	}
	return o.write(terminalMessageWriter{o.terminal, terminalMessageMsg{Role: transcriptAssistant}}, text)
}
