package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

const (
	eqjmNsVirtual  = "pi-virtual"
	eqjmNsNodeShim = "node-shim"
)

func eqjmEsbuildPlugin() api.Plugin {
	return api.Plugin{
		Name: "pi-go-virtual-modules",
		Setup: func(build api.PluginBuild) {
			build.OnResolve(api.OnResolveOptions{Filter: `.*`}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
				if target, ok := eqjmResolveVirtualSpecifier(args.Path); ok {
					return api.OnResolveResult{
						Path:      target,
						Namespace: eqjmNsVirtual,
					}, nil
				}
				if target, ok := eqjmResolveNodeShim(args.Path); ok {
					return api.OnResolveResult{
						Path:      target,
						Namespace: eqjmNsNodeShim,
					}, nil
				}
				if (strings.HasPrefix(args.Path, "./") || strings.HasPrefix(args.Path, "../")) && args.ResolveDir != "" {
					if resolved, ok := eqjmResolveTSExtensionFallback(args.ResolveDir, args.Path); ok {
						return api.OnResolveResult{Path: resolved}, nil
					}
				}
				return api.OnResolveResult{}, nil
			})

			build.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: eqjmNsVirtual}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				code, ok := eqjmVirtualModuleSource(args.Path)
				if !ok {
					return api.OnLoadResult{}, fmt.Errorf("unknown virtual module %q", args.Path)
				}
				return api.OnLoadResult{
					Contents: &code,
					Loader:   api.LoaderJS,
				}, nil
			})

			build.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: eqjmNsNodeShim}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				code, ok := eqjmNodeShimSource(args.Path)
				if !ok {
					return api.OnLoadResult{}, fmt.Errorf("unsupported node builtin module %q in QuickJS extension runtime", args.Path)
				}
				return api.OnLoadResult{
					Contents: &code,
					Loader:   api.LoaderJS,
				}, nil
			})
		},
	}
}

func eqjmResolveTSExtensionFallback(resolveDir, relPath string) (string, bool) {
	joined := filepath.Join(resolveDir, relPath)
	if info, err := os.Stat(joined); err == nil && !info.IsDir() {
		return "", false
	}
	ext := strings.ToLower(filepath.Ext(relPath))
	var replacements []string
	switch ext {
	case ".js":
		replacements = []string{".ts", ".tsx", ".mts", ".cts", ".jsx"}
	case ".mjs":
		replacements = []string{".mts", ".ts"}
	case ".cjs":
		replacements = []string{".cts", ".ts"}
	default:
		return "", false
	}
	stem := strings.TrimSuffix(joined, filepath.Ext(joined))
	for _, repl := range replacements {
		cand := stem + repl
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand, true
		}
	}
	return "", false
}

func eqjmResolveVirtualSpecifier(spec string) (string, bool) {
	switch spec {
	case "typebox", "@sinclair/typebox":
		return "typebox", true
	case "typebox/value", "@sinclair/typebox/value":
		return "typebox/value", true
	case "typebox/compile", "@sinclair/typebox/compile":
		return "typebox/compile", true
	case "@earendil-works/pi-agent-core", "@mariozechner/pi-agent-core":
		return "pi-agent-core", true
	case "@earendil-works/pi-tui", "@mariozechner/pi-tui":
		return "pi-tui", true
	case "@earendil-works/pi-ai", "@mariozechner/pi-ai",
		"@earendil-works/pi-ai/compat", "@mariozechner/pi-ai/compat":
		return "pi-ai", true
	case "@earendil-works/pi-ai/models", "@mariozechner/pi-ai/models":
		return "pi-ai/models", true
	case "@earendil-works/pi-ai/oauth", "@mariozechner/pi-ai/oauth":
		return "pi-ai/oauth", true
	case "@earendil-works/pi-ai/providers/all", "@mariozechner/pi-ai/providers/all":
		return "pi-ai/providers/all", true
	case "@earendil-works/pi-coding-agent", "@mariozechner/pi-coding-agent":
		return "pi-coding-agent", true
	default:
		return "", false
	}
}

func eqjmResolveNodeShim(spec string) (string, bool) {
	trimmed := strings.TrimPrefix(spec, "node:")
	switch trimmed {
	case "path", "path/posix", "path/win32",
		"os",
		"fs", "fs/promises",
		"url",
		"crypto",
		"events",
		"buffer",
		"process",
		"util",
		"assert", "assert/strict",
		"stream",
		"module",
		"tty",
		"string_decoder",
		"querystring":
		return trimmed, true
	default:
		return "", false
	}
}

func eqjmVirtualModuleSource(name string) (string, bool) {
	switch name {
	case "typebox":
		return eqjmTypeboxSource, true
	case "typebox/value":
		return eqjmTypeboxValueSource, true
	case "typebox/compile":
		return eqjmTypeboxCompileSource, true
	case "pi-agent-core":
		return eqjmPiAgentCoreSource, true
	case "pi-tui":
		return eqjmPiTuiSource, true
	case "pi-ai":
		return eqjmPiAiSource, true
	case "pi-ai/models":
		return eqjmPiAiModelsSource, true
	case "pi-ai/oauth":
		return eqjmPiAiOauthSource, true
	case "pi-ai/providers/all":
		return eqjmPiAiProvidersAllSource, true
	case "pi-coding-agent":
		return eqjmPiCodingAgentSource, true
	default:
		return "", false
	}
}

func eqjmNodeShimSource(name string) (string, bool) {
	switch name {
	case "path", "path/posix", "path/win32":
		return `const p = globalThis.__piShims.path;
export const { sep, delimiter, join, resolve, dirname, basename, extname, normalize, isAbsolute, relative, parse, format, posix, win32 } = p;
export default p;`, true
	case "os":
		return `const o = globalThis.__piShims.os;
export const { homedir, tmpdir, platform, arch, hostname, cpus, EOL, type, release, userInfo } = o;
export default o;`, true
	case "fs":
		return `const f = globalThis.__piShims.fs;
export const { constants, existsSync, readFileSync, writeFileSync, appendFileSync, mkdirSync, readdirSync, statSync, lstatSync, unlinkSync, rmSync, rmdirSync, renameSync, copyFileSync, realpathSync, chmodSync, accessSync, watch, promises } = f;
export default f;`, true
	case "fs/promises":
		return `const fp = globalThis.__piShims.fs.promises;
export const { readFile, writeFile, appendFile, mkdir, readdir, stat, lstat, unlink, rm, rmdir, rename, copyFile, realpath, access, chmod, constants } = fp;
export default fp;`, true
	case "url":
		return `const u = globalThis.__piShims.url;
export const { fileURLToPath, pathToFileURL, URL, URLSearchParams } = u;
export default u;`, true
	case "crypto":
		return `const c = globalThis.__piShims.crypto;
export const { randomBytes, randomUUID, randomInt, createHash, createHmac, webcrypto } = c;
export default c;`, true
	case "events":
		return `const e = globalThis.__piShims.events;
export const { EventEmitter } = e;
export default e.EventEmitter;`, true
	case "buffer":
		return `const b = globalThis.__piShims.buffer;
export const { Buffer, kMaxLength } = b;
export default b;`, true
	case "process":
		return `const proc = globalThis.process;
export const { env, argv, cwd, chdir, platform, arch, version, versions, pid, nextTick, stdout, stderr, stdin, exit } = proc;
export default proc;`, true
	case "util":
		return `const u = globalThis.__piShims.util;
export const { promisify, format, inspect, inherits, types, TextEncoder, TextDecoder } = u;
export default u;`, true
	case "assert", "assert/strict":
		return `const a = globalThis.__piShims.assert;
export const { ok, strictEqual, notStrictEqual, deepStrictEqual, equal, notEqual, fail, throws, strict } = a;
export default a;`, true
	case "stream":
		return `const s = globalThis.__piShims.stream;
export const { EventEmitter, Readable, Writable, Transform, PassThrough, Stream } = s;
export default s;`, true
	case "module":
		return `const m = globalThis.__piShims.module;
export const { createRequire, builtinModules } = m;
export default m;`, true
	case "tty":
		return `export const isatty = () => false;
export default { isatty };`, true
	case "string_decoder":
		return `export class StringDecoder {
  constructor(enc = "utf8") { this.encoding = enc; this._dec = new TextDecoder(); }
  write(buf) { return typeof buf === "string" ? buf : this._dec.decode(buf, { stream: true }); }
  end(buf) { return buf ? (typeof buf === "string" ? buf : this._dec.decode(buf)) : this._dec.decode(); }
}
export default { StringDecoder };`, true
	case "querystring":
		return `export function parse(str) {
  const out = {};
  for (const [k, v] of new URLSearchParams(String(str || ""))) {
    if (k in out) {
      out[k] = Array.isArray(out[k]) ? [...out[k], v] : [out[k], v];
    } else {
      out[k] = v;
    }
  }
  return out;
}
export function stringify(obj) {
  const p = new URLSearchParams();
  if (obj && typeof obj === "object") {
    for (const [k, v] of Object.entries(obj)) {
      if (Array.isArray(v)) { for (const x of v) p.append(k, String(x)); }
      else if (v !== undefined) p.append(k, String(v));
    }
  }
  return p.toString();
}
export default { parse, stringify };`, true
	default:
		return "", false
	}
}

const eqjmTypeboxSource = `
export const Kind = Symbol.for("TypeBox.Kind");
export const OptionalKind = Symbol.for("TypeBox.Optional");
export const ReadonlyKind = Symbol.for("TypeBox.Readonly");

function cloneWith(base, opts, kind) {
  const out = {};
  if (base && typeof base === "object") {
    for (const k of Object.keys(base)) out[k] = base[k];
  }
  if (opts && typeof opts === "object") {
    for (const k of Object.keys(opts)) {
      if (opts[k] !== undefined) out[k] = opts[k];
    }
  }
  if (kind !== undefined) {
    Object.defineProperty(out, Kind, { value: kind, enumerable: false, configurable: true });
  }
  return out;
}

function copyFlags(dst, src) {
  if (src && src[OptionalKind]) {
    Object.defineProperty(dst, OptionalKind, { value: "Optional", enumerable: false, configurable: true });
  }
  if (src && src[ReadonlyKind]) {
    Object.defineProperty(dst, ReadonlyKind, { value: "Readonly", enumerable: false, configurable: true });
  }
  return dst;
}

export const Type = {
  Object(properties = {}, options) {
    const props = {};
    const required = [];
    for (const key of Object.keys(properties || {})) {
      const schema = properties[key];
      props[key] = schema;
      if (schema && !schema[OptionalKind]) {
        required.push(key);
      }
    }
    const res = cloneWith({ type: "object", properties: props }, options, "Object");
    if (required.length > 0) {
      res.required = required;
    }
    return res;
  },
  String(options) {
    return cloneWith({ type: "string" }, options, "String");
  },
  Number(options) {
    return cloneWith({ type: "number" }, options, "Number");
  },
  Integer(options) {
    return cloneWith({ type: "integer" }, options, "Integer");
  },
  Boolean(options) {
    return cloneWith({ type: "boolean" }, options, "Boolean");
  },
  Null(options) {
    return cloneWith({ type: "null" }, options, "Null");
  },
  Undefined(options) {
    return cloneWith({}, options, "Undefined");
  },
  Any(options) {
    return cloneWith({}, options, "Any");
  },
  Unknown(options) {
    return cloneWith({}, options, "Unknown");
  },
  Never(options) {
    return cloneWith({ not: {} }, options, "Never");
  },
  Void(options) {
    return cloneWith({}, options, "Void");
  },
  Array(items, options) {
    return cloneWith({ type: "array", items }, options, "Array");
  },
  Tuple(items = [], options) {
    return cloneWith({ type: "array", items: [...items], minItems: items.length, maxItems: items.length }, options, "Tuple");
  },
  Optional(schema) {
    const cloned = cloneWith(schema, undefined, schema && schema[Kind]);
    if (schema && schema[ReadonlyKind]) {
      Object.defineProperty(cloned, ReadonlyKind, { value: "Readonly", enumerable: false, configurable: true });
    }
    Object.defineProperty(cloned, OptionalKind, { value: "Optional", enumerable: false, configurable: true });
    return cloned;
  },
  Readonly(schema) {
    const cloned = cloneWith(schema, undefined, schema && schema[Kind]);
    copyFlags(cloned, schema);
    Object.defineProperty(cloned, ReadonlyKind, { value: "Readonly", enumerable: false, configurable: true });
    return cloned;
  },
  ReadonlyOptional(schema) {
    return Type.Readonly(Type.Optional(schema));
  },
  Literal(value, options) {
    return cloneWith({ const: value, type: typeof value === "boolean" ? "boolean" : typeof value === "number" ? "number" : typeof value === "string" ? "string" : undefined }, options, "Literal");
  },
  Union(anyOf = [], options) {
    return cloneWith({ anyOf: [...anyOf] }, options, "Union");
  },
  Intersect(allOf = [], options) {
    return cloneWith({ allOf: [...allOf] }, options, "Intersect");
  },
  Enum(item, options) {
    const vals = [];
    if (item && typeof item === "object") {
      for (const k of Object.keys(item)) {
        if (typeof item[item[k]] === "string" && typeof item[k] === "number") {
          vals.push(item[k]);
        } else if (typeof item[k] === "string" && !(Number(k) >= 0)) {
          vals.push(item[k]);
        }
      }
      if (vals.length === 0) {
        for (const v of Object.values(item)) vals.push(v);
      }
    }
    return cloneWith({ anyOf: vals.map((v) => Type.Literal(v)) }, options, "Union");
  },
  Record(keySchema, valueSchema, options) {
    const pat = (keySchema && keySchema.pattern) ? keySchema.pattern : "^.*$";
    return cloneWith({ type: "object", patternProperties: { [pat]: valueSchema }, additionalProperties: valueSchema }, options, "Record");
  },
  Ref(ref, options) {
    return cloneWith({ $ref: typeof ref === "string" ? ref : (ref && ref.$id) || "#" }, options, "Ref");
  },
  Unsafe(raw) {
    return cloneWith(raw, undefined, "Unsafe");
  },
  Partial(schema, options) {
    if (!schema || !schema.properties) return cloneWith(schema, options, "Object");
    const cloned = cloneWith(schema, options, "Object");
    delete cloned.required;
    return cloned;
  },
  Required(schema, options) {
    if (!schema || !schema.properties) return cloneWith(schema, options, "Object");
    const cloned = cloneWith(schema, options, "Object");
    cloned.required = Object.keys(schema.properties);
    return cloned;
  },
  Pick(schema, keys, options) {
    const keyList = Array.isArray(keys) ? keys : (keys && keys.anyOf ? keys.anyOf.map((x) => x.const) : []);
    const props = {};
    for (const k of keyList) {
      if (schema && schema.properties && k in schema.properties) {
        props[k] = schema.properties[k];
      }
    }
    return Type.Object(props, options);
  },
  Omit(schema, keys, options) {
    const skip = new Set(Array.isArray(keys) ? keys : (keys && keys.anyOf ? keys.anyOf.map((x) => x.const) : []));
    const props = {};
    if (schema && schema.properties) {
      for (const k of Object.keys(schema.properties)) {
        if (!skip.has(k)) props[k] = schema.properties[k];
      }
    }
    return Type.Object(props, options);
  },
  RegExp(re, options) {
    return cloneWith({ type: "string", pattern: re instanceof RegExp ? re.source : String(re) }, options, "RegExp");
  },
  Date(options) {
    return cloneWith({ type: "string", format: "date-time" }, options, "Date");
  },
  Function(_params, _ret, options) {
    return cloneWith({}, options, "Function");
  },
  Promise(_item, options) {
    return cloneWith({}, options, "Promise");
  },
  KeyOf(schema, options) {
    const keys = schema && schema.properties ? Object.keys(schema.properties) : [];
    return Type.Union(keys.map((k) => Type.Literal(k)), options);
  },
};

export const TypeRegistry = {
  _map: new Map(),
  Has(kind) { return this._map.has(kind); },
  Set(kind, fn) { this._map.set(kind, fn); },
  Get(kind) { return this._map.get(kind); },
  Clear() { this._map.clear(); },
};

export const FormatRegistry = {
  _map: new Map(),
  Has(format) { return this._map.has(format); },
  Set(format, fn) { this._map.set(format, fn); },
  Get(format) { return this._map.get(format); },
  Clear() { this._map.clear(); },
};

export const TypeGuard = {
  IsObject: (s) => Boolean(s && typeof s === "object" && s.type === "object" && s.properties),
  IsArray: (s) => Boolean(s && typeof s === "object" && s.type === "array"),
  IsString: (s) => Boolean(s && typeof s === "object" && s.type === "string"),
  IsNumber: (s) => Boolean(s && typeof s === "object" && s.type === "number"),
  IsInteger: (s) => Boolean(s && typeof s === "object" && s.type === "integer"),
  IsBoolean: (s) => Boolean(s && typeof s === "object" && s.type === "boolean"),
  IsOptional: (s) => Boolean(s && s[OptionalKind]),
  IsUnion: (s) => Boolean(s && Array.isArray(s.anyOf)),
  IsLiteral: (s) => Boolean(s && Object.prototype.hasOwnProperty.call(s, "const")),
};

export default Type;
`

const eqjmTypeboxValueSource = `
export const Value = {
  Check(schema, value) {
    return globalThis.__piHost.typebox.check(schema, value);
  },
  Errors(schema, value) {
    return globalThis.__piHost.typebox.errors(schema, value);
  },
  Convert(schema, value) {
    return globalThis.__piHost.typebox.convert(schema, value);
  },
  Cast(schema, value) {
    return globalThis.__piHost.typebox.convert(schema, value);
  },
  Clone(value) {
    return value === undefined ? undefined : JSON.parse(JSON.stringify(value));
  },
  Clean(schema, value) {
    return value;
  },
  Default(schema, value) {
    return value !== undefined ? value : (schema && "default" in schema ? schema.default : value);
  },
  Equal(a, b) {
    return JSON.stringify(a) === JSON.stringify(b);
  },
};
export default Value;
`

const eqjmTypeboxCompileSource = `
import { Value } from "typebox/value";
export const TypeCompiler = {
  Compile(schema) {
    return {
      Check(value) { return Value.Check(schema, value); },
      Errors(value) { return Value.Errors(schema, value); },
      Decode(value) {
        const converted = Value.Convert(schema, value);
        if (!Value.Check(schema, converted)) {
          const errs = Value.Errors(schema, converted);
          const msg = errs && errs.length > 0 ? errs[0].message : "Type validation failed";
          throw new Error(msg);
        }
        return converted;
      },
      Encode(value) { return value; },
    };
  },
};
export default TypeCompiler;
`

const eqjmPiAgentCoreSource = `
export default {};
`

const eqjmPiTuiSource = `
export const CURSOR_MARKER = "\x1b_pi:c\x07";
export function renderFakeCursor(text = " ") {
  return "\x1b[7m" + (text || " ") + "\x1b[27m";
}

export const Key = {
  up: "up",
  down: "down",
  left: "left",
  right: "right",
  enter: "enter",
  return: "return",
  escape: "escape",
  esc: "escape",
  tab: "tab",
  backspace: "backspace",
  delete: "delete",
  home: "home",
  end: "end",
  pageUp: "pageUp",
  pageDown: "pageDown",
  space: "space",
  ctrl: (k) => "ctrl+" + k,
  alt: (k) => "alt+" + k,
  shift: (k) => "shift+" + k,
  ctrlShift: (k) => "ctrl+shift+" + k,
  shiftCtrl: (k) => "ctrl+shift+" + k,
  ctrlAlt: (k) => "ctrl+alt+" + k,
};

export function visibleWidth(str) {
  return globalThis.__piHost.tui.visibleWidth(String(str ?? ""));
}

export function truncateToWidth(str, maxWidth, ellipsis = "...", pad = false) {
  return globalThis.__piHost.tui.truncateToWidth(String(str ?? ""), Number(maxWidth) || 0, String(ellipsis ?? "..."), Boolean(pad));
}

export function wrapTextWithAnsi(str, width) {
  return globalThis.__piHost.tui.wrapTextWithAnsi(String(str ?? ""), Number(width) || 80);
}

export function parseKey(data) {
  return globalThis.__piHost.tui.parseKey(String(data ?? ""));
}

export function matchesKey(data, key) {
  return globalThis.__piHost.tui.matchesKey(String(data ?? ""), String(key ?? ""));
}

export class Text {
  constructor(text = "", paddingX = 1, paddingY = 1, customBgFn = undefined) {
    this.text = String(text ?? "");
    this.paddingX = paddingX;
    this.paddingY = paddingY;
    this.customBgFn = customBgFn;
  }
  setText(t) { this.text = String(t ?? ""); }
  invalidate() {}
  render(width) {
    const raw = this.text === "" ? [] : wrapTextWithAnsi(this.text, Math.max(1, (width || 80) - this.paddingX * 2));
    const pad = " ".repeat(Math.max(0, this.paddingX));
    const lines = raw.map((l) => pad + l);
    const empty = [];
    for (let i = 0; i < (this.paddingY || 0); i++) empty.push("");
    return [...empty, ...lines, ...empty];
  }
}

export class TruncatedText {
  constructor(text = "", paddingX = 0, paddingY = 0) {
    this.text = String(text ?? "");
    this.paddingX = paddingX;
    this.paddingY = paddingY;
  }
  invalidate() {}
  render(width) {
    const line = truncateToWidth(this.text.replace(/\r?\n/g, " "), Math.max(1, (width || 80) - this.paddingX * 2));
    const pad = " ".repeat(Math.max(0, this.paddingX));
    const empty = Array(Math.max(0, this.paddingY || 0)).fill("");
    return [...empty, pad + line, ...empty];
  }
}

export class Spacer {
  constructor(lines = 1) { this.lines = lines; }
  invalidate() {}
  render() { return Array(Math.max(0, this.lines)).fill(""); }
}

export class Container {
  constructor() { this.children = []; }
  addChild(c) { if (c) this.children.push(c); }
  removeChild(c) { this.children = this.children.filter((x) => x !== c); }
  clear() { this.children = []; }
  invalidate() { for (const c of this.children) { if (c && c.invalidate) c.invalidate(); } }
  render(width) {
    const lines = [];
    for (const c of this.children) {
      if (c && c.render) lines.push(...c.render(width));
    }
    return lines;
  }
}

export class Box extends Container {
  constructor(paddingX = 1, paddingY = 1, bgFn = undefined) {
    super();
    this.paddingX = paddingX;
    this.paddingY = paddingY;
    this.bgFn = bgFn;
  }
  setBgFn(fn) { this.bgFn = fn; }
  render(width) {
    const innerWidth = Math.max(1, (width || 80) - this.paddingX * 2);
    const pad = " ".repeat(Math.max(0, this.paddingX));
    const out = [];
    for (let i = 0; i < (this.paddingY || 0); i++) out.push("");
    for (const c of this.children) {
      if (c && c.render) {
        for (const l of c.render(innerWidth)) {
          out.push(this.bgFn ? this.bgFn(pad + l) : pad + l);
        }
      }
    }
    for (let i = 0; i < (this.paddingY || 0); i++) out.push("");
    return out;
  }
}

export class Markdown {
  constructor(text = "", paddingX = 1, paddingY = 0, theme = undefined) {
    this.text = String(text ?? "");
    this.paddingX = paddingX;
    this.paddingY = paddingY;
    this.theme = theme;
  }
  setText(t) { this.text = String(t ?? ""); }
  invalidate() {}
  render(width) { return new Text(this.text, this.paddingX, this.paddingY).render(width); }
}

export class Input {
  constructor() { this.value = ""; this.focused = false; this.onSubmit = undefined; this.onEscape = undefined; }
  setValue(v) { this.value = String(v ?? ""); }
  getValue() { return this.value; }
  invalidate() {}
  handleInput(data) {
    if (data === "\r" || data === "\n") { this.onSubmit && this.onSubmit(this.value); return; }
    if (data === "\x1b") { this.onEscape && this.onEscape(); return; }
    if (data === "\x7f" || data === "\b") { this.value = this.value.slice(0, -1); return; }
    if (data && !data.startsWith("\x1b")) this.value += data;
  }
  render(width) { return [truncateToWidth("> " + this.value, width || 80)]; }
}

export class Editor extends Input {}

export class SelectList {
  constructor(items = [], maxVisible = 10) {
    this.items = items || [];
    this.maxVisible = maxVisible;
    this.selectedIndex = 0;
    this.onSelect = undefined;
    this.onCancel = undefined;
  }
  getSelectedItem() { return this.items[this.selectedIndex]; }
  invalidate() {}
  handleInput(data) {
    if (matchesKey(data, Key.up) && this.selectedIndex > 0) this.selectedIndex--;
    else if (matchesKey(data, Key.down) && this.selectedIndex + 1 < this.items.length) this.selectedIndex++;
    else if (matchesKey(data, Key.enter) && this.items[this.selectedIndex]) this.onSelect && this.onSelect(this.items[this.selectedIndex]);
    else if (matchesKey(data, Key.escape)) this.onCancel && this.onCancel();
  }
  render(width) {
    return this.items.slice(0, this.maxVisible).map((it, i) => {
      const prefix = i === this.selectedIndex ? "> " : "  ";
      const label = typeof it === "string" ? it : (it.label || it.value || "");
      return truncateToWidth(prefix + label, width || 80);
    });
  }
}

export class SettingsList extends SelectList {
  constructor(items = [], maxVisible = 10, theme = {}, onChange = undefined, onCancel = undefined) {
    super(items, maxVisible);
    this.theme = theme || {};
    this.onChange = onChange;
    this.onCancel = onCancel;
  }
  handleInput(data) {
    if (matchesKey(data, Key.up) && this.selectedIndex > 0) this.selectedIndex--;
    else if (matchesKey(data, Key.down) && this.selectedIndex + 1 < this.items.length) this.selectedIndex++;
    else if ((matchesKey(data, Key.enter) || data === " ") && this.items[this.selectedIndex]) {
      const item = this.items[this.selectedIndex];
      if (Array.isArray(item.values) && item.values.length > 0) {
        const cur = item.values.indexOf(item.currentValue);
        const nextVal = item.values[(cur + 1) % item.values.length];
        item.currentValue = nextVal;
        if (this.onChange) this.onChange(item.id, nextVal);
      }
    } else if (matchesKey(data, Key.escape) || data === "escape") {
      if (this.onCancel) this.onCancel();
    }
  }
  render(width) {
    return this.items.slice(0, this.maxVisible).map((it, i) => {
      const prefix = i === this.selectedIndex ? "> " : "  ";
      const val = it.currentValue !== undefined ? " [" + it.currentValue + "]" : "";
      return truncateToWidth(prefix + (it.label || it.id || "") + val, width || 80);
    });
  }
}

export class Loader {
  constructor(_tui, _spinnerColorFn, _messageColorFn, message = "") { this.message = message; }
  setMessage(m) { this.message = m; }
  start() {}
  stop() {}
  invalidate() {}
  render(width) { return [truncateToWidth(this.message || "Loading...", width || 80)]; }
}

export class CancellableLoader extends Loader {
  constructor(tui, sc, mc, message = "") {
    super(tui, sc, mc, message);
    this._ctrl = new AbortController();
    this.signal = this._ctrl.signal;
    this.onAbort = undefined;
  }
  dispose() { this.stop(); }
  handleInput(data) {
    if (matchesKey(data, Key.escape)) {
      this._ctrl.abort();
      if (this.onAbort) this.onAbort();
    }
  }
}

export default {
  CURSOR_MARKER, renderFakeCursor, Key, visibleWidth, truncateToWidth, wrapTextWithAnsi, parseKey, matchesKey,
  Text, TruncatedText, Spacer, Container, Box, Markdown, Input, Editor, SelectList, SettingsList, Loader, CancellableLoader,
};
`

const eqjmPiAiSource = `
import { Type } from "typebox";
export { Type };

export function StringEnum(values, options) {
  const schema = {
    type: "string",
    enum: Array.from(values || []),
  };
  if (options && options.description) schema.description = options.description;
  if (options && options.default !== undefined) schema.default = options.default;
  return Type.Unsafe(schema);
}

export function uuidv7() {
  return globalThis.__piHost.crypto.uuidv7();
}

export class AssistantMessageEventStream {
  constructor() {
    this._queue = [];
    this._waiters = [];
    this._ended = false;
    this._finalResult = undefined;
    this._resultWaiters = [];
  }
  push(event) {
    if (this._ended) return;
    if (event && event.type === "done") this._finalResult = event.message;
    if (event && event.type === "error") this._finalResult = event.error;
    if (this._waiters.length > 0) {
      const resolve = this._waiters.shift();
      resolve({ value: event, done: false });
    } else {
      this._queue.push(event);
    }
    if (event && (event.type === "done" || event.type === "error")) {
      this.end(this._finalResult);
    }
  }
  end(result) {
    if (this._ended) return;
    this._ended = true;
    if (result !== undefined) this._finalResult = result;
    while (this._waiters.length > 0) {
      const resolve = this._waiters.shift();
      resolve({ value: undefined, done: true });
    }
    while (this._resultWaiters.length > 0) {
      const resolve = this._resultWaiters.shift();
      resolve(this._finalResult);
    }
  }
  result() {
    if (this._ended) return Promise.resolve(this._finalResult);
    return new Promise((resolve) => this._resultWaiters.push(resolve));
  }
  [Symbol.asyncIterator]() {
    const self = this;
    return {
      next() {
        if (self._queue.length > 0) {
          return Promise.resolve({ value: self._queue.shift(), done: false });
        }
        if (self._ended) {
          return Promise.resolve({ value: undefined, done: true });
        }
        return new Promise((resolve) => self._waiters.push(resolve));
      },
    };
  }
}

export function createAssistantMessageEventStream() {
  return new AssistantMessageEventStream();
}

function __piAiContentText(content) {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  const parts = [];
  for (const part of content) {
    if (part && part.type === "text" && typeof part.text === "string") {
      parts.push(part.text);
    }
  }
  return parts.join("");
}

function __piAiSystemMessageText(message) {
  if (!message) return "";
  const parts = [];
  const base = __piAiContentText(message.content);
  if (base) parts.push(base);
  if (message.sections && typeof message.sections === "object") {
    for (const value of Object.values(message.sections)) {
      if (typeof value === "string" && value.length > 0) parts.push(value);
    }
  }
  return parts.join("\n\n");
}

export function createInitialSystemMessage(systemPrompt, tools) {
  const hasSystemPrompt = systemPrompt !== undefined && systemPrompt.length > 0;
  const hasTools = Array.isArray(tools) && tools.length > 0;
  if (!hasSystemPrompt && !hasTools) return undefined;
  return {
    role: "system",
    content: systemPrompt ?? "",
    ...(hasTools ? { toolsAdded: tools } : {}),
    timestamp: 0,
  };
}

export function normalizeContext(context) {
  const ctx = context || { messages: [] };
  const initialMessage = createInitialSystemMessage(ctx.systemPrompt, ctx.tools);
  const base = Array.isArray(ctx.messages) ? ctx.messages : [];
  return { messages: initialMessage ? [initialMessage, ...base] : base };
}

export function getInitialSystemMessage(messages) {
  const first = Array.isArray(messages) ? messages[0] : undefined;
  return first && first.role === "system" ? first : undefined;
}

export function withoutInitialSystemMessage(messages) {
  if (!Array.isArray(messages)) return [];
  return getInitialSystemMessage(messages) ? messages.slice(1) : messages;
}

export function getCurrentTools(messages) {
  const tools = new Map();
  if (!Array.isArray(messages)) return [];
  for (const message of messages) {
    if (!message || message.role !== "system") continue;
    for (const tool of message.toolsRemoved ?? []) {
      if (tool && tool.name) tools.delete(tool.name);
    }
    for (const tool of message.toolsAdded ?? []) {
      if (tool && tool.name) tools.set(tool.name, tool);
    }
  }
  return Array.from(tools.values());
}

export function getCurrentSystemMessage(messages) {
  if (!Array.isArray(messages)) return undefined;
  const content = [];
  const sections = new Map();
  let timestamp;
  for (const message of messages) {
    if (!message || message.role !== "system") continue;
    if (timestamp === undefined) timestamp = message.timestamp;
    const text = __piAiContentText(message.content);
    if (text.length > 0) content.push(text);
    if (message.sections && typeof message.sections === "object") {
      for (const [name, value] of Object.entries(message.sections)) {
        if (value === null) sections.delete(name);
        else sections.set(name, value);
      }
    }
  }
  const tools = getCurrentTools(messages);
  if (timestamp === undefined && tools.length === 0) return undefined;
  return {
    role: "system",
    content: content.join("\n\n"),
    ...(sections.size > 0 ? { sections: Object.fromEntries(sections) } : {}),
    ...(tools.length > 0 ? { toolsAdded: tools } : {}),
    timestamp: timestamp ?? 0,
  };
}

export function getCurrentSystemPrompt(messages) {
  const message = getCurrentSystemMessage(messages);
  return message ? __piAiSystemMessageText(message) : "";
}

export function collapseSystemMessages(context) {
  const list = Array.isArray(context?.messages) ? context.messages : [];
  const head = getCurrentSystemMessage(list);
  const messages = list.filter((message) => !message || message.role !== "system");
  return { messages: head ? [head, ...messages] : messages };
}

export function resolveTranscript(context, supportsMidConvoSystemMessages) {
  return supportsMidConvoSystemMessages ? context : collapseSystemMessages(context);
}

export function toToolDeclaration(tool) {
  return {
    name: tool.name,
    description: tool.description,
    parameters: JSON.parse(JSON.stringify(tool.parameters ?? {})),
    ...(tool.constrainedSampling === undefined ? {} : { constrainedSampling: tool.constrainedSampling }),
  };
}

export function parseStreamingJson(raw) {
  if (!raw || typeof raw !== "string") return {};
  try { return JSON.parse(raw); } catch { return {}; }
}

export function calculateCost(_model, usage) {
  return usage && usage.cost ? usage.cost : { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 };
}

export function modelsAreEqual(a, b) {
  return Boolean(a && b && a.provider === b.provider && a.id === b.id);
}

export function getEnvApiKey(provider) {
  return globalThis.__piHost.ai.getEnvApiKey(String(provider ?? ""));
}

export function complete() {
  return Promise.reject(new Error("pi-ai complete() requires host model client; use ctx.modelRegistry or streamSimple inside custom provider"));
}

export function completeSimple() {
  return complete();
}

export function stream() {
  const s = new AssistantMessageEventStream();
  s.end({ role: "assistant", content: [], stopReason: "error", errorMessage: "pi-ai stream() is not wired outside host model registry", timestamp: Date.now() });
  return s;
}

export function streamSimple() {
  return stream();
}

export function getOAuthApiKey(providerId, creds) {
  if (creds && creds[providerId] && creds[providerId].access) return Promise.resolve({ apiKey: creds[providerId].access, newCredentials: creds[providerId] });
  return Promise.resolve(null);
}

export default {
  Type, StringEnum, uuidv7, AssistantMessageEventStream, createAssistantMessageEventStream,
  getCurrentSystemMessage, parseStreamingJson, calculateCost, modelsAreEqual, getEnvApiKey,
  complete, completeSimple, stream, streamSimple, getOAuthApiKey,
};
`

const eqjmPiAiModelsSource = `
export function getModels(provider) {
  return globalThis.__piHost.ai.getModels(provider ? String(provider) : "");
}
export function getModel(provider, id) {
  return globalThis.__piHost.ai.getModel(String(provider ?? ""), String(id ?? ""));
}
export function getProviders() {
  return globalThis.__piHost.ai.getProviders();
}
export default { getModels, getModel, getProviders };
`

const eqjmPiAiOauthSource = `
export function loginAnthropic() { return Promise.reject(new Error("OAuth login must run in host UI")); }
export function loginOpenAICodex() { return Promise.reject(new Error("OAuth login must run in host UI")); }
export default { loginAnthropic, loginOpenAICodex };
`

const eqjmPiAiProvidersAllSource = `
export function registerBuiltInApiProviders() {}
export default { registerBuiltInApiProviders };
`

const eqjmPiCodingAgentSource = `
import { Text } from "@earendil-works/pi-tui";

export const VERSION = globalThis.__piHost.constants.version;
export const APP_NAME = globalThis.__piHost.constants.appName;
export const CONFIG_DIR_NAME = globalThis.__piHost.constants.configDirName;
export const DEFAULT_MAX_BYTES = 50 * 1024;
export const DEFAULT_MAX_LINES = 2000;

export function getAgentDir() {
  return globalThis.__piHost.constants.agentDir;
}
export function getModelsPath() {
  return globalThis.__piHost.constants.modelsPath;
}
export function getSettingsPath() {
  return globalThis.__piHost.constants.settingsPath;
}
export function getSessionsDir() {
  return globalThis.__piHost.constants.sessionsDir;
}
export function formatSize(bytes) {
  const b = Number(bytes) || 0;
  if (b < 1024) return b + " B";
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + " KB";
  return (b / (1024 * 1024)).toFixed(1) + " MB";
}
export function parseFrontmatter(content) {
  return globalThis.__piHost.coding.parseFrontmatter(String(content ?? ""));
}
export function defineTool(tool) {
  return tool;
}
export function truncateLine(line, maxWidth = 500) {
  const s = String(line ?? "");
  return s.length <= maxWidth ? { text: s, wasTruncated: false } : { text: s.slice(0, maxWidth), wasTruncated: true };
}
export function truncateHead(content, opts = {}) {
  const maxLines = opts.maxLines ?? DEFAULT_MAX_LINES;
  const maxBytes = opts.maxBytes ?? DEFAULT_MAX_BYTES;
  const text = String(content ?? "");
  const lines = text.split("\n");
  const totalLines = lines.length;
  const totalBytes = new TextEncoder().encode(text).length;
  if (totalLines <= maxLines && totalBytes <= maxBytes) {
    return { content: text, truncated: false, truncatedBy: null, totalLines, totalBytes, outputLines: totalLines, outputBytes: totalBytes, lastLinePartial: false, firstLineExceedsLimit: false, maxLines, maxBytes };
  }
  const kept = [];
  let bytes = 0;
  let truncatedBy = null;
  for (let i = 0; i < lines.length; i++) {
    if (i >= maxLines) { truncatedBy = "lines"; break; }
    const add = (i > 0 ? 1 : 0) + new TextEncoder().encode(lines[i]).length;
    if (bytes + add > maxBytes) { truncatedBy = "bytes"; break; }
    kept.push(lines[i]);
    bytes += add;
  }
  const out = kept.join("\n");
  return { content: out, truncated: true, truncatedBy, totalLines, totalBytes, outputLines: kept.length, outputBytes: bytes, lastLinePartial: false, firstLineExceedsLimit: kept.length === 0 && totalLines > 0, maxLines, maxBytes };
}
export function truncateTail(content, opts = {}) {
  const maxLines = opts.maxLines ?? DEFAULT_MAX_LINES;
  const maxBytes = opts.maxBytes ?? DEFAULT_MAX_BYTES;
  const text = String(content ?? "");
  const lines = text.split("\n");
  const totalLines = lines.length;
  const totalBytes = new TextEncoder().encode(text).length;
  if (totalLines <= maxLines && totalBytes <= maxBytes) {
    return { content: text, truncated: false, truncatedBy: null, totalLines, totalBytes, outputLines: totalLines, outputBytes: totalBytes, lastLinePartial: false, firstLineExceedsLimit: false, maxLines, maxBytes };
  }
  const kept = [];
  let bytes = 0;
  let truncatedBy = null;
  for (let i = lines.length - 1; i >= 0; i--) {
    if (kept.length >= maxLines) { truncatedBy = "lines"; break; }
    const add = (kept.length > 0 ? 1 : 0) + new TextEncoder().encode(lines[i]).length;
    if (bytes + add > maxBytes) { truncatedBy = "bytes"; break; }
    kept.unshift(lines[i]);
    bytes += add;
  }
  const out = kept.join("\n");
  return { content: out, truncated: true, truncatedBy, totalLines, totalBytes, outputLines: kept.length, outputBytes: bytes, lastLinePartial: false, firstLineExceedsLimit: false, maxLines, maxBytes };
}

function makeBuiltinToolStub(name, cwd) {
  return {
    name,
    label: name,
    description: "Built-in " + name + " tool",
    parameters: { type: "object", properties: {} },
    execute(toolCallId, params, signal) {
      return globalThis.__piHost.coding.execBuiltinTool(name, cwd || "", toolCallId || "", params || {});
    },
  };
}
export function createReadTool(cwd) { return makeBuiltinToolStub("read", cwd); }
export function createBashTool(cwd) { return makeBuiltinToolStub("bash", cwd); }
export function createEditTool(cwd) { return makeBuiltinToolStub("edit", cwd); }
export function createWriteTool(cwd) { return makeBuiltinToolStub("write", cwd); }
export function createGrepTool(cwd) { return makeBuiltinToolStub("grep", cwd); }
export function createFindTool(cwd) { return makeBuiltinToolStub("find", cwd); }
export function createLsTool(cwd) { return makeBuiltinToolStub("ls", cwd); }
export function createCodingTools(cwd) { return [createReadTool(cwd), createBashTool(cwd), createEditTool(cwd), createWriteTool(cwd)]; }
export function createReadOnlyTools(cwd) { return [createReadTool(cwd), createGrepTool(cwd), createFindTool(cwd), createLsTool(cwd)]; }

export class DynamicBorder {
  constructor(colorFn = (s) => s) { this.colorFn = colorFn; }
  invalidate() {}
  render(width = 80) { return [this.colorFn("─".repeat(Math.max(1, width)))]; }
}
export class BorderedLoader {
  constructor(_tui, _theme, message = "Loading...") {
    this.message = message;
    this.signal = new AbortController().signal;
    this.onAbort = undefined;
  }
  invalidate() {}
  render(width = 80) { return new Text(this.message).render(width); }
}
export class CustomEditor {
  constructor(tui, theme, keybindings) {
    this.tui = tui;
    this.theme = theme;
    this.keybindings = keybindings;
    this.text = "";
    this.onSubmit = undefined;
  }
  getText() { return this.text; }
  setText(t) { this.text = String(t ?? ""); }
  handleInput(_data) {}
  invalidate() {}
  render(width = 80) { return [this.text.slice(0, width)]; }
}
export class KeybindingsManager {
  static create() { return new KeybindingsManager(); }
  matches(_data, _action) { return false; }
}
export function getMarkdownTheme() { return {}; }
export function getSettingsListTheme() {
  const id = (s) => s;
  return { label: id, value: id, description: id, hint: id };
}
export function highlightCode(code) { return String(code ?? "").split("\n"); }
export function getLanguageFromPath(p) {
  const m = /\.([a-zA-Z0-9]+)$/.exec(String(p || ""));
  return m ? m[1].toLowerCase() : undefined;
}
export function serializeConversation(messages) {
  return JSON.stringify(messages || []);
}
export function convertToLlm(messages) {
  return Array.isArray(messages) ? messages : [];
}

export default {
  VERSION, APP_NAME, CONFIG_DIR_NAME, DEFAULT_MAX_BYTES, DEFAULT_MAX_LINES,
  getAgentDir, getModelsPath, getSettingsPath, getSessionsDir,
  formatSize, parseFrontmatter, defineTool, truncateLine, truncateHead, truncateTail,
  createReadTool, createBashTool, createEditTool, createWriteTool, createGrepTool, createFindTool, createLsTool,
  createCodingTools, createReadOnlyTools,
  DynamicBorder, BorderedLoader, CustomEditor, KeybindingsManager,
  getMarkdownTheme, getSettingsListTheme, highlightCode, getLanguageFromPath, serializeConversation, convertToLlm,
};
`

const eqjmRuntimePreludeJS = `
(function() {
  const host = globalThis.__piHost;

  // --- Utilities ---
  function formatArgs(args) {
    return args.map((a) => {
      if (typeof a === "string") return a;
      if (a instanceof Error) return (a.stack || (a.name + ": " + a.message));
      try { return JSON.stringify(a); } catch { return String(a); }
    }).join(" ");
  }

  globalThis.console = {
    log: (...args) => host.consoleLog("log", formatArgs(args)),
    info: (...args) => host.consoleLog("info", formatArgs(args)),
    warn: (...args) => host.consoleLog("warn", formatArgs(args)),
    error: (...args) => host.consoleLog("error", formatArgs(args)),
    debug: (...args) => host.consoleLog("debug", formatArgs(args)),
  };

  // --- Timers & Microtasks ---
  const timerCbs = new Map();
  let nextTimerToken = 1;
  globalThis.__piFireTimer = function(token) {
    const entry = timerCbs.get(token);
    if (!entry) return;
    if (!entry.interval) {
      timerCbs.delete(token);
    }
    try {
      entry.fn(...entry.args);
    } finally {
      if (entry.interval && timerCbs.has(token)) {
        host.timerSchedule(token, entry.delay, false);
      }
    }
  };
  globalThis.setTimeout = function(fn, delay = 0, ...args) {
    if (typeof fn !== "function") throw new TypeError("setTimeout callback must be a function");
    const token = nextTimerToken++;
    const ms = Math.max(0, Number(delay) || 0);
    timerCbs.set(token, { fn, args, delay: ms, interval: false });
    host.timerSchedule(token, ms, false);
    return token;
  };
  globalThis.clearTimeout = function(token) {
    if (timerCbs.delete(token)) host.timerCancel(token);
  };
  globalThis.setInterval = function(fn, delay = 0, ...args) {
    if (typeof fn !== "function") throw new TypeError("setInterval callback must be a function");
    const token = nextTimerToken++;
    const ms = Math.max(1, Number(delay) || 1);
    timerCbs.set(token, { fn, args, delay: ms, interval: true });
    host.timerSchedule(token, ms, false);
    return token;
  };
  globalThis.clearInterval = function(token) {
    if (timerCbs.delete(token)) host.timerCancel(token);
  };
  if (typeof globalThis.queueMicrotask !== "function") {
    globalThis.queueMicrotask = (fn) => Promise.resolve().then(fn);
  }

  // --- TextEncoder / TextDecoder ---
  class TextEncoder {
    constructor() { this.encoding = "utf-8"; }
    encode(str = "") {
      return new Uint8Array(host.utf8Encode(String(str ?? "")));
    }
    encodeInto(src, dst) {
      const bytes = this.encode(src);
      const n = Math.min(bytes.length, dst.length);
      dst.set(bytes.subarray(0, n));
      return { read: String(src).length, written: n };
    }
  }
  class TextDecoder {
    constructor(label = "utf-8") { this.encoding = String(label || "utf-8").toLowerCase(); }
    decode(input) {
      if (input == null) return "";
      let u8;
      if (input instanceof ArrayBuffer) u8 = new Uint8Array(input);
      else if (ArrayBuffer.isView(input)) u8 = new Uint8Array(input.buffer, input.byteOffset, input.byteLength);
      else u8 = new Uint8Array(input);
      return host.utf8Decode(u8);
    }
  }
  globalThis.TextEncoder = TextEncoder;
  globalThis.TextDecoder = TextDecoder;

  // --- AbortSignal / AbortController ---
  class AbortSignal {
    constructor() {
      this.aborted = false;
      this.reason = undefined;
      this._listeners = [];
      this.onabort = null;
    }
    addEventListener(type, fn, opts) {
      if (type !== "abort" || typeof fn !== "function") return;
      if (this.aborted) { fn({ type: "abort" }); return; }
      this._listeners.push({ fn, once: Boolean(opts && opts.once) });
    }
    removeEventListener(type, fn) {
      if (type !== "abort") return;
      this._listeners = this._listeners.filter((x) => x.fn !== fn);
    }
    throwIfAborted() {
      if (this.aborted) throw this.reason;
    }
    _trigger(reason) {
      if (this.aborted) return;
      this.aborted = true;
      this.reason = reason !== undefined ? reason : new Error("The operation was aborted");
      if (this.reason instanceof Error) this.reason.name = "AbortError";
      if (typeof this.onabort === "function") {
        try { this.onabort({ type: "abort" }); } catch {}
      }
      const list = this._listeners.slice();
      this._listeners = list.filter((x) => !x.once);
      for (const item of list) {
        try { item.fn({ type: "abort" }); } catch {}
      }
    }
    static abort(reason) {
      const s = new AbortSignal();
      s._trigger(reason);
      return s;
    }
    static timeout(ms) {
      const c = new AbortController();
      setTimeout(() => c.abort(new Error("TimeoutError")), ms);
      return c.signal;
    }
    static any(signals = []) {
      const c = new AbortController();
      for (const s of signals) {
        if (!s) continue;
        if (s.aborted) { c.abort(s.reason); return c.signal; }
        s.addEventListener("abort", () => c.abort(s.reason), { once: true });
      }
      return c.signal;
    }
  }
  class AbortController {
    constructor() { this.signal = new AbortSignal(); }
    abort(reason) { this.signal._trigger(reason); }
  }
  globalThis.AbortSignal = AbortSignal;
  globalThis.AbortController = AbortController;

  // --- URLSearchParams / URL ---
  class URLSearchParams {
    constructor(init) {
      this._entries = [];
      if (!init) return;
      if (typeof init === "string") {
        const s = init.startsWith("?") ? init.slice(1) : init;
        if (s) {
          for (const part of s.split("&")) {
            if (!part) continue;
            const eq = part.indexOf("=");
            const k = decodeURIComponent((eq >= 0 ? part.slice(0, eq) : part).replace(/\+/g, " "));
            const v = eq >= 0 ? decodeURIComponent(part.slice(eq + 1).replace(/\+/g, " ")) : "";
            this._entries.push([k, v]);
          }
        }
      } else if (Array.isArray(init)) {
        for (const pair of init) {
          if (Array.isArray(pair) && pair.length >= 2) this._entries.push([String(pair[0]), String(pair[1])]);
        }
      } else if (typeof init === "object") {
        for (const [k, v] of Object.entries(init)) this._entries.push([String(k), String(v)]);
      }
    }
    append(k, v) { this._entries.push([String(k), String(v)]); }
    delete(k) { const ks = String(k); this._entries = this._entries.filter(([ek]) => ek !== ks); }
    get(k) { const ks = String(k); const f = this._entries.find(([ek]) => ek === ks); return f ? f[1] : null; }
    getAll(k) { const ks = String(k); return this._entries.filter(([ek]) => ek === ks).map(([, v]) => v); }
    has(k) { const ks = String(k); return this._entries.some(([ek]) => ek === ks); }
    set(k, v) {
      const ks = String(k), vs = String(v);
      let replaced = false;
      this._entries = this._entries.filter(([ek]) => {
        if (ek !== ks) return true;
        if (!replaced) { replaced = true; return true; }
        return false;
      }).map(([ek, ev]) => (ek === ks ? [ks, vs] : [ek, ev]));
      if (!replaced) this._entries.push([ks, vs]);
    }
    entries() { return this._entries[Symbol.iterator](); }
    keys() { return this._entries.map(([k]) => k)[Symbol.iterator](); }
    values() { return this._entries.map(([, v]) => v)[Symbol.iterator](); }
    forEach(cb, thisArg) { for (const [k, v] of this._entries) cb.call(thisArg, v, k, this); }
    [Symbol.iterator]() { return this.entries(); }
    toString() {
      return this._entries.map(([k, v]) => encodeURIComponent(k) + "=" + encodeURIComponent(v)).join("&");
    }
  }
  class URL {
    constructor(input, base) {
      const p = host.urlParse(String(input), base !== undefined ? String(base) : "");
      this.protocol = p.protocol;
      this.username = p.username;
      this.password = p.password;
      this.hostname = p.hostname;
      this.port = p.port;
      this.host = p.host;
      this.pathname = p.pathname;
      this.search = p.search;
      this.hash = p.hash;
      this.origin = p.origin;
      this.href = p.href;
      this.searchParams = new URLSearchParams(p.search);
    }
    toString() {
      const qs = this.searchParams.toString();
      if (!qs) return this.href.split("?")[0] + (this.hash || "");
      const base = this.href.split("?")[0].split("#")[0];
      return base + "?" + qs + (this.hash || "");
    }
    toJSON() { return this.toString(); }
  }
  globalThis.URLSearchParams = URLSearchParams;
  globalThis.URL = URL;

  // --- Buffer ---
  function toBytesFromInput(input, enc) {
    if (typeof input === "string") return new Uint8Array(host.bufferFromString(input, String(enc || "utf8")));
    if (input instanceof ArrayBuffer) return new Uint8Array(input);
    if (ArrayBuffer.isView(input)) return new Uint8Array(input.buffer, input.byteOffset, input.byteLength);
    if (Array.isArray(input)) return new Uint8Array(input);
    if (input && typeof input === "object" && input.type === "Buffer" && Array.isArray(input.data)) return new Uint8Array(input.data);
    return new Uint8Array(0);
  }
  class Buffer extends Uint8Array {
    static isBuffer(obj) { return obj instanceof Buffer; }
    static from(input, enc) {
      const bytes = toBytesFromInput(input, enc);
      const b = new Buffer(bytes.length);
      b.set(bytes);
      return b;
    }
    static alloc(size, fill = 0, enc = "utf8") {
      const n = Math.max(0, Number(size) || 0);
      const b = new Buffer(n);
      if (fill !== 0 && fill !== undefined) {
        if (typeof fill === "number") b.fill(fill);
        else {
          const f = toBytesFromInput(fill, enc);
          if (f.length > 0) for (let i = 0; i < n; i++) b[i] = f[i % f.length];
        }
      }
      return b;
    }
    static allocUnsafe(size) { return Buffer.alloc(size); }
    static byteLength(input, enc) { return toBytesFromInput(input, enc).length; }
    static concat(list, totalLength) {
      const bufs = (list || []).map((x) => toBytesFromInput(x));
      const len = totalLength !== undefined ? Number(totalLength) : bufs.reduce((a, b) => a + b.length, 0);
      const out = new Buffer(Math.max(0, len));
      let off = 0;
      for (const b of bufs) {
        if (off >= out.length) break;
        const chunk = b.length > out.length - off ? b.subarray(0, out.length - off) : b;
        out.set(chunk, off);
        off += chunk.length;
      }
      return out;
    }
    toString(enc = "utf8", start = 0, end = this.length) {
      const slice = this.subarray(start, end);
      return host.bufferToString(slice, String(enc || "utf8"));
    }
    toJSON() { return { type: "Buffer", data: Array.from(this) }; }
    equals(other) {
      const o = toBytesFromInput(other);
      if (o.length !== this.length) return false;
      for (let i = 0; i < this.length; i++) if (this[i] !== o[i]) return false;
      return true;
    }
  }
  globalThis.Buffer = Buffer;

  // --- fetch ---
  class Headers {
    constructor(init) {
      this._map = new Map();
      if (!init) return;
      if (init instanceof Headers) {
        for (const [k, v] of init.entries()) this.set(k, v);
      } else if (Array.isArray(init)) {
        for (const [k, v] of init) this.append(k, v);
      } else if (typeof init === "object") {
        for (const [k, v] of Object.entries(init)) this.set(k, v);
      }
    }
    set(k, v) { this._map.set(String(k).toLowerCase(), String(v)); }
    append(k, v) {
      const lk = String(k).toLowerCase();
      this._map.set(lk, this._map.has(lk) ? this._map.get(lk) + ", " + String(v) : String(v));
    }
    get(k) { return this._map.get(String(k).toLowerCase()) ?? null; }
    has(k) { return this._map.has(String(k).toLowerCase()); }
    delete(k) { this._map.delete(String(k).toLowerCase()); }
    entries() { return this._map.entries(); }
    keys() { return this._map.keys(); }
    values() { return this._map.values(); }
    forEach(cb, thisArg) { for (const [k, v] of this._map) cb.call(thisArg, v, k, this); }
    [Symbol.iterator]() { return this._map.entries(); }
  }
  globalThis.Headers = Headers;
  globalThis.fetch = function(input, init = {}) {
    const reqUrl = typeof input === "string" ? input : (input && input.url ? String(input.url) : String(input));
    const opts = Object.assign({}, (typeof input === "object" && input) ? input : {}, init || {});
    const sig = opts.signal;
    if (sig && sig.aborted) {
      return Promise.reject(sig.reason || new Error("Aborted"));
    }
    const hdrObj = {};
    const h = new Headers(opts.headers);
    for (const [k, v] of h.entries()) hdrObj[k] = v;
    let bodyBytes = null;
    if (opts.body !== undefined && opts.body !== null) {
      if (typeof opts.body === "string") bodyBytes = host.utf8Encode(opts.body);
      else if (opts.body instanceof ArrayBuffer || ArrayBuffer.isView(opts.body)) bodyBytes = toBytesFromInput(opts.body);
      else bodyBytes = host.utf8Encode(String(opts.body));
    }
    let abortId = 0;
    let onAbort = null;
    if (sig && typeof sig.addEventListener === "function") {
      abortId = host.fetchAllocAbort();
      onAbort = () => host.fetchAbort(abortId);
      sig.addEventListener("abort", onAbort, { once: true });
    }
    return host.fetch(reqUrl, String(opts.method || "GET"), hdrObj, bodyBytes, abortId).finally(() => {
      if (sig && onAbort && typeof sig.removeEventListener === "function") {
        sig.removeEventListener("abort", onAbort);
      }
    }).then((resp) => {
      if (sig && sig.aborted) throw (sig.reason || new Error("Aborted"));
      const respHeaders = new Headers(resp.headers || {});
      const rawBytes = new Uint8Array(resp.body || []);
      let consumed = false;
      function readBytes() {
        if (consumed) return Promise.reject(new TypeError("Body already read"));
        consumed = true;
        return Promise.resolve(rawBytes);
      }
      const response = {
        ok: resp.status >= 200 && resp.status < 300,
        status: resp.status,
        statusText: resp.statusText || "",
        url: resp.url || reqUrl,
        redirected: Boolean(resp.redirected),
        headers: respHeaders,
        get bodyUsed() { return consumed; },
        arrayBuffer() { return readBytes().then((u8) => u8.buffer.slice(u8.byteOffset, u8.byteOffset + u8.byteLength)); },
        bytes() { return readBytes(); },
        text() { return readBytes().then((u8) => host.utf8Decode(u8)); },
        json() { return readBytes().then((u8) => JSON.parse(host.utf8Decode(u8))); },
        clone() { return Object.assign({}, response); },
      };
      return response;
    });
  };

  // --- EventEmitter ---
  class EventEmitter {
    constructor() { this._events = new Map(); }
    on(ev, fn) {
      if (!this._events.has(ev)) this._events.set(ev, []);
      this._events.get(ev).push({ fn, once: false });
      return this;
    }
    addListener(ev, fn) { return this.on(ev, fn); }
    once(ev, fn) {
      if (!this._events.has(ev)) this._events.set(ev, []);
      this._events.get(ev).push({ fn, once: true });
      return this;
    }
    off(ev, fn) {
      const list = this._events.get(ev);
      if (!list) return this;
      this._events.set(ev, list.filter((x) => x.fn !== fn));
      return this;
    }
    removeListener(ev, fn) { return this.off(ev, fn); }
    removeAllListeners(ev) {
      if (ev === undefined) this._events.clear();
      else this._events.delete(ev);
      return this;
    }
    emit(ev, ...args) {
      const list = this._events.get(ev);
      if (!list || list.length === 0) return false;
      const snap = list.slice();
      this._events.set(ev, list.filter((x) => !x.once));
      for (const item of snap) item.fn.apply(this, args);
      return true;
    }
    listeners(ev) { return (this._events.get(ev) || []).map((x) => x.fn); }
    listenerCount(ev) { return (this._events.get(ev) || []).length; }
    eventNames() { return Array.from(this._events.keys()); }
  }

  // --- process & Node Shims ---
  const envProxy = new Proxy({}, {
    get(_t, prop) { return typeof prop === "string" ? host.env.get(prop) : undefined; },
    set(_t, prop, val) { if (typeof prop === "string") host.env.set(prop, val === undefined ? undefined : String(val)); return true; },
    deleteProperty(_t, prop) { if (typeof prop === "string") host.env.del(prop); return true; },
    has(_t, prop) { return typeof prop === "string" ? host.env.has(prop) : false; },
    ownKeys() { return host.env.keys(); },
    getOwnPropertyDescriptor(_t, prop) {
      if (typeof prop !== "string" || !host.env.has(prop)) return undefined;
      return { enumerable: true, configurable: true, writable: true, value: host.env.get(prop) };
    },
  });

  const processObj = {
    env: envProxy,
    argv: ["pi"],
    cwd: () => host.cwd(),
    chdir: () => { throw new Error("process.chdir is not supported inside isolated QuickJS extension"); },
    platform: host.os.platform(),
    arch: host.os.arch(),
    version: "v20.0.0",
    versions: { node: "20.0.0" },
    pid: host.os.pid(),
    nextTick: (fn, ...args) => Promise.resolve().then(() => fn(...args)),
    stdout: { isTTY: false, write: (s) => { host.consoleLog("log", String(s)); return true; } },
    stderr: { isTTY: false, write: (s) => { host.consoleLog("error", String(s)); return true; } },
    stdin: { isTTY: false },
    exit: (code = 0) => { throw new Error("process.exit(" + code + ") is not supported in extension"); },
  };
  globalThis.process = processObj;

  function makePathShim() {
    const sep = host.path.sep();
    const delimiter = host.path.delimiter();
    function join(...parts) { return host.path.join(parts.map(String)); }
    function resolve(...parts) { return host.path.resolve(parts.map(String)); }
    function dirname(p) { return host.path.dirname(String(p)); }
    function basename(p, ext) { return host.path.basename(String(p), ext !== undefined ? String(ext) : ""); }
    function extname(p) { return host.path.extname(String(p)); }
    function normalize(p) { return host.path.normalize(String(p)); }
    function isAbsolute(p) { return host.path.isAbsolute(String(p)); }
    function relative(from, to) { return host.path.relative(String(from), String(to)); }
    function parse(p) {
      const s = String(p);
      const dir = dirname(s);
      const base = basename(s);
      const ext = extname(s);
      const name = ext ? base.slice(0, -ext.length) : base;
      const root = isAbsolute(s) ? sep : "";
      return { root, dir, base, ext, name };
    }
    function format(obj) {
      const dir = obj.dir || obj.root || "";
      const base = obj.base || ((obj.name || "") + (obj.ext || ""));
      return dir ? join(dir, base) : base;
    }
    const api = { sep, delimiter, join, resolve, dirname, basename, extname, normalize, isAbsolute, relative, parse, format };
    api.posix = api;
    api.win32 = api;
    return api;
  }

  function makeStatObj(raw) {
    return {
      size: raw.size,
      mode: raw.mode,
      mtimeMs: raw.mtimeMs,
      mtime: new Date(raw.mtimeMs),
      isFile: () => raw.isFile,
      isDirectory: () => raw.isDir,
      isSymbolicLink: () => raw.isSymlink,
    };
  }

  function resolveEncoding(opts) {
    if (typeof opts === "string") return opts;
    if (opts && typeof opts === "object" && opts.encoding) return opts.encoding;
    return null;
  }

  function makeFsShim() {
    const constants = { F_OK: 0, R_OK: 4, W_OK: 2, X_OK: 1 };
    const existsSync = (p) => host.fs.existsSync(String(p));
    const readFileSync = (p, opts) => {
      const bytes = host.fs.readFileSync(String(p));
      const enc = resolveEncoding(opts);
      const buf = Buffer.from(bytes);
      return enc ? buf.toString(enc) : buf;
    };
    const writeFileSync = (p, data, opts) => {
      const enc = resolveEncoding(opts) || "utf8";
      const bytes = typeof data === "string" ? host.bufferFromString(data, enc) : toBytesFromInput(data);
      host.fs.writeFileSync(String(p), bytes);
    };
    const appendFileSync = (p, data, opts) => {
      const enc = resolveEncoding(opts) || "utf8";
      const bytes = typeof data === "string" ? host.bufferFromString(data, enc) : toBytesFromInput(data);
      host.fs.appendFileSync(String(p), bytes);
    };
    const mkdirSync = (p, opts) => host.fs.mkdirSync(String(p), Boolean(opts && opts.recursive));
    const readdirSync = (p, opts) => {
      const entries = host.fs.readdirSync(String(p));
      if (opts && opts.withFileTypes) {
        return entries.map((e) => ({ name: e.name, isFile: () => e.isFile, isDirectory: () => e.isDir, isSymbolicLink: () => e.isSymlink }));
      }
      return entries.map((e) => e.name);
    };
    const statSync = (p) => makeStatObj(host.fs.statSync(String(p)));
    const lstatSync = (p) => makeStatObj(host.fs.lstatSync(String(p)));
    const unlinkSync = (p) => host.fs.unlinkSync(String(p));
    const rmSync = (p, opts) => host.fs.rmSync(String(p), Boolean(opts && opts.recursive), Boolean(opts && opts.force));
    const rmdirSync = (p, opts) => host.fs.rmSync(String(p), Boolean(opts && opts.recursive), false);
    const renameSync = (a, b) => host.fs.renameSync(String(a), String(b));
    const copyFileSync = (a, b) => host.fs.copyFileSync(String(a), String(b));
    const realpathSync = (p) => host.fs.realpathSync(String(p));
    const chmodSync = (p, mode) => host.fs.chmodSync(String(p), Number(mode) || 0o644);
    const accessSync = (p, mode) => host.fs.accessSync(String(p), Number(mode) || 0);
    const watch = () => ({ close() {}, ref() { return this; }, unref() { return this; } });

    const promises = {
      constants,
      readFile: (p, opts) => Promise.resolve().then(() => readFileSync(p, opts)),
      writeFile: (p, data, opts) => Promise.resolve().then(() => writeFileSync(p, data, opts)),
      appendFile: (p, data, opts) => Promise.resolve().then(() => appendFileSync(p, data, opts)),
      mkdir: (p, opts) => Promise.resolve().then(() => mkdirSync(p, opts)),
      readdir: (p, opts) => Promise.resolve().then(() => readdirSync(p, opts)),
      stat: (p) => Promise.resolve().then(() => statSync(p)),
      lstat: (p) => Promise.resolve().then(() => lstatSync(p)),
      unlink: (p) => Promise.resolve().then(() => unlinkSync(p)),
      rm: (p, opts) => Promise.resolve().then(() => rmSync(p, opts)),
      rmdir: (p, opts) => Promise.resolve().then(() => rmdirSync(p, opts)),
      rename: (a, b) => Promise.resolve().then(() => renameSync(a, b)),
      copyFile: (a, b) => Promise.resolve().then(() => copyFileSync(a, b)),
      realpath: (p) => Promise.resolve().then(() => realpathSync(p)),
      chmod: (p, mode) => Promise.resolve().then(() => chmodSync(p, mode)),
      access: (p, mode) => Promise.resolve().then(() => accessSync(p, mode)),
    };

    return {
      constants, existsSync, readFileSync, writeFileSync, appendFileSync,
      mkdirSync, readdirSync, statSync, lstatSync, unlinkSync, rmSync, rmdirSync,
      renameSync, copyFileSync, realpathSync, chmodSync, accessSync, watch, promises,
    };
  }

  function makeCryptoShim() {
    const randomBytes = (size) => Buffer.from(host.crypto.randomBytes(Number(size) || 0));
    const randomUUID = () => host.crypto.randomUUID();
    const randomInt = (min, max) => {
      if (max === undefined) { max = min; min = 0; }
      return host.crypto.randomInt(Number(min), Number(max));
    };
    const createHash = (alg) => {
      const chunks = [];
      const h = {
        update(data, enc) { chunks.push(toBytesFromInput(data, enc)); return h; },
        digest(enc) {
          const all = Buffer.concat(chunks);
          const raw = host.crypto.hash(String(alg), all);
          const buf = Buffer.from(raw);
          return enc ? buf.toString(enc) : buf;
        },
      };
      return h;
    };
    const createHmac = (alg, key) => {
      const chunks = [];
      const keyBytes = toBytesFromInput(key);
      const h = {
        update(data, enc) { chunks.push(toBytesFromInput(data, enc)); return h; },
        digest(enc) {
          const all = Buffer.concat(chunks);
          const raw = host.crypto.hmac(String(alg), keyBytes, all);
          const buf = Buffer.from(raw);
          return enc ? buf.toString(enc) : buf;
        },
      };
      return h;
    };
    const webcrypto = {
      randomUUID,
      getRandomValues(arr) {
        const bytes = host.crypto.randomBytes(arr.byteLength);
        new Uint8Array(arr.buffer, arr.byteOffset, arr.byteLength).set(bytes);
        return arr;
      },
      subtle: {
        async digest(alg, data) {
          const norm = String(typeof alg === "string" ? alg : (alg && alg.name) || "SHA-256").toLowerCase().replace(/[^a-z0-9]/g, "");
          const buf = createHash(norm).update(toBytesFromInput(data)).digest();
          return buf.buffer.slice(buf.byteOffset, buf.byteOffset + buf.byteLength);
        },
      },
    };
    return { randomBytes, randomUUID, randomInt, createHash, createHmac, webcrypto };
  }

  const cryptoModule = makeCryptoShim();
  globalThis.crypto = cryptoModule.webcrypto;
  globalThis.btoa = (s) => Buffer.from(String(s), "latin1").toString("base64");
  globalThis.atob = (s) => Buffer.from(String(s), "base64").toString("latin1");

  globalThis.__piShims = {
    path: makePathShim(),
    os: {
      homedir: () => host.os.homedir(),
      tmpdir: () => host.os.tmpdir(),
      platform: () => host.os.platform(),
      arch: () => host.os.arch(),
      hostname: () => host.os.hostname(),
      cpus: () => Array.from({ length: Math.max(1, host.os.numCPU()) }, () => ({ model: "cpu", speed: 2400 })),
      EOL: host.os.eol(),
      type: () => host.os.type(),
      release: () => host.os.release(),
      userInfo: () => ({ username: host.os.username(), homedir: host.os.homedir(), uid: -1, gid: -1, shell: null }),
    },
    fs: makeFsShim(),
    url: {
      fileURLToPath: (u) => host.url.fileURLToPath(typeof u === "string" ? u : u.href),
      pathToFileURL: (p) => new URL(host.url.pathToFileURL(String(p))),
      URL,
      URLSearchParams,
    },
    crypto: cryptoModule,
    events: { EventEmitter },
    buffer: { Buffer, kMaxLength: 0x7fffffff },
    util: {
      TextEncoder,
      TextDecoder,
      promisify: (fn) => (...args) => new Promise((resolve, reject) => {
        fn(...args, (err, ...res) => (err ? reject(err) : resolve(res.length <= 1 ? res[0] : res)));
      }),
      format: (...args) => formatArgs(args),
      inspect: (v) => { try { return typeof v === "string" ? v : JSON.stringify(v, null, 2); } catch { return String(v); } },
      inherits: (ctor, superCtor) => {
        if (ctor && superCtor && superCtor.prototype) Object.setPrototypeOf(ctor.prototype, superCtor.prototype);
      },
      types: {
        isUint8Array: (v) => v instanceof Uint8Array,
        isArrayBuffer: (v) => v instanceof ArrayBuffer,
        isPromise: (v) => v instanceof Promise,
      },
    },
    assert: {
      ok(v, msg) { if (!v) throw new Error(msg || "AssertionError: expected truthy value"); },
      strictEqual(a, b, msg) { if (!Object.is(a, b)) throw new Error(msg || ("AssertionError: " + String(a) + " !== " + String(b))); },
      notStrictEqual(a, b, msg) { if (Object.is(a, b)) throw new Error(msg || ("AssertionError: expected !== " + String(a))); },
      equal(a, b, msg) { if (a != b) throw new Error(msg || ("AssertionError: " + String(a) + " != " + String(b))); },
      notEqual(a, b, msg) { if (a == b) throw new Error(msg || ("AssertionError: expected != " + String(a))); },
      deepStrictEqual(a, b, msg) { if (JSON.stringify(a) !== JSON.stringify(b)) throw new Error(msg || "AssertionError: deepStrictEqual failed"); },
      fail(msg) { throw new Error(msg || "AssertionError: fail"); },
      throws(fn, msg) { let threw = false; try { fn(); } catch { threw = true; } if (!threw) throw new Error(msg || "AssertionError: expected function to throw"); },
    },
    stream: {
      EventEmitter,
      Stream: EventEmitter,
      Readable: EventEmitter,
      Writable: EventEmitter,
      Transform: EventEmitter,
      PassThrough: EventEmitter,
    },
    module: {
      createRequire: () => (id) => { throw new Error("require('" + id + "') is not supported at runtime inside QuickJS extension; use ES imports"); },
      builtinModules: ["path", "os", "fs", "fs/promises", "url", "crypto", "events", "buffer", "process", "util", "assert", "stream", "module"],
    },
  };
  globalThis.__piShims.assert.strict = globalThis.__piShims.assert;

  // --- Promise Await Bridge ---
  globalThis.__piAwait = function(fn, thisArg, args, resolveCb, rejectCb) {
    try {
      const r = fn.apply(thisArg, args || []);
      Promise.resolve(r).then(
        (v) => resolveCb(v),
        (e) => rejectCb(e)
      );
    } catch (e) {
      rejectCb(e);
    }
  };

  // --- Signal Bridge ---
  const activeAbortSignals = new Map();
  globalThis.__piNewSignal = function(id) {
    const ctrl = new AbortController();
    if (id > 0) activeAbortSignals.set(id, ctrl);
    return ctrl.signal;
  };
  globalThis.__piAbortSignal = function(id, reason) {
    const ctrl = activeAbortSignals.get(id);
    if (ctrl) {
      activeAbortSignals.delete(id);
      ctrl.abort(reason ? new Error(reason) : undefined);
    }
  };
  globalThis.__piDropSignal = function(id) {
    activeAbortSignals.delete(id);
  };

  // --- Context / UI Proxy ---
  const jsTheme = {
    fg: (color, text) => host.ctx.themeCall("fg", String(color || ""), String(text ?? "")),
    bg: (color, text) => host.ctx.themeCall("bg", String(color || ""), String(text ?? "")),
    bold: (text) => host.ctx.themeCall("bold", "", String(text ?? "")),
    italic: (text) => host.ctx.themeCall("italic", "", String(text ?? "")),
    dim: (text) => host.ctx.themeCall("dim", "", String(text ?? "")),
    strikethrough: (text) => host.ctx.themeCall("strikethrough", "", String(text ?? "")),
    underline: (text) => host.ctx.themeCall("underline", "", String(text ?? "")),
    inverse: (text) => host.ctx.themeCall("inverse", "", String(text ?? "")),
    getFgAnsi: (color) => host.ctx.themeCall("getFgAnsi", String(color || ""), ""),
    getBgAnsi: (color) => host.ctx.themeCall("getBgAnsi", String(color || ""), ""),
  };
  globalThis.__piTheme = jsTheme;

  function makeUIProxy(ctxId) {
    return {
      select: (title, options, opts) => host.ui.select(ctxId, String(title ?? ""), Array.from(options || [], String), opts || null),
      confirm: (title, message, opts) => host.ui.confirm(ctxId, String(title ?? ""), String(message ?? ""), opts || null),
      input: (title, placeholder, opts) => host.ui.input(ctxId, String(title ?? ""), placeholder ?? null, opts || null),
      editor: (title, prefill) => host.ui.editor(ctxId, String(title ?? ""), prefill ?? null),
      notify: (msg, notifyType) => host.ui.notify(ctxId, String(msg ?? ""), notifyType ? String(notifyType) : ""),
      setStatus: (key, text) => host.ui.setStatus(ctxId, String(key ?? ""), text === undefined || text === null ? null : String(text)),
      setWorkingMessage: (msg) => host.ui.setWorkingMessage(ctxId, msg === undefined ? null : String(msg)),
      setWorkingVisible: (vis) => host.ui.setWorkingVisible(ctxId, Boolean(vis)),
      setWorkingIndicator: (opts) => host.ui.setWorkingIndicator(ctxId, opts || null),
      setHiddenThinkingLabel: (label) => host.ui.setHiddenThinkingLabel(ctxId, label === undefined ? null : String(label)),
      setWidget: (key, content, opts) => host.ui.setWidget(ctxId, String(key ?? ""), content, opts || null),
      setFooter: (factory) => host.ui.setFooter(ctxId, typeof factory === "function" ? factory : null),
      setHeader: (factory) => host.ui.setHeader(ctxId, typeof factory === "function" ? factory : null),
      setTitle: (title) => host.ui.setTitle(ctxId, String(title ?? "")),
      custom: (factory, opts) => host.ui.custom(ctxId, factory, opts || null),
      pasteToEditor: (text) => host.ui.pasteToEditor(ctxId, String(text ?? "")),
      setEditorText: (text) => host.ui.setEditorText(ctxId, String(text ?? "")),
      getEditorText: () => host.ui.getEditorText(ctxId),
      onTerminalInput: (handler) => host.ui.onTerminalInput(ctxId, handler),
      get theme() { return jsTheme; },
      getAllThemes: () => host.ui.getAllThemes(ctxId),
      getTheme: (name) => name ? jsTheme : undefined,
      setTheme: (nameOrTheme) => host.ui.setTheme(ctxId, typeof nameOrTheme === "string" ? nameOrTheme : (nameOrTheme && nameOrTheme.name) || ""),
      getToolsExpanded: () => host.ui.getToolsExpanded(ctxId),
      setToolsExpanded: (exp) => host.ui.setToolsExpanded(ctxId, Boolean(exp)),
    };
  }

  function makeSessionManagerProxy(ctxId) {
    return {
      getCwd: () => host.ctx.smCall(ctxId, "getCwd"),
      getSessionDir: () => host.ctx.smCall(ctxId, "getSessionDir"),
      getSessionId: () => host.ctx.smCall(ctxId, "getSessionId"),
      getSessionFile: () => host.ctx.smCall(ctxId, "getSessionFile"),
      getLeafId: () => host.ctx.smCall(ctxId, "getLeafId"),
      getLeafEntry: () => host.ctx.smCall(ctxId, "getLeafEntry"),
      getEntry: (id) => host.ctx.smCall(ctxId, "getEntry", String(id)),
      getLabel: (id) => host.ctx.smCall(ctxId, "getLabel", String(id)),
      getBranch: (fromId) => host.ctx.smCall(ctxId, "getBranch", fromId === undefined ? null : String(fromId)),
      getEntries: () => host.ctx.smCall(ctxId, "getEntries"),
      getTree: () => host.ctx.smCall(ctxId, "getTree"),
      getSessionName: () => host.ctx.smCall(ctxId, "getSessionName"),
      getHeader: () => host.ctx.smCall(ctxId, "getHeader"),
    };
  }

  function makeModelRegistryProxy(ctxId) {
    return {
      find: (provider, id) => host.ctx.mrCall(ctxId, "find", String(provider), String(id)),
      getAll: () => host.ctx.mrCall(ctxId, "getAll"),
      getAvailable: () => host.ctx.mrCall(ctxId, "getAvailable"),
      getApiKey: (model) => Promise.resolve(host.ctx.mrCall(ctxId, "getApiKey", model)),
      getApiKeyAndHeaders: (model) => Promise.resolve(host.ctx.mrCall(ctxId, "getApiKeyAndHeaders", model)),
      getApiKeyForProvider: (provider) => Promise.resolve(host.ctx.mrCall(ctxId, "getApiKeyForProvider", String(provider))),
      isUsingOAuth: (model) => host.ctx.mrCall(ctxId, "isUsingOAuth", model),
    };
  }

  globalThis.__piMakeContext = function(ctxId, signalId, isCommand, isTool) {
    const ui = makeUIProxy(ctxId);
    const sm = makeSessionManagerProxy(ctxId);
    const mr = makeModelRegistryProxy(ctxId);
    const signal = signalId > 0 ? globalThis.__piNewSignal(signalId) : new AbortController().signal;
    const ctxObj = {
      get ui() { return ui; },
      get mode() { return host.ctx.getProp(ctxId, "mode"); },
      get hasUI() { return host.ctx.getProp(ctxId, "hasUI"); },
      get cwd() { return host.ctx.getProp(ctxId, "cwd"); },
      get model() { return host.ctx.getProp(ctxId, "model"); },
      get scopedModels() { return host.ctx.getProp(ctxId, "scopedModels"); },
      get thinkingLevel() { return host.ctx.getProp(ctxId, "thinkingLevel"); },
      get sessionManager() { return sm; },
      get modelRegistry() { return mr; },
      get signal() { return signal; },
      isIdle: () => host.ctx.getProp(ctxId, "isIdle"),
      isProjectTrusted: () => host.ctx.getProp(ctxId, "isProjectTrusted"),
      hasPendingMessages: () => host.ctx.getProp(ctxId, "hasPendingMessages"),
      abort: () => host.ctx.callAction(ctxId, "abort"),
      shutdown: () => host.ctx.callAction(ctxId, "shutdown"),
      getContextUsage: () => host.ctx.getProp(ctxId, "getContextUsage"),
      compact: (opts) => host.ctx.compact(ctxId, opts || null),
      getSystemPrompt: () => host.ctx.getProp(ctxId, "getSystemPrompt"),
    };
    if (isCommand) {
      ctxObj.waitForIdle = () => host.ctx.cmdAsync(ctxId, "waitForIdle", null);
      ctxObj.newSession = (opts) => host.ctx.cmdAsync(ctxId, "newSession", opts || null);
      ctxObj.fork = (entryId, opts) => host.ctx.cmdAsync(ctxId, "fork", { entryId: String(entryId), opts: opts || null });
      ctxObj.navigateTree = (targetId, opts) => host.ctx.cmdAsync(ctxId, "navigateTree", { targetId: String(targetId), opts: opts || null });
      ctxObj.switchSession = (path, opts) => host.ctx.cmdAsync(ctxId, "switchSession", { path: String(path), opts: opts || null });
      ctxObj.reload = () => host.ctx.cmdAsync(ctxId, "reload", null);
      ctxObj.getSystemPromptOptions = () => host.ctx.getProp(ctxId, "getSystemPromptOptions");
      ctxObj.sendUserMessage = (content, opts) => host.ctx.cmdAsync(ctxId, "sendUserMessage", { content, opts: opts || null });
    }
    if (isTool) {
      ctxObj.tools = () => host.ctx.toolCall(ctxId, "tools", null);
      ctxObj.executeTool = (name, args, opts) => host.ctx.executeTool(ctxId, String(name), args, opts || null);
    }
    return ctxObj;
  };

  globalThis.__piCreateAPI = function() {
    const eventsObj = {
      on: (channel, handler) => host.api.eventsOn(String(channel), handler),
      emit: (channel, data) => host.api.eventsEmit(String(channel), data),
    };
    return {
      on: (event, handler) => host.api.on(String(event), handler),
      registerTool: (tool) => {
        if (!tool || typeof tool !== "object") throw new TypeError("registerTool expects an object");
        const paramsJson = tool.parameters !== undefined ? JSON.stringify(tool.parameters) : "";
        const outJson = tool.outputSchema !== undefined ? JSON.stringify(tool.outputSchema) : "";
        host.api.registerTool(tool, paramsJson, outJson);
      },
      registerCommand: (name, opts) => host.api.registerCommand(String(name), opts),
      registerShortcut: (shortcut, opts) => host.api.registerShortcut(String(shortcut), opts),
      registerFlag: (name, opts) => host.api.registerFlag(String(name), opts),
      getFlag: (name) => host.api.getFlag(String(name)),
      registerMessageRenderer: (customType, renderer) => host.api.registerMessageRenderer(String(customType), renderer),
      registerEntryRenderer: (customType, renderer) => host.api.registerEntryRenderer(String(customType), renderer),
      registerToolRenderer: (resolver) => host.api.registerToolRenderer(resolver),
      registerMarkdownTransformer: (transformer, opts) => host.api.registerMarkdownTransformer(transformer, opts || null),
      sendMessage: (msg, opts) => host.api.sendMessage(msg, opts || null),
      sendUserMessage: (content, opts) => host.api.sendUserMessage(content, opts || null),
      appendEntry: (customType, data) => host.api.appendEntry(String(customType), data),
      setSessionName: (name) => host.api.setSessionName(String(name)),
      getSessionName: () => host.api.getSessionName(),
      setLabel: (entryId, label) => host.api.setLabel(String(entryId), label ?? null),
      exec: (command, args, opts) => host.api.exec(String(command), Array.from(args || [], String), opts || null),
      getActiveTools: () => host.api.getActiveTools(),
      getAllTools: () => host.api.getAllTools(),
      getSettings: () => host.api.getSettings(),
      setActiveTools: (names) => host.api.setActiveTools(Array.from(names || [], String)),
      getCommands: () => host.api.getCommands(),
      setModel: (model) => host.api.setModel(model),
      getThinkingLevel: () => host.api.getThinkingLevel(),
      setThinkingLevel: (level) => host.api.setThinkingLevel(String(level)),
      registerNativeProvider: (provider) => host.api.registerNativeProvider(provider),
      registerProvider: (name, config) => host.api.registerProvider(String(name), config),
      unregisterProvider: (name) => host.api.unregisterProvider(String(name)),
      registerMcpServer: (name, config) => host.api.registerMcpServer(String(name), config),
      unregisterMcpServer: (name) => host.api.unregisterMcpServer(String(name)),
      getMcpServers: () => host.api.getMcpServers(),
      registerVirtualModel: (def) => host.api.registerVirtualModel(def),
      unregisterVirtualModel: (provider, id) => host.api.unregisterVirtualModel(String(provider), String(id)),
      events: eventsObj,
    };
  };

  globalThis.__piRunExtension = async function(api) {
    const mod = globalThis.__piExtModule;
    let fn = null;
    if (typeof mod === "function") fn = mod;
    else if (mod && typeof mod.default === "function") fn = mod.default;
    else if (mod && mod.default && typeof mod.default.default === "function") fn = mod.default.default;
    if (!fn) {
      throw new Error("Extension must export a default function (export default function(pi) { ... })");
    }
    return await fn(api);
  };

  globalThis.__piConsumeStream = async function(streamOrPromise, pushEvCb, doneCb) {
    try {
      const s = await streamOrPromise;
      if (!s) {
        doneCb(null, null);
        return;
      }
      if (typeof s[Symbol.asyncIterator] === "function") {
        let finalMsg = null;
        for await (const ev of s) {
          if (ev && (ev.type === "done" || ev.type === "error")) {
            finalMsg = ev.message || ev.error || finalMsg;
          }
          pushEvCb(ev);
        }
        doneCb(finalMsg, null);
        return;
      }
      if (typeof s.result === "function") {
        const res = await s.result();
        doneCb(res, null);
        return;
      }
      doneCb(s, null);
    } catch (e) {
      doneCb(null, e);
    }
  };
})();
`
