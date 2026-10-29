package main

import "strings"

var initialisms = map[string]string{"id": "ID", "qq": "QQ", "url": "URL"}

func words(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == '_' }) {
		start := 0
		for i := 1; i < len(part); i++ {
			if isUpper(part[i]) && !isUpper(part[i-1]) {
				out = append(out, part[start:i])
				start = i
			}
		}
		out = append(out, part[start:])
	}
	return out
}

func isUpper(c byte) bool {
	return 'A' <= c && c <= 'Z'
}

func pascal(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		b.WriteString(title(w))
	}
	return b.String()
}

func camel(s string) string {
	w := words(s)
	var b strings.Builder
	b.WriteString(strings.ToLower(w[0]))
	for _, x := range w[1:] {
		b.WriteString(title(x))
	}
	return b.String()
}

func title(w string) string {
	lower := strings.ToLower(w)
	if s, ok := initialisms[lower]; ok {
		return s
	}
	return strings.ToUpper(lower[:1]) + lower[1:]
}

func lowerFirst(s string) string {
	return strings.ToLower(s[:1]) + s[1:]
}
