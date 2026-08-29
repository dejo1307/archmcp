// Package gqlscan lexes the operation surface shared by GraphQL extractors.
package gqlscan

// TODO: Add benchmarks and large hostile-input tests for the shared lexer,
// including multi-megabyte malformed strings and deeply nested selections.

// OperationHeads returns regexp-compatible index tuples: full start/end,
// followed by the operation-kind start/end.
func OperationHeads(text string) [][]int {
	var out [][]int
	inBlockString := false
	for line := 0; line < len(text); {
		lineEnd := line
		for lineEnd < len(text) && text[lineEnd] != '\n' {
			lineEnd++
		}
		start := line
		for start < len(text) && (text[start] == ' ' || text[start] == '\t' || text[start] == '\r') {
			start++
		}
		kindEnd := scanName(text, start)
		if !inBlockString && kindEnd > start {
			kind := text[start:kindEnd]
			if kind == "query" || kind == "mutation" || kind == "subscription" {
				if end, ok := scanOperationHeader(text, kindEnd); ok {
					out = append(out, []int{line, end, start, kindEnd})
				}
			}
		}
		for i := line; i+2 < lineEnd; i++ {
			if text[i:i+3] == `"""` && !isEscaped(text, i) {
				inBlockString = !inBlockString
				i += 2
			}
		}
		next := lineEnd
		if next < len(text) {
			next++
		}
		if next <= line {
			break
		}
		line = next
	}
	return out
}

func scanOperationHeader(text string, i int) (int, bool) {
	i = skipIgnored(text, i)
	if i < len(text) && text[i] != '(' && text[i] != '@' && text[i] != '{' {
		next := scanName(text, i)
		if next == i {
			return i, false
		}
		i = skipIgnored(text, next)
	}
	if i < len(text) && text[i] == '(' {
		var ok bool
		i, ok = skipDelimited(text, i, '(', ')')
		if !ok {
			return i, false
		}
		i = skipIgnored(text, i)
	}
	for i < len(text) && text[i] == '@' {
		i++
		next := scanName(text, i)
		if next == i {
			return i, false
		}
		i = skipIgnored(text, next)
		if i < len(text) && text[i] == '(' {
			var ok bool
			i, ok = skipDelimited(text, i, '(', ')')
			if !ok {
				return i, false
			}
			i = skipIgnored(text, i)
		}
	}
	return i + 1, i < len(text) && text[i] == '{'
}

// RootFields returns the fields immediately inside an operation selection set.
func RootFields(body string) []string {
	fields, _ := scanSelectionSet(body, 0, true)
	return fields
}

func scanSelectionSet(text string, i int, collect bool) ([]string, int) {
	var fields []string
	for i < len(text) {
		i = skipIgnoredAndInterpolations(text, i)
		if i >= len(text) || text[i] == '}' {
			return fields, i + 1
		}
		if i+2 < len(text) && text[i:i+3] == "..." {
			i = skipIgnored(text, i+3)
			i = scanName(text, i)
			i = skipIgnored(text, i)
			if next := scanName(text, i); next > i {
				i = next
			}
			i = skipDirectives(text, i)
			if i < len(text) && text[i] == '{' {
				_, i = scanSelectionSet(text, i+1, false)
			}
			continue
		}
		end := scanName(text, i)
		if end == i {
			i = skipToken(text, i)
			continue
		}
		field := text[i:end]
		i = skipIgnored(text, end)
		if i < len(text) && text[i] == ':' {
			i = skipIgnored(text, i+1)
			end = scanName(text, i)
			if end == i {
				continue
			}
			field, i = text[i:end], end
		}
		if collect {
			fields = append(fields, field)
		}
		i = skipIgnored(text, i)
		if i < len(text) && text[i] == '(' {
			i, _ = skipDelimited(text, i, '(', ')')
		}
		i = skipDirectives(text, i)
		if i < len(text) && text[i] == '{' {
			_, i = scanSelectionSet(text, i+1, false)
		}
	}
	return fields, i
}

func skipDirectives(text string, i int) int {
	for {
		i = skipIgnoredAndInterpolations(text, i)
		if i >= len(text) || text[i] != '@' {
			return i
		}
		i = scanName(text, i+1)
		i = skipIgnored(text, i)
		if i < len(text) && text[i] == '(' {
			i, _ = skipDelimited(text, i, '(', ')')
		}
	}
}

func skipIgnored(text string, i int) int {
	for i < len(text) {
		switch text[i] {
		case ' ', '\t', '\r', '\n', ',':
			i++
		case '#':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		default:
			return i
		}
	}
	return i
}

func skipIgnoredAndInterpolations(text string, i int) int {
	for {
		i = skipIgnored(text, i)
		if i+1 >= len(text) || text[i:i+2] != "${" {
			return i
		}
		i, _ = skipDelimited(text, i+1, '{', '}')
	}
}

func skipDelimited(text string, i int, open, close byte) (int, bool) {
	if i >= len(text) || text[i] != open {
		return i, false
	}
	depth := 1
	for i++; i < len(text); {
		if text[i] == '"' {
			i = skipString(text, i)
			continue
		}
		if text[i] == '#' {
			i = skipIgnored(text, i)
			continue
		}
		if text[i] == open {
			depth++
		}
		if text[i] == close {
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
		i++
	}
	return i, false
}

func skipString(text string, i int) int {
	if i+2 < len(text) && text[i:i+3] == `"""` {
		for i += 3; i+2 < len(text); i++ {
			if text[i:i+3] == `"""` && !isEscaped(text, i) {
				return i + 3
			}
		}
		return len(text)
	}
	for i++; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if i < len(text) && text[i] == '"' {
			return i + 1
		}
	}
	return i
}

func isEscaped(text string, i int) bool {
	backslashes := 0
	for i--; i >= 0 && text[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 != 0
}

func skipToken(text string, i int) int {
	if i < len(text) && text[i] == '"' {
		return skipString(text, i)
	}
	return i + 1
}

func scanName(text string, i int) int {
	if i >= len(text) || !isNameStart(text[i]) {
		return i
	}
	for i++; i < len(text) && isNameContinue(text[i]); i++ {
	}
	return i
}

func isNameStart(c byte) bool    { return c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }
func isNameContinue(c byte) bool { return isNameStart(c) || c >= '0' && c <= '9' }
