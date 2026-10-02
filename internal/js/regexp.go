package js

// WhitespaceClass is the JS \s set (WhiteSpace and LineTerminator) as the body
// of an RE2 character class: use "[" + WhitespaceClass + "]" for \s and
// "[^" + WhitespaceClass + "]" for \S.
const WhitespaceClass = `\t\n\v\f\r \x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}`
