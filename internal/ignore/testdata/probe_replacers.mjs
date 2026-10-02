const samples = {
  "2": String.raw`(?:^|\/)\*\*`,
  "3": String.raw`(?:^|\/)\*\*\*`,
  "4": String.raw`(?:^|\/)\*\*\*\*`,
  a3: String.raw`(?:^|\/)a\*\*\*`,
  a3b: String.raw`(?:^|\/)a\*\*\*b`,
  "3b": String.raw`(?:^|\/)\*\*\*b`,
};
const re = /(^|[^\\]+)(\\\*)+(?=.+)/g;
for (const [k, s] of Object.entries(samples)) {
  const out = s.replace(re, (_, p1, p2) => {
    console.log(k, "P1", JSON.stringify(p1), "P2", JSON.stringify(p2));
    return p1 + p2.replace(/\\\*/g, "[^\\/]*");
  });
  console.log(k, "IN ", JSON.stringify(s));
  console.log(k, "OUT", JSON.stringify(out));
}
