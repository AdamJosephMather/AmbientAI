# Brave Search plugin

This standalone Go plugin exposes the backend-only `Brave_search` tool to Zoe's
worker. It returns structured web results with no LLM calls or generated summaries.
The worker interprets results and reports useful findings through `inject_context`.

## Setup

From the repository root:

```powershell
go -C brave_search_plugin build -o brave_search_plugin.exe .
Copy-Item brave_search_plugin/config_example.toml brave_search_plugin/config.toml
```

If `config.toml` already exists, edit it instead of overwriting your settings.
Set `api_key` in that file to a Brave Search API key with Web Search access.
The real config is ignored by Git. Get a key from the
[Brave Search API dashboard](https://api-dashboard.search.brave.com/app/keys).
Alternatively, set the key in the environment used to launch Zoe:

```powershell
$env:BRAVE_SEARCH_API_KEY = 'your-key'
```

The environment key overrides the file. By default, the plugin reads `config.toml`
**beside its executable**, regardless of Zoe's working directory. Optional
`BRAVE_SEARCH_CONFIG` selects another path; relative overrides use Zoe's working
directory. Configuration is read once at startup; restart Zoe after edits.

Add the executable to Zoe's root config, preserving other plugin paths:

```toml
[plugins]
paths = ["brave_search_plugin/brave_search_plugin.exe"]
call_timeout_seconds = 30
```

The default HTTP timeout is 20 seconds. Keep it shorter than the host's plugin
call timeout. Optional settings include `country`, `search_lang`, and `safesearch`
(`off`, `moderate`, or `strict`). Empty country/language values use Brave's
defaults. See `config_example.toml`.

A missing key allows registration, but search calls return an actionable
configuration error until a key is supplied and Zoe restarts. Invalid TOML or
invalid timeout/safesearch settings stop the process at startup with a stderr
diagnostic.

## Tool

Worker call arguments:

```json
{"query":"Go release notes site:go.dev","count":5,"offset":0,"freshness":"py"}
```

| Argument | Meaning |
| --- | --- |
| `query` | Required terms; up to 600 characters and 75 words. |
| `count` | Results per page, 1–20; defaults to 5. |
| `offset` | Page number, 0–9; defaults to 0. Keep `count` fixed when paging. |
| `freshness` | Optional `pd`, `pw`, `pm`, `py`, or `YYYY-MM-DDtoYYYY-MM-DD`. |

Responses contain `query`, optional `altered_query`, `more_results_available`,
and `results`. Results contain title, URL, description, and available extra
snippets, relative age, page date, and language. Empty searches return
`results: []`. Page dates may mean publication or modification, not event dates.

The plugin does not fetch individual pages or use Brave's AI summarization APIs.
Search snippets are external data; the worker's instructions tell it to interpret
results and cite source URLs. HTTP failures, rate limits, missing credentials,
malformed responses, oversized results, and network timeouts become JSON-RPC tool
errors. The plugin stays alive after ordinary search errors and does not retry
automatically. Upstream error bodies are not forwarded and API keys are not logged.
Responses are limited to 8 MiB upstream and 512 KiB after selecting tool fields.

The implementation follows Brave's official
[Web Search API reference](https://api-dashboard.search.brave.com/api-reference/web/search/get),
using `GET /res/v1/web/search` with `X-Subscription-Token`, requesting extra snippets
and selecting web results.

See the [example plugin protocol](../example_plugin/README.md) for registration,
calls, and unsolicited updates. Brave currently sends tool replies only.

## Verification

```powershell
go -C brave_search_plugin test -race ./...
go test -race ./...
```

Plugin tests use local HTTP servers and fake credentials. Host tests build and
launch the plugin to check registration and missing-key behavior over stdio.
They do not call Brave's live API or incur charges.
