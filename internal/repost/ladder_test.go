// SPDX-License-Identifier: AGPL-3.0-or-later

package repost

import "testing"

func passed() GateResult { return GateResult{Passed: true} }
func undetermined() GateResult {
	return GateResult{Passed: true, Undetermined: true, Why: "продавец неизвестен"}
}

// aPair is a predecessor and a successor that the gates have already let
// through, carrying every signal. Tests take signals away rather than adding
// them, because taking away is what this ladder must get right.
func aPair() (newer, older Listing) {
	older = aVanished(100, 1000, 2000)
	older.DescriptionSHA = "d-same"
	older.ImageKey = "https://90.img.avito.st/image/1/1.ureJN7aAFl"
	lat, lng := 55.751244, 37.618423
	older.Lat, older.Lng, older.CoordsPrecise = &lat, &lng, true

	newer = anArrival(200, 3000)
	newer.DescriptionSHA = older.DescriptionSHA
	newer.ImageKey = older.ImageKey
	nlat, nlng := lat, lng
	newer.Lat, newer.Lng, newer.CoordsPrecise = &nlat, &nlng, true
	return newer, older
}

// TestTheStrongRungNeedsTheTitleAndOneCorroboratorAndOneCandidate.
//
// All three, and the third is the one people forget. A dealer with eight
// identical phones is the common case on this site, not the edge case.
func TestTheStrongRungNeedsTheTitleAndOneCorroboratorAndOneCandidate(t *testing.T) {
	newer, older := aPair()

	got := Judge(newer, older, passed(), 1)
	if got.State != "linked" || got.Basis != BasisStrong {
		t.Fatalf("a complete pair: %+v", got)
	}

	// Two candidates and nothing links, whatever the evidence.
	if got := Judge(newer, older, passed(), 2); got.State == "linked" {
		t.Errorf("two candidates produced a link: %+v", got)
	}

	// The title is required even when both photos and descriptions match:
	// sellers re-use photos across their own different listings.
	other := newer
	other.Title = "совсем другой телефон"
	if got := Judge(other, older, passed(), 1); got.State == "linked" {
		t.Errorf("a different title was linked on its photo: %+v", got)
	}
}

// TestAMissingSignalContributesNothing.
//
// The rule the whole ladder is built on. A weighted average that renormalised
// over the signals that happen to be present would let a title-and-price pair
// score as high as a title-price-description-image pair — and description and
// image are on only twenty rows in fifty, measured. That is a false-positive
// factory, and a false link silently withholds an alert.
func TestAMissingSignalContributesNothing(t *testing.T) {
	full, older := aPair()

	bare := full
	bare.DescriptionSHA, bare.ImageKey = "", ""
	bare.Lat, bare.Lng, bare.CoordsPrecise = nil, nil, false

	rich := Judge(full, older, passed(), 1)
	poor := Judge(bare, older, passed(), 1)

	if poor.Score >= rich.Score {
		t.Errorf("a pair with fewer signals scored %v against %v",
			poor.Score, rich.Score)
	}
	if poor.State == "linked" {
		t.Errorf("a title-only pair was auto-linked: %+v", poor)
	}
}

// TestAnAbsentSignalIsNotAMatchEvenWhenBothSidesLackIt.
//
// Two empty description digests are not an identical description; two empty
// image keys are not the same photo. Counting them would make the mobile
// surface — which carries neither, on any row — look like a perfect match with
// itself every single time.
func TestAnAbsentSignalIsNotAMatchEvenWhenBothSidesLackIt(t *testing.T) {
	newer, older := aPair()
	for _, c := range []struct {
		term  string
		blank func(*Listing)
	}{
		{termDescription, func(l *Listing) { l.DescriptionSHA = "" }},
		{termImage, func(l *Listing) { l.ImageKey = "" }},
	} {
		a, b := newer, older
		c.blank(&a)
		c.blank(&b)
		got := Judge(a, b, passed(), 1)
		for _, term := range got.Evidence {
			if term.Name == c.term && term.Present {
				t.Errorf("%q counted as present with both sides empty", term.Name)
			}
		}
	}
}

// TestTheWeakRungNeedsPreciseCoordinatesAndAPriceWithinAFifth.
func TestTheWeakRungNeedsPreciseCoordinatesAndAPriceWithinAFifth(t *testing.T) {
	newer, older := aPair()
	newer.DescriptionSHA, newer.ImageKey = "", "" // no strong corroborator
	older.DescriptionSHA, older.ImageKey = "", ""

	got := Judge(newer, older, passed(), 1)
	if got.State != "linked" || got.Basis != BasisWeak {
		t.Fatalf("title, coordinates and price: %+v", got)
	}

	// Twenty percent is inclusive, and past it nothing links.
	within := newer
	within.PriceMinor = older.PriceMinor * 80 / 100
	if got := Judge(within, older, passed(), 1); got.State != "linked" {
		t.Errorf("a price exactly a fifth lower did not link: %+v", got)
	}
	beyond := newer
	beyond.PriceMinor = older.PriceMinor / 2
	if got := Judge(beyond, older, passed(), 1); got.State == "linked" {
		t.Errorf("a price half the old one was linked: %+v", got)
	}
}

// TestAPriceOfZeroIsNotAPriceAtAnyPercentage.
//
// An unread price is our silence. Treated as a number, every pair of unpriced
// listings would be a perfect price match with each other.
func TestAPriceOfZeroIsNotAPriceAtAnyPercentage(t *testing.T) {
	newer, older := aPair()
	newer.DescriptionSHA, newer.ImageKey = "", ""
	older.DescriptionSHA, older.ImageKey = "", ""
	newer.PriceMinor, older.PriceMinor = 0, 0

	if got := Judge(newer, older, passed(), 1); got.State == "linked" {
		t.Errorf("two unread prices were linked as a match: %+v", got)
	}
}

// TestACityCentroidIsNotEvidenceOfAnything.
//
// A centroid is shared by half a district, which is why the extractor records
// coords_precise at all. Counting an imprecise pair would link two strangers
// selling the same phone model in the same city.
func TestACityCentroidIsNotEvidenceOfAnything(t *testing.T) {
	newer, older := aPair()
	newer.DescriptionSHA, newer.ImageKey = "", ""
	older.DescriptionSHA, older.ImageKey = "", ""
	newer.CoordsPrecise = false

	if got := Judge(newer, older, passed(), 1); got.State == "linked" {
		t.Errorf("an imprecise coordinate was linked: %+v", got)
	}
}

// TestAnUndeterminedSellerCapsAtAProposalWhateverTheEvidence.
//
// A proposal withholds nothing: the ordinary new-listing alert still fires,
// with a line attached. That ceiling is the only thing standing between the
// mobile surface and a title-only auto-link.
func TestAnUndeterminedSellerCapsAtAProposalWhateverTheEvidence(t *testing.T) {
	newer, older := aPair()
	got := Judge(newer, older, undetermined(), 1)
	if got.State == "linked" {
		t.Fatalf("an undetermined seller produced a link: %+v", got)
	}
	if got.State != "proposed" {
		t.Errorf("state = %q, want proposed", got.State)
	}
}

// TestARefusedPairIsRejectedAndCarriesTheGatesReason.
//
// Written down all the same. «Почему мне не сказали» has to be answerable for
// the pairs that were considered and refused, not only for the ones that were
// linked.
func TestARefusedPairIsRejectedAndCarriesTheGatesReason(t *testing.T) {
	newer, older := aPair()
	gate := Gates(anArrival(200, 1500), older, false, thirtyDays) // co-existed

	got := Judge(newer, older, gate, 1)
	if got.State != "rejected" {
		t.Errorf("state = %q, want rejected", got.State)
	}
	if len(got.Evidence) == 0 {
		t.Fatal("a rejection carried no evidence at all")
	}
	if got.Evidence[0].Detail != gate.Why {
		t.Errorf("the gate's reason was lost: %q", got.Evidence[0].Detail)
	}
}

// TestEvidenceIsCompleteEitherWay.
//
// A term that was absent appears with Present false. The panel's whole job is
// answering why, and «the description did not match» and «there was no
// description» are different answers.
func TestEvidenceIsCompleteEitherWay(t *testing.T) {
	newer, older := aPair()
	newer.ImageKey = ""

	got := Judge(newer, older, passed(), 1)
	seen := map[string]bool{}
	for _, term := range got.Evidence {
		seen[term.Name] = true
		if term.Detail == "" {
			t.Errorf("term %q carries no detail", term.Name)
		}
	}
	for _, want := range []string{termTitle, termDescription, termImage, termCoords, termPrice} {
		if !seen[want] {
			t.Errorf("evidence does not mention %q", want)
		}
	}
	for _, term := range got.Evidence {
		if term.Name == termImage && term.Present {
			t.Error("a missing image key was reported as present")
		}
	}
}
