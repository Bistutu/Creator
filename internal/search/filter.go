// Package search filters indexes built from display names, addresses and aliases.
package search

import "strings"

// Filter first looks for the whole phrase. When there are no phrase matches,
// each word can match a different part of the index. text supplies a lowercase
// index for each eligible item and an empty string for excluded items.
func Filter[T any](items []T, query string, text func(T) string) []T {
	query = strings.ToLower(strings.TrimSpace(query))
	matches := []T{}
	for _, item := range items {
		if index := text(item); index != "" && strings.Contains(index, query) {
			matches = append(matches, item)
		}
	}
	if len(matches) != 0 {
		return matches
	}
	terms := strings.Fields(query)
	if len(terms) < 2 {
		return matches
	}
	for _, item := range items {
		if containsTerms(text(item), terms) {
			matches = append(matches, item)
		}
	}
	return matches
}

func containsTerms(index string, terms []string) bool {
	for _, term := range terms {
		if !strings.Contains(index, term) {
			return false
		}
	}
	return true
}
