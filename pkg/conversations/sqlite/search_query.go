package sqlite

import (
	"strings"
	"unicode"
)

type searchTerm struct {
	text   string
	phrase bool
}

// Quote every term to prevent user input from becoming FTS5 query syntax.
// Bare words use prefixes; quoted phrases are exact; AND keeps terms in one entry.
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
