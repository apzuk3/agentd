// This example demonstrates ADK session state — the key-value scratchpad that
// agents and tools share during a conversation.
//
// What it shows:
//
//  1. Initial state seeding — WithInitialState() populates the ADK session
//     before the agent starts. The agent's instruction uses {key} placeholders
//     (e.g. {cart_item_count}) that ADK replaces with current state values on
//     every LLM call, so the model always sees up-to-date context.
//
//  2. Stateful tools — Tools that accept *client.ToolContext instead of
//     context.Context can call ctx.SetState(key, value) to push state changes
//     back into the ADK session. These deltas travel over the wire inside the
//     ToolCallResponse and are applied server-side, exactly as if a native ADK
//     tool had modified state directly.
//
//  3. Stateless tools — Tools using plain context.Context work as before, with
//     no state overhead.
//
//  4. OutputKey — Setting LlmAgent.OutputKey causes ADK to automatically save
//     the agent's final text response into that state key, making it available
//     in result.FinalState alongside tool-written values.
//
//  5. FinalState — After the run completes, result.FinalState contains the
//     accumulated state from all StateUpdate events streamed during the session.
//
// The scenario: a shopping assistant that searches a product catalog, adds items
// to a cart (updating cart totals via state), and applies a discount code.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"

	"github.com/apzuk3/agentd/pkg/client"

	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
)

type Product struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

var catalog = []Product{
	{ID: "p1", Name: "Wireless Mouse", Price: 29.99},
	{ID: "p2", Name: "Mechanical Keyboard", Price: 89.99},
	{ID: "p3", Name: "USB-C Hub", Price: 49.99},
	{ID: "p4", Name: "Monitor Stand", Price: 39.99},
	{ID: "p5", Name: "Webcam HD", Price: 59.99},
}

func main() {
	clnt := client.New("http://localhost:8080",
		// Stateless tool — uses plain context.Context
		client.MustTool("search_products", "Search the product catalog by keyword",
			func(ctx context.Context, input struct {
				Query string `json:"query" jsonschema:"description=search keyword"`
			}) (string, error) {
				var matches []Product
				for _, p := range catalog {
					matches = append(matches, p)
				}
				b, _ := json.Marshal(matches)
				return string(b), nil
			},
		),

		// Stateful tool — uses *client.ToolContext to update cart state
		client.MustTool("add_to_cart", "Add a product to the shopping cart",
			func(ctx *client.ToolContext, input struct {
				ProductID string `json:"product_id" jsonschema:"description=product ID to add"`
			}) (string, error) {
				var product *Product
				for _, p := range catalog {
					if p.ID == input.ProductID {
						product = &p
						break
					}
				}
				if product == nil {
					return fmt.Sprintf("Product %s not found", input.ProductID), nil
				}

				prev, _ := ctx.GetState("cart_total")
				prevTotal, _ := prev.(float64)
				prevCount, _ := func() (any, bool) { return ctx.GetState("cart_item_count") }()
				prevCountN, _ := prevCount.(float64)

				ctx.SetState("cart_total", prevTotal+product.Price)
				ctx.SetState("cart_item_count", prevCountN+1)
				ctx.SetState("last_added_item", product.Name)

				return fmt.Sprintf("Added %s ($%.2f) to cart", product.Name, product.Price), nil
			},
		),

		// Stateful tool — applies a random discount
		client.MustTool("apply_discount", "Apply a random discount to the cart",
			func(ctx *client.ToolContext, input struct {
				Code string `json:"code" jsonschema:"description=discount code"`
			}) (string, error) {
				pct := 10 + rand.IntN(21) // 10-30%
				ctx.SetState("discount_percent", pct)
				ctx.SetState("discount_code", input.Code)
				return fmt.Sprintf("Discount code %q applied: %d%% off!", input.Code, pct), nil
			},
		),
	)

	agent := &agentdv1.Agent{
		Name:        "shopping_assistant",
		Description: "A shopping assistant that manages a cart with stateful tools",
		AgentType: &agentdv1.Agent_Llm{
			Llm: &agentdv1.LlmAgent{
				Model: client.ModelGemini25Flash,
				Instruction: `You are a shopping assistant. Help users browse and add items to their cart.

Current cart state:
- Items in cart: {cart_item_count}
- Cart total: ${cart_total}
- Last item added: {last_added_item}
- Discount: {discount_percent}% (code: {discount_code})

Always search products first, then add the best match to the cart.
If the user mentions a discount code, apply it.`,
				ToolNames: []string{"search_products", "add_to_cart", "apply_discount"},
				OutputKey: "assistant_response",
			},
		},
	}

	fmt.Println("=== Stateful Shopping Assistant ===")
	fmt.Println()

	// Seed initial state and run
	result, err := clnt.Run(context.Background(), agent,
		"Find me a good mouse and a keyboard, add them both to my cart. Also apply discount code SAVE20.",
		client.WithInitialState(map[string]any{
			"cart_item_count": 0,
			"cart_total":      0.0,
			"last_added_item": "none",
			"discount_percent": 0,
			"discount_code":   "",
		}),
		client.WithGeminiAPIKey("YOUR_KEY"), // or set GEMINI_API_KEY env var
	)
	if err != nil {
		log.Fatalf("Run failed: %v", err)
	}

	fmt.Println(result.Output)
	fmt.Println()
	fmt.Println("=== Final State ===")
	for k, v := range result.FinalState {
		fmt.Printf("  %s = %v\n", k, v)
	}
	fmt.Println()
	if usage := result.UsageSummary; usage != nil {
		fmt.Printf("LLM calls: %d, total tokens: %d\n",
			usage.LlmCalls, usage.GetTotalUsage().GetTotalTokens())
	}
}
