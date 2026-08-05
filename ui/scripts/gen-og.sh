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

# The heading's measured width, which is how a font fallback is caught. The
# brand serif sets "Is Xiaomi MiMo / down?" at 76px to a longest line of 527px;
# with the woff2 unreachable Chrome falls back and the same two lines measure
# 481px, so the band below rejects it. That was checked by breaking the
# @font-face URLs on purpose — do the same before widening this range, or it
# stops asserting anything.
#
# The band is tighter than the old one (which sat around a 534px single-word
# wordmark) because the real and fallback renders are only 46px apart here. That
# is the price of a longer string: the per-glyph difference stays a fixed
# fraction while the absolute gap does not grow. Do not "round it out".
#
# The line break in card.html is a <br>, not a wrap, and this assertion is why.
# Left to the measure, the fallback face re-broke the question one word later
# and measured 533px against the real 485px — WIDER, and inside any band drawn
# around the real value. A forced break makes this a measurement of the font
# rather than of where a line happened to fall.
#
# This doubles as the square-crop guard. WhatsApp keeps only the middle 630px
# (see card.html), so a heading wider than that is cut in half there and
# nowhere else — the one failure that never shows up in a 1.91:1 preview.
#
# Threshold, not -fuzz -trim. The ground is the aura gradient rather than a flat
# colour, so trim's corner-colour comparison walks out into the wash and reports
# most of the canvas as ink (862px against a true 534px). Reducing to a
# black-and-white mask keeps the type and drops the gradient.
#
# 50%, not the 60% this used while the heading was a single ink-coloured word.
# "Xiaomi MiMo" is set in the accent, #d97757, which is about 57% grey — a 60%
# cut dropped those two words from the mask and measured "down?" alone at 376px.
# 50% keeps the accent. It does NOT drop --color-muted (#9c9a92, ~60%) — a lower
# threshold keeps more, never less; the y band below is what excludes the lede.
# Repainting the accent to white before the mask was tried instead and does not
# work; the aura's warm corners are within any usable -fuzz of it.
#
# The crop is $((W - 240))x190+120+135 — 960 wide at the current canvas — and
# both offsets earn their place:
#
#   x — inset 120px from each edge. At 50% the aura's bright corner survives as
#   a few stray pixels at the extreme left, and -trim measures to them: 858px
#   for a heading that is really 527px. Nothing legitimate goes near the edge;
#   the composition is centred. The square-crop guard still holds, because a
#   heading wider than the 960px window clips to it and reports ~960, well past
#   the upper bound below.
#
#   y — starts at 135 rather than 115, because the eyebrow's icon has an accent
#   border that at 50% reaches into the top of a taller band.
#
# The ink sits at 171..301 inside that window, clear of the eyebrow above and
# the lede below. Keep the slack: a band that clips the heading measures a
# fragment and reports a fallback that is not there. Not hypothetical — at 84px
# the two lines outgrew a 200px window and this check read 347px, a clipped
# fragment, rather than the overflow it was actually looking at.
#
# If the band comes back all black — type gone, or the band moved off the
# wordmark — -trim has nothing to trim. ImageMagick 7 treats that as a WARNING,
# not an error: it prints "geometry does not contain image" on stderr, reports a
# width of 1 and still exits 0, so the range check below catches it and prints
# the diagnosis. Verified against `magick -size 1200x630 xc:black`, not assumed.
#
# The `|| echo 0` is only insurance against a version that decides to exit
# non-zero instead, where `set -e` would otherwise kill the script on the raw
# ImageMagick error. stderr is deliberately NOT silenced: on a real failure that
# warning names the cause, and this is the check most likely to be the first
# sign that something moved.
MARK_W="$(magick "$SHOT" -crop "$((W - 240))x190+120+135" +repage -alpha off \
	-colorspace gray -threshold 50% -trim -format '%w' info: || echo 0)"
if ((MARK_W < 505 || MARK_W > 570)); then
	echo "gen-og: heading measures ${MARK_W}px, expected 505-570 — font fallback, or type resized past the square crop" >&2
	fail=1
fi

[[ "$fail" == 0 ]] || exit 1

# oxipng/pngcrush are not assumed; Chrome's PNG is already reasonable and the
# file is served once per scrape, not once per visitor.
mkdir -p "$(dirname "$OUT")"
cp "$SHOT" "$OUT"
echo "gen-og: wrote og.png (${geom}, heading ${MARK_W}px, $(wc -c <"$OUT" | tr -d ' ') bytes) -> $OUT"
