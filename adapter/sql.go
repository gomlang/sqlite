package adapter

import (
	"fmt"
	"strings"
)

func tokens(text string) ([]string, error) {
	result := make([]string, 0)
	for i := 0; i < len(text); {
		b := text[i]
		// SQLite treats a UTF-8 BOM at a token boundary as whitespace.
		if strings.HasPrefix(text[i:], "\ufeff") {
			i += len("\ufeff")
			continue
		}
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' {
			i++
			continue
		}
		if i+1 < len(text) && text[i:i+2] == "--" {
			for i < len(text) && text[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(text) && text[i:i+2] == "/*" {
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated SQL comment", ErrArgument)
			}
			i += end + 4
			continue
		}
		if b == '\'' || b == '"' || b == '`' || b == '[' {
			end := b
			if b == '[' {
				end = ']'
			}
			i++
			closed := false
			for i < len(text) {
				if text[i] == end {
					i++
					if end != ']' && i < len(text) && text[i] == end {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("%w: unterminated SQL quote", ErrArgument)
			}
			result = append(result, "?")
			continue
		}
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_' || b >= 128 {
			start := i
			i++
			for i < len(text) {
				b = text[i]
				if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '$' || b >= 128) {
					break
				}
				i++
			}
			// SQLite keywords fold ASCII only. Unicode uppercasing can turn
			// ordinary identifiers such as caſe into control-flow keywords.
			word := []byte(text[start:i])
			for index, ch := range word {
				if ch >= 'a' && ch <= 'z' {
					word[index] = ch - ('a' - 'A')
				}
			}
			result = append(result, string(word))
			continue
		}
		result = append(result, string(b))
		i++
	}
	return result, nil
}
func singleStatement(text string) error {
	values, err := tokens(text)
	if err != nil {
		return err
	}
	for len(values) > 0 && values[0] == ";" {
		values = values[1:]
	}
	if len(values) == 0 {
		return fmt.Errorf("%w: empty SQL statement", ErrArgument)
	}
	switch values[0] {
	case "BEGIN", "COMMIT", "END", "ROLLBACK", "SAVEPOINT", "RELEASE":
		return fmt.Errorf("%w: use the transaction API for transaction control", ErrArgument)
	}
	trigger := false
	if values[0] == "CREATE" {
		position := 1
		if position < len(values) && (values[position] == "TEMP" || values[position] == "TEMPORARY") {
			position++
		}
		trigger = position < len(values) && values[position] == "TRIGGER"
	}
	body, finished := false, false
	for index, value := range values {
		if finished {
			if value != ";" {
				return fmt.Errorf("%w: expected one SQL statement", ErrArgument)
			}
			continue
		}
		if trigger && value == "BEGIN" && !body {
			body = true
			continue
		}
		// A trigger ends with "; END", not every END token in its body.
		// END can also terminate a CASE expression or name a column.
		if body && value == "END" && index > 0 && values[index-1] == ";" {
			body = false
			trigger = false
		}
		if value == ";" && !body {
			finished = true
		}
	}
	return nil
}
