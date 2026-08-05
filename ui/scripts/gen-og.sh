#!/usr/bin/env bash
# Regenerate the link-preview card, public/og.png, from assets/og/card.html.
# Run it by hand after editing that file, and commit what it writes:
#
#   ui/scripts/gen-og.sh
#
# The output is COMMITTED rather than generated during the build, for the same
# reason gen-icons.sh commits its rasters: neither `npm run build` nor CI then
# needs a browser or an image toolchain.
#
# Chrome does the rendering, not librsvg. The card is typeset in the two
# Anthropic variable faces that ui/src/fonts carries as woff2, and fontconfig —
# which is how librsvg resolves type — neither reads woff2 nor picks fonts up
# out of a repo directory. An SVG source would fall back to a system serif
# without erroring, and the card would already be cached in someone's chat
# before anyone noticed. Chrome loads @font-face over a relative path and is the
# same engine that renders the site.
#
# The card carries no measurements; see the comment at the top of card.html for
# why. Nothing here needs the backend, a database, or a running app.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)" # ui/
SRC="$DIR/assets/og/card.html"
OUT="$DIR/public/og.png"
W=1200
H=630
VOID='#131312' # index.css --color-void, the card's ground

CHROME="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
if [[ ! -x "$CHROME" ]]; then
	echo "gen-og: Chrome not found at $CHROME — set CHROME=/path/to/chrome" >&2
	exit 1
fi
# bc is checked for the same reason gen-icons.sh checks it: the float
# comparisons below are command substitutions, and without bc they come back
# empty, the `if` reads false, and every assertion passes silently.
for tool in magick bc; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "gen-og: $tool not found — brew install imagemagick bc" >&2
		exit 1
	fi
done

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
SHOT="$TMP/og.png"

# --allow-file-access-from-files is what lets the file:// page fetch the woff2
# beside it; without it Chrome blocks the font as a cross-origin read, renders
# in Times and exits 0. --virtual-time-budget makes it wait for the fonts to
# load and the paint to settle instead of shooting the first frame.
# --force-device-scale-factor pins the output to exactly WxH on a retina
# display, where the default would silently produce a 2x image.
"$CHROME" \
	--headless \
	--disable-gpu \
	--hide-scrollbars \
	--force-device-scale-factor=1 \
	--window-size="$W,$H" \
	--virtual-time-budget=4000 \
	--allow-file-access-from-files \
	--screenshot="$SHOT" \
	"file://$SRC" >/dev/null 2>&1 || true

if [[ ! -s "$SHOT" ]]; then
	echo "gen-og: Chrome wrote no screenshot" >&2
	exit 1
fi

# --- verify -----------------------------------------------------------------
# Chrome exits 0 on most of the ways this goes wrong — a blocked font, a blank
# paint, a doubled scale factor — so every property the card depends on is
# asserted here rather than trusted.
fail=0

geom="$(magick identify -format '%wx%h' "$SHOT")"
if [[ "$geom" != "${W}x${H}" ]]; then
	echo "gen-og: rendered ${geom}, expected ${W}x${H}" >&2
	fail=1
fi

# The ground, sampled at the bottom-right where the card is empty by design.
# Catches a lost background rule, which would ship a white card into every dark
# chat client.
ground="$(magick "$SHOT" -alpha off -format "%[hex:p{$((W - 8)),$((H - 8))}]" info: | tr '[:upper:]' '[:lower:]')"
if [[ "$ground" != "${VOID#\#}" ]]; then
	echo "gen-og: ground is #$ground, expected $VOID" >&2
	fail=1
fi

# A blank canvas has a standard deviation of 0. This is the check that fires
# when the page fails to load at all and Chrome shoots an empty viewport.
SPREAD="$(magick "$SHOT" -alpha off -format '%[fx:standard_deviation]' info:)"
if (($(echo "$SPREAD < 0.02" | bc -l))); then
	echo "gen-og: image is nearly flat (stddev $SPREAD) — did the page load?" >&2
	fail=1
fi

# The wordmark's measured width, which is how a font fallback is caught. The
# brand serif sets "mimostats" at 116px to 534px; with the woff2 unreachable
# Chrome falls back and the same string measures 442px, so the band below
# rejects it. That was checked by breaking the @font-face URLs on purpose — do
# the same before widening this range, or it stops asserting anything.
#
# This doubles as the square-crop guard. WhatsApp keeps only the middle 630px
# (see card.html), so a wordmark wider than that is cut in half there and
# nowhere else — the one failure that never shows up in a 1.91:1 preview.
#
# Threshold, not -fuzz -trim. The ground is the aura gradient rather than a flat
# colour, so trim's corner-colour comparison walks out into the wash and reports
# most of the canvas as ink (862px against a true 534px). Reducing to a
# black-and-white mask at 60% keeps the near-white wordmark and drops both the
# gradient and the muted text around it.
#
# The band is y=140..270; the wordmark's ink sits at 171..258, clear of the
# eyebrow above and the lede below. Keep the slack — a band that clips the
# wordmark measures a fragment and reports a fallback that is not there.
MARK_W="$(magick "$SHOT" -crop "${W}x130+0+140" +repage -alpha off \
	-colorspace gray -threshold 60% -trim -format '%w' info:)"
if ((MARK_W < 480 || MARK_W > 600)); then
	echo "gen-og: wordmark measures ${MARK_W}px, expected 480-600 — font fallback, or type resized past the square crop" >&2
	fail=1
fi

[[ "$fail" == 0 ]] || exit 1

# oxipng/pngcrush are not assumed; Chrome's PNG is already reasonable and the
# file is served once per scrape, not once per visitor.
mkdir -p "$(dirname "$OUT")"
cp "$SHOT" "$OUT"
echo "gen-og: wrote og.png (${geom}, wordmark ${MARK_W}px, $(wc -c <"$OUT" | tr -d ' ') bytes) -> $OUT"
