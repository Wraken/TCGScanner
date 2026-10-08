package main

import "testing"

func TestNormName(t *testing.T) {
	cases := map[string]string{
		"Weedle [Multiply]":                        "weedle",
		"Roronoa Zoro (OP01-025)":                  "roronoa zoro",
		"Fix‐It Felix, Jr. - Trusty Builder":       "fix it felix jr trusty builder",
		"Flotsam - Ursula's \"\"Baby\"\"":          "flotsam ursula s baby",
		"Has Set My Heaaaaaaart…":                  "has set my heaaaaaaart",
		"Pokémon Trainer":                          "pokemon trainer",
		"Monkey.D.Luffy":                           "monkey d luffy",
		"Mickey Mouse - Brave Little Tailor (V.2)": "mickey mouse brave little tailor",
		"Kakuna [Bug Bite | Primal Clash]":         "kakuna",
	}
	for in, want := range cases {
		if got := normName(in); got != want {
			t.Errorf("normName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNamesAgree(t *testing.T) {
	yes := [][2]string{
		{"Weedle [Multiply]", "Weedle"},
		{"Roronoa Zoro (OP01-025)", "Roronoa Zoro"},
		{"Miss Bianca - International Rescue Aid Society Agent", "Miss Bianca - Rescue Aid Society Agent"[:11]},
		{"Smoliv [Tackle]", "Smoliv"},
		{"MRayquaza EX [Emerald Break]", "M Rayquaza EX"},
		{"Miracle Sphere alpha", "Miracle Sphere α"},
		{"Mystery Plate delta", "Mystery Plate δ"},
		{"Palkia [G] Lv.78 [Splashing Turn | Pearl Breath]", "Palkia G"},
		{"Luxray [GL] LV.X [Bright Look | Flash Impact]", "Luxray GL LV.X"},
		{"Alakazam [4] LV.X [Damage Switch | Mind Shock]", "Alakazam E4 LV.X"},
		{"Staraptor [FB] Lv.50 [Quick Attack | Whirlwind]", "Staraptor FB"},
		{"Growing [G] Energy", "Growing Grass Energy"},
		{"Horror [P] Energy", "Horror Psychic Energy"},
		{"Powerful [C] Energy", "Powerful Colorless Energy"},
		{"Rayquaza [C] LV.X [Dragon Spirit | Final Blowup]", "Rayquaza C LV.X"},
	}
	for _, c := range yes {
		if !namesAgree(c[0], c[1]) {
			t.Errorf("namesAgree(%q, %q) = false", c[0], c[1])
		}
	}
	no := [][2]string{
		{"Roronoa Zoro (OP01-025)", "Monkey.D.Luffy"},
		{"Armaldo [Primal Veil | Blade Arms]", "Team Aqua's Cacturne"},
		{"Darkness Energy", "Fighting Energy"},
		{"Imposter Professor Oak", "Impostor Professor Oak"},
		// An owner tag on the wrong card still fails: the name itself differs.
		{"Palkia [G] Lv.78 [Splashing Turn | Pearl Breath]", "Dialga G"},
		{"Growing [G] Energy", "Growing Fire Energy"},
		{"", "Weedle"},
		{"Weedle [Multiply]", ""},
	}
	for _, c := range no {
		if namesAgree(c[0], c[1]) {
			t.Errorf("namesAgree(%q, %q) = true", c[0], c[1])
		}
	}
}

func TestVariantBase(t *testing.T) {
	cases := map[string]string{
		"OP01-025": "OP01-025", "OP01-025A": "OP01-025", "P-001P2": "P-001", "P-001": "P-001",
		"EB01-006M": "EB01-006", "007A": "007", "299S": "299", "ST01-012": "ST01-012", "T04": "T04",
	}
	for in, want := range cases {
		if got := variantBase(in); got != want {
			t.Errorf("variantBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsPocket(t *testing.T) {
	pocket := []string{"A1-001", "A1a-019", "A2b-101", "B1-019", "B2a-058", "P-A-001"}
	for _, id := range pocket {
		if !isPocket(id) {
			t.Errorf("isPocket(%q) = false", id)
		}
	}
	physical := []string{"sv01-020", "base1-1", "bw9-110", "swsh1-1", "A-1", "xy11-43", "dpp-DP34", "tk-xy-p-25"}
	for _, id := range physical {
		if isPocket(id) {
			t.Errorf("isPocket(%q) = true", id)
		}
	}
}

func TestGameOfID(t *testing.T) {
	cases := map[string]string{
		"ogn-001-298": "riftbound", "sfd-190-221": "riftbound", "ogn-007a-298": "riftbound",
		"OP01-025": "onepiece", "OP01-025_p1": "onepiece", "ST01-012": "onepiece", "P-001": "onepiece", "EB01-006_r1": "onepiece",
		"1-1": "lorcana", "P1-4": "lorcana", "3-4a": "lorcana", "Coconut-1": "lorcana", "cp-10": "lorcana", "Q1-1": "lorcana",
		"sv01-020": "pokemon", "base1-1": "pokemon", "bw9-110": "pokemon", "tk-xy-p-25": "pokemon", "A1-001": "pokemon",
	}
	for id, want := range cases {
		for _, g := range allGames {
			if got := gameOfID(id, g); got != (g == want) {
				t.Errorf("gameOfID(%q, %q) = %v", id, g, got)
			}
		}
	}
}

func TestGameOfIDByMembership(t *testing.T) {
	// Ids the shape rules cannot place (signed Riftbound cards, Riftbound
	// tokens, Pokémon trainer kits, Pokémon one-dash lower-case sets) are
	// told apart by the fetchers' lists.
	saved := ourGames
	defer func() { ourGames = saved }()
	ourGames = map[string]string{
		"ogn-299-star-298": "riftbound", "sfd-t03": "riftbound", "unl-t01": "riftbound",
		"tk-bw-e-1": "pokemon", "2011bw-1": "pokemon",
	}
	for id, want := range ourGames {
		for _, g := range allGames {
			if got := gameOfID(id, g); got != (g == want) {
				t.Errorf("gameOfID(%q, %q) = %v", id, g, got)
			}
		}
	}
}
