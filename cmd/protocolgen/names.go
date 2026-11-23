package main

import "strings"

var initialisms = map[string]string{
	"github":  "GitHub",
	"id":      "ID",
	"netease": "NetEase",
	"qq":      "QQ",
	"url":     "URL",
	"youtube": "YouTube",
}

func words(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == '_' }) {
		start := 0
		for i := 1; i < len(part); i++ {
			prevLowerOrDigit := isLower(part[i-1]) || isDigit(part[i-1])
			upperBeforeLower := isUpper(part[i-1]) && i+1 < len(part) && isLower(part[i+1])
			if isUpper(part[i]) && (prevLowerOrDigit || upperBeforeLower) {
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

func isLower(c byte) bool {
	return 'a' <= c && c <= 'z'
}

func isDigit(c byte) bool {
	return '0' <= c && c <= '9'
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
