package probe

import (
	"fmt"
	"strings"
)

// The question bank exists for one reason no timing metric can serve: a silent
// reroute to a smaller or quantised model shows up as a CORRECTNESS dip before
// it shows up as a latency dip.
//
// Three constraints shape every entry:
//
//  1. Near-identical length and difficulty. The rotation must not inject
//     variance into the very metric being measured — a test asserts every
//     question is within ±10% of the median token length.
//  2. An assertable answer. "Contains Paris" is a canary; "seems reasonable"
//     is not.
//  3. Exactly one defensible answer. A wrong-but-defensible answer is worse
//     than no canary, because it fires without a fault. That rules out
//     contested and dual capitals, countries whose name means more than one
//     place, answers with two accepted spellings, and any subject where the
//     answer itself is politically contested. excludedSubjects carries the
//     list and the reason for each, and a test asserts none of them reappear.
//
// The shared suffix keeps the output length stable, which matters as much as
// the input length: ITL p50 is computed over the inter-token gaps of this
// response, so a question that provokes one sentence and another that provokes
// six would not be comparable readings.
const questionSuffix = " Answer in two or three sentences."

// Question is one bank entry.
type Question struct {
	// ID is stable across restarts and is stored on the sample, so a
	// correctness dip can be traced to which questions failed.
	ID string
	// Ask is the question text, without the shared suffix.
	Ask string
	// Want is the substring the answer must contain, matched case-insensitively.
	Want string
}

// Prompt returns the full user message.
func (q Question) Prompt() string { return q.Ask + questionSuffix }

// Assert reports whether content contains the expected answer.
//
// Case-insensitive substring, deliberately loose: the canary is watching for a
// model that has stopped knowing the answer, not for one that phrased it
// differently. A stricter match would fire on prose changes and get ignored,
// which is worse than no canary at all.
//
// Loose in the phrasing, but not to the point of matching inside another word:
// the occurrence must sit on word boundaries. A bare substring test grades
// "platinum" as a correct answer for tin, "ytterbium" for erbium, and
// "protactinium" for actinium — the canary's silent failure mode, where a model
// that has genuinely stopped knowing the answer is scored correct. Boundaries
// cost nothing in looseness: punctuation, markdown emphasis and possessives all
// still match, because none of them are letters or digits.
//
// Diacritics are folded on BOTH sides, which is not cosmetic. `capitals` spells
// the answers in ASCII, but a model writes Brasília, Bogotá, Asunción, San
// José, Reykjavík, Chișinău — so a plain substring match would score six
// perfectly correct answers as wrong, forever. That is the cries-wolf failure
// the bank is built to avoid, and the reason folding lives here rather than in
// the data: the next accented capital added would otherwise reintroduce it.
func (q Question) Assert(content string) bool {
	return containsWord(fold(content), fold(q.Want))
}

// containsWord reports whether want occurs in content delimited by something
// other than a letter or a digit on both sides. Both arguments are already
// folded.
func containsWord(content, want string) bool {
	if want == "" {
		return false
	}
	for off := 0; ; {
		i := strings.Index(content[off:], want)
		if i < 0 {
			return false
		}
		start := off + i
		end := start + len(want)
		if !isWordByte(content, start-1) && !isWordByte(content, end) {
			return true
		}
		// Overlapping occurrences matter: the first hit being mid-word says
		// nothing about the next one.
		off = start + 1
	}
}

// isWordByte reports whether the byte at i is a letter or a digit, treating an
// index outside the string — the start and end of the content — as a boundary.
//
// Byte-wise rather than rune-wise because fold has already reduced the answers
// to ASCII; a multi-byte rune left in the content is not a letter or digit here
// and so reads as a boundary, which is the right answer for the punctuation and
// emphasis a model actually writes around a name.
func isWordByte(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	c := s[i]
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// fold lowercases and strips diacritics from the Latin letters that appear in
// place names, so "Brasília" and "Brasilia" compare equal.
func fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if repl, ok := diacriticFolds[r]; ok {
			b.WriteString(repl)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

var diacriticFolds = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a", 'ă': "a", 'ą': "a",
	'ç': "c", 'ć': "c", 'č': "c",
	'ď': "d", 'đ': "d",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ė': "e", 'ę': "e", 'ě': "e",
	'ğ': "g",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ī': "i", 'į': "i", 'ı': "i",
	'ł': "l", 'ĺ': "l", 'ľ': "l",
	'ñ': "n", 'ń': "n", 'ň': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ō': "o", 'ő': "o",
	'ř': "r", 'ŕ': "r",
	'ś': "s", 'š': "s", 'ş': "s", 'ș': "s",
	'ť': "t", 'ţ': "t", 'ț': "t",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ū': "u", 'ů': "u", 'ű': "u",
	'ý': "y", 'ÿ': "y",
	'ź': "z", 'ż': "z", 'ž': "z",
	'ß': "ss", 'æ': "ae", 'œ': "oe",
}

// Bank is the rotating question set.
var Bank = buildBank()

// Pick returns the question for cycle n, rotating deterministically.
//
// Deterministic rather than random: with a fixed rotation, every question is
// asked the same number of times over any window, so a per-question correctness
// rate is meaningful. Random selection would leave some questions
// under-sampled and make a dip indistinguishable from sampling noise.
func Pick(n int64) Question {
	if len(Bank) == 0 {
		return Question{}
	}
	idx := n % int64(len(Bank))
	if idx < 0 {
		idx += int64(len(Bank))
	}
	return Bank[idx]
}

// capitals holds country -> capital pairs. Contested and dual capitals are
// excluded by construction; excludedSubjects documents which and why, and a
// test asserts none of them reappear.
var capitals = [][2]string{
	{"France", "Paris"}, {"Japan", "Tokyo"}, {"Canada", "Ottawa"},
	{"Australia", "Canberra"}, {"Brazil", "Brasilia"}, {"Norway", "Oslo"},
	{"Portugal", "Lisbon"}, {"Greece", "Athens"}, {"Austria", "Vienna"},
	{"Poland", "Warsaw"}, {"Hungary", "Budapest"}, {"Denmark", "Copenhagen"},
	{"Finland", "Helsinki"}, {"Sweden", "Stockholm"}, {"Ireland", "Dublin"},
	{"Iceland", "Reykjavik"}, {"Romania", "Bucharest"}, {"Bulgaria", "Sofia"},
	{"Croatia", "Zagreb"}, {"Serbia", "Belgrade"}, {"Slovakia", "Bratislava"},
	{"Slovenia", "Ljubljana"}, {"Estonia", "Tallinn"}, {"Latvia", "Riga"},
	{"Lithuania", "Vilnius"}, {"Belarus", "Minsk"},
	{"Armenia", "Yerevan"}, {"Mongolia", "Ulaanbaatar"},
	{"Nepal", "Kathmandu"}, {"Thailand", "Bangkok"}, {"Vietnam", "Hanoi"},
	{"Cambodia", "Phnom Penh"}, {"Laos", "Vientiane"},
	{"Philippines", "Manila"},
	{"Bangladesh", "Dhaka"}, {"Pakistan", "Islamabad"}, {"Afghanistan", "Kabul"},
	{"Iran", "Tehran"}, {"Iraq", "Baghdad"}, {"Jordan", "Amman"},
	{"Lebanon", "Beirut"}, {"Syria", "Damascus"}, {"Turkey", "Ankara"},
	{"Egypt", "Cairo"}, {"Morocco", "Rabat"}, {"Algeria", "Algiers"},
	{"Tunisia", "Tunis"},
	{"Ethiopia", "Addis Ababa"}, {"Kenya", "Nairobi"}, {"Uganda", "Kampala"},
	{"Rwanda", "Kigali"}, {"Zambia", "Lusaka"}, {"Zimbabwe", "Harare"},
	{"Angola", "Luanda"}, {"Mozambique", "Maputo"}, {"Namibia", "Windhoek"},
	{"Botswana", "Gaborone"}, {"Ghana", "Accra"}, {"Nigeria", "Abuja"},
	{"Senegal", "Dakar"}, {"Mali", "Bamako"}, {"Cuba", "Havana"},
	{"Jamaica", "Kingston"}, {"Mexico", "Mexico City"}, {"Guatemala", "Guatemala City"},
	{"Honduras", "Tegucigalpa"}, {"Nicaragua", "Managua"}, {"Costa Rica", "San Jose"},
	{"Panama", "Panama City"}, {"Colombia", "Bogota"}, {"Venezuela", "Caracas"},
	{"Ecuador", "Quito"}, {"Peru", "Lima"}, {"Paraguay", "Asuncion"},
	{"Uruguay", "Montevideo"}, {"Argentina", "Buenos Aires"},
	{"Uzbekistan", "Tashkent"}, {"Azerbaijan", "Baku"}, {"Qatar", "Doha"},
	{"Kuwait", "Kuwait City"}, {"Oman", "Muscat"},
	{"Malta", "Valletta"}, {"Albania", "Tirana"}, {"Moldova", "Chisinau"},
}

// excludedSubjects are the subjects deliberately absent from the bank —
// whatever shape a question about them would take, not only a capital one.
//
// For each one a model can answer "wrongly" while being entirely defensible,
// which would fire the correctness canary with no fault behind it. A canary
// that cries wolf gets ignored, and an ignored canary is worse than none.
//
// A test asserts none of these appear in the bank — in any question, not only a
// capital one, so a later question shape cannot smuggle one back in.
//
// The reason recorded against each is the ambiguity, never a position on which
// answer is right: this repo is public, and the bank has no business taking one.
var excludedSubjects = []string{
	// Contested, dual, or de-facto-split capitals.
	"Bolivia",       // Sucre (constitutional) vs La Paz (seat of government)
	"South Africa",  // Pretoria / Cape Town / Bloemfontein
	"Netherlands",   // Amsterdam (capital) vs The Hague (seat of government)
	"Myanmar",       // Naypyidaw, but Yangon is widely given
	"Côte d'Ivoire", // Yamoussoukro (official) vs Abidjan (de facto)
	"Eswatini",      // Mbabane (administrative) vs Lobamba (legislative)
	"Sri Lanka",     // Sri Jayawardenepura Kotte vs Colombo
	"Benin",         // Porto-Novo (official) vs Cotonou (de facto)
	"Tanzania",      // Dodoma (official) vs Dar es Salaam (de facto)
	"Malaysia",      // Kuala Lumpur (official) vs Putrajaya (administrative)
	"Indonesia",     // Jakarta vs Nusantara, mid-relocation
	"Libya",         // rival governments, so the seat depends on who is asked
	"Sudan",         // Khartoum (official) vs the wartime de-facto seat

	// Politically contested subjects. Whatever a model answers, the answer is
	// disputed by somebody, so it can never be scored — and a monitoring probe
	// that runs 288 times a day is the wrong place to hold the argument.
	"Israel",
	"Palestine",
	"Taiwan",
	"Kosovo",
	"Western Sahara",
	"Cyprus", // de-facto divided since 1974, capital included

	// One name, more than one place. Confirmed in production: a model answered
	// "Atlanta" for Georgia and was graded wrong, which is exactly the
	// cries-wolf failure the bank is built to avoid.
	"Georgia",

	// One place, more than one accepted answer. Want is a single string, so a
	// model writing the other spelling scores wrong while being correct.
	"Ukraine",    // Kyiv / Kiev
	"Kazakhstan", // Astana / Nur-Sultan, renamed and renamed back

	// A name that is no country's full name, so it can never be the Want:
	// it matches inside both of the countries that carry it, and a model that
	// named the wrong one would be graded correct.
	"Korea", // North Korea / South Korea; bare "Korea" names neither

	// A city-state answers its own question: Want would be the country name
	// already in the Ask, so the assertion could never fail and the entry would
	// be a dead canary that always passes. TestNoQuestionContainsItsOwnAnswer
	// enforces this by construction, so the next one cannot slip in.
	"Singapore",
}

// cities holds city -> country pairs, giving the bank a third question shape
// and one that is not a capital, so the rotation does not lean on the single
// subject where the "one defensible answer" rule is hardest to hold.
//
// Every entry is a single-word city — a two-word name pushes the prompt outside
// the ±10% length band the bank is policed by — that is well known, that is not
// its country's capital, and whose country is named the same way by everyone.
// Countries whose common name a model may shorten (the United States, the
// United Kingdom) are absent: "the US" or "England" would be a correct answer
// scored wrong. So are countries that appear as an answer in `capitals`, so
// that no question's expected answer can be matched by another's.
//
// Absent for the opposite reason: a country whose whole name sits inside the
// name of a different place, where a wrong answer is graded right and the canary
// fails silently. Ireland is the one that got as far as being written down —
// "Cork is in Northern Ireland" would have scored correct. No test can catch
// this class, because the name that swallows the answer need not be in the bank
// at all, so it is a rule the next entry has to be read against.
var cities = [][2]string{
	{"Kyoto", "Japan"}, {"Osaka", "Japan"}, {"Hiroshima", "Japan"},
	{"Marseille", "France"}, {"Lyon", "France"}, {"Bordeaux", "France"},
	{"Hamburg", "Germany"}, {"Munich", "Germany"}, {"Cologne", "Germany"},
	{"Milan", "Italy"}, {"Naples", "Italy"}, {"Turin", "Italy"},
	{"Seville", "Spain"}, {"Zaragoza", "Spain"},
	{"Porto", "Portugal"}, {"Krakow", "Poland"}, {"Gdansk", "Poland"},
	{"Salzburg", "Austria"}, {"Zurich", "Switzerland"}, {"Geneva", "Switzerland"},
	{"Antwerp", "Belgium"}, {"Bergen", "Norway"}, {"Gothenburg", "Sweden"},
	{"Tampere", "Finland"}, {"Aarhus", "Denmark"},
	{"Thessaloniki", "Greece"}, {"Bursa", "Turkey"}, {"Isfahan", "Iran"},
	{"Toronto", "Canada"}, {"Vancouver", "Canada"}, {"Montreal", "Canada"},
	{"Melbourne", "Australia"}, {"Brisbane", "Australia"}, {"Auckland", "New Zealand"},
	{"Shanghai", "China"}, {"Guangzhou", "China"},
	{"Kolkata", "India"}, {"Bengaluru", "India"}, {"Lahore", "Pakistan"},
	{"Alexandria", "Egypt"}, {"Casablanca", "Morocco"}, {"Marrakesh", "Morocco"},
	{"Mombasa", "Kenya"}, {"Ibadan", "Nigeria"}, {"Rosario", "Argentina"},
	{"Cusco", "Peru"}, {"Medellin", "Colombia"}, {"Valparaiso", "Chile"},
}

// elements holds symbol -> name pairs, giving the bank a second question shape
// so a correctness dip cannot be an artefact of one phrasing.
var elements = [][2]string{
	{"Fe", "iron"}, {"Au", "gold"}, {"Ag", "silver"}, {"Cu", "copper"},
	{"Pb", "lead"}, {"Sn", "tin"}, {"Zn", "zinc"}, {"Hg", "mercury"},
	{"Na", "sodium"}, {"K", "potassium"}, {"Ca", "calcium"}, {"Mg", "magnesium"},
	{"Si", "silicon"}, {"He", "helium"}, {"Ne", "neon"},
	{"Ar", "argon"}, {"Kr", "krypton"}, {"Xe", "xenon"}, {"Li", "lithium"},
	{"Be", "beryllium"}, {"Ti", "titanium"}, {"Cr", "chromium"}, {"Mn", "manganese"},
	{"Ni", "nickel"}, {"Co", "cobalt"}, {"Pt", "platinum"}, {"U", "uranium"},
	{"Rn", "radon"}, {"Br", "bromine"}, {"Cl", "chlorine"}, {"F", "fluorine"},
	{"Ba", "barium"}, {"Sr", "strontium"}, {"Rb", "rubidium"},
	{"Mo", "molybdenum"}, {"Nb", "niobium"}, {"Zr", "zirconium"}, {"Cd", "cadmium"},
	{"Bi", "bismuth"}, {"Sb", "antimony"}, {"As", "arsenic"}, {"Se", "selenium"},
	{"Te", "tellurium"}, {"Ge", "germanium"}, {"Ga", "gallium"}, {"In", "indium"},
	{"Tl", "thallium"}, {"Ra", "radium"}, {"Th", "thorium"}, {"Pd", "palladium"},
	{"Rh", "rhodium"}, {"Ru", "ruthenium"}, {"Os", "osmium"}, {"Ir", "iridium"},
	{"Re", "rhenium"}, {"Ta", "tantalum"}, {"Hf", "hafnium"}, {"Ce", "cerium"},
	{"La", "lanthanum"}, {"Nd", "neodymium"}, {"Sm", "samarium"}, {"Gd", "gadolinium"},
	{"Dy", "dysprosium"}, {"Er", "erbium"}, {"Yb", "ytterbium"}, {"Lu", "lutetium"},
	{"Sc", "scandium"}, {"V", "vanadium"}, {"Tc", "technetium"}, {"Po", "polonium"},
	{"At", "astatine"}, {"Fr", "francium"}, {"Ac", "actinium"}, {"Pa", "protactinium"},
	{"Np", "neptunium"}, {"Pu", "plutonium"}, {"Am", "americium"}, {"Cm", "curium"},
	{"Bk", "berkelium"}, {"Cf", "californium"}, {"Es", "einsteinium"}, {"Fm", "fermium"},
	{"Md", "mendelevium"}, {"No", "nobelium"}, {"Lr", "lawrencium"}, {"Rf", "rutherfordium"},
	{"Db", "dubnium"}, {"Sg", "seaborgium"},
}

// unusableSymbols are the element symbols that cannot serve as an expected
// answer, and so are skipped by the name -> symbol shape. Both kinds fail the
// same way: a wrong answer is graded right, silently.
//
//   - A symbol that is also an English word. "No" for nobelium matches the "no"
//     in any sentence; so do in, as, at, be, he, am, re, la, es, er and pa.
//   - A one-letter symbol. Too small to carry a boundary: a stray "U" or "K" in
//     prose, a bullet, a unit, an initial, all match.
//
// The symbol -> name shape keeps every one of these — "Which chemical element
// has the symbol No?" is a perfectly good question, because there the symbol is
// the prompt and the unambiguous element name is the answer. Only the reverse
// direction is affected, which is why this filters the shape rather than the
// data.
var unusableSymbols = map[string]bool{
	"No": true, "In": true, "As": true, "At": true, "Be": true, "He": true,
	"Am": true, "Re": true, "La": true, "Es": true, "Er": true, "Pa": true,
}

// buildBank assembles the rotation from the four question shapes.
//
// Generated rather than hand-written so the length constraint holds by
// construction: every entry differs only in a country or element name, which
// keeps them all within a token or two of each other. Hand-writing 185
// individually-phrased questions is how length variance gets in.
func buildBank() []Question {
	bank := make([]Question, 0, len(capitals)+len(cities)+2*len(elements))
	for _, c := range capitals {
		bank = append(bank, Question{
			ID:   "capital-" + slug(c[0]),
			Ask:  fmt.Sprintf("What is the capital city of %s?", c[0]),
			Want: c[1],
		})
	}
	for _, c := range cities {
		bank = append(bank, Question{
			ID:   "city-" + slug(c[0]),
			Ask:  fmt.Sprintf("Which country is the city of %s in?", c[0]),
			Want: c[1],
		})
	}
	for _, e := range elements {
		bank = append(bank, Question{
			ID:   "element-" + slug(e[1]),
			Ask:  fmt.Sprintf("Which chemical element has the symbol %s?", e[0]),
			Want: e[1],
		})
	}
	// The same pairs asked backwards. A model that has been quantised or
	// swapped can lose one direction of a fact while keeping the other, so
	// asking symbol -> name and name -> symbol is not the same question twice.
	// It also costs nothing to vet: the pairs are already in the bank.
	for _, e := range elements {
		if len(e[0]) < 2 || unusableSymbols[e[0]] {
			continue
		}
		bank = append(bank, Question{
			ID:   "symbol-" + slug(e[1]),
			Ask:  fmt.Sprintf("What is the chemical symbol for %s?", e[1]),
			Want: e[0],
		})
	}
	return bank
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}
