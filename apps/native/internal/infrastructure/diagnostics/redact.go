// Package diagnostics produces bounded, non-sensitive diagnostic projections.
package diagnostics

import (
	"regexp"
	"strings"
	"unicode"
)

var sensitive = regexp.MustCompile(`(?i)(?:["'](?:api[_-]?key|token|secret|transcript|providerBody|petText)["']|\b(?:api[_-]?key|token|secret|transcript|providerBody|petText))\s*[:=]\s*`)
var bearer = regexp.MustCompile(`(?i)\bbearer\s+[^\s,;]+`)
var locationStart = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://|[a-z]:[\\/]|\\\\|\\?/`)
var locationOrQuote = regexp.MustCompile(`(?i)["']|[a-z][a-z0-9+.-]*://|[a-z]:[\\/]|\\\\|\\?/`)
var terminalControl = regexp.MustCompile("\x1b(?:\\[[0-?]*[ -/]*[@-~]|\\][^\x07\x1b]*(?:\x07|\x1b\\\\))")

func Text(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	value = terminalControl.ReplaceAllString(value, "")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			// Keep line boundaries until paths have been concealed. An unquoted
			// path may contain spaces, but must not consume the next log line.
			if r == '\n' || r == '\r' {
				return r
			}
			if unicode.IsSpace(r) {
				return ' '
			}
			return -1
		}
		return r
	}, value)
	value = redactValues(value)
	value = bearer.ReplaceAllString(value, "Bearer [redacted]")
	// Logs are diagnostics, not a way to transfer paths or remote URLs. Conceal
	// locations completely, including whitespace-bearing Windows paths.
	value = redactLocations(value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit {
		runes = runes[:max(0, limit)]
	}
	return string(runes)
}

// quotedEnd consumes escaped quotes and backslashes as part of the value. A
// truncated quoted value consumes the remainder rather than exposing its tail.
func quotedEnd(value string, start int) (end int, closed bool) {
	for i := start + 1; i < len(value); i++ {
		if value[i] == '\\' {
			i++
		} else if value[i] == value[start] {
			return i + 1, true
		}
	}
	return len(value), false
}

func valueEnd(value string, start int) int {
	if start == len(value) {
		return start
	}
	if value[start] == '"' || value[start] == '\'' {
		end, _ := quotedEnd(value, start)
		return end
	}
	if value[start] == '{' || value[start] == '[' {
		// An iterative stack bounds memory by input length without recursive
		// calls for deeply nested provider responses.
		var stack []byte
		for i := start; i < len(value); i++ {
			switch value[i] {
			case '"', '\'':
				end, _ := quotedEnd(value, i)
				i = end - 1
			case '{', '[':
				stack = append(stack, value[i])
			case '}', ']':
				opening := stack[len(stack)-1]
				if opening == '{' && value[i] != '}' || opening == '[' && value[i] != ']' {
					return len(value)
				}
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					return i + 1
				}
			}
		}
		return len(value)
	}
	if end := strings.IndexAny(value[start:], " \t\r\n,;}]"); end >= 0 {
		return start + end
	}
	return len(value)
}

func redactValues(value string) string {
	var output strings.Builder
	for {
		match := sensitive.FindStringIndex(value)
		if match == nil {
			output.WriteString(value)
			return output.String()
		}
		output.WriteString(value[:match[1]])
		output.WriteString("[redacted]")
		value = value[valueEnd(value, match[1]):]
	}
}

func redactLocations(value string) string {
	var output strings.Builder
	for {
		match := locationOrQuote.FindStringIndex(value)
		if match == nil {
			output.WriteString(value)
			return output.String()
		}
		start := match[0]
		output.WriteString(value[:start])
		if value[start] == '"' || value[start] == '\'' {
			end, closed := quotedEnd(value, start)
			contentEnd := end
			if closed {
				contentEnd--
			}
			content := value[start+1 : contentEnd]
			if location := locationStart.FindStringIndex(content); location != nil {
				output.WriteByte(value[start])
				output.WriteString(content[:location[0]])
				output.WriteString("[redacted-path]")
				if closed {
					output.WriteByte(value[start])
				}
			} else {
				output.WriteString(value[start:end])
			}
			value = value[end:]
			continue
		}
		// No reliable delimiter separates a space-bearing unquoted filename
		// from following prose. Conceal the remainder of this line.
		end := strings.IndexAny(value[start:], "\r\n")
		if end < 0 {
			end = len(value) - start
		}
		output.WriteString("[redacted-path]")
		value = value[start+end:]
	}
}
