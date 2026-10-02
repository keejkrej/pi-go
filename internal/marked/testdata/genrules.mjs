import { writeFileSync } from "node:fs";
import { Lexer } from "file:///C:/Users/ctyja/workspace/pi/node_modules/marked/lib/marked.esm.js";

const other = {
  codeRemoveIndent: /^(?: {1,4}| {0,3}\t)/gm,
  outputLinkReplace: /\\([\[\]])/g,
  indentCodeCompensation: /^(\s+)(?:```)/,
  beginningSpace: /^\s+/,
  endingHash: /#$/,
  startingSpaceChar: /^ /,
  endingSpaceChar: / $/,
  nonSpaceChar: /[^ ]/,
  newLineCharGlobal: /\n/g,
  tabCharGlobal: /\t/g,
  multipleSpaceGlobal: /\s+/g,
  blankLine: /^[ \t]*$/,
  doubleBlankLine: /\n[ \t]*\n[ \t]*$/,
  blockquoteStart: /^ {0,3}>/,
  blockquoteSetextReplace: /\n {0,3}((?:=+|-+) *)(?=\n|$)/g,
  blockquoteSetextReplace2: /^ {0,3}>[ \t]?/gm,
  listReplaceNesting: /^ {1,4}(?=( {4})*[^ ])/g,
  listIsTask: /^\[[ xX]\] +\S/,
  listReplaceTask: /^\[[ xX]\] +/,
  listTaskCheckbox: /\[[ xX]\]/,
  anyLine: /\n.*\n/,
  hrefBrackets: /^<(.*)>$/,
  tableDelimiter: /[:|]/,
  tableAlignChars: /^\||\| *$/g,
  tableRowBlankLine: /\n[ \t]*$/,
  tableAlignRight: /^ *-+: *$/,
  tableAlignCenter: /^ *:-+: *$/,
  tableAlignLeft: /^ *:-+ *$/,
  startATag: /^<a /i,
  endATag: /^<\/a>/i,
  startPreScriptTag: /^<(pre|code|kbd|script)(\s|>)/i,
  endPreScriptTag: /^<\/(pre|code|kbd|script)(\s|>)/i,
  startAngleBracket: /^</,
  endAngleBracket: />$/,
  pedanticHrefTitle: /^([^'"]*[^\s])\s+(['"])(.*)\2/,
  unicodeAlphaNumeric: /[\p{L}\p{N}]/u,
  findPipe: /\|/g,
  splitPipe: / \|/,
  slashPipe: /\\\|/g,
  carriageReturn: /\r\n|\r/g,
  spaceLine: /^ +$/gm,
  endingNewline: /\n$/,
};

function pack(re) {
  if (!(re instanceof RegExp)) return { noop: true };
  return { source: re.source, flags: re.flags };
}

function packSet(obj) {
  const out = {};
  for (const [k, v] of Object.entries(obj)) out[k] = pack(v);
  return out;
}

const rules = Lexer.rules;
const doc = {
  block: {
    normal: packSet(rules.block.normal),
    gfm: packSet(rules.block.gfm),
    pedantic: packSet(rules.block.pedantic),
  },
  inline: {
    normal: packSet(rules.inline.normal),
    gfm: packSet(rules.inline.gfm),
    breaks: packSet(rules.inline.breaks),
    pedantic: packSet(rules.inline.pedantic),
  },
  other: packSet(other),
};

writeFileSync(new URL("../rules.json", import.meta.url), JSON.stringify(doc));
console.log("wrote rules.json");
