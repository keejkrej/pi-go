// Regenerates golden.json from grok-mermaid@0.2.3. Usage:
//   node gen_golden.mjs <path to the pi TS checkout>
import {writeFileSync} from 'node:fs';
import {pathToFileURL} from 'node:url';
import path from 'node:path';

const root = path.resolve(process.argv[2] ?? '../../../../../../pi');
const dist = path.join(root, 'node_modules/grok-mermaid/dist/index.js');
const {render, sourceBox, diagramKind, toAnsi} = await import(pathToFileURL(dist).href);

const cases = [
  {name: 'readme-lr', src: 'flowchart LR\n  A[Start] --> B[Done]'},
  {name: 'readme-broken', src: 'graph TD\n A[Start --> B'},
  {name: 'readme-state-drop', src: 'stateDiagram-v2\n A --> B\n some garbage line'},
  {name: 'flowchart-td', src: 'flowchart TD\n  A{Go?} -->|yes| B[Do it]\n  A -->|no| C[Stop]'},
  {name: 'flowchart-bt', src: 'flowchart BT\n  A[Top] --> B[Bottom]'},
  {name: 'flowchart-rl', src: 'flowchart RL\n  A[Left] --> B[Right]'},
  {name: 'flowchart-styles', src: 'flowchart LR\n  A[a] ==> B[b]\n  B -.-> C[c]\n  C -- text --> D[d]\n  D --o E[e]\n  E x--x F[f]'},
  {name: 'subgraph', src: 'flowchart TB\n  subgraph one\n    A-->B\n  end\n  subgraph two\n    C-->D\n  end\n  A-->C'},
  {name: 'state', src: 'stateDiagram-v2\n  [*] --> A\n  A --> B: go\n  B --> [*]\n  state C <<choice>>'},
  {name: 'class', src: 'classDiagram\n  Animal <|-- Duck\n  Animal : +int age\n  Animal : +isMammal()\n  class Duck {\n    +String beak\n    +swim()\n  }'},
  {name: 'er', src: 'erDiagram\n  CUSTOMER ||--o{ ORDER : places\n  ORDER {\n    int id\n    string name\n  }'},
  {name: 'sequence', src: 'sequenceDiagram\n  participant Alice\n  participant Bob\n  Alice->>Bob: Hello\n  Bob-->>Alice: Hi\n  Note right of Bob: thinking\n  loop Every day\n    Alice->>Alice: think\n  end'},
  {name: 'self', src: 'flowchart TD\n  A[Loop] --> A'},
  {name: 'cjk', src: 'flowchart LR\n  A[开始] --> B[结束]'},
  {name: 'empty', src: '   \n'},
  {name: 'unsupported', src: 'pie title Pets\n  "Dogs" : 10'},
  {name: 'blank-graph', src: 'flowchart TD'},
];

const out = [];
for (const c of cases) {
  const art = render(c.src);
  const row = {name: c.name, fn: 'render', src: c.src, kind: diagramKind(c.src)};
  if (!art) row.err = true;
  else {
    row.err = false;
    row.plain = art.plain;
    row.width = art.width;
    row.warnings = art.warnings;
    row.styled = art.styled;
  }
  out.push(row);
}

const boxed = sourceBox('flowchart LR\n  A[Start] --> B[Done]', 40);
out.push({
  name: 'source-box',
  fn: 'sourceBox',
  src: 'flowchart LR\n  A[Start] --> B[Done]',
  maxWidth: 40,
  err: false,
  plain: boxed.plain,
  width: boxed.width,
  warnings: boxed.warnings,
  styled: boxed.styled,
});
const ansi = toAnsi(render('flowchart LR\n  A[Start] --> B[Done]'));
out.push({name: 'ansi', fn: 'ansi', src: 'flowchart LR\n  A[Start] --> B[Done]', lines: ansi});

writeFileSync(new URL('./golden.json', import.meta.url), JSON.stringify(out, null, 2) + '\n');
console.log('wrote', out.length, 'cases');
