package main

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// normName reduces a card name to what the catalog check compares: lower
// case, accents stripped, every dash and quote variant folded, Cardmarket's
// bracketed suffixes dropped ("Weedle [Multiply]", "Zoro (OP01-025)",
// "(V.2)"), anything that is not a letter or digit collapsed to one space.
// Measured on Lorcana: the 30 disagreements between LorcanaJSON names and
// Cardmarket product names were all of this kind (‐ vs -, … vs ..., doubled
// quotes), so this is exactly as loose as it needs to be and no looser.
func normName(s string) string {
	s = tags.Replace(greek.Replace(norm.NFD.String(s)))
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '[' || r == '(':
			depth++
		case r == ']' || r == ')':
			if depth > 0 {
				depth--
			}
		case depth > 0:
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// greek spells out the letters Pokémon prints on a few e-Card trainers
// ("Miracle Sphere α"); Cardmarket writes them in full ("Miracle Sphere
// alpha"). A transliteration, not a tolerance: the two strings name the
// same thing.
var greek = strings.NewReplacer("α", "alpha", "β", "beta", "γ", "gamma", "δ", "delta")

// tags rewrites the bracketed markers Cardmarket puts on Pokémon names so
// they read like ours before the bracket stripper runs: the Platinum-era
// owner tags ("Palkia [G]" for "Palkia G", "Luxray [GL]", "Alakazam [4]"
// for our "Alakazam E4", "[FB]", "[C]") and the type letter of special
// energies ("Growing [G] Energy" for "Growing Grass Energy"). A closed
// list, each entry spelled out: a bracket that is not one of these is an
// attack list and still goes. Measured on 2026-10-07: 109 of the 307
// Pokémon drops were exactly these, over `pl1`-`pl4`, `me03`-`me05` and
// `swsh2`-`swsh4`, whose other cards all validate.
var tags = strings.NewReplacer(
	" [GL] ", " GL ", " [GL]", " GL",
	" [FB] ", " FB ", " [FB]", " FB",
	" [4] ", " E4 ", " [4]", " E4",
	" [G] Energy", " Grass Energy",
	" [R] Energy", " Fire Energy",
	" [W] Energy", " Water Energy",
	" [L] Energy", " Lightning Energy",
	" [P] Energy", " Psychic Energy",
	" [F] Energy", " Fighting Energy",
	" [D] Energy", " Darkness Energy",
	" [M] Energy", " Metal Energy",
	" [C] Energy", " Colorless Energy",
	" [G] ", " G ", " [G]", " G",
	" [C] ", " C ", " [C]", " C",
)

// namesAgree is the catalog check: the product's name must contain the
// card's name (or the reverse, for sources that append a version to the
// name). Both sides go through normName first.
func namesAgree(product, card string) bool {
	p, c := normName(product), normName(card)
	if p == "" || c == "" {
		return false
	}
	if strings.Contains(p, c) || strings.Contains(c, p) {
		return true
	}
	// Cardmarket glues the Mega prefix ("MRayquaza EX" for "M Rayquaza EX")
	// and brackets suffixes ("Luxray [GL] LV.X" for "Luxray GL LV.X");
	// compare once more with every space removed. Containment still has to
	// hold, so a different card's name never passes this way.
	ps, cs := strings.ReplaceAll(p, " ", ""), strings.ReplaceAll(c, " ", "")
	return strings.Contains(ps, cs) || strings.Contains(cs, ps)
}
