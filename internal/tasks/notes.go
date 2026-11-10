package tasks

import "strings"

// Notes vteam writes start with a "<writer>: <outcome>" headline, and a run note ends with a "PR: <url>"
// paragraph when the run pushed to a PR. The Overview's pipeline stats read both back, so writers use these.
const prParagraphPrefix = "PR: "

func NoteHeadline(writer, outcome string) string {
	return writer + ": " + outcome
}

func HasNoteHeadline(body, writer, outcome string) bool {
	first, _, _ := strings.Cut(body, "\n")
	return first == NoteHeadline(writer, outcome)
}

func PRParagraph(url string) string {
	return prParagraphPrefix + url
}

func HasPRParagraph(body string) bool {
	for _, p := range strings.Split(body, "\n\n") {
		if strings.HasPrefix(p, prParagraphPrefix) {
			return true
		}
	}
	return false
}
