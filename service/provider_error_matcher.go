package service

import (
	"strings"
	"unicode"
)

func splitProviderErrorMatchers(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ';' || r == '；'
	})

	matchers := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		matchers = append(matchers, part)
	}
	return matchers
}

func compactLower(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func matchProviderBodyError(body string, rawMatchers string) (bool, string) {
	matchers := splitProviderErrorMatchers(rawMatchers)
	if len(matchers) == 0 {
		return false, ""
	}

	bodyLower := strings.ToLower(body)
	bodyCompact := compactLower(body)
	for _, matcher := range matchers {
		matcherLower := strings.ToLower(matcher)
		if strings.Contains(bodyLower, matcherLower) {
			return true, matcher
		}

		matcherCompact := compactLower(matcherLower)
		if matcherCompact != "" && strings.Contains(bodyCompact, matcherCompact) {
			return true, matcher
		}
	}

	return false, ""
}
