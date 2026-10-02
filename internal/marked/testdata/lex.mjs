import { Marked, Tokenizer, walkTokens } from "file:///C:/Users/ctyja/workspace/pi/node_modules/marked/lib/marked.esm.js";
import { readFileSync } from "node:fs";

const STRICT_STRIKETHROUGH_REGEX = /^(~~)(?=[^\s~])((?:\\.|[^\\])*?(?:\\.|[^\s~\\]))\1(?=[^~]|$)/;

class StrictDel extends Tokenizer {
  del(src) {
    const match = STRICT_STRIKETHROUGH_REGEX.exec(src);
    if (!match) return;
    const text = match[2];
    return {
      type: "del",
      raw: match[0],
      text,
      tokens: this.lexer.inlineTokens(text),
    };
  }
}

function isEscaped(source, index) {
  let backslashes = 0;
  for (let position = index - 1; position >= 0 && source[position] === "\\"; position--) {
    backslashes++;
  }
  return backslashes % 2 === 1;
}

function findClosingDelimiter(source, closing, start) {
  let index = source.indexOf(closing, start);
  while (index >= 0 && isEscaped(source, index)) {
    index = source.indexOf(closing, index + closing.length);
  }
  return index;
}

function looksLikePendingDollarMath(source) {
  return /\\[A-Za-z]+|[_^=+*/<>()[\]|±≤≥≠≈∈→⇒∞∫∑√-]/.test(source);
}

function tokenizeInlineLatex(source) {
  let opening = "";
  let closing = "";
  if (source.startsWith("$$")) {
    opening = "$$";
    closing = "$$";
  } else if (source.startsWith("\\(")) {
    opening = "\\(";
    closing = "\\)";
  } else if (source.startsWith("\\[")) {
    opening = "\\[";
    closing = "\\]";
  } else if (source.startsWith("$") && !/^\$\s/.test(source)) {
    opening = "$";
    closing = "$";
  } else {
    return;
  }
  const closingIndex = findClosingDelimiter(source, closing, opening.length);
  if (
    closingIndex >= 0 &&
    opening === "$" &&
    (/\s$/.test(source.slice(opening.length, closingIndex)) ||
      /^\d/.test(source.slice(closingIndex + 1)) ||
      (/^[A-Z_][A-Z0-9_]*(?:[^A-Za-z0-9_\s])?$/.test(source.slice(opening.length, closingIndex)) &&
        /^[A-Za-z_][A-Za-z0-9_]*/.test(source.slice(closingIndex + 1))) ||
      source.slice(opening.length, closingIndex).includes("`"))
  ) {
    return;
  }
  if (closingIndex < 0) {
    const pendingSource = source.slice(opening.length);
    if (opening.startsWith("\\") || looksLikePendingDollarMath(pendingSource)) {
      return { type: "latex", raw: source, text: pendingSource, pending: true };
    }
    return;
  }
  const text = source.slice(opening.length, closingIndex);
  if (!text || text.includes("\n")) return;
  const raw = source.slice(0, closingIndex + closing.length);
  return { type: "latex", raw, text };
}

function tokenizeBlockLatex(source) {
  const dollarMatch = /^ {0,3}\$\$[ \t]*(?:\n)?([\s\S]*?)\$\$[ \t]*(?:\n|$)/.exec(source);
  if (dollarMatch?.[1]) {
    return { type: "latexBlock", raw: dollarMatch[0], text: dollarMatch[1].trim() };
  }
  const bracketMatch = /^ {0,3}\\\[[ \t]*(?:\n)?([\s\S]*?)\\\][ \t]*(?:\n|$)/.exec(source);
  if (bracketMatch?.[1]) {
    return { type: "latexBlock", raw: bracketMatch[0], text: bracketMatch[1].trim() };
  }
  const pendingBracket = /^ {0,3}\\\[[ \t]*(?:\n)?([\s\S]*)$/.exec(source);
  if (pendingBracket) {
    return { type: "latexBlock", raw: pendingBracket[0], text: pendingBracket[1], pending: true };
  }
  const pendingDollar = /^ {0,3}\$\$[ \t]*(?:\n)?([\s\S]*)$/.exec(source);
  if (pendingDollar?.[1] && looksLikePendingDollarMath(pendingDollar[1])) {
    return { type: "latexBlock", raw: pendingDollar[0], text: pendingDollar[1], pending: true };
  }
}

const latexBlock = {
  name: "latexBlock",
  level: "block",
  start(source) {
    const match = /(?:^|\n) {0,3}(?:\$\$|\\\[)/.exec(source);
    return match ? match.index + (match[0].startsWith("\n") ? 1 : 0) : undefined;
  },
  tokenizer: tokenizeBlockLatex,
};

const latex = {
  name: "latex",
  level: "inline",
  start(source) {
    const indices = [source.indexOf("$"), source.indexOf("\\("), source.indexOf("\\[")].filter((index) => index >= 0);
    return indices.length > 0 ? Math.min(...indices) : undefined;
  },
  tokenizer: tokenizeInlineLatex,
};

const mention = {
  name: "mention",
  level: "inline",
  start(src) {
    const i = src.indexOf("@");
    return i < 0 ? undefined : i;
  },
  tokenizer(src) {
    const m = /^@([A-Za-z0-9_]+)/.exec(src);
    if (!m) return;
    return { type: "mention", raw: m[0], text: m[1] };
  },
};

const alert = {
  name: "alert",
  level: "block",
  start(src) {
    const i = src.indexOf("::");
    return i < 0 ? undefined : i;
  },
  tokenizer(src) {
    const m = /^::([A-Za-z0-9_]+)[ \t]*\n([\s\S]*?)\n::(?:\n|$)/.exec(src);
    if (!m) return;
    const text = m[2];
    return { type: "alert", raw: m[0], text, tokens: this.lexer.inlineTokens(text) };
  },
};

const textTypes = new Set([
  "heading", "paragraph", "text", "code", "blockquote", "list_item", "html",
  "em", "strong", "del", "codespan", "link", "image", "escape", "mention", "alert", "latex", "latexBlock",
]);

function project(tok) {
  const o = { type: tok.type };
  if ("raw" in tok) o.raw = tok.raw;
  if ("text" in tok && (tok.text !== "" || textTypes.has(tok.type))) o.text = tok.text;
  if ("href" in tok) o.href = tok.href;
  if (typeof tok.title === "string") o.title = tok.title;
  if (tok.depth) o.depth = tok.depth;
  if (typeof tok.lang === "string") o.lang = tok.lang;
  if (tok.codeBlockStyle) o.codeBlockStyle = tok.codeBlockStyle;
  if (Array.isArray(tok.tokens)) o.tokens = tok.tokens.map(project);
  if (tok.type === "list") {
    o.ordered = !!tok.ordered;
    o.start = tok.start === "" || tok.start == null ? null : tok.start;
    o.loose = !!tok.loose;
    o.items = (tok.items || []).map(project);
  }
  if (tok.type === "list_item") {
    o.task = !!tok.task;
    o.loose = !!tok.loose;
    if (tok.checked != null) o.checked = !!tok.checked;
  }
  if (tok.type === "checkbox" && tok.checked != null) o.checked = !!tok.checked;
  if (tok.tag) o.tag = tok.tag;
  if (tok.pending) o.pending = true;
  if (tok.block) o.block = true;
  if (tok.pre) o.pre = true;
  if (tok.type === "table") {
    o.align = (tok.align || []).map((a) => a ?? null);
    o.header = (tok.header || []).map(cell);
    o.rows = (tok.rows || []).map((row) => row.map(cell));
  }
  return o;
}

function cell(c) {
  return {
    text: c.text ?? "",
    header: !!c.header,
    align: c.align ?? null,
    tokens: (c.tokens || []).map(project),
  };
}

const input = JSON.parse(readFileSync(0, "utf8"));
const out = [];
for (const c of input.cases) {
  const m = new Marked();
  const opt = {};
  if (c.gfm === false) opt.gfm = false;
  if (c.breaks) opt.breaks = true;
  if (c.pedantic) opt.pedantic = true;
  if (c.strictDel) opt.tokenizer = new StrictDel();
  if (Object.keys(opt).length) m.setOptions(opt);
  if (c.latex) m.use({ extensions: [latexBlock, latex] });
  if (c.ext) m.use({ extensions: [alert, mention] });
  try {
    const tokens = m.lexer(c.src);
    const links = [];
    walkTokens(tokens, (t) => {
      if (t.type === "link" && t.href) links.push(t.href);
    });
    out.push({ name: c.name, tokens: tokens.map(project), links });
  } catch (err) {
    out.push({ name: c.name, error: String(err && err.stack || err) });
  }
}
process.stdout.write(JSON.stringify(out));
