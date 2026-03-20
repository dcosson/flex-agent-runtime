#!/usr/bin/env python3
"""
Regenerate internal/ai/models/catalog.json and embedding_catalog.json.

Run periodically to update model pricing and add new models.
This script dynamically discovers models from provider APIs (primarily
OpenRouter) and supplements with direct provider APIs when API keys are
available.

Usage:
    python3 scripts/update-model-catalog.py
    python3 scripts/update-model-catalog.py --force  # skip freshness check

    Set these env vars for richer data (all optional):
      ANTHROPIC_API_KEY   - enrich Anthropic models with capabilities
      GEMINI_API_KEY      - enrich Google models with token limits
      OPENAI_API_KEY      - (minimal benefit, just model ID validation)

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

Pricing Sources
===============
Primary: OpenRouter API (https://openrouter.ai/api/v1/models) - no auth required
Supplementary: Anthropic, Google, OpenAI direct APIs (require API keys)
"""

import hashlib
import json
import os
import re
import sys
import tempfile
import time
import urllib.request
import urllib.error
from collections import OrderedDict
from datetime import datetime, timezone

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
PROJECT_ROOT = os.path.dirname(SCRIPT_DIR)
CATALOG_PATH = os.path.join(PROJECT_ROOT, "internal", "ai", "models", "catalog.json")
EMBEDDING_CATALOG_PATH = os.path.join(PROJECT_ROOT, "internal", "ai", "models", "embedding_catalog.json")

CACHE_DIR = os.path.join(tempfile.gettempdir(), "model-catalog-cache")
CACHE_TTL_SECONDS = 3600  # 1 hour

FRESHNESS_TTL_SECONDS = 3600  # 1 hour — skip if catalog was updated recently


# ---------------------------------------------------------------------------
# Provider configs — emitted into the "providers" section of catalog.json.
# These define the ProviderConfig structs that Go code registers.
# ---------------------------------------------------------------------------

PROVIDER_CONFIGS = OrderedDict([
    ("anthropic", OrderedDict([
        ("apiClientType", "anthropic-messages"),
        ("baseUrl", "https://api.anthropic.com"),
        ("keyEnvVars", ["ANTHROPIC_API_KEY"]),
        ("providerSpecific", {"apiVersion": "2023-06-01"}),
    ])),
    ("google", OrderedDict([
        ("apiClientType", "google-genai"),
        ("embeddingApiClientType", "google-embeddings"),
        ("baseUrl", "https://generativelanguage.googleapis.com"),
        ("keyEnvVars", ["GOOGLE_API_KEY", "GEMINI_API_KEY"]),
        ("providerSpecific", {"apiVersion": "v1beta"}),
    ])),
    ("openai", OrderedDict([
        ("apiClientType", "openai-completions"),
        ("embeddingApiClientType", "openai-embeddings"),
        ("baseUrl", "https://api.openai.com/v1"),
        ("keyEnvVars", ["OPENAI_API_KEY"]),
    ])),
    ("openrouter", OrderedDict([
        ("apiClientType", "openai-completions"),
        ("baseUrl", "https://openrouter.ai/api/v1"),
        ("keyEnvVars", ["OPENROUTER_API_KEY"]),
    ])),
    ("cohere", OrderedDict([
        ("embeddingApiClientType", "cohere-embeddings"),
        ("baseUrl", "https://api.cohere.com/v2"),
        ("keyEnvVars", ["COHERE_API_KEY"]),
    ])),
])


# ---------------------------------------------------------------------------
# API caching
# ---------------------------------------------------------------------------

def _cache_path(url):
    """Return a filesystem cache path for a URL."""
    h = hashlib.sha256(url.encode()).hexdigest()[:16]
    return os.path.join(CACHE_DIR, f"{h}.json")


def _fetch_cached(url, headers=None):
    """Fetch a URL with 1-hour caching to disk. Returns parsed JSON or None."""
    os.makedirs(CACHE_DIR, exist_ok=True)
    cp = _cache_path(url)

    if os.path.exists(cp):
        age = time.time() - os.path.getmtime(cp)
        if age < CACHE_TTL_SECONDS:
            with open(cp) as f:
                return json.load(f)

    req = urllib.request.Request(url)
    if headers:
        for k, v in headers.items():
            req.add_header(k, v)

    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            data = json.loads(resp.read())
        with open(cp, "w") as f:
            json.dump(data, f)
        return data
    except (urllib.error.URLError, urllib.error.HTTPError, json.JSONDecodeError, OSError) as e:
        print(f"  WARNING: Failed to fetch {url}: {e}", file=sys.stderr)
        # Try stale cache
        if os.path.exists(cp):
            print(f"  Using stale cache for {url}", file=sys.stderr)
            with open(cp) as f:
                return json.load(f)
        return None


# ---------------------------------------------------------------------------
# OpenRouter fetching
# ---------------------------------------------------------------------------

def fetch_openrouter_models():
    """Fetch all models from OpenRouter. Returns list of model dicts."""
    print("Fetching models from OpenRouter...")
    data = _fetch_cached("https://openrouter.ai/api/v1/models")
    if data is None:
        return []
    models = data.get("data", data) if isinstance(data, dict) else data
    print(f"  Got {len(models)} models from OpenRouter")
    return models


# ---------------------------------------------------------------------------
# Anthropic enrichment
# ---------------------------------------------------------------------------

def fetch_anthropic_models():
    """Fetch model list from Anthropic API. Returns dict of id -> model info."""
    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if not api_key:
        print("  Skipping Anthropic API (ANTHROPIC_API_KEY not set)")
        return {}

    print("Fetching models from Anthropic API...")
    data = _fetch_cached(
        "https://api.anthropic.com/v1/models",
        headers={
            "x-api-key": api_key,
            "anthropic-version": "2023-06-01",
        },
    )
    if data is None:
        return {}

    models = data.get("data", []) if isinstance(data, dict) else data
    result = {}
    for m in models:
        result[m["id"]] = m
    print(f"  Got {len(result)} models from Anthropic")
    return result


# ---------------------------------------------------------------------------
# Google enrichment
# ---------------------------------------------------------------------------

def fetch_google_models():
    """Fetch model list from Google Gemini API. Returns dict of short_name -> model info."""
    api_key = os.environ.get("GEMINI_API_KEY")
    if not api_key:
        print("  Skipping Google API (GEMINI_API_KEY not set)")
        return {}

    print("Fetching models from Google API...")
    data = _fetch_cached(
        f"https://generativelanguage.googleapis.com/v1beta/models?key={api_key}",
    )
    if data is None:
        return {}

    models = data.get("models", [])
    result = {}
    for m in models:
        # name is like "models/gemini-2.5-pro", strip prefix
        short = m.get("name", "").replace("models/", "")
        result[short] = m
    print(f"  Got {len(result)} models from Google")
    return result


# ---------------------------------------------------------------------------
# Model filtering
# ---------------------------------------------------------------------------

# Provider prefixes that map to native providers (stripped from model ID).
# Note: api/baseUrl are now in PROVIDER_CONFIGS, not per-model.
NATIVE_PROVIDER_KEYS = {"anthropic", "openai", "google"}

# Models that should stay under the openrouter provider (third-party / open)
OPENROUTER_PROVIDER_KEY = "openrouter"

# Anthropic models on OpenRouter use undated aliases (e.g. "claude-sonnet-4"),
# but our codebase / tests reference the dated snapshot IDs (e.g.
# "claude-sonnet-4-20250514"). This mapping emits both the undated alias AND the
# dated snapshot as separate catalog entries (identical data) so that callers
# using either form find the model.
#
# The canonical_slug from OpenRouter sometimes contains a date, but in a
# rearranged format that doesn't match the Anthropic API model ID. We maintain
# this explicit mapping instead.
ANTHROPIC_DATED_ALIASES = {
    # OpenRouter suffix -> list of dated snapshot IDs to also emit
    "claude-opus-4-6":  [],  # newest, no dated snapshot yet
    "claude-sonnet-4-6": [],  # newest, no dated snapshot yet
    "claude-opus-4.5":  ["claude-opus-4-5-20251101"],
    "claude-sonnet-4.5": ["claude-sonnet-4-5-20250929"],
    "claude-opus-4.1":  ["claude-opus-4-1-20250805"],
    "claude-opus-4":    ["claude-opus-4-20250514"],
    "claude-sonnet-4":  ["claude-sonnet-4-20250514"],
    "claude-haiku-4.5": ["claude-haiku-4-5-20251001"],
    "claude-3.5-haiku": ["claude-3-5-haiku-20241022"],
    "claude-3.5-sonnet": ["claude-3-5-sonnet-20241022"],
    "claude-3.7-sonnet": ["claude-3-7-sonnet-20250219"],
}

# For dated aliases, we may want to override the display name to match the
# original catalog style (e.g. "Claude Sonnet 4" instead of "Claude Sonnet 4.6").
ANTHROPIC_DATED_DISPLAY_NAMES = {
    "claude-opus-4-5-20251101": "Claude Opus 4.5",
    "claude-sonnet-4-5-20250929": "Claude Sonnet 4.5",
    "claude-opus-4-1-20250805": "Claude Opus 4.1",
    "claude-opus-4-20250514": "Claude Opus 4",
    "claude-sonnet-4-20250514": "Claude Sonnet 4",
    "claude-haiku-4-5-20251001": "Claude Haiku 4.5",
    "claude-3-5-haiku-20241022": "Claude Haiku 3.5",
    "claude-3-5-sonnet-20241022": "Claude 3.5 Sonnet",
    "claude-3-7-sonnet-20250219": "Claude 3.7 Sonnet",
}

# OpenRouter uses dots in version numbers (e.g. "claude-sonnet-4.5") but
# the Anthropic API / our catalog uses hyphens (e.g. "claude-sonnet-4-5").
# Normalize OpenRouter alias suffixes to our catalog convention.
def _normalize_anthropic_id(or_suffix):
    """Normalize an OpenRouter Anthropic model suffix to our catalog ID style.

    Examples:
        claude-opus-4.5  -> claude-opus-4-5
        claude-sonnet-4.6 -> claude-sonnet-4-6
        claude-3.5-haiku -> claude-3-5-haiku
        claude-sonnet-4  -> claude-sonnet-4 (no change)
    """
    # Replace version dots with hyphens: "4.5" -> "4-5", "3.5" -> "3-5"
    return re.sub(r'(\d+)\.(\d+)', r'\1-\2', or_suffix)


def _model_id_matches_filter(model_id):
    """Return True if this OpenRouter model ID should be included in our catalog.

    We curate a focused set of models rather than including everything.
    For native providers (anthropic, openai, google) we include current-gen models.
    For openrouter third-party models we include the latest flagship from each family.
    """
    # Anthropic Claude models (all of them - we filter old ones in _should_skip)
    if model_id.startswith("anthropic/claude-"):
        return True

    # OpenAI models - curated list of current-gen models
    if model_id.startswith("openai/"):
        suffix = model_id.split("/", 1)[1]
        # GPT-4.1 family
        if suffix in ("gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano"):
            return True
        # GPT-4o family (still widely used)
        if suffix in ("gpt-4o", "gpt-4o-mini"):
            return True
        # GPT-5 base family (no codex/image/chat/pro variants)
        if suffix in ("gpt-5", "gpt-5-mini", "gpt-5-nano"):
            return True
        # GPT-5.x series - latest base models only
        if suffix in ("gpt-5.1", "gpt-5.2", "gpt-5.3", "gpt-5.4",
                       "gpt-5.4-mini", "gpt-5.4-nano"):
            return True
        # o-series reasoning models
        if suffix in ("o1", "o3", "o3-mini", "o4-mini"):
            return True
        return False

    # Google Gemini - current stable and latest preview only
    if model_id.startswith("google/gemini-"):
        suffix = model_id.split("/", 1)[1]
        # Gemini 2.x stable models
        if suffix in ("gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite",
                       "gemini-2.0-flash", "gemini-2.0-flash-001"):
            return True
        # Gemini 3.x preview (latest generation, skip specialized variants)
        if suffix.startswith("gemini-3"):
            if suffix.endswith(("-customtools", "-image-preview")):
                return False
            return True
        return False

    # DeepSeek - latest of each line only
    if model_id in ("deepseek/deepseek-chat", "deepseek/deepseek-r1",
                     "deepseek/deepseek-v3.2", "deepseek/deepseek-r1-0528"):
        return True

    # Kimi / Moonshot - latest models
    if model_id in ("moonshotai/kimi-k2.5", "moonshotai/kimi-k2-thinking"):
        return True

    # Meta Llama 4
    if model_id in ("meta-llama/llama-4-scout", "meta-llama/llama-4-maverick"):
        return True

    # Qwen - flagship models only
    if model_id in ("qwen/qwen3-235b-a22b", "qwen/qwen3.5-397b-a17b"):
        return True

    # Mistral - latest large/medium/small only
    if model_id in ("mistralai/mistral-large-2512", "mistralai/mistral-medium-3",
                     "mistralai/mistral-small-2603"):
        return True

    # MiniMax - latest
    if model_id in ("minimax/minimax-m2.5", "minimax/minimax-m2.7"):
        return True

    return False


def _should_skip(model_id):
    """Return True for models we explicitly exclude."""
    # Skip :free variants (we want the paid versions)
    if ":free" in model_id:
        return True
    # Skip ":extended" variants
    if ":extended" in model_id:
        return True
    # Skip ":thinking" variants (these are alias modes, not distinct models)
    if model_id.endswith(":thinking"):
        return True
    # Skip old Anthropic models (pre-Claude 3.5)
    if model_id == "anthropic/claude-3-haiku":
        return True
    return False


# ---------------------------------------------------------------------------
# Model classification helpers
# ---------------------------------------------------------------------------

def _is_reasoning_model(or_model):
    """Determine if a model is a native reasoning/thinking model.

    Note: Many models on OpenRouter expose 'include_reasoning' or 'reasoning'
    as supported parameters even if they're not true reasoning models. We use
    a curated list of model ID patterns that are known reasoning models instead.
    """
    model_id = or_model["id"]
    suffix = model_id.split("/", 1)[1] if "/" in model_id else model_id

    # Anthropic: Claude 3.7+ models are reasoning models (extended thinking)
    # Claude 3.5 and older are NOT reasoning models
    if model_id.startswith("anthropic/"):
        if "3.5-" in suffix or "3-haiku" in suffix:
            return False
        # Claude 3.7+ and Claude 4+ are reasoning
        if suffix.startswith("claude-"):
            return True

    # OpenAI o-series are always reasoning
    if suffix.startswith(("o1", "o3", "o4")):
        return True
    # GPT-5+ are reasoning (they support chain of thought)
    if suffix.startswith("gpt-5"):
        return True

    # Google Gemini 2.5+ are reasoning (thinking models)
    if model_id.startswith("google/"):
        if suffix.startswith(("gemini-2.5", "gemini-3")):
            return True
        return False

    # Third-party reasoning models
    if "thinking" in suffix:
        return True
    if suffix in ("deepseek-r1", "deepseek-r1-0528"):
        return True

    return False


def _get_input_modalities(or_model):
    """Extract input modalities for our catalog format."""
    arch = or_model.get("architecture", {})
    input_mods = arch.get("input_modalities", [])
    result = []
    if "text" in input_mods:
        result.append("text")
    if "image" in input_mods:
        result.append("image")
    if not result:
        result = ["text"]
    return result


def _pricing_to_per_million(price_str):
    """Convert OpenRouter per-token price string to per-million-tokens float."""
    if price_str is None:
        return 0.0
    try:
        return float(price_str) * 1_000_000
    except (ValueError, TypeError):
        return 0.0


def _round_price(val):
    """Round a price, keeping up to 6 significant figures to avoid float noise."""
    if val == 0:
        return 0
    # Round to remove float artifacts
    rounded = round(val, 10)
    # If it's a clean number, return as int or simple float
    if rounded == int(rounded) and rounded < 1e15:
        return int(rounded) if int(rounded) == rounded else rounded
    return rounded


def _build_cost(or_model, provider_key):
    """Build cost dict from OpenRouter pricing."""
    pricing = or_model.get("pricing", {})

    input_price = _round_price(_pricing_to_per_million(pricing.get("prompt")))
    output_price = _round_price(_pricing_to_per_million(pricing.get("completion")))

    cache_read = _round_price(_pricing_to_per_million(pricing.get("input_cache_read")))
    cache_write = _round_price(_pricing_to_per_million(pricing.get("input_cache_write")))

    # For Anthropic models, derive cache pricing from known ratios if not provided
    if provider_key == "anthropic" and cache_read == 0 and input_price > 0:
        cache_read = _round_price(input_price * 0.1)
    if provider_key == "anthropic" and cache_write == 0 and input_price > 0:
        cache_write = _round_price(input_price * 1.25)

    # For OpenAI models, derive cache pricing if not fully provided
    if provider_key == "openai":
        if cache_read == 0 and input_price > 0:
            cache_read = _round_price(input_price * 0.5)
        if cache_write == 0 and input_price > 0:
            cache_write = _round_price(input_price)

    # For Google models, always derive cache pricing from known ratios.
    # Google's cache pricing documentation states cache reads are discounted
    # and cache writes cost the same as input tokens. OpenRouter's
    # input_cache_write values represent amortized storage costs which don't
    # match Google's documented per-token pricing, so we derive instead.
    if provider_key == "google" and input_price > 0:
        if cache_read == 0:
            cache_read = _round_price(input_price * 0.25)
        cache_write = _round_price(input_price)

    return {
        "input": input_price,
        "output": output_price,
        "cacheRead": cache_read,
        "cacheWrite": cache_write,
    }


def _make_display_name(or_model):
    """Build a short display name from OpenRouter model name."""
    name = or_model.get("name", "")
    # OpenRouter names are like "Anthropic: Claude Sonnet 4.6" - strip provider prefix
    if ": " in name:
        name = name.split(": ", 1)[1]
    return name


# ---------------------------------------------------------------------------
# Compat fields for specific model families
# ---------------------------------------------------------------------------

OPENAI_REASONING_COMPAT = OrderedDict([
    ("supportsReasoningEffort", True),
    ("supportsDeveloperRole", True),
    ("maxTokensField", "max_completion_tokens"),
])


def _get_compat(model_id, provider_key):
    """Return compat dict if needed for this model, else None."""
    if provider_key != "openai":
        return None
    suffix = model_id
    # OpenAI reasoning models need special compat
    if suffix.startswith(("o1", "o3", "o4")):
        return OPENAI_REASONING_COMPAT
    return None


# ---------------------------------------------------------------------------
# Build catalog from OpenRouter data
# ---------------------------------------------------------------------------

def build_chat_catalog(or_models, anthropic_extra, google_extra):
    """Build the chat model catalog from OpenRouter models.

    Returns a dict of provider_key -> {model_id -> model_entry}.
    Model entries do NOT contain 'api' or 'baseUrl' — those are derived
    from the provider config at load time.
    """
    providers = OrderedDict()
    providers["anthropic"] = OrderedDict()
    providers["google"] = OrderedDict()
    providers["openai"] = OrderedDict()
    providers["openrouter"] = OrderedDict()

    for m in or_models:
        model_id = m["id"]

        if _should_skip(model_id):
            continue
        if not _model_id_matches_filter(model_id):
            continue

        # Determine provider and catalog model ID
        prefix = model_id.split("/", 1)[0] if "/" in model_id else ""
        suffix = model_id.split("/", 1)[1] if "/" in model_id else model_id

        if prefix in NATIVE_PROVIDER_KEYS:
            provider_key = prefix
            # For Anthropic, normalize dots to hyphens in version numbers
            if prefix == "anthropic":
                catalog_id = _normalize_anthropic_id(suffix)
            else:
                catalog_id = suffix
        else:
            provider_key = OPENROUTER_PROVIDER_KEY
            catalog_id = model_id

        # Build model entry — no api or baseUrl, those come from provider config
        entry = OrderedDict()
        entry["name"] = _make_display_name(m)
        entry["reasoning"] = _is_reasoning_model(m)
        entry["input"] = _get_input_modalities(m)
        entry["cost"] = _build_cost(m, provider_key)
        entry["contextWindow"] = m.get("context_length", 0)

        # Max output tokens from top_provider (default to 16384 if missing/None)
        top = m.get("top_provider", {}) or {}
        max_tokens = top.get("max_completion_tokens")
        entry["maxTokens"] = max_tokens if max_tokens is not None else 16384

        compat = _get_compat(catalog_id, provider_key)
        if compat:
            entry["compat"] = compat

        providers[provider_key][catalog_id] = entry

        # For Anthropic models, also emit dated snapshot aliases
        if provider_key == "anthropic" and suffix in ANTHROPIC_DATED_ALIASES:
            for dated_id in ANTHROPIC_DATED_ALIASES[suffix]:
                dated_entry = OrderedDict(entry)
                if dated_id in ANTHROPIC_DATED_DISPLAY_NAMES:
                    dated_entry["name"] = ANTHROPIC_DATED_DISPLAY_NAMES[dated_id]
                providers["anthropic"][dated_id] = dated_entry

    # Enrich from direct Anthropic API
    if anthropic_extra:
        for model_id, info in anthropic_extra.items():
            if model_id in providers["anthropic"]:
                # Could update capabilities, max tokens etc.
                if "max_tokens" in info:
                    # Anthropic API's max_tokens is the output limit
                    pass  # OpenRouter top_provider usually has this right

    # Enrich from direct Google API
    if google_extra:
        for short_name, info in google_extra.items():
            if short_name in providers["google"]:
                # Update context window if Google API has different value
                if "inputTokenLimit" in info:
                    pass  # OpenRouter usually matches

    return providers


# ---------------------------------------------------------------------------
# Embedding models (kept hardcoded - these are not in OpenRouter's models API
# and have provider-specific parameters like dims, batch size, task type support
# that are not discoverable from any API)
# ---------------------------------------------------------------------------

def _embedding_model(id_, name, api, provider, max_input, default_dims,
                     max_dims, min_dims, max_batch, dim_ctrl, task_type, cost_per_mtok):
    """Build an embedding model entry.

    Note: baseUrl is no longer included — it comes from the provider config.
    The 'api' field is included for models whose api differs from the provider's
    embeddingApiClientType (e.g. openrouter models use openai-embeddings but
    openrouter provider doesn't have an embeddingApiClientType).
    """
    return OrderedDict([
        ("id", id_),
        ("name", name),
        ("api", api),
        ("provider", provider),
        ("maxInputTokens", max_input),
        ("defaultDims", default_dims),
        ("maxDims", max_dims),
        ("minDims", min_dims),
        ("maxBatchSize", max_batch),
        ("supportsDimCtrl", dim_ctrl),
        ("supportsTaskType", task_type),
        ("cost", {"perMTok": cost_per_mtok}),
    ])


def build_embedding_catalog():
    """Build the embedding model catalog. These remain hardcoded because embedding
    model metadata (dimensions, batch sizes, task type support) is not available
    from any discovery API."""
    return [
        # OpenAI embeddings -- source: https://openai.com/api/pricing/
        _embedding_model(
            "text-embedding-3-small", "Text Embedding 3 Small",
            "openai-embeddings", "openai",
            8192, 1536, 1536, 256, 2048, True, False, 0.02,
        ),
        _embedding_model(
            "text-embedding-3-large", "Text Embedding 3 Large",
            "openai-embeddings", "openai",
            8192, 3072, 3072, 256, 2048, True, False, 0.13,
        ),
        _embedding_model(
            "text-embedding-ada-002", "Ada v2",
            "openai-embeddings", "openai",
            8192, 1536, 1536, 1536, 2048, False, False, 0.1,
        ),
        # Google embeddings -- source: https://ai.google.dev/gemini-api/docs/pricing
        _embedding_model(
            "gemini-embedding-001", "Gemini Embedding 001",
            "google-embeddings", "google",
            2048, 3072, 3072, 128, 100, True, True, 0.0,
        ),
        # Cohere embeddings -- source: https://cohere.com/pricing
        _embedding_model(
            "embed-v4.0", "Cohere Embed v4",
            "cohere-embeddings", "cohere",
            128000, 1536, 1536, 256, 96, True, True, 0.12,
        ),
        _embedding_model(
            "embed-english-v3.0", "Cohere Embed English v3",
            "cohere-embeddings", "cohere",
            512, 1024, 1024, 1024, 96, False, True, 0.1,
        ),
        _embedding_model(
            "embed-multilingual-v3.0", "Cohere Embed Multilingual v3",
            "cohere-embeddings", "cohere",
            512, 1024, 1024, 1024, 96, False, True, 0.1,
        ),
        # OpenRouter embeddings -- source: https://openrouter.ai/models
        _embedding_model(
            "openai/text-embedding-3-small", "Text Embedding 3 Small (OpenRouter)",
            "openai-embeddings", "openrouter",
            8192, 1536, 1536, 256, 2048, True, False, 0.02,
        ),
        _embedding_model(
            "openai/text-embedding-3-large", "Text Embedding 3 Large (OpenRouter)",
            "openai-embeddings", "openrouter",
            8192, 3072, 3072, 256, 2048, True, False, 0.13,
        ),
        _embedding_model(
            "qwen/qwen3-embedding-8b", "Qwen3 Embedding 8B",
            "openai-embeddings", "openrouter",
            32000, 1024, 1024, 1024, 2048, False, False, 0.01,
        ),
        _embedding_model(
            "qwen/qwen3-embedding-4b", "Qwen3 Embedding 4B",
            "openai-embeddings", "openrouter",
            32768, 1024, 1024, 1024, 2048, False, False, 0.02,
        ),
    ]


# ---------------------------------------------------------------------------
# Validation
# ---------------------------------------------------------------------------

REQUIRED_MODELS = {
    "anthropic": ["claude-sonnet-4-20250514"],
    "openai": ["gpt-4o"],
    "google": ["gemini-2.5-pro", "gemini-2.5-flash"],
}


def validate_catalog(models_by_provider):
    """Check that test-critical models are present in the models section."""
    ok = True
    for provider, model_ids in REQUIRED_MODELS.items():
        if provider not in models_by_provider:
            print(f"  ERROR: Missing provider '{provider}' in models", file=sys.stderr)
            ok = False
            continue
        for mid in model_ids:
            if mid not in models_by_provider[provider]:
                print(f"  ERROR: Missing required model '{mid}' in '{provider}'", file=sys.stderr)
                ok = False
    return ok


# ---------------------------------------------------------------------------
# Freshness check
# ---------------------------------------------------------------------------

def _is_fresh(path):
    """Return True if the catalog at path was updated within FRESHNESS_TTL_SECONDS."""
    if not os.path.exists(path):
        return False
    try:
        with open(path) as f:
            data = json.load(f)
        ts = data.get("lastUpdated", "")
        if not ts:
            return False
        # Parse ISO 8601 timestamp
        updated = datetime.fromisoformat(ts.replace("Z", "+00:00"))
        age = (datetime.now(timezone.utc) - updated).total_seconds()
        return age < FRESHNESS_TTL_SECONDS
    except (json.JSONDecodeError, ValueError, OSError):
        return False


# ---------------------------------------------------------------------------
# Fallback
# ---------------------------------------------------------------------------

def load_existing_catalog(path):
    """Load an existing catalog file if it exists."""
    if os.path.exists(path):
        with open(path) as f:
            return json.load(f)
    return None


# ---------------------------------------------------------------------------
# Output
# ---------------------------------------------------------------------------

def main():
    force = "--force" in sys.argv

    # Freshness check — skip if catalog was recently updated
    if not force and _is_fresh(CATALOG_PATH):
        print(f"Catalog is fresh (updated within {FRESHNESS_TTL_SECONDS}s). Use --force to override.")
        return

    # Fetch from OpenRouter (primary source)
    or_models = fetch_openrouter_models()
    if not or_models:
        print("ERROR: Could not fetch models from OpenRouter.", file=sys.stderr)
        print("Keeping existing catalog files unchanged.", file=sys.stderr)
        sys.exit(1)

    # Optionally fetch from direct provider APIs for enrichment
    anthropic_extra = fetch_anthropic_models()
    google_extra = fetch_google_models()

    # Build chat model data
    models_by_provider = build_chat_catalog(or_models, anthropic_extra, google_extra)

    # Validate
    if not validate_catalog(models_by_provider):
        existing = load_existing_catalog(CATALOG_PATH)
        if existing:
            print("WARNING: Validation failed. Falling back to existing catalog.", file=sys.stderr)
            # Still write the embedding catalog since it's hardcoded
        else:
            print("ERROR: Validation failed and no existing catalog to fall back to.", file=sys.stderr)
            sys.exit(1)
    else:
        # Build full catalog with providers section
        now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        catalog = OrderedDict([
            ("lastUpdated", now),
            ("providers", PROVIDER_CONFIGS),
            ("models", models_by_provider),
        ])

        with open(CATALOG_PATH, "w") as f:
            json.dump(catalog, f, indent=2, sort_keys=True)
            f.write("\n")
        total = sum(len(v) for v in models_by_provider.values())
        print(f"Wrote {CATALOG_PATH}")
        print(f"  {total} chat models across {len(models_by_provider)} providers")
        for provider, models in models_by_provider.items():
            print(f"    {provider}: {len(models)} models")

    # Build and write embedding catalog
    embedding_models = build_embedding_catalog()
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    embedding_catalog = OrderedDict([
        ("lastUpdated", now),
        ("models", embedding_models),
    ])
    with open(EMBEDDING_CATALOG_PATH, "w") as f:
        json.dump(embedding_catalog, f, indent=2, sort_keys=True)
        f.write("\n")
    print(f"Wrote {EMBEDDING_CATALOG_PATH}")
    print(f"  {len(embedding_models)} embedding models")


if __name__ == "__main__":
    main()
