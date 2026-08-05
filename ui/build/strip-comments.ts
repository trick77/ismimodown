// Keeps the source comments out of what the container serves.
//
// index.html and the text files in public/ carry long notes about WHY they look
// the way they do — the Host() rule the og: URLs hardcode, the script that
// redraws the card, the Safari bug the tab icon's fill exists for. Every one of
// them is worth keeping where it is: the reader who breaks the rule is the one
// editing the tag right next to it. None of them is worth serving. They name
// compose.yaml, hack scripts, component files and repo paths, and View Source
// is not where any of that belongs.
//
// Nothing else in the pipeline strips them. Vite leaves HTML comments alone,
// public/ is copied byte for byte, and there is no minifier after it — so a
// comment written in ui/ reaches the reader verbatim. esbuild already drops the
// TSX ones, which is why only these file kinds are handled here.
import { readdir, readFile, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import type { Plugin, ResolvedConfig } from "vite";

// Non-greedy on purpose. `<!--[\s\S]*-->` matches from the FIRST comment to the
// LAST one and takes every og:/twitter: tag between them with it — a build that
// still looks fine in a browser and ships link previews with no card.
const MARKUP_COMMENT = /[ \t]*<!--[\s\S]*?-->[ \t]*\r?\n?/g;

// robots.txt has its own syntax: # to end of line, whole lines here.
const ROBOTS_COMMENT = /^[ \t]*#.*\r?\n?/gm;

/** Strips <!-- --> comments from HTML, SVG or XML. */
export function stripMarkupComments(source: string): string {
  return source.replace(MARKUP_COMMENT, "");
}

/** Strips # comment lines from a robots.txt. */
export function stripRobotsComments(source: string): string {
  return source.replace(ROBOTS_COMMENT, "");
}

// Two hooks because the two kinds of file reach the output by different routes.
// transformIndexHtml sees index.html; publicDir assets are copied outside the
// bundle entirely and exist only on disk, after closeBundle.
export function stripCommentsPlugin(): Plugin {
  let outDir = "";
  return {
    name: "ismimodown:strip-comments",
    // Dev serves from source and nobody but the author is looking.
    apply: "build",
    // Last, so comments injected by another plugin are caught too.
    enforce: "post",
    configResolved(config: ResolvedConfig) {
      outDir = resolve(config.root, config.build.outDir);
    },
    transformIndexHtml: {
      order: "post",
      handler: (html: string) => stripMarkupComments(html),
    },
    async closeBundle() {
      const entries = await readdir(outDir, {
        withFileTypes: true,
        recursive: true,
      });
      for (const entry of entries) {
        if (!entry.isFile()) continue;
        const strip = stripperFor(entry.name);
        if (!strip) continue;
        const file = resolve(entry.parentPath, entry.name);
        const source = await readFile(file, "utf8");
        const stripped = strip(source);
        if (stripped !== source) await writeFile(file, stripped);
      }
    },
  };
}

function stripperFor(name: string): ((source: string) => string) | null {
  if (name === "robots.txt") return stripRobotsComments;
  // .html is here for a public/ page that never passes through
  // transformIndexHtml; the entry shell is handled by the hook above.
  if (/\.(svg|xml|html)$/.test(name)) return stripMarkupComments;
  return null;
}
