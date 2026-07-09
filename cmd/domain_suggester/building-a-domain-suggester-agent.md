# Building a domain suggester agent

*by Aram Petrosyan*

I know y'all cannot start building a side project unless you secure a domain name for it, and neither can I. As part of my exploration of building AI agents and experimentation, I decided to build an agent that will take an idea and suggest an available domain name.

In this post I'll walk you through it step by step: we'll start with a tiny agent that just *suggests* names, then give it a tool so it can actually *check* whether those names are free, and finally run the whole thing in the terminal and watch it work.

The full code lives in a single file, [`main.go`](./main.go), and it's small enough to read in one sitting. Let's build it up piece by piece.

---

## Step 1: An agent that suggests names

Before we worry about availability, let's get the simplest possible thing working: an agent that takes your idea and brainstorms domain names. No tools, no API calls, just a model with a personality and a job.

```go
model, err := provider.New("gemini-3.5-flash", "")
if err != nil {
    log.Fatal(err)
}

agent, err := llmagent.New(llmagent.Config{
    Model:       model,
    Description: "A domain suggester agent that suggests domain names for a given company name",
    Instruction: "You are a domain suggester agent that suggests domain names given a user idea. " +
        "You MUST refuse to discuss anything other than domain names.",
})
if err != nil {
    log.Fatal(err)
}

ui := terminal.NewUI(agent)
if err := ui.StartChat(context.Background()); err != nil {
    log.Fatal(err)
}
```

That's it. We pick a model, hand it a `Description` (who it is) and an `Instruction` (how it should behave), and drop it into a terminal chat. The `Instruction` is doing a lot of the heavy lifting here: it gives the agent a job *and* a boundary so it doesn't wander off into writing poetry about your startup.

And honestly? It works. Ask it for "a coffee subscription for developers" and it'll happily fire back `brewedcode.com`, `devbeans.io`, `commitcaffeine.dev`, and so on.

But here's the catch. It's *guessing*. The model has no idea whether any of those names are actually available — it's pattern-matching on what a good domain name sounds like. Half of them are probably taken. That's the gap we're going to close.

> **[VIDEO PLACEHOLDER — Step 1]**
> *Short clip: the bare agent suggesting names from an idea, with a note that none of them are verified.*

---

## Step 2: A tool that can actually check

To know whether a domain is *really* available, we need to ask someone who knows: a registrar API. I used [Fastly's Domain Research API](https://www.fastly.com/documentation/reference/api/domain-management/domain-research), which has a neat little `status` endpoint that tells you the registration status of a single domain.

So the first thing I wrote was a plain Go function — no AI involved yet — that calls that endpoint and tells me whether a domain is free:

```go
// checkDomainAvailable reports whether domain is available for registration via
// Fastly's Domain Research Status API. A Precise (registry-level) check is
// performed. If apiKey is empty it falls back to the FASTLY_API_TOKEN env var.
func checkDomainAvailable(ctx context.Context, apiKey, domain string) (DomainStatus, error) {
    // ... normalize input, resolve the API key, build the request ...

    req.Header.Set("Fastly-Key", apiKey)
    req.Header.Set("Accept", "application/json")

    // ... call the API, decode the JSON ...

    return DomainStatus{
        Domain:    payload.Domain,
        Zone:      payload.Zone,
        Status:    payload.Status,
        Available: slices.Contains(strings.Fields(payload.Status), "inactive"),
    }, nil
}
```

The result comes back as a small struct:

```go
type DomainStatus struct {
    Domain    string
    Zone      string
    Status    string // space-delimited status flags from Fastly
    Tags      string
    Available bool   // true when the registry reports the domain as inactive (free to register)
}
```

The one bit worth pointing out is how we decide "available". Fastly returns `status` as a space-delimited list of flags like `"undelegated inactive"`. In their world, `inactive` means *nobody has registered this, it's yours for the taking*. So that single line:

```go
Available: slices.Contains(strings.Fields(payload.Status), "inactive"),
```

is the whole availability rule. If `inactive` is in the list, the domain is free.

A couple of small things I baked in to keep it honest:

- The API key never gets hardcoded — it's passed in, or read from the `FASTLY_API_TOKEN` environment variable.
- The domain gets lowercased and trimmed, so `  Example.COM ` and `example.com` behave the same.

> **[VIDEO PLACEHOLDER — Step 2]**
> *Short clip: calling the Fastly endpoint for a taken domain vs. a free one, showing the raw status flags.*

---

## Step 3: Handing the tool to the agent

Here's where it gets fun. A regular Go function is useful to *us*, but the agent can't call it on its own. We need to wrap it as a **tool** the model is allowed to invoke.

In ADK that means describing the function's inputs and giving it a name and description the model can reason about:

```go
// checkDomainArgs is the input the model supplies when invoking the tool.
type checkDomainArgs struct {
    Domain string `json:"domain" jsonschema_description:"Fully-qualified domain name to check, e.g. example.com"`
}

func newDomainAvailabilityTool() (tool.Tool, error) {
    return functiontool.New(functiontool.Config{
        Name:        "check_domain_availability",
        Description: "Check whether a domain name is available for registration using Fastly's Domain Research API. Returns whether the domain is available along with the raw registry status flags.",
    }, func(ctx tool.Context, args checkDomainArgs) (DomainStatus, error) {
        return checkDomainAvailable(ctx, "", args.Domain)
    })
}
```

That `Description` and the `jsonschema_description` on the field are basically the agent's instructions for *when* and *how* to use the tool. Write them like you're explaining the function to a new teammate.

Then we register the tool with the agent and nudge the instruction so it knows to actually use it:

```go
domainTool, err := newDomainAvailabilityTool()
if err != nil {
    log.Fatal(err)
}

agent, err := llmagent.New(llmagent.Config{
    Model:       model,
    Description: "A domain suggester agent that suggests domain names for a given company name",
    Instruction: "You are a domain suggester agent that suggests domain names given a user idea. " +
        "When the user asks whether a domain is available, or after proposing names, use the " +
        "check_domain_availability tool to verify registration availability before reporting back. " +
        "You MUST refuse to discuss anything other than domain names.",
    Tools: []tool.Tool{domainTool},
})
```

### How the result is different

This is the part I find genuinely cool. Same model, same idea, but the behaviour changes completely once the tool is in play:

| | Without the tool (Step 1) | With the tool (Step 3) |
|---|---|---|
| What it does | Guesses names that *sound* available | Suggests names, then **checks each one** |
| Confidence | "These might be free?" | "These three are actually free, the rest are taken" |
| Source of truth | The model's intuition | Fastly's registry data |
| Failure mode | You go register it and... it's gone | You get a verified shortlist |

Instead of a wishlist, you get a shortlist you can act on. The agent will propose a batch of names, quietly call `check_domain_availability` on each, and come back having already filtered out the ones that are taken.

> **[VIDEO PLACEHOLDER — Step 3]**
> *Side-by-side: same prompt, agent without the tool vs. with the tool, highlighting the verified results.*

---

## Step 4: Run it in the terminal

Time to actually talk to it. First, set your Fastly API token so the tool can authenticate:

```bash
export FASTLY_API_TOKEN="your-fastly-api-token"
```

Then run the agent:

```bash
go run ./cmd/domain_suggester/main.go
```

You'll drop into an interactive chat. Try something like:

```
> I'm building a coffee subscription service for developers
```

Watch what happens: the agent suggests a handful of names, and then — without you asking — calls the `check_domain_availability` tool on each candidate, and reports back which ones are genuinely free to register. Ask it a direct question like *"is devbeans.io available?"* and it'll go straight to the tool and tell you.

> **[VIDEO PLACEHOLDER — Step 4]**
> *Full demo: starting the agent in the terminal, giving it an idea, and watching it suggest + verify domains live.*

---

## Wrapping up

That's the whole thing. We went from a model that *guesses* domain names to an agent that *verifies* them, and the only real ingredient was a small Go function wrapped as a tool. A few takeaways I'm holding onto:

- **Start dumb, then add tools.** The bare agent was useful immediately. The tool just made it trustworthy.
- **The tool's name and description are prompt engineering.** That's how the model decides when to reach for it.
- **Keep the function boring.** Plain `net/http`, key from the environment, a tiny struct out. The agent doesn't need it to be clever — it needs it to be reliable.

Next on my list: have it suggest names *and* surface aftermarket pricing for the good-but-taken ones. But that's a story for another post.

Now go secure that domain before someone else does.
