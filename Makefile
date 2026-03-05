.PHONY: build run default

# Default target
default: build run

# Build using goreleaser snapshot
build:
	goreleaser release --snapshot --clean

# Run the built Docker image
# For snapshot builds, goreleaser creates tags like ghcr.io/apzuk3/agentd:latest-amd64
# We'll use the latest-amd64 tag which is always created
run:
	docker run -p 8080:8080 -e GEMINI_API_KEY=${GEMINI_API_KEY} -e ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY} -e OPENAI_API_KEY=${OPENAI_API_KEY} -e TAVILY_API_KEY=${TAVILY_API_KEY} ghcr.io/apzuk3/agentd:latest-amd64
