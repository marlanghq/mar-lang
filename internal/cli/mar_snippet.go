package cli

import (
	"strings"
	"unicode"
)

const (
	cliMarParenColor   = "\033[38;5;244m"
	cliMarFormColor    = "\033[38;5;141m"
	cliMarSymbolColor  = "\033[38;5;110m"
	cliMarStringColor  = "\033[38;5;114m"
	cliMarLiteralColor = "\033[38;5;179m"
)

type marSnippetFrame struct {
	expectHead bool
}

func colorizeCLIMarSnippet(enabled bool, snippet string) string {
	if !enabled || strings.TrimSpace(snippet) == "" {
		return snippet
	}

	var out strings.Builder
	frames := []marSnippetFrame{}

	for i := 0; i < len(snippet); {
		switch snippet[i] {
		case '(':
			out.WriteString(colorizeCLI(true, cliMarParenColor, "("))
			frames = append(frames, marSnippetFrame{expectHead: true})
			i++
		case ')':
			out.WriteString(colorizeCLI(true, cliMarParenColor, ")"))
			if len(frames) > 0 {
				frames = frames[:len(frames)-1]
			}
			i++
		case '"':
			end := i + 1
			for end < len(snippet) {
				if snippet[end] == '\\' && end+1 < len(snippet) {
					end += 2
					continue
				}
				if snippet[end] == '"' {
					end++
					break
				}
				end++
			}
			out.WriteString(colorizeCLI(true, cliMarStringColor, snippet[i:end]))
			if len(frames) > 0 && frames[len(frames)-1].expectHead {
				frames[len(frames)-1].expectHead = false
			}
			i = end
		default:
			if isMarSnippetDelimiter(snippet[i]) {
				out.WriteByte(snippet[i])
				i++
				continue
			}

			end := i
			for end < len(snippet) && !isMarSnippetDelimiter(snippet[end]) {
				end++
			}
			token := snippet[i:end]
			color := cliMarSymbolColor
			if len(frames) > 0 && frames[len(frames)-1].expectHead {
				color = cliMarFormColor
				frames[len(frames)-1].expectHead = false
			} else if isMarSnippetLiteral(token) {
				color = cliMarLiteralColor
			}
			out.WriteString(colorizeCLI(true, color, token))
			i = end
		}
	}

	return out.String()
}

func isMarSnippetDelimiter(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '(', ')':
		return true
	default:
		return false
	}
}

func isMarSnippetLiteral(token string) bool {
	switch token {
	case "true", "false":
		return true
	}

	hasDigit := false
	for i, r := range token {
		if i == 0 && (r == '-' || r == '+') {
			continue
		}
		if r == '.' {
			continue
		}
		if !unicode.IsDigit(r) {
			return false
		}
		hasDigit = true
	}
	return hasDigit
}
