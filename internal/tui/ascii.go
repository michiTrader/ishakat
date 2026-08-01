package tui

import "strings"

// foldASCII rewrites accented Latin letters and Latin-1 punctuation as their
// closest ASCII equivalent, one rune in for one rune out.
//
// It exists because the glyph table only covers decoration, and decoration was
// never the whole problem: the interface's own prose says "catálogo",
// "conversación", "línea" and "tú", and on a console decoding our UTF-8 output
// as cp437 — the default of conhost.exe — those two bytes surface as two wrong
// characters each. "catÃ¡logo" is not a missing glyph the font can be blamed
// for; it is text the terminal cannot represent at all, so the only honest thing
// to send is "catalogo".
//
// The mapping is deliberately restricted to letters and punctuation. Anything
// else above U+007F is left exactly as it is, so a block character that sneaks
// into a render path still reaches the repertoire test instead of being quietly
// turned into an ASCII stand-in — the fold is a backstop for prose, not a
// licence to stop choosing glyphs.
//
// Every replacement is a single rune, which keeps the string's width unchanged.
// That is not cosmetic: the footer trims to a column budget and the cursor is
// placed by measuring rendered rows, so a fold that changed any line's width
// would move the cursor off the text it belongs to.
func foldASCII(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x80 {
			b.WriteRune(r)
			continue
		}
		if folded, ok := asciiFolds[r]; ok {
			b.WriteRune(folded)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// asciiFolds is the table of one-rune-for-one-rune replacements. It covers the
// letters Spanish, Portuguese, French, German and Italian actually put on
// screen, which is the population of strings this program has: its own copy,
// model names, branch names and paths under the user's home directory.
var asciiFolds = map[rune]rune{
	'á': 'a', 'à': 'a', 'ä': 'a', 'â': 'a', 'ã': 'a', 'å': 'a',
	'é': 'e', 'è': 'e', 'ë': 'e', 'ê': 'e',
	'í': 'i', 'ì': 'i', 'ï': 'i', 'î': 'i',
	'ó': 'o', 'ò': 'o', 'ö': 'o', 'ô': 'o', 'õ': 'o', 'ø': 'o',
	'ú': 'u', 'ù': 'u', 'ü': 'u', 'û': 'u',
	'ñ': 'n', 'ç': 'c', 'ý': 'y', 'ÿ': 'y',

	'Á': 'A', 'À': 'A', 'Ä': 'A', 'Â': 'A', 'Ã': 'A', 'Å': 'A',
	'É': 'E', 'È': 'E', 'Ë': 'E', 'Ê': 'E',
	'Í': 'I', 'Ì': 'I', 'Ï': 'I', 'Î': 'I',
	'Ó': 'O', 'Ò': 'O', 'Ö': 'O', 'Ô': 'O', 'Õ': 'O', 'Ø': 'O',
	'Ú': 'U', 'Ù': 'U', 'Ü': 'U', 'Û': 'U',
	'Ñ': 'N', 'Ç': 'C', 'Ý': 'Y',

	// Punctuation the interface and its error messages use.
	'¿': '?', '¡': '!', '·': '-', '–': '-', '—': '-',
	'«': '"', '»': '"', '“': '"', '”': '"', '‘': '\'', '’': '\'',
	'…': '.', '×': 'x', '✓': '+', '✗': 'x', '›': '>', '‹': '<',
}
