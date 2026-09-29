package sqlite

import (
	"strings"
	"unicode"
)

type searchTerm struct {
	text   string
	phrase bool
}

// searchMatchExpression converts user search text into a safe FTS5 MATCH
// expression. Bare words become prefix terms, double-quoted text becomes an
// exact phrase, and all terms are combined with AND, so they must appear in
// the same entry. Every term is quoted, so user input never reaches FTS5 as
// query syntax. It returns "" when the text has no searchable characters.
func searchMatchExpression(input string) string {
	var parts []string
	for _, term := range parseSearchTerms(input) {
		if !strings.ContainsFunc(term.text, isSearchTokenRune) {
			continue
		}
		part := `"` + strings.ReplaceAll(term.text, `"`, `""`) + `"`
		if !term.phrase {
			part += "*"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " AND ")
}

// parseSearchTerms splits text into bare words and double-quoted phrases.
// Unbalanced quotes fall back to whitespace-separated bare words.
func parseSearchTerms(input string) []searchTerm {
	var terms []searchTerm
	var current strings.Builder
	inPhrase := false
	flush := func(phrase bool) {
		text := strings.TrimSpace(current.String())
		current.Reset()
		if text != "" {
			terms = append(terms, searchTerm{text: text, phrase: phrase})
		}
	}
	for _, r := range input {
		switch {
		case r == '"':
			flush(inPhrase)
			inPhrase = !inPhrase
		case unicode.IsSpace(r) && !inPhrase:
			flush(false)
		default:
			current.WriteRune(r)
		}
	}
	if inPhrase {
		terms = terms[:0]
		for _, word := range strings.Fields(strings.ReplaceAll(input, `"`, " ")) {
			terms = append(terms, searchTerm{text: word})
		}
		return terms
	}
	flush(false)
	return terms
}

// isSearchTokenRune mirrors the FTS5 unicode61 tokenizer's token characters.
func isSearchTokenRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Co, r)
}
