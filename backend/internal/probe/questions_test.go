package probe

import (
	"sort"
	"strings"
	"testing"
)

// estimateTokens approximates token length well enough to police the bank's
// uniformity. A real tokenizer is not available offline and is not needed: the
// questions differ only by a proper noun, so a whitespace+punctuation count
// tracks the real figure closely and any entry that drifts badly will drift
// here too.
func estimateTokens(s string) int {
	return len(strings.Fields(s))
}

func TestBankIsLargeEnoughToRotate(t *testing.T) {
	// At 288 cycles/day the bank must be big enough that a question is not
	// repeated many times a day, or a single unlucky entry dominates the
	// correctness signal.
	if len(Bank) < 150 {
		t.Errorf("bank has %d questions, want at least 150", len(Bank))
	}
}

// The rotation must not inject variance into the very metric being measured:
// a longer question is a longer prefill, and a longer answer is more
// inter-token gaps.
func TestEveryQuestionIsWithinTenPercentOfTheMedianLength(t *testing.T) {
	lengths := make([]int, 0, len(Bank))
	for _, q := range Bank {
		lengths = append(lengths, estimateTokens(q.Prompt()))
	}
	sorted := append([]int(nil), lengths...)
	sort.Ints(sorted)
	median := sorted[len(sorted)/2]

	lo := float64(median) * 0.9
	hi := float64(median) * 1.1
	for i, q := range Bank {
		n := lengths[i]
		if float64(n) < lo || float64(n) > hi {
			t.Errorf("question %q is %d tokens, outside ±10%% of the median %d (%.1f–%.1f): %q",
				q.ID, n, median, lo, hi, q.Prompt())
		}
	}
}

func TestEveryQuestionHasAnAssertableAnswer(t *testing.T) {
	for _, q := range Bank {
		if q.ID == "" {
			t.Errorf("question %q has no ID", q.Ask)
		}
		if q.Want == "" {
			t.Errorf("question %q has no expected answer — it cannot act as a canary", q.ID)
		}
		if !strings.HasSuffix(q.Prompt(), questionSuffix) {
			t.Errorf("question %q does not carry the shared suffix that stabilises output length", q.ID)
		}
		// The assertion must actually pass on a correct answer, or the canary
		// would report a permanent correctness failure.
		if !q.Assert("The answer is " + q.Want + ".") {
			t.Errorf("question %q does not match its own expected answer %q", q.ID, q.Want)
		}
	}
}

func TestQuestionIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, q := range Bank {
		if seen[q.ID] {
			t.Errorf("duplicate question ID %q — per-question correctness rates would merge", q.ID)
		}
		seen[q.ID] = true
	}
}

// A wrong-but-defensible answer is worse than no canary: it fires without a
// fault, and a canary that cries wolf gets ignored.
//
// Every question is checked, not only the capital ones: an excluded subject is
// excluded from the bank, whatever shape the question asking about it takes.
//
// Both halves of the question are checked, because the shapes put the subject
// in different places: a capital question names the country in the Ask, a city
// question names it in the Want. Checking only the Ask would let a city in an
// excluded country in through the back door.
// Compared folded, not raw: the bank spells its data in ASCII (Bogota,
// Chisinau, Asuncion, Medellin), so a raw match against "Côte d'Ivoire" could
// never fire and the guard for it would be dead — a later {"Cote d'Ivoire", …}
// entry would sail past the very test written to stop it.
func TestNoContestedSubjectsInTheBank(t *testing.T) {
	for _, raw := range excludedSubjects {
		excluded := fold(raw)
		for _, q := range Bank {
			if strings.Contains(fold(q.Ask), excluded) {
				t.Errorf("question %q asks about %s, which has no single defensible answer",
					q.ID, excluded)
			}
			if strings.Contains(fold(q.Want), excluded) {
				t.Errorf("question %q expects %q, which names %s, a subject with no single defensible answer",
					q.ID, q.Want, excluded)
			}
		}
	}
}

// Every question shape must be present, so a correctness dip cannot be an
// artefact of one phrasing.
func TestBankCoversEveryQuestionShape(t *testing.T) {
	var capitalsN, citiesN, elementsN, symbolsN int
	for _, q := range Bank {
		switch {
		case strings.HasPrefix(q.ID, "capital-"):
			capitalsN++
		case strings.HasPrefix(q.ID, "city-"):
			citiesN++
		case strings.HasPrefix(q.ID, "element-"):
			elementsN++
		case strings.HasPrefix(q.ID, "symbol-"):
			symbolsN++
		default:
			t.Errorf("question %q has an unrecognised ID shape", q.ID)
		}
	}
	if capitalsN < 50 || citiesN < 30 || elementsN < 50 || symbolsN < 50 {
		t.Errorf("shapes are unbalanced: %d capitals, %d cities, %d elements, %d symbols",
			capitalsN, citiesN, elementsN, symbolsN)
	}
}

// A symbol that is also an English word, or a single letter, matches prose that
// says nothing about the element — the canary's silent direction. The name ->
// symbol shape must skip them; the symbol -> name shape, where they appear in
// the question rather than the answer, must keep them.
func TestTheSymbolShapeSkipsSymbolsThatMatchProse(t *testing.T) {
	for _, q := range Bank {
		if !strings.HasPrefix(q.ID, "symbol-") {
			continue
		}
		if len(q.Want) < 2 {
			t.Errorf("question %q expects the one-letter symbol %q, which any stray capital matches",
				q.ID, q.Want)
		}
		if unusableSymbols[q.Want] {
			t.Errorf("question %q expects %q, an English word — prose alone would grade it correct",
				q.ID, q.Want)
		}
	}
	// The filtered symbols are not lost, only asked the other way round.
	var asked int
	for _, q := range Bank {
		if q.ID == "element-nobelium" && strings.Contains(q.Ask, "No") {
			asked++
		}
	}
	if asked != 1 {
		t.Error("a symbol unusable as an answer must still be asked as a question")
	}
}

// The same rule pointed the other way: an element name that is an everyday
// English word matches a sentence that names no element, so the symbol -> name
// shape must skip it — and, as with the symbols, must still ask the pair in the
// direction where the answer is unambiguous.
func TestTheElementShapeSkipsNamesThatMatchProse(t *testing.T) {
	for _, q := range Bank {
		if strings.HasPrefix(q.ID, "element-") && unusableNames[q.Want] {
			t.Errorf("question %q expects %q, an everyday word — prose alone would grade it correct",
				q.ID, q.Want)
		}
	}
	// The concrete hole this closes: hedging prose that names no element.
	lead := Question{ID: "element-lead", Ask: "a", Want: "lead"}
	if !lead.Assert("Guesses like that lead to errors; the element is tin.") {
		t.Fatal("the premise of the skip has changed — this sentence no longer grades correct")
	}
	var symbolAsked bool
	for _, q := range Bank {
		if q.ID == "symbol-lead" {
			symbolAsked = true
		}
	}
	if !symbolAsked {
		t.Error("a name unusable as an answer must still be asked as a question")
	}
}

// A question that contains its own answer is a dead canary: the assertion can
// never fail, so the entry passes whatever the model has become — including a
// reply that only echoes the question back. City-states are how this gets in
// ("What is the capital city of Singapore?" wants "Singapore"), which is why
// the rule lives here rather than in a note on the data.
func TestNoQuestionContainsItsOwnAnswer(t *testing.T) {
	for _, q := range Bank {
		// Prompt rather than Ask: the shared suffix is sent to the model too, so
		// an answer hiding in it would be just as dead a canary.
		if q.Assert(q.Prompt()) {
			t.Errorf("question %q contains its own expected answer %q — it can never fail: %q",
				q.ID, q.Want, q.Prompt())
		}
	}
}

// The silent half of the correctness canary: a question whose answer also
// matches a *different* question's answer cannot tell the two apart, so a model
// that has genuinely stopped knowing one of them is scored correct.
//
// This is what the word-boundary rule in Assert buys — a bare substring match
// grades "platinum" as tin, "ytterbium" as erbium and "protactinium" as
// actinium, and this test is what would catch the boundary rule regressing.
func TestNoAnswerMatchesAnotherQuestionsAnswer(t *testing.T) {
	for _, q := range Bank {
		for _, other := range Bank {
			if q.ID == other.ID || strings.EqualFold(q.Want, other.Want) {
				continue
			}
			if q.Assert(other.Want) {
				t.Errorf("question %q (wants %q) also accepts %q, the answer to %q — "+
					"a wrong answer would be graded right",
					q.ID, q.Want, other.Want, other.ID)
			}
		}
	}
}

func TestPickRotatesDeterministically(t *testing.T) {
	first := Pick(0)
	if Pick(0).ID != first.ID {
		t.Error("Pick must be deterministic for the same cycle number")
	}
	// A full lap returns to the start, so every question is asked equally often
	// and a per-question correctness rate is meaningful.
	if Pick(int64(len(Bank))).ID != first.ID {
		t.Error("Pick must wrap after a full lap of the bank")
	}
	if Pick(1).ID == first.ID {
		t.Error("consecutive cycles must ask different questions")
	}
	// A negative counter must not panic or index out of range.
	if Pick(-1).ID == "" {
		t.Error("Pick must handle a negative cycle number")
	}
}

func TestAssertIsCaseInsensitiveSubstring(t *testing.T) {
	q := Question{ID: "q", Ask: "a", Want: "Paris"}
	for _, content := range []string{
		"Paris is the capital.",
		"the capital is PARIS",
		"...paris...",
	} {
		if !q.Assert(content) {
			t.Errorf("Assert(%q) = false, want true", content)
		}
	}
	if q.Assert("The capital of France is Lyon.") {
		t.Error("Assert must reject a wrong answer — that is the whole canary")
	}
}

// The match must sit on word boundaries, or a wrong answer that happens to
// contain the right one is graded correct — the canary failing silently, which
// is worse than it crying wolf.
func TestAssertDoesNotMatchInsideAnotherWord(t *testing.T) {
	tin := Question{ID: "element-tin", Ask: "a", Want: "tin"}
	for _, content := range []string{
		"The symbol Sn belongs to platinum.",
		"Sn is a metal with a low melting point.",
		"That element is interesting.",
	} {
		if tin.Assert(content) {
			t.Errorf("Assert(%q) = true, want false — %q appears only inside another word",
				content, tin.Want)
		}
	}
	// ...but the looseness that matters is kept: punctuation, markdown emphasis
	// and a possessive are all boundaries, not word characters.
	for _, content := range []string{
		"Sn is tin.",
		"The answer is **tin**",
		"tin's symbol is Sn",
		"(tin)",
	} {
		if !tin.Assert(content) {
			t.Errorf("Assert(%q) = false, want true — a correct answer must still match", content)
		}
	}
	// An earlier mid-word hit must not hide a later real one.
	if !tin.Assert("Platinum is not it; the answer is tin.") {
		t.Error("Assert must keep scanning past an occurrence that sits inside another word")
	}
}
