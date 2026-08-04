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
//  3. No contested or dual capitals (Bolivia, South Africa, Israel, the
//     Netherlands, Myanmar, Côte d'Ivoire, Eswatini, Sri Lanka, Benin,
//     Tanzania). A wrong-but-defensible answer is worse than no canary,
//     because it fires without a fault.
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
func (q Question) Assert(content string) bool {
	return strings.Contains(strings.ToLower(content), strings.ToLower(q.Want))
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
// excluded by construction; capitalExclusions documents which and why, and a
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
	{"Lithuania", "Vilnius"}, {"Ukraine", "Kyiv"}, {"Belarus", "Minsk"},
	{"Georgia", "Tbilisi"}, {"Armenia", "Yerevan"}, {"Mongolia", "Ulaanbaatar"},
	{"Nepal", "Kathmandu"}, {"Thailand", "Bangkok"}, {"Vietnam", "Hanoi"},
	{"Cambodia", "Phnom Penh"}, {"Laos", "Vientiane"}, {"Indonesia", "Jakarta"},
	{"Philippines", "Manila"}, {"Malaysia", "Kuala Lumpur"}, {"Singapore", "Singapore"},
	{"Bangladesh", "Dhaka"}, {"Pakistan", "Islamabad"}, {"Afghanistan", "Kabul"},
	{"Iran", "Tehran"}, {"Iraq", "Baghdad"}, {"Jordan", "Amman"},
	{"Lebanon", "Beirut"}, {"Syria", "Damascus"}, {"Turkey", "Ankara"},
	{"Egypt", "Cairo"}, {"Morocco", "Rabat"}, {"Algeria", "Algiers"},
	{"Tunisia", "Tunis"}, {"Libya", "Tripoli"}, {"Sudan", "Khartoum"},
	{"Ethiopia", "Addis Ababa"}, {"Kenya", "Nairobi"}, {"Uganda", "Kampala"},
	{"Rwanda", "Kigali"}, {"Zambia", "Lusaka"}, {"Zimbabwe", "Harare"},
	{"Angola", "Luanda"}, {"Mozambique", "Maputo"}, {"Namibia", "Windhoek"},
	{"Botswana", "Gaborone"}, {"Ghana", "Accra"}, {"Nigeria", "Abuja"},
	{"Senegal", "Dakar"}, {"Mali", "Bamako"}, {"Cuba", "Havana"},
	{"Jamaica", "Kingston"}, {"Mexico", "Mexico City"}, {"Guatemala", "Guatemala City"},
	{"Honduras", "Tegucigalpa"}, {"Nicaragua", "Managua"}, {"Costa Rica", "San Jose"},
	{"Panama", "Panama City"}, {"Colombia", "Bogota"}, {"Venezuela", "Caracas"},
	{"Ecuador", "Quito"}, {"Peru", "Lima"}, {"Paraguay", "Asuncion"},
	{"Uruguay", "Montevideo"}, {"Argentina", "Buenos Aires"}, {"Kazakhstan", "Astana"},
	{"Uzbekistan", "Tashkent"}, {"Azerbaijan", "Baku"}, {"Qatar", "Doha"},
	{"Kuwait", "Kuwait City"}, {"Oman", "Muscat"}, {"Cyprus", "Nicosia"},
	{"Malta", "Valletta"}, {"Albania", "Tirana"}, {"Moldova", "Chisinau"},
}

// capitalExclusions are the countries deliberately absent from `capitals`.
//
// Each has a contested, dual, or de-facto-split capital, so a model can answer
// "wrongly" while being entirely defensible — which would fire the correctness
// canary with no fault behind it. A canary that cries wolf gets ignored, and an
// ignored canary is worse than none.
//
// A test asserts none of these appear in the bank.
var capitalExclusions = []string{
	"Bolivia",       // Sucre (constitutional) vs La Paz (seat of government)
	"South Africa",  // Pretoria / Cape Town / Bloemfontein
	"Israel",        // internationally contested
	"Netherlands",   // Amsterdam (capital) vs The Hague (seat of government)
	"Myanmar",       // Naypyidaw, but Yangon is widely given
	"Côte d'Ivoire", // Yamoussoukro (official) vs Abidjan (de facto)
	"Eswatini",      // Mbabane (administrative) vs Lobamba (legislative)
	"Sri Lanka",     // Sri Jayawardenepura Kotte vs Colombo
	"Benin",         // Porto-Novo (official) vs Cotonou (de facto)
	"Tanzania",      // Dodoma (official) vs Dar es Salaam (de facto)
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

// buildBank assembles the rotation from the two question shapes.
//
// Generated rather than hand-written so the length constraint holds by
// construction: every entry differs only in a country or element name, which
// keeps them all within a token or two of each other. Hand-writing 185
// individually-phrased questions is how length variance gets in.
func buildBank() []Question {
	bank := make([]Question, 0, len(capitals)+len(elements))
	for _, c := range capitals {
		bank = append(bank, Question{
			ID:   "capital-" + slug(c[0]),
			Ask:  fmt.Sprintf("What is the capital city of %s?", c[0]),
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
