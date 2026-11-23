package qq

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// qrcWord 是一段带独立时间的歌词片段，通常是单个字。时间单位为毫秒，相对于曲目起点。
type qrcWord struct {
	StartMs    int
	DurationMs int
	Text       string
}

// qrcLine 是一行逐字歌词。时间单位为毫秒，相对于曲目起点。
type qrcLine struct {
	StartMs    int
	DurationMs int
	Words      []qrcWord
}

func (l qrcLine) text() string {
	var b strings.Builder
	for _, w := range l.Words {
		b.WriteString(w.Text)
	}
	return b.String()
}

var (
	contentRe = regexp.MustCompile(`(?s)<Lyric_1[^>]*LyricContent="(.*?)"\s*/>`)
	tagRe     = regexp.MustCompile(`^\[([A-Za-z]+):([^\]]*)\]$`)
	lineRe    = regexp.MustCompile(`^\[(\d+),(\d+)\](.*)$`)
	timingRe  = regexp.MustCompile(`^\d+,\d+$`)
)

// QRC 的时间标记跟在它所定时的文本后面。只有内容为纯数字时间的半角圆括号才是时间标记，其余括号按歌词原文保留。
func parseWords(content string) []qrcWord {
	var words []qrcWord
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] != '(' {
			continue
		}
		end := strings.IndexByte(content[i:], ')')
		if end < 0 {
			break
		}
		inner := content[i+1 : i+end]
		if !timingRe.MatchString(inner) {
			continue
		}
		text := strings.TrimSuffix(content[start:i], "\r")
		if text != "" {
			parts := strings.SplitN(inner, ",", 2)
			words = append(words, qrcWord{StartMs: atoi(parts[0]), DurationMs: atoi(parts[1]), Text: text})
		}
		i += end
		start = i + 1
	}
	if tail := strings.TrimSpace(content[start:]); tail != "" && len(words) > 0 {
		last := words[len(words)-1]
		words = append(words, qrcWord{StartMs: last.StartMs + last.DurationMs, Text: tail})
	}
	return words
}

// parseQRC 剥掉解密后 QRC 文本的 XML 外壳，分离出方括号头部标签与逐字行。
func parseQRC(text string) (map[string]string, []qrcLine) {
	if m := contentRe.FindStringSubmatch(text); m != nil {
		text = html.UnescapeString(m[1])
	}
	tags := map[string]string{}
	var lines []qrcLine
	for _, raw := range strings.Split(text, "\n") {
		row := strings.TrimSpace(raw)
		if row == "" {
			continue
		}
		if m := tagRe.FindStringSubmatch(row); m != nil {
			tags[m[1]] = m[2]
			continue
		}
		m := lineRe.FindStringSubmatch(row)
		if m == nil {
			continue
		}
		line := qrcLine{StartMs: atoi(m[1]), DurationMs: atoi(m[2]), Words: parseWords(m[3])}
		lines = append(lines, line)
	}
	return tags, lines
}

// renderYRCLines 输出传输用的 YRC 逐字格式，毫秒为相对曲目起点的绝对时间。
func renderYRCLines(lines []qrcLine) string {
	var b strings.Builder
	for _, l := range lines {
		fmt.Fprintf(&b, "[%d,%d]", l.StartMs, l.DurationMs)
		for _, w := range l.Words {
			fmt.Fprintf(&b, "(%d,%d,0)%s", w.StartMs, w.DurationMs, w.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// renderLRCLines 输出行级 LRC，只保留常用的元信息标签，kana 等注音标签不输出。
func renderLRCLines(tags map[string]string, lines []qrcLine) string {
	var b strings.Builder
	for _, k := range []string{"ti", "ar", "al", "by", "offset"} {
		if v, ok := tags[k]; ok {
			fmt.Fprintf(&b, "[%s:%s]\n", k, v)
		}
	}
	for _, l := range lines {
		fmt.Fprintf(&b, "[%02d:%05.2f]%s\n", l.StartMs/60000, float64(l.StartMs%60000)/1000, l.text())
	}
	return b.String()
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
