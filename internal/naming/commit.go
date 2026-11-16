package naming

import "strings"

const trailerKey = "Ralph-Task:"

// RenderSubject renders a commit subject template into a single line.
func RenderSubject(tmpl string, d Data) (string, error) {
	s, err := render(tmpl, d)
	if err != nil {
		return "", err
	}
	s = strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
	return strings.TrimSpace(s), nil
}

// FormatCommitMessage joins the subject and, if trailer is set, a Ralph-Task trailer.
func FormatCommitMessage(subject, id string, trailer bool) string {
	if !trailer {
		return subject + "\n"
	}
	return subject + "\n\n" + trailerKey + " " + id + "\n"
}

// ParseTaskTrailer returns the ID of the last Ralph-Task trailer in a message's final paragraph.
func ParseTaskTrailer(message string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	for i := end - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			break
		}
		if rest, ok := strings.CutPrefix(line, trailerKey); ok {
			if id := strings.TrimSpace(rest); id != "" {
				return id, true
			}
		}
	}
	return "", false
}
