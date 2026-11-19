# Example plugin and protocol

This standalone Go project provides test functions only. It makes no API calls
and has no external dependencies. Build from the repository root:

```powershell
go -C example_plugin build -o example_plugin.exe .
```

Set the following in the root `config.toml`, then start the assistant normally:

```toml
[plugins]
paths = ["example_plugin/example_plugin.exe"]
call_timeout_seconds = 30
```

Paths are executable paths, with no shell command parsing or arguments. Absolute
paths and paths relative to the assistant's working directory are supported. Plugins inherit
that working directory and environment. Empty `paths` disables plugins. Omitting
the timeout, or setting it to zero, uses 30 seconds. A negative timeout is invalid.
The repository's example config enables the Brave Search plugin; build it before
using that config (see [its setup guide](../brave_search_plugin/README.md)).

The example exposes:

| Model | Tool | Arguments | Result |
| --- | --- | --- | --- |
| Frontend | `Test_echo` | `{"text":"hello"}` | `{"text":"hello"}` |
| Worker | `Test_add` | `{"a":2,"b":3}` | `{"sum":5}` |
| Worker | `Test_wait` | `{"seconds":0}` | `{"finished":true}` |
| Worker | `Test_fail` | `{}` | Intentional JSON-RPC error |

`Test_wait` accepts up to 120 seconds; use a value above your configured timeout
to test failure handling. A timed-out plugin is stopped and remains unavailable
until the assistant restarts. Other plugins keep running.

## Transport and registration

Each stdin/stdout message is a single UTF-8 JSON object followed by a newline
(NDJSON). Do not pretty-print across multiple lines. Incoming plugin messages
must be smaller than 1 MiB. Write diagnostics to stderr; stdout is reserved for
the protocol. JSON-RPC batches and plugin-initiated requests after registration
are not supported. Unsolicited `plugin/notify` notifications are supported.

Immediately after launch, the plugin must send a JSON-RPC 2.0 request with method
`plugin/register` within **5 seconds**. Its ID may be a string or number. The
following is formatted for readability; send it on one line:

```json
{
  "jsonrpc": "2.0",
  "id": "register",
  "method": "plugin/register",
  "params": {
    "name": "Test",
    "version": "1.0.0",
    "front_end_tools": [
      {
        "name": "echo",
        "description": "Return the supplied test text without performing any action.",
        "parameters": {
          "type": "object",
          "properties": {"text": {"type": "string"}},
          "required": ["text"],
          "additionalProperties": false
        }
      }
    ],
    "back_end_tools": []
  }
}
```

the assistant acknowledges accepted registration with the same ID:

```json
{"jsonrpc":"2.0","id":"register","result":{"registered":true}}
```

Wait for this acknowledgement before handling calls. Invalid registrations are
reported to the assistant with instructions to inform the user; their processes
are stopped and their tools are omitted. A registration failure does not prevent
other plugins from loading. Rejected registrations are handled by disconnecting
the process rather than sending a registration error response.

Tool parameters use standard object JSON Schema, including `type`, `properties`,
and optional `required`. Constraints such as `anyOf`, `minLength`, and numeric
bounds are retained for the model. the assistant checks the top-level schema shape; each
plugin must validate arguments and enforce its own constraints when called.
Frontend schemas cannot declare a parameter named `tool`, which is reserved for
dispatch. All tools need a nonempty description.

## Names and model formats

Every model-facing tool is named `<plugin namespace>_<tool name>`, such as
`Spotify_play`. Plugin namespaces replace runs of characters outside
`A-Z`, `a-z`, `0-9`, `_`, and `-` with `_` and trim leading/trailing `_`.
An empty namespace is invalid. Tool names must already use that character set.
Combined names cannot exceed 64 characters.

Duplicate namespaces and tool-name collisions are checked without regard to
case, including names that normalize to the same namespace or collide with
built-in tools. A collision rejects the entire later plugin. A plugin may expose
the same tool name to both frontend and backend; these have separate registries.

The frontend receives flattened examples plus tool descriptions and complete
parameter schemas in its system prompt. For example:

```json
{"speech":null,"tools":[{"tool":"Test_echo","text":"hello"}]}
```

Workers receive standard function definitions and call the same namespaced names.
Neither model needs to know which executable implements a tool.

## Calls and responses

the assistant sends `tools/call` with a generated numeric ID. `name` is the original
unprefixed tool name. `target` is `front_end` or `back_end`, identifying which
registered list the tool belongs to. `arguments` is an object:

```json
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","target":"front_end","arguments":{"text":"hello"}}}
```

The plugin must respond with the same ID and exactly one of `result` or `error`.
`result` may be any JSON value, including `null`:

```json
{"jsonrpc":"2.0","id":1,"result":{"text":"hello"}}
```

An ordinary tool failure should return a JSON-RPC error and keep the process alive:

```json
{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"Test failure","data":{"details":"Optional JSON details"}}}
```

Calls are serialized within each plugin, including frontend and worker calls.
Separate plugin processes can run concurrently. The call timeout includes time
waiting for that plugin's current call. If a queued call times out before being
sent, it fails without stopping the currently running call. A timeout during a
sent call, malformed response, unexpected response ID, broken pipe, or process
exit stops that plugin. All waiting callers receive failures. Stopping a process
cannot undo actions it has already performed. the assistant closes plugins on normal exit.

Frontend calls run outside the conversation lock. Their results/errors are
appended in call order to the conversation using its existing `[From: system]`
tag and copied into worker context. A follow-up is requested when speech,
transcription, inference, and playback permit it. Workers wait for each response
and receive results/errors as the corresponding standard tool message before
their next inference.

## Unsolicited updates

After the registration acknowledgement, a plugin can send an update at any time,
including while idle or before a pending tool response. Send a JSON-RPC
notification with method `plugin/notify`, **no `id` field**, and params containing
a nonempty `message` string plus optional arbitrary JSON `data`:

```json
{"jsonrpc":"2.0","method":"plugin/notify","params":{"message":"The song changed.","data":{"song":"Can't Sleep Love","artist":"Pentatonix"}}}
```

The assistant does not acknowledge notifications. The host adds the registered plugin name;
payloads cannot choose their source identity. Updates appear in conversation
history as `[From: Plugin: Spotify]` and are copied into the context given to
future workers. Existing workers keep their own conversation snapshots.
The frontend processes updates when available and decides whether to speak;
routine state changes may stay silent. Updates do not interrupt an active
inference, user speech, transcription, or playback, and do not wake a sleeping
assistant. Updates received during sleep are processed on wakeup.

Notifications do not reset tool-call timeouts. A pending queue holds 128 events
across all plugins. If a burst fills it before the host can consume it, additional
events are dropped with a stderr diagnostic so tool replies can still be read.
Prefer meaningful state changes over frequent polling updates. Malformed
notifications stop the plugin. Plugins writing from multiple goroutines must
serialize complete JSON lines with a mutex or one output goroutine; see `send`
in `main.go`.

To demonstrate independent events, rebuild the example plugin and set this
environment variable in the terminal used to launch the assistant:

```powershell
$env:EXAMPLE_PLUGIN_UPDATE_INTERVAL = '20s'
```

The example sends a timer event every 20 seconds, even with no tool calls. Its
optional `data` contains a sequence number. Disable events by unsetting it:

```powershell
Remove-Item Env:EXAMPLE_PLUGIN_UPDATE_INTERVAL
```

Run the host integration tests from the repository root with `go test ./...`.
They also build and exercise this nested example project over actual stdio.
