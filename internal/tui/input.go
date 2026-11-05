package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// lineInput is a single-line text field edited with readline-style keys.
type lineInput struct {
	text   []rune
	cursor int
}

func newLineInput(s string) lineInput {
	r := []rune(s)
	return lineInput{text: r, cursor: len(r)}
}

func (in lineInput) String() string { return string(in.text) }

// handle applies an editing key and reports whether it was one.
func (in lineInput) handle(key string) (lineInput, bool) {
	switch key {
	case "left", "ctrl+b":
		in.cursor = max(0, in.cursor-1)
	case "right", "ctrl+f":
		in.cursor = min(len(in.text), in.cursor+1)
	case "home", "ctrl+a":
		in.cursor = 0
	case "end", "ctrl+e":
		in.cursor = len(in.text)
	case "backspace", "ctrl+h":
		if in.cursor > 0 {
			in.text = append(in.text[:in.cursor-1:in.cursor-1], in.text[in.cursor:]...)
			in.cursor--
		}
	case "delete", "ctrl+d":
		if in.cursor < len(in.text) {
			in.text = append(in.text[:in.cursor:in.cursor], in.text[in.cursor+1:]...)
		}
	case "ctrl+u":
		in.text = append([]rune{}, in.text[in.cursor:]...)
		in.cursor = 0
	case "ctrl+w":
		start := in.cursor
		for start > 0 && in.text[start-1] == ' ' {
			start--
		}
		for start > 0 && in.text[start-1] != ' ' {
			start--
		}
		in.text = append(in.text[:start:start], in.text[in.cursor:]...)
		in.cursor = start
	case "space":
		return in.insert(' '), true
	default:
		r, size := utf8.DecodeRuneInString(key)
		if size != len(key) || !unicode.IsPrint(r) {
			return in, false
		}
		return in.insert(r), true
	}
	return in, true
}

func (in lineInput) insert(r rune) lineInput {
	text := make([]rune, 0, len(in.text)+1)
	text = append(append(append(text, in.text[:in.cursor]...), r), in.text[in.cursor:]...)
	in.text = text
	in.cursor++
	return in
}

// view renders the text with the cursor as a reversed cell.
func (in lineInput) view() string {
	under := " "
	if in.cursor < len(in.text) {
		under = string(in.text[in.cursor])
	}
	after := ""
	if in.cursor < len(in.text) {
		after = string(in.text[in.cursor+1:])
	}
	return string(in.text[:in.cursor]) + styleSelected.Render(under) + after
}

func (in lineInput) trimmed() string {
	return strings.Join(strings.Fields(in.String()), " ")
}
