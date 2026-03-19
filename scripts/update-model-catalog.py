#!/usr/bin/env python3
"""
Regenerate internal/ai/models/catalog.json and embedding_catalog.json.

Run periodically to update model pricing and add new models.
This script is the single source of truth for model metadata.

Usage:
    python3 scripts/update-model-catalog.py

OpenRouter Provider Assessment
==============================
OpenRouter exposes an OpenAI-compatible chat completions API at
https://openrouter.ai/api/v1. Because the existing openai provider already
supports a configurable BaseURL, OpenRouter models can reuse the
"openai-completions" api identifier with baseUrl set to
"https://openrouter.ai/api/v1".

No new Go provider code is needed. The caller should:
  1. Set OPENROUTER_API_KEY in the environment.
  2. When constructing the openai.Config for OpenRouter models, pass:
       Config{BaseURL: "https://openrouter.ai/api/v1", APIKey: os.Getenv("OPENROUTER_API_KEY")}
  3. Optionally set the HTTP-Referer and X-Title headers per OpenRouter's docs
     using the model's Headers field in the catalog.

Models in the catalog under the "openrouter" provider key use
api="openai-completions" so they will be dispatched to the OpenAI provider.
The baseUrl in each model entry directs requests to OpenRouter's endpoint.
The application layer should detect provider="openrouter" and supply the
OPENROUTER_API_KEY instead of OPENAI_API_KEY when constructing the provider.

Pricing Sources (last verified 2026-03-18)
==========================================
Anthropic:  https://platform.claude.com/docs/en/about-claude/pricing
OpenAI:     https://openai.com/api/pricing/ and https://pecollective.com/tools/openai-api-pricing/
Google:     https://ai.google.dev/gemini-api/docs/pricing
OpenRouter: https://openrouter.ai/models (individual model pages)
Cohere:     https://cohere.com/pricing and https://docs.cohere.com/docs/models
"""

import json
import os
import sys
from collections import OrderedDict

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
PROJECT_ROOT = os.path.dirname(SCRIPT_DIR)
CATALOG_PATH = os.path.join(PROJECT_ROOT, "internal", "ai", "models", "catalog.json")
EMBEDDING_CATALOG_PATH = os.path.join(PROJECT_ROOT, "internal", "ai", "models", "embedding_catalog.json")


def cost(inp, out, cache_read, cache_write):
    """Helper to build a cost dict. All values are USD per million tokens."""
    return {"input": inp, "output": out, "cacheRead": cache_read, "cacheWrite": cache_write}


def model(name, api, base_url, reasoning, inputs, cost_dict, ctx_window, max_tokens, compat=None):
    """Build a model entry dict."""
    entry = OrderedDict()
    entry["name"] = name
    entry["api"] = api
    entry["baseUrl"] = base_url
    entry["reasoning"] = reasoning
    entry["input"] = inputs
    entry["cost"] = cost_dict
    entry["contextWindow"] = ctx_window
    entry["maxTokens"] = max_tokens
    if compat:
        entry["compat"] = compat
    return entry


# ---------------------------------------------------------------------------
# Anthropic models
# Source: https://platform.claude.com/docs/en/about-claude/pricing
#         https://platform.claude.com/docs/en/about-claude/models/overview
# Cache pricing: 5-min cache write = 1.25x input, cache read = 0.1x input
# ---------------------------------------------------------------------------
ANTHROPIC_API = "anthropic-messages"
ANTHROPIC_URL = "https://api.anthropic.com"
TEXT_IMAGE = ["text", "image"]

anthropic_models = OrderedDict()

# Claude Opus 4.6 -- $5/$25, 1M ctx, 128k out
anthropic_models["claude-opus-4-6"] = model(
    "Claude Opus 4.6", ANTHROPIC_API, ANTHROPIC_URL, True, TEXT_IMAGE,
    cost(5, 25, 0.5, 6.25), 1_000_000, 128_000,
)

# Claude Opus 4.5 -- $5/$25, 200k ctx, 64k out
anthropic_models["claude-opus-4-5-20251101"] = model(
    "Claude Opus 4.5", ANTHROPIC_API, ANTHROPIC_URL, True, TEXT_IMAGE,
    cost(5, 25, 0.5, 6.25), 200_000, 64_000,
)

# Claude Opus 4.1 -- $15/$75, 200k ctx, 32k out
anthropic_models["claude-opus-4-1-20250805"] = model(
    "Claude Opus 4.1", ANTHROPIC_API, ANTHROPIC_URL, True, TEXT_IMAGE,
    cost(15, 75, 1.5, 18.75), 200_000, 32_000,
)

# Claude Sonnet 4.6 -- $3/$15, 1M ctx, 64k out
anthropic_models["claude-sonnet-4-6"] = model(
    "Claude Sonnet 4.6", ANTHROPIC_API, ANTHROPIC_URL, True, TEXT_IMAGE,
    cost(3, 15, 0.3, 3.75), 1_000_000, 64_000,
)

# Claude Sonnet 4.5 -- $3/$15, 200k ctx (1M with beta), 64k out
anthropic_models["claude-sonnet-4-5-20250929"] = model(
    "Claude Sonnet 4.5", ANTHROPIC_API, ANTHROPIC_URL, True, TEXT_IMAGE,
    cost(3, 15, 0.3, 3.75), 200_000, 64_000,
)

# Claude Sonnet 4 -- $3/$15, 200k ctx, 16k out (legacy, still needed by tests)
anthropic_models["claude-sonnet-4-20250514"] = model(
    "Claude Sonnet 4", ANTHROPIC_API, ANTHROPIC_URL, True, TEXT_IMAGE,
    cost(3, 15, 0.3, 3.75), 200_000, 16_384,
)

# Claude Opus 4 -- $15/$75, 200k ctx, 32k out
anthropic_models["claude-opus-4-20250514"] = model(
    "Claude Opus 4", ANTHROPIC_API, ANTHROPIC_URL, True, TEXT_IMAGE,
    cost(15, 75, 1.5, 18.75), 200_000, 32_000,
)

# Claude Haiku 4.5 -- $1/$5, 200k ctx, 64k out
anthropic_models["claude-haiku-4-5-20251001"] = model(
    "Claude Haiku 4.5", ANTHROPIC_API, ANTHROPIC_URL, True, TEXT_IMAGE,
    cost(1, 5, 0.1, 1.25), 200_000, 64_000,
)

# Claude Haiku 3.5 -- $0.80/$4, 200k ctx, 8k out
anthropic_models["claude-3-5-haiku-20241022"] = model(
    "Claude Haiku 3.5", ANTHROPIC_API, ANTHROPIC_URL, False, TEXT_IMAGE,
    cost(0.8, 4, 0.08, 1), 200_000, 8_192,
)


# ---------------------------------------------------------------------------
# OpenAI models
# Source: https://openai.com/api/pricing/
# Cache pricing: cached input is typically 50-75% off input price
# ---------------------------------------------------------------------------
OPENAI_API = "openai-completions"
OPENAI_URL = "https://api.openai.com/v1"

OPENAI_REASONING_COMPAT = OrderedDict([
    ("supportsReasoningEffort", True),
    ("supportsDeveloperRole", True),
    ("maxTokensField", "max_completion_tokens"),
])

openai_models = OrderedDict()

# GPT-4.1 family
openai_models["gpt-4.1"] = model(
    "GPT-4.1", OPENAI_API, OPENAI_URL, False, TEXT_IMAGE,
    cost(2, 8, 0.5, 2), 1_000_000, 32_768,
)
openai_models["gpt-4.1-mini"] = model(
    "GPT-4.1 Mini", OPENAI_API, OPENAI_URL, False, TEXT_IMAGE,
    cost(0.4, 1.6, 0.1, 0.4), 1_000_000, 32_768,
)
openai_models["gpt-4.1-nano"] = model(
    "GPT-4.1 Nano", OPENAI_API, OPENAI_URL, False, ["text"],
    cost(0.1, 0.4, 0.025, 0.1), 1_000_000, 32_768,
)

# GPT-4o family (legacy, still referenced in tests)
openai_models["gpt-4o"] = model(
    "GPT-4o", OPENAI_API, OPENAI_URL, False, TEXT_IMAGE,
    cost(2.5, 10, 1.25, 2.5), 128_000, 16_384,
)
openai_models["gpt-4o-mini"] = model(
    "GPT-4o Mini", OPENAI_API, OPENAI_URL, False, TEXT_IMAGE,
    cost(0.15, 0.6, 0.075, 0.15), 128_000, 16_384,
)

# o-series reasoning models
openai_models["o3"] = model(
    "o3", OPENAI_API, OPENAI_URL, True, TEXT_IMAGE,
    cost(2, 8, 0.5, 2), 200_000, 100_000,
    compat=OPENAI_REASONING_COMPAT,
)
openai_models["o3-mini"] = model(
    "o3 Mini", OPENAI_API, OPENAI_URL, True, ["text"],
    cost(1.1, 4.4, 0.55, 1.1), 200_000, 100_000,
    compat=OPENAI_REASONING_COMPAT,
)
openai_models["o4-mini"] = model(
    "o4 Mini", OPENAI_API, OPENAI_URL, True, TEXT_IMAGE,
    cost(1.1, 4.4, 0.275, 1.1), 200_000, 100_000,
    compat=OPENAI_REASONING_COMPAT,
)
openai_models["o1"] = model(
    "o1", OPENAI_API, OPENAI_URL, True, TEXT_IMAGE,
    cost(15, 60, 7.5, 15), 200_000, 100_000,
    compat=OPENAI_REASONING_COMPAT,
)

# GPT-5 family
openai_models["gpt-5"] = model(
    "GPT-5", OPENAI_API, OPENAI_URL, False, TEXT_IMAGE,
    cost(1.25, 10, 0.125, 1.25), 128_000, 32_768,
)
openai_models["gpt-5-mini"] = model(
    "GPT-5 Mini", OPENAI_API, OPENAI_URL, False, TEXT_IMAGE,
    cost(0.25, 2, 0.025, 0.25), 128_000, 32_768,
)


# ---------------------------------------------------------------------------
# Google Gemini models
# Source: https://ai.google.dev/gemini-api/docs/pricing
# Cache pricing: cache read = 0.25x input for most models
# ---------------------------------------------------------------------------
GOOGLE_API = "google-genai"
GOOGLE_URL = "https://generativelanguage.googleapis.com"

google_models = OrderedDict()

# Gemini 2.5 Pro -- $1.25/$10 (<=200k), 1M ctx, 65k out
google_models["gemini-2.5-pro"] = model(
    "Gemini 2.5 Pro", GOOGLE_API, GOOGLE_URL, True, TEXT_IMAGE,
    cost(1.25, 10, 0.3125, 1.25), 1_000_000, 65_536,
)

# Gemini 2.5 Flash -- $0.30/$2.50, 1M ctx, 65k out
google_models["gemini-2.5-flash"] = model(
    "Gemini 2.5 Flash", GOOGLE_API, GOOGLE_URL, True, TEXT_IMAGE,
    cost(0.3, 2.5, 0.03, 0.3), 1_048_576, 65_536,
)

# Gemini 2.5 Flash-Lite -- $0.10/$0.40, 1M ctx, 65k out
google_models["gemini-2.5-flash-lite"] = model(
    "Gemini 2.5 Flash-Lite", GOOGLE_API, GOOGLE_URL, False, TEXT_IMAGE,
    cost(0.1, 0.4, 0.01, 0.1), 1_048_576, 65_536,
)

# Gemini 2.0 Flash (deprecated) -- $0.10/$0.40, 1M ctx, 8k out
google_models["gemini-2.0-flash"] = model(
    "Gemini 2.0 Flash", GOOGLE_API, GOOGLE_URL, False, TEXT_IMAGE,
    cost(0.1, 0.4, 0.025, 0.1), 1_048_576, 8_192,
)


# ---------------------------------------------------------------------------
# OpenRouter models (open-weight / third-party via OpenRouter)
# Source: https://openrouter.ai/models (individual model pages)
# OpenRouter is OpenAI-compatible, so api = "openai-completions"
# Cache pricing not supported by OpenRouter, set to 0
# ---------------------------------------------------------------------------
OPENROUTER_API = "openai-completions"
OPENROUTER_URL = "https://openrouter.ai/api/v1"
NO_CACHE = cost  # same function, just semantic alias

openrouter_models = OrderedDict()

# DeepSeek
openrouter_models["deepseek/deepseek-chat"] = model(
    "DeepSeek V3", OPENROUTER_API, OPENROUTER_URL, False, ["text"],
    cost(0.32, 0.89, 0, 0), 163_840, 16_384,
)
openrouter_models["deepseek/deepseek-r1"] = model(
    "DeepSeek R1", OPENROUTER_API, OPENROUTER_URL, True, ["text"],
    cost(0.7, 2.5, 0, 0), 64_000, 16_384,
)
openrouter_models["deepseek/deepseek-v3.2"] = model(
    "DeepSeek V3.2", OPENROUTER_API, OPENROUTER_URL, False, ["text"],
    cost(0.26, 0.38, 0, 0), 163_840, 16_384,
)

# Kimi (Moonshot AI)
openrouter_models["moonshotai/kimi-k2.5"] = model(
    "Kimi K2.5", OPENROUTER_API, OPENROUTER_URL, False, TEXT_IMAGE,
    cost(0.45, 2.2, 0, 0), 262_144, 16_384,
)
openrouter_models["moonshotai/kimi-k2-thinking"] = model(
    "Kimi K2 Thinking", OPENROUTER_API, OPENROUTER_URL, True, ["text"],
    cost(0.47, 2, 0, 0), 131_072, 16_384,
)

# Meta Llama 4
openrouter_models["meta-llama/llama-4-scout"] = model(
    "Llama 4 Scout", OPENROUTER_API, OPENROUTER_URL, False, TEXT_IMAGE,
    cost(0.08, 0.3, 0, 0), 512_000, 16_384,
)
openrouter_models["meta-llama/llama-4-maverick"] = model(
    "Llama 4 Maverick", OPENROUTER_API, OPENROUTER_URL, False, TEXT_IMAGE,
    cost(0.15, 0.6, 0, 0), 1_048_576, 16_384,
)

# Qwen
openrouter_models["qwen/qwen3-235b-a22b"] = model(
    "Qwen3 235B", OPENROUTER_API, OPENROUTER_URL, True, ["text"],
    cost(0.455, 1.82, 0, 0), 131_072, 8_192,
)

# Mistral
openrouter_models["mistralai/mistral-large-2512"] = model(
    "Mistral Large 3", OPENROUTER_API, OPENROUTER_URL, False, TEXT_IMAGE,
    cost(0.5, 1.5, 0, 0), 262_144, 16_384,
)
openrouter_models["mistralai/mistral-medium-3"] = model(
    "Mistral Medium 3", OPENROUTER_API, OPENROUTER_URL, False, ["text"],
    cost(0.4, 2, 0, 0), 131_072, 16_384,
)
openrouter_models["mistralai/mistral-small-2603"] = model(
    "Mistral Small 4", OPENROUTER_API, OPENROUTER_URL, False, ["text"],
    cost(0.15, 0.6, 0, 0), 262_144, 16_384,
)

# MiniMax
openrouter_models["minimax/minimax-m2.5"] = model(
    "MiniMax M2.5", OPENROUTER_API, OPENROUTER_URL, False, ["text"],
    cost(0.2, 1.2, 0, 0), 196_608, 16_384,
)


# ---------------------------------------------------------------------------
# Embedding models
# ---------------------------------------------------------------------------

def embedding_model(id_, name, api, provider, base_url, max_input, default_dims,
                    max_dims, min_dims, max_batch, dim_ctrl, task_type, cost_per_mtok):
    """Build an embedding model entry."""
    return OrderedDict([
        ("id", id_),
        ("name", name),
        ("api", api),
        ("provider", provider),
        ("baseUrl", base_url),
        ("maxInputTokens", max_input),
        ("defaultDims", default_dims),
        ("maxDims", max_dims),
        ("minDims", min_dims),
        ("maxBatchSize", max_batch),
        ("supportsDimCtrl", dim_ctrl),
        ("supportsTaskType", task_type),
        ("cost", {"perMTok": cost_per_mtok}),
    ])


embedding_models = [
    # OpenAI embeddings -- source: https://openai.com/api/pricing/
    embedding_model(
        "text-embedding-3-small", "Text Embedding 3 Small",
        "openai-embeddings", "openai", "https://api.openai.com/v1",
        8192, 1536, 1536, 256, 2048, True, False, 0.02,
    ),
    embedding_model(
        "text-embedding-3-large", "Text Embedding 3 Large",
        "openai-embeddings", "openai", "https://api.openai.com/v1",
        8192, 3072, 3072, 256, 2048, True, False, 0.13,
    ),
    embedding_model(
        "text-embedding-ada-002", "Ada v2",
        "openai-embeddings", "openai", "https://api.openai.com/v1",
        8192, 1536, 1536, 1536, 2048, False, False, 0.1,
    ),
    # Google embeddings -- source: https://ai.google.dev/gemini-api/docs/pricing
    embedding_model(
        "gemini-embedding-001", "Gemini Embedding 001",
        "google-embeddings", "google", "https://generativelanguage.googleapis.com/v1beta",
        2048, 3072, 3072, 128, 100, True, True, 0.0,
    ),
    # Cohere embeddings -- source: https://cohere.com/pricing
    embedding_model(
        "embed-v4.0", "Cohere Embed v4",
        "cohere-embeddings", "cohere", "https://api.cohere.com/v2",
        128000, 1536, 1536, 256, 96, True, True, 0.12,
    ),
    embedding_model(
        "embed-english-v3.0", "Cohere Embed English v3",
        "cohere-embeddings", "cohere", "https://api.cohere.com/v2",
        512, 1024, 1024, 1024, 96, False, True, 0.1,
    ),
    embedding_model(
        "embed-multilingual-v3.0", "Cohere Embed Multilingual v3",
        "cohere-embeddings", "cohere", "https://api.cohere.com/v2",
        512, 1024, 1024, 1024, 96, False, True, 0.1,
    ),
]


# ---------------------------------------------------------------------------
# Output
# ---------------------------------------------------------------------------

def main():
    catalog = OrderedDict()
    catalog["anthropic"] = anthropic_models
    catalog["openai"] = openai_models
    catalog["google"] = google_models
    catalog["openrouter"] = openrouter_models

    with open(CATALOG_PATH, "w") as f:
        json.dump(catalog, f, indent=2)
        f.write("\n")
    print(f"Wrote {CATALOG_PATH}")
    print(f"  {sum(len(v) for v in catalog.values())} chat models across {len(catalog)} providers")

    with open(EMBEDDING_CATALOG_PATH, "w") as f:
        json.dump(embedding_models, f, indent=2)
        f.write("\n")
    print(f"Wrote {EMBEDDING_CATALOG_PATH}")
    print(f"  {len(embedding_models)} embedding models")


if __name__ == "__main__":
    main()
