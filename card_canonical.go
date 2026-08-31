package main

import (
	"regexp"
	"strings"
)

// The match margin compares the top hit against the best *different* card. For
// that to mean anything, every printing of one card face must reduce to a
// single key: otherwise an alt-art or a cross-set reprint of the winning card
// sits just behind it and collapses the margin to nearly zero, so a correct,
// confident match looks ambiguous. canonicalCard produces that key.

// reprintAliases maps a card id to the canonical id of a visually identical
// printing in another set. These pairs were verified by eye during index
// construction (they retrieve each other at 0.98-0.99). Mapping them together
// keeps a reprint from counting as the rival that kills the margin.
var reprintAliases = map[string]string{
	"ven-r02": "ogn-042-298",
	"ven-r06": "ogn-214-298",
	"unl-t05": "sfd-t03",
}

// opVariantSuffix matches One Piece alt-art markers: _p1, _p2, _r1, _r2, ...
var opVariantSuffix = regexp.MustCompile(`_[pr]\d+$`)

// canonicalCard reduces a card id to a key shared by every printing of the same
// face. One Piece alt-art suffixes are dropped (OP13-079_p1 -> OP13-079),
// Riftbound "star" foils fold onto their base (ven-194-star-166 -> ven-194-166),
// and known cross-set reprints map to one id. Two ids with the same canonical
// are treated as the same card when computing the match margin.
func canonicalCard(id string) string {
	id = opVariantSuffix.ReplaceAllString(id, "")
	id = strings.Replace(id, "-star-", "-", 1)
	if c, ok := reprintAliases[id]; ok {
		return c
	}
	return id
}
