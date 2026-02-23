import { useState } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";
import {
  Github,
  Monitor,
  Server,
  ArrowLeftRight,
  RefreshCw,
  AlertTriangle,
  XCircle,
  BarChart3,
  Radio,
  Cpu,
} from "lucide-react";

export const Route = createFileRoute("/")({ component: LandingPage });

function LandingPage() {
  return (
    <div className="relative min-h-screen bg-background text-foreground overflow-hidden">
      <GridBackground />
      <div className="relative z-10">
        <Hero />
        <Architecture />
        <Examples />
        <Features />
      </div>
    </div>
  );
}

function GridBackground() {
  return (
    <div className="pointer-events-none fixed inset-0 z-0">
      <div
        className="absolute inset-0 opacity-[0.03]"
        style={{
          backgroundImage: `linear-gradient(rgba(255,255,255,0.1) 1px, transparent 1px),
                            linear-gradient(90deg, rgba(255,255,255,0.1) 1px, transparent 1px)`,
          backgroundSize: "64px 64px",
        }}
      />
      <div className="absolute top-[-20%] left-1/2 -translate-x-1/2 h-[600px] w-[800px] rounded-full bg-primary/5 blur-[120px]" />
    </div>
  );
}

function Hero() {
  return (
    <section className="flex flex-col items-center px-6 pt-32 pb-20 text-center">
      <h1 className="mb-6 text-7xl font-extrabold tracking-tighter sm:text-8xl">
        <span className="bg-gradient-to-b from-foreground to-muted-foreground/60 bg-clip-text text-transparent">
          agentd
        </span>
      </h1>
      <p className="mb-4 max-w-xl text-xl leading-relaxed text-muted-foreground sm:text-2xl">
        Agents run on the server. Tools run on your machine. Your data never
        leaves.
      </p>
      <p className="mb-10 max-w-lg text-base text-muted-foreground/60">
        Compose agents into sequential, parallel, and looping pipelines —
        stream output in real time.
      </p>
      <a
        href="https://github.com/apzuk3/agentd"
        target="_blank"
        rel="noopener noreferrer"
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground/60 transition-colors hover:text-foreground"
      >
        <Github className="size-4" />
        GitHub
      </a>
    </section>
  );
}

function Architecture() {
  return (
    <section className="mx-auto max-w-4xl px-6 pb-24">
      <p className="mb-8 text-center text-sm font-medium uppercase tracking-widest text-muted-foreground/50">
        Architecture
      </p>
      <div className="grid gap-4 md:grid-cols-[1fr_auto_1fr]">
        <div className="rounded-xl border border-border/50 bg-card p-6">
          <div className="mb-4 flex items-center gap-2 text-sm font-semibold text-foreground">
            <Monitor className="size-4 text-muted-foreground" />
            Client (your machine or server)
          </div>
          <ul className="space-y-2 text-sm text-muted-foreground">
            <li>Holds your tools and data</li>
            <li>Executes tool calls locally</li>
            <li>Private data never leaves</li>
          </ul>
        </div>
        <div className="flex items-center justify-center">
          <div className="flex flex-col items-center gap-1.5 text-muted-foreground/50">
            <ArrowLeftRight className="size-5" />
            <span className="text-[10px] font-medium uppercase tracking-wider">
              ConnectRPC
            </span>
            <span className="text-[10px] font-medium uppercase tracking-wider">
              bidi stream
            </span>
          </div>
        </div>
        <div className="rounded-xl border border-border/50 bg-card p-6">
          <div className="mb-4 flex items-center gap-2 text-sm font-semibold text-foreground">
            <Server className="size-4 text-muted-foreground" />
            Server (agentd)
          </div>
          <ul className="space-y-2 text-sm text-muted-foreground">
            <li>Google ADK agent orchestration</li>
            <li>LLM calls and routing</li>
            <li>Streams OutputChunks in real time</li>
          </ul>
        </div>
      </div>
    </section>
  );
}

function TerminalWindow({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="overflow-hidden rounded-xl border border-border/50 bg-card shadow-2xl shadow-background/80">
      <div className="flex items-center gap-2 border-b border-border/50 px-4 py-3">
        <div className="flex gap-1.5">
          <div className="size-3 rounded-full bg-red-500/70" />
          <div className="size-3 rounded-full bg-yellow-500/70" />
          <div className="size-3 rounded-full bg-green-500/70" />
        </div>
        <span className="ml-2 text-xs text-muted-foreground">{title}</span>
      </div>
      <div className="p-5">{children}</div>
    </div>
  );
}

type Lang = "go" | "node";

const langPatterns: Record<
  Lang,
  { keywords: RegExp; strings: RegExp; comments: RegExp }
> = {
  go: {
    keywords:
      /\b(package|import|func|type|struct|for|range|if|return|var|const|defer|go|select|case|switch|default|break|continue|fallthrough|chan|map|interface|nil|err|any|error)\b/g,
    strings: /("(?:[^"\\]|\\.)*"|`[^`]*`)/g,
    comments: /(\/\/.*)/g,
  },
  node: {
    keywords:
      /\b(import|from|export|const|let|var|function|async|await|for|of|if|else|return|new|class|throw|try|catch|finally|null|undefined|true|false|this|typeof|process)\b/g,
    strings: /("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|`[^`]*`)/g,
    comments: /(\/\/.*)/g,
  },
};

function highlight(code: string, lang: Lang) {
  const { keywords, strings, comments } = langPatterns[lang];

  const parts: {
    text: string;
    type: "keyword" | "string" | "comment" | "plain";
  }[] = [];
  let last = 0;

  const allMatches: {
    index: number;
    length: number;
    type: "keyword" | "string" | "comment";
    text: string;
  }[] = [];

  for (const m of code.matchAll(comments)) {
    allMatches.push({
      index: m.index!,
      length: m[0].length,
      type: "comment",
      text: m[0],
    });
  }
  for (const m of code.matchAll(strings)) {
    allMatches.push({
      index: m.index!,
      length: m[0].length,
      type: "string",
      text: m[0],
    });
  }
  for (const m of code.matchAll(keywords)) {
    allMatches.push({
      index: m.index!,
      length: m[0].length,
      type: "keyword",
      text: m[0],
    });
  }

  allMatches.sort((a, b) => a.index - b.index);

  const used = new Set<number>();
  const filtered = allMatches.filter((m) => {
    for (let i = m.index; i < m.index + m.length; i++) {
      if (used.has(i)) return false;
    }
    for (let i = m.index; i < m.index + m.length; i++) {
      used.add(i);
    }
    return true;
  });

  for (const m of filtered) {
    if (m.index > last) {
      parts.push({ text: code.slice(last, m.index), type: "plain" });
    }
    parts.push({ text: m.text, type: m.type });
    last = m.index + m.length;
  }
  if (last < code.length) {
    parts.push({ text: code.slice(last), type: "plain" });
  }

  return parts;
}

function HighlightedCode({ code, lang }: { code: string; lang: Lang }) {
  const parts = highlight(code, lang);
  const colorMap = {
    keyword: "text-purple-400",
    string: "text-amber-300",
    comment: "text-muted-foreground/50 italic",
    plain: "text-card-foreground/90",
  };

  return (
    <pre className="overflow-x-auto p-5 text-[13px] leading-relaxed">
      <code>
        {parts.map((p, i) => (
          <span key={i} className={colorMap[p.type]}>
            {p.text}
          </span>
        ))}
      </code>
    </pre>
  );
}

const examples: {
  id: string;
  label: string;
  description: string;
  go: { title: string; code: string };
  node: { title: string; code: string };
}[] = [
  {
    id: "simple-chat",
    label: "Simple Chat",
    description:
      "A minimal single-agent setup. One LLM agent, no tools — just define, run, and stream.",
    go: {
      title: "simple_chat/main.go",
      code: `c := client.New("http://localhost:8080")

agent := &agentdv1.Agent{
\tName: "assistant",
\tAgentType: &agentdv1.Agent_Llm{
\t\tLlm: &agentdv1.LlmAgent{
\t\t\tModel:       "gemini-2.5-flash",
\t\t\tInstruction: "You are a helpful, concise assistant.",
\t\t},
\t},
}

for ev, err := range c.Run(ctx, agent, "Explain concurrency vs parallelism") {
\tif err != nil {
\t\tlog.Fatal(err) // structured ErrorResponse with code + retryable flag
\t}
\tfmt.Print(ev.OutputChunk.Content)
}`,
    },
    node: {
      title: "simple-chat/index.ts",
      code: `const c = new Client("http://localhost:8080")

const agent = {
  name: "assistant",
  llm: {
    model: "gemini-2.5-flash",
    instruction: "You are a helpful, concise assistant.",
  },
}

try {
  for await (const ev of c.run(agent, "Explain concurrency vs parallelism")) {
    process.stdout.write(ev.outputChunk.content)
  }
} catch (err) {
  console.error(err) // AgentdError with .code and .retryable
}`,
    },
  },
  {
    id: "calculator",
    label: "Calculator (Tools)",
    description:
      "Client-side tool execution. Tools run on your machine — the server never sees the implementation, only the schema.",
    go: {
      title: "calculator/main.go",
      code: `c := client.New("http://localhost:8080")

type MathInput struct {
\tA float64 \`json:"a" jsonschema:"description=First operand"\`
\tB float64 \`json:"b" jsonschema:"description=Second operand"\`
}

client.AddTool(c, "add", "Add two numbers", func(_ context.Context, in MathInput) (any, error) {
\treturn in.A + in.B, nil
})

client.AddTool(c, "multiply", "Multiply two numbers", func(_ context.Context, in MathInput) (any, error) {
\treturn in.A * in.B, nil
})

// ... subtract, divide, sqrt

agent := &agentdv1.Agent{
\tName: "calculator",
\tAgentType: &agentdv1.Agent_Llm{
\t\tLlm: &agentdv1.LlmAgent{
\t\t\tModel:       "gemini-2.5-flash",
\t\t\tInstruction: "Use the math tools for every operation. Show your work.",
\t\t\tTools:       []*agentdv1.Tool{c.Tool("add"), c.Tool("multiply") /* ... */},
\t\t},
\t},
}

for ev, err := range c.Run(ctx, agent, "What is (12.5 * 4) + (100 / 8) - 3.75?") {
\tif err != nil {
\t\tlog.Fatal(err)
\t}
\tfmt.Print(ev.OutputChunk.Content)
}`,
    },
    node: {
      title: "calculator/index.ts",
      code: `const c = new Client("http://localhost:8080")

c.addTool("add", "Add two numbers", {
  a: { type: "number", description: "First operand" },
  b: { type: "number", description: "Second operand" },
}, async ({ a, b }) => a + b)

c.addTool("multiply", "Multiply two numbers", {
  a: { type: "number", description: "First operand" },
  b: { type: "number", description: "Second operand" },
}, async ({ a, b }) => a * b)

// ... subtract, divide, sqrt

const agent = {
  name: "calculator",
  llm: {
    model: "gemini-2.5-flash",
    instruction: "Use the math tools for every operation. Show your work.",
    tools: [c.tool("add"), c.tool("multiply") /* ... */],
  },
}

for await (const ev of c.run(agent, "What is (12.5 * 4) + (100 / 8) - 3.75?")) {
  process.stdout.write(ev.outputChunk.content)
}`,
    },
  },
  {
    id: "multi-agent",
    label: "Multi-Agent",
    description:
      "Agent composition with sub-agent delegation. An orchestrator delegates research to a sub-agent, then writes an article from the findings.",
    go: {
      title: "multi_agent/main.go",
      code: `c := client.New("http://localhost:8080")

client.AddTool(c, "web_search", "Search the web", searchHandler)
client.AddTool(c, "fact_check", "Verify a claim", factCheckHandler)

agent := &agentdv1.Agent{
\tName: "article_pipeline",
\tAgentType: &agentdv1.Agent_Llm{
\t\tLlm: &agentdv1.LlmAgent{
\t\t\tModel:       "gemini-3-pro-preview",
\t\t\tInstruction: "Delegate research to the researcher, then write a short article.",
\t\t\tTools:       []*agentdv1.Tool{c.Tool("web_search"), c.Tool("fact_check")},
\t\t\tSubAgents: []*agentdv1.Agent{
\t\t\t\t{
\t\t\t\t\tName: "researcher",
\t\t\t\t\tAgentType: &agentdv1.Agent_Llm{
\t\t\t\t\t\tLlm: &agentdv1.LlmAgent{
\t\t\t\t\t\t\tModel:       "gemini-2.5-flash",
\t\t\t\t\t\t\tInstruction: "Search and fact-check, then summarize findings.",
\t\t\t\t\t\t\tTools:       []*agentdv1.Tool{c.Tool("web_search"), c.Tool("fact_check")},
\t\t\t\t\t\t},
\t\t\t\t\t},
\t\t\t\t},
\t\t\t},
\t\t},
\t},
}

for ev, err := range c.Run(ctx, agent, "Write about quantum computing") {
\tif err != nil {
\t\tlog.Fatal(err)
\t}
\t// ev.OutputChunk.AgentPath tells you which agent produced each chunk
\tfmt.Print(ev.OutputChunk.Content)
}`,
    },
    node: {
      title: "multi-agent/index.ts",
      code: `const c = new Client("http://localhost:8080")

c.addTool("web_search", "Search the web", searchSchema, searchHandler)
c.addTool("fact_check", "Verify a claim", factCheckSchema, factCheckHandler)

const agent = {
  name: "article_pipeline",
  llm: {
    model: "gemini-3-pro-preview",
    instruction: "Delegate research to the researcher, then write a short article.",
    tools: [c.tool("web_search"), c.tool("fact_check")],
    subAgents: [{
      name: "researcher",
      llm: {
        model: "gemini-2.5-flash",
        instruction: "Search and fact-check, then summarize findings.",
        tools: [c.tool("web_search"), c.tool("fact_check")],
      },
    }],
  },
}

for await (const ev of c.run(agent, "Write about quantum computing")) {
  // ev.outputChunk.agentPath tells you which agent produced each chunk
  process.stdout.write(ev.outputChunk.content)
}`,
    },
  },
  {
    id: "sequential",
    label: "Sequential Pipeline",
    description:
      "Run agents in strict order. A researcher gathers facts, then a writer produces the final article — each step feeds into the next.",
    go: {
      title: "sequential_pipeline/main.go",
      code: `c := client.New("http://localhost:8080")

client.AddTool(c, "web_search", "Search the web", searchHandler)

researcher := &agentdv1.Agent{
\tName: "researcher",
\tAgentType: &agentdv1.Agent_Llm{
\t\tLlm: &agentdv1.LlmAgent{
\t\t\tModel:       "gemini-2.5-flash",
\t\t\tInstruction: "Research the topic thoroughly using web_search.",
\t\t\tTools:       []*agentdv1.Tool{c.Tool("web_search")},
\t\t},
\t},
}

writer := &agentdv1.Agent{
\tName: "writer",
\tAgentType: &agentdv1.Agent_Llm{
\t\tLlm: &agentdv1.LlmAgent{
\t\t\tModel:       "gemini-2.5-pro",
\t\t\tInstruction: "Write a polished article from the research findings.",
\t\t},
\t},
}

agent := &agentdv1.Agent{
\tName: "pipeline",
\tAgentType: &agentdv1.Agent_Sequential{
\t\tSequential: &agentdv1.SequentialAgent{
\t\t\tAgents: []*agentdv1.Agent{researcher, writer},
\t\t},
\t},
}

for ev, err := range c.Run(ctx, agent, "Write about WebAssembly") {
\tif err != nil {
\t\tlog.Fatal(err)
\t}
\tfmt.Print(ev.OutputChunk.Content)
}`,
    },
    node: {
      title: "sequential-pipeline/index.ts",
      code: `const c = new Client("http://localhost:8080")

c.addTool("web_search", "Search the web", searchSchema, searchHandler)

const researcher = {
  name: "researcher",
  llm: {
    model: "gemini-2.5-flash",
    instruction: "Research the topic thoroughly using web_search.",
    tools: [c.tool("web_search")],
  },
}

const writer = {
  name: "writer",
  llm: {
    model: "gemini-2.5-pro",
    instruction: "Write a polished article from the research findings.",
  },
}

const agent = {
  name: "pipeline",
  sequential: { agents: [researcher, writer] },
}

for await (const ev of c.run(agent, "Write about WebAssembly")) {
  process.stdout.write(ev.outputChunk.content)
}`,
    },
  },
  {
    id: "parallel",
    label: "Parallel Fan-out",
    description:
      "Run agents in parallel. Fan out to multiple specialists and collect all results concurrently.",
    go: {
      title: "parallel_fanout/main.go",
      code: `c := client.New("http://localhost:8080")

client.AddTool(c, "web_search", "Search the web", searchHandler)

mkResearcher := func(name, focus string) *agentdv1.Agent {
\treturn &agentdv1.Agent{
\t\tName: name,
\t\tAgentType: &agentdv1.Agent_Llm{
\t\t\tLlm: &agentdv1.LlmAgent{
\t\t\t\tModel:       "gemini-2.5-flash",
\t\t\t\tInstruction: "Research " + focus + " aspects of the topic.",
\t\t\t\tTools:       []*agentdv1.Tool{c.Tool("web_search")},
\t\t\t},
\t\t},
\t}
}

agent := &agentdv1.Agent{
\tName: "fan_out",
\tAgentType: &agentdv1.Agent_Parallel{
\t\tParallel: &agentdv1.ParallelAgent{
\t\t\tAgents: []*agentdv1.Agent{
\t\t\t\tmkResearcher("tech", "technical"),
\t\t\t\tmkResearcher("market", "market and business"),
\t\t\t\tmkResearcher("legal", "legal and regulatory"),
\t\t\t},
\t\t},
\t},
}

for ev, err := range c.Run(ctx, agent, "Analyze the state of AI regulation") {
\tif err != nil {
\t\tlog.Fatal(err)
\t}
\tfmt.Printf("[%s] %s", ev.OutputChunk.AgentPath, ev.OutputChunk.Content)
}`,
    },
    node: {
      title: "parallel-fanout/index.ts",
      code: `const c = new Client("http://localhost:8080")

c.addTool("web_search", "Search the web", searchSchema, searchHandler)

const mkResearcher = (name, focus) => ({
  name,
  llm: {
    model: "gemini-2.5-flash",
    instruction: "Research " + focus + " aspects of the topic.",
    tools: [c.tool("web_search")],
  },
})

const agent = {
  name: "fan_out",
  parallel: {
    agents: [
      mkResearcher("tech", "technical"),
      mkResearcher("market", "market and business"),
      mkResearcher("legal", "legal and regulatory"),
    ],
  },
}

for await (const ev of c.run(agent, "Analyze the state of AI regulation")) {
  console.log("[" + ev.outputChunk.agentPath + "] " + ev.outputChunk.content)
}`,
    },
  },
  {
    id: "loop",
    label: "Loop Agent",
    description:
      "Iterate until done. A drafter writes, a reviewer critiques — repeat up to N times until the output meets quality standards.",
    go: {
      title: "loop_refiner/main.go",
      code: `c := client.New("http://localhost:8080")

drafter := &agentdv1.Agent{
\tName: "drafter",
\tAgentType: &agentdv1.Agent_Llm{
\t\tLlm: &agentdv1.LlmAgent{
\t\t\tModel:       "gemini-2.5-flash",
\t\t\tInstruction: "Write or revise the essay based on any prior feedback.",
\t\t},
\t},
}

reviewer := &agentdv1.Agent{
\tName: "reviewer",
\tAgentType: &agentdv1.Agent_Llm{
\t\tLlm: &agentdv1.LlmAgent{
\t\t\tModel:       "gemini-2.5-pro",
\t\t\tInstruction: "Critique the draft. Be specific about what to improve.",
\t\t},
\t},
}

agent := &agentdv1.Agent{
\tName: "refiner",
\tAgentType: &agentdv1.Agent_Loop{
\t\tLoop: &agentdv1.LoopAgent{
\t\t\tAgents:        []*agentdv1.Agent{drafter, reviewer},
\t\t\tMaxIterations: 3,
\t\t},
\t},
}

for ev, err := range c.Run(ctx, agent, "Write an essay on climate change") {
\tif err != nil {
\t\tlog.Fatal(err)
\t}
\tfmt.Print(ev.OutputChunk.Content)
}`,
    },
    node: {
      title: "loop-refiner/index.ts",
      code: `const c = new Client("http://localhost:8080")

const drafter = {
  name: "drafter",
  llm: {
    model: "gemini-2.5-flash",
    instruction: "Write or revise the essay based on any prior feedback.",
  },
}

const reviewer = {
  name: "reviewer",
  llm: {
    model: "gemini-2.5-pro",
    instruction: "Critique the draft. Be specific about what to improve.",
  },
}

const agent = {
  name: "refiner",
  loop: {
    agents: [drafter, reviewer],
    maxIterations: 3,
  },
}

for await (const ev of c.run(agent, "Write an essay on climate change")) {
  process.stdout.write(ev.outputChunk.content)
}`,
    },
  },
];

function Examples() {
  const [lang, setLang] = useState<Lang>("go");

  return (
    <section className="mx-auto max-w-5xl px-6 pb-28">
      <div className="mb-6 flex items-center justify-between">
        <p className="text-sm font-medium uppercase tracking-widest text-muted-foreground/50">
          Examples
        </p>
        <div className="flex gap-1 rounded-lg border border-border/50 p-1">
          {(["go", "node"] as const).map((l) => (
            <button
              key={l}
              onClick={() => setLang(l)}
              className={cn(
                "rounded-md px-3 py-1 text-xs font-medium transition-colors",
                lang === l
                  ? "bg-muted text-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {l === "go" ? "Go" : "Node"}
            </button>
          ))}
        </div>
      </div>
      <Tabs defaultValue="simple-chat">
        <TabsList className="mb-6">
          {examples.map((ex) => (
            <TabsTrigger key={ex.id} value={ex.id}>
              {ex.label}
            </TabsTrigger>
          ))}
        </TabsList>
        {examples.map((ex) => {
          const variant = ex[lang];
          return (
            <TabsContent key={ex.id} value={ex.id}>
              <p className="mb-4 text-sm text-muted-foreground leading-relaxed">
                {ex.description}
              </p>
              <TerminalWindow title={variant.title}>
                <HighlightedCode code={variant.code} lang={lang} />
              </TerminalWindow>
            </TabsContent>
          );
        })}
      </Tabs>
    </section>
  );
}

const features = [
  {
    icon: RefreshCw,
    title: "Session Resumption",
    description:
      "Pass a session_id to pick up where you left off. Agent state is preserved across connections.",
  },
  {
    icon: AlertTriangle,
    title: "Structured Errors",
    description:
      "7 typed error codes with a retryable flag so clients can decide whether to retry automatically.",
  },
  {
    icon: XCircle,
    title: "Cancellation",
    description:
      "Cancel a specific tool call or the entire generation mid-stream with CancelRequest.",
  },
  {
    icon: BarChart3,
    title: "Usage Tracking",
    description:
      "Token counts, LLM call counts, and estimated cost returned in every EndResponse.",
  },
  {
    icon: Radio,
    title: "Streaming Output",
    description:
      "agent_path on every OutputChunk tells you exactly which agent is speaking at which depth.",
  },
  {
    icon: Cpu,
    title: "4 Supported Models",
    description:
      "gemini-2.5-pro, gemini-2.5-flash, gemini-3-pro-preview, and gemini-3-flash-preview.",
  },
];

function Features() {
  return (
    <section className="mx-auto max-w-5xl px-6 pb-28">
      <p className="mb-8 text-center text-sm font-medium uppercase tracking-widest text-muted-foreground/50">
        Features
      </p>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {features.map((f) => (
          <div
            key={f.title}
            className="rounded-xl border border-border/50 bg-card p-6"
          >
            <div className="mb-3 flex items-center gap-2">
              <f.icon className="size-4 text-muted-foreground" />
              <h3 className="text-sm font-semibold text-foreground">
                {f.title}
              </h3>
            </div>
            <p className="text-sm leading-relaxed text-muted-foreground">
              {f.description}
            </p>
          </div>
        ))}
      </div>
    </section>
  );
}
