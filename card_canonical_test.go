package main

import "testing"

func TestCanonicalCard(t *testing.T) {
	cases := []struct{ id, want string }{
		// One Piece alt-arts fold onto the base card.
		{"OP13-079", "OP13-079"},
		{"OP13-079_p1", "OP13-079"},
		{"OP06-118_p2", "OP06-118"},
		{"EB01-009_r1", "EB01-009"},
		{"ST04-005_p3", "ST04-005"},
		// Riftbound star foils fold onto their base.
		{"ven-194-166", "ven-194-166"},
		{"ven-194-star-166", "ven-194-166"},
		{"ogn-299-star-298", "ogn-299-298"},
		// Cross-set reprints canonicalize to one id, in either direction.
		{"ven-r02", "ogn-042-298"},
		{"ogn-042-298", "ogn-042-298"},
		{"unl-t05", "sfd-t03"},
		{"sfd-t03", "sfd-t03"},
		{"ven-r06", "ogn-214-298"},
		// Distinct cards must stay distinct — the letter suffix on a Riftbound
		// number is a different card, not a variant.
		{"ogn-027-298", "ogn-027-298"},
		{"ogn-027a-298", "ogn-027a-298"},
		{"unl-101-219", "unl-101-219"},
	}
	for _, c := range cases {
		if got := canonicalCard(c.id); got != c.want {
			t.Errorf("canonicalCard(%q) = %q, want %q", c.id, got, c.want)
		}
	}
}

// TestCanonicalGroupsReprints guards the property the margin relies on: known
// equivalent printings share a canonical, distinct cards do not.
func TestCanonicalGroupsReprints(t *testing.T) {
	same := [][2]string{
		{"ven-r02", "ogn-042-298"},
		{"ven-r06", "ogn-214-298"},
		{"unl-t05", "sfd-t03"},
		{"OP13-079", "OP13-079_p1"},
		{"ven-194-166", "ven-194-star-166"},
	}
	for _, p := range same {
		if canonicalCard(p[0]) != canonicalCard(p[1]) {
			t.Errorf("%s and %s should share a canonical (%q vs %q)",
				p[0], p[1], canonicalCard(p[0]), canonicalCard(p[1]))
		}
	}

	distinct := [][2]string{
		{"ogn-027-298", "ogn-027a-298"},
		{"OP13-079", "OP13-080"},
		{"unl-101-219", "unl-104-219"},
	}
	for _, p := range distinct {
		if canonicalCard(p[0]) == canonicalCard(p[1]) {
			t.Errorf("%s and %s should stay distinct (both %q)",
				p[0], p[1], canonicalCard(p[0]))
		}
	}
}
