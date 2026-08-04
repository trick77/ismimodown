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
func TestNoContestedCapitalsInTheBank(t *testing.T) {
	for _, excluded := range capitalExclusions {
		for _, q := range Bank {
			if strings.Contains(q.Ask, excluded) {
				t.Errorf("question %q asks about %s, whose capital is contested or dual",
					q.ID, excluded)
			}
		}
	}
}

// Both question shapes must be present, so a correctness dip cannot be an
// artefact of one phrasing.
func TestBankCoversBothQuestionShapes(t *testing.T) {
	var capitalsN, elementsN int
	for _, q := range Bank {
		switch {
		case strings.HasPrefix(q.ID, "capital-"):
			capitalsN++
		case strings.HasPrefix(q.ID, "element-"):
			elementsN++
		default:
			t.Errorf("question %q has an unrecognised ID shape", q.ID)
		}
	}
	if capitalsN < 50 || elementsN < 50 {
		t.Errorf("shapes are unbalanced: %d capitals, %d elements", capitalsN, elementsN)
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
