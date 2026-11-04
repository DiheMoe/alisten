package qrc

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Word 是一段带独立时间的歌词片段，通常是单个字。毫秒，绝对于曲目起点。
type Word struct {
	StartMs    int
	DurationMs int
	Text       string
}

// Line 是一行逐字歌词。毫秒，绝对于曲目起点。
type Line struct {
	StartMs    int
	DurationMs int
	Words      []Word
}

// Text 拼接整行文本。
func (l Line) Text() string {
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

// QRC 的时间标记跟在它所定时的文本之后；只有内容是纯数字时间的圆括号才是标记，其余按歌词原文保留。
func parseWords(content string) []Word {
	var words []Word
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
			words = append(words, Word{StartMs: atoi(parts[0]), DurationMs: atoi(parts[1]), Text: text})
		}
		i += end
		start = i + 1
	}
	if tail := strings.TrimSpace(content[start:]); tail != "" && len(words) > 0 {
		last := words[len(words)-1]
		words = append(words, Word{StartMs: last.StartMs + last.DurationMs, Text: tail})
	}
	return words
}

// Parse 解析解密后的 QRC 文本：剥掉 XML 外壳，返回头部标签和逐字行；kana 等注音注解行被丢弃。
func Parse(text string) (map[string]string, []Line) {
	if m := contentRe.FindStringSubmatch(text); m != nil {
		text = html.UnescapeString(m[1])
	}
	tags := map[string]string{}
	var lines []Line
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
		line := Line{StartMs: atoi(m[1]), DurationMs: atoi(m[2]), Words: parseWords(m[3])}
		lines = append(lines, line)
	}
	return tags, lines
}

// RenderYRC 输出传输用的逐字格式（YRC 语法）：[行起始,行时长](字起始,字时长,0)字…，毫秒为绝对时间。
func RenderYRC(lines []Line) string {
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

// RenderLRC 输出行级 LRC：头部标签行在前，随后是 [分:秒.厘秒]行文本。
func RenderLRC(tags map[string]string, lines []Line) string {
	var b strings.Builder
	for _, k := range []string{"ti", "ar", "al", "by", "offset"} {
		if v, ok := tags[k]; ok {
			fmt.Fprintf(&b, "[%s:%s]\n", k, v)
		}
	}
	for _, l := range lines {
		fmt.Fprintf(&b, "[%02d:%05.2f]%s\n", l.StartMs/60000, float64(l.StartMs%60000)/1000, l.Text())
	}
	return b.String()
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
