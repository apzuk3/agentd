import { create } from "@bufbuild/protobuf";
import { AgentdClient } from "../src/client";
import { AgentSchema } from "../../gen/proto/ts/agentd/v1/types_pb";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

const topic = process.argv.slice(2).join(" ") || "the future of quantum computing";

const client = new AgentdClient("http://localhost:8080");

// ── Register tools ──────────────────────────────────────────────────────────

interface SearchInput {
  query: string;
}

client.addTool<SearchInput>(
  "web_search",
  "Search the web for information on a topic",
  async (input) => {
    console.log(`[tool] web_search("${input.query}")`);
    await sleep(200);
    return {
      results: [
        {
          title: "Quantum Computing Breakthroughs 2026",
          snippet:
            "Recent advances in error correction have brought fault-tolerant quantum computing closer to reality. IBM and Google both achieved milestones in qubit coherence times.",
        },
        {
          title: "Practical Applications of Quantum Computing",
          snippet:
            "Drug discovery, cryptography, and materials science are the three most promising near-term application areas for quantum computers.",
        },
        {
          title: "Quantum vs Classical Computing",
          snippet:
            "While quantum computers excel at specific problem classes like optimization and simulation, they are unlikely to replace classical computers for general-purpose tasks.",
        },
      ],
    };
  },
  {
    type: "object",
    properties: {
      query: { type: "string", description: "The search query to look up" },
    },
    required: ["query"],
  },
);

interface FactCheckInput {
  claim: string;
}

client.addTool<FactCheckInput>(
  "fact_check",
  "Verify whether a factual claim is accurate",
  async (input) => {
    console.log(`[tool] fact_check("${input.claim}")`);
    await sleep(100);
    return {
      verdict: "plausible",
      confidence: 0.85,
      note: "Claim aligns with current published research as of early 2026.",
    };
  },
  {
    type: "object",
    properties: {
      claim: {
        type: "string",
        description: "A factual claim to verify",
      },
    },
    required: ["claim"],
  },
);

// ── Build agent tree ────────────────────────────────────────────────────────

const agent = create(AgentSchema, {
  name: "article_pipeline",
  description:
    "A multi-agent pipeline that researches a topic and writes an article",
  agentType: {
    case: "llm",
    value: {
      model: "gemini-3-pro-preview",
      instruction:
        "You are an orchestrator. Delegate research to the researcher sub-agent, then write a polished short article based on the gathered facts. Keep it under 300 words.",
      tools: [client.tool("web_search")!, client.tool("fact_check")!],
      subAgents: [
        create(AgentSchema, {
          name: "researcher",
          description:
            "Gathers information by searching the web and fact-checking claims",
          agentType: {
            case: "llm",
            value: {
              model: "gemini-2.5-flash",
              instruction:
                "You are a research assistant. Use the web_search tool to find relevant information about the given topic. Use the fact_check tool to verify key claims. Summarize your findings clearly.",
              tools: [
                client.tool("web_search")!,
                client.tool("fact_check")!,
              ],
            },
          },
        }),
      ],
    },
  },
});

// ── Run ─────────────────────────────────────────────────────────────────────

const userPrompt = `Write a short article about ${topic}`;
console.log(`Prompt: ${userPrompt}\n`);

for await (const event of client.run(agent, userPrompt)) {
  switch (event.type) {
    case "output_chunk": {
      if (event.agentPath.length > 0) {
        const agentName = event.agentPath[event.agentPath.length - 1];
        if (event.last) {
          process.stdout.write(`\n--- [${agentName} done] ---\n\n`);
          break;
        }
      }
      process.stdout.write(event.content);
      break;
    }

    case "error":
      console.error(`agent error [${event.code}]: ${event.message}`);
      process.exit(1);

    case "end":
      console.log();
      if (event.usageSummary) {
        const u = event.usageSummary;
        console.log(
          `--- Usage: ${u.llmCalls} LLM calls, ${u.totalUsage?.totalTokens ?? 0} total tokens, $${u.estimatedCost.toFixed(6)} ---`,
        );
      }
      break;
  }
}
