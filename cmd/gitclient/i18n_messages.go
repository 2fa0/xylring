package main

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Status and error strings keep their source text in app state. Translate only
// at their display boundary, preserving captured paths, refs and commit content.
type messagePattern struct {
	match  *regexp.Regexp
	parts  []string
	nested []bool
	weight int
}

var formatPlaceholder = regexp.MustCompile(`%(?:\[\d+\])?[-+# 0]*\d*(?:\.\d+)?[a-zA-Z%]`)
var messagePatterns struct {
	sync.Once
	english []messagePattern
}

func buildMessagePatterns() {
	keys := make([]string, 0, len(englishText))
	for source := range englishText {
		keys = append(keys, source)
	}
	sort.Strings(keys)
	for _, source := range keys {
		translated := englishText[source]
		if p, ok := compileMessagePattern(source, translated); ok {
			messagePatterns.english = append(messagePatterns.english, p)
		}
	}
	sort.SliceStable(messagePatterns.english, func(i, j int) bool { return messagePatterns.english[i].weight > messagePatterns.english[j].weight })
}

func compileMessagePattern(source, translated string) (messagePattern, bool) {
	indices := formatPlaceholder.FindAllStringIndex(source, -1)
	targetIndices := formatPlaceholder.FindAllStringIndex(translated, -1)
	p := messagePattern{}
	var pattern strings.Builder
	pattern.WriteString(`(?s)^`)
	last := 0
	for _, span := range indices {
		literal := source[last:span[0]]
		pattern.WriteString(regexp.QuoteMeta(literal))
		p.weight += len(literal)
		verb := source[span[0]:span[1]]
		if verb == "%%" {
			return messagePattern{}, false
		}
		switch verb[len(verb)-1] {
		case 'd':
			pattern.WriteString(`([-+]?\d+)`)
		case 'q':
			pattern.WriteString(`("(?:\\.|[^"\\])*")`)
		default:
			pattern.WriteString(`(.*?)`)
		}
		p.nested = append(p.nested, verb[len(verb)-1] == 'w' || verb[len(verb)-1] == 'v')
		last = span[1]
	}
	pattern.WriteString(regexp.QuoteMeta(source[last:]))
	p.weight += len(source[last:])
	if len(indices) > 0 {
		if len(indices) != len(targetIndices) {
			return messagePattern{}, false
		}
		last = 0
		for _, span := range targetIndices {
			p.parts = append(p.parts, translated[last:span[0]])
			last = span[1]
		}
		p.parts = append(p.parts, translated[last:])
	} else {
		// Concatenated notices use explicit punctuation or a trailing space before
		// their dynamic value. Never replace matching words inside user content.
		if !strings.HasSuffix(source, " ") && !strings.HasSuffix(source, "：") && !strings.HasSuffix(source, ":") && !strings.HasSuffix(source, "\n") {
			return messagePattern{}, false
		}
		pattern.WriteString(`(.+)`)
		p.parts = []string{translated, ""}
		p.nested = []bool{strings.Contains(source, "失败") || strings.Contains(strings.ToLower(source), "failed")}
	}
	pattern.WriteByte('$')
	// These placeholders are app-owned labels; other %s arguments are user data.
	for _, key := range []string{"%s完成", "%s 不能为空或包含换行", "请先完成或中止 %s", "按%s筛选：%s"} {
		if source == key {
			p.nested[0] = true
			break
		}
	}
	p.match = regexp.MustCompile(pattern.String())
	return p, true
}

func trMessage(text string) string {
	if !englishUI.Load() {
		return text
	}
	messagePatterns.Do(buildMessagePatterns)
	return translateMessage(text, 0)
}

func translateMessage(text string, depth int) string {
	if depth > 8 {
		return text
	}

	if translated, ok := englishText[text]; ok {
		return translated
	}
	for _, p := range messagePatterns.english {
		values := p.match.FindStringSubmatch(text)
		if values == nil {
			continue
		}
		var result strings.Builder
		for i, value := range values[1:] {
			result.WriteString(p.parts[i])
			if p.nested[i] {
				value = translateMessage(value, depth+1)
			}
			result.WriteString(value)
		}
		result.WriteString(p.parts[len(p.parts)-1])
		return result.String()
	}
	return text
}
