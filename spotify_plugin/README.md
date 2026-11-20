# Spotify plugin

A standalone Go plugin derived from [the example plugin](../example_plugin/README.md).
Both the frontend and backend receive the same six tools. It controls the
official Spotify desktop app through the Web API; it does not stream audio itself.

## Setup for each user

1. Use your own Spotify account to create your own developer app at the
   [Spotify developer dashboard](https://developer.spotify.com/dashboard).
   Every user of this plugin needs their own app/client ID and must authorize
   their own account. Spotify requires Premium for these player APIs and for
   development-mode app owners. If authorizing an additional account against
   an app, Spotify requires that account to be on the app's user allowlist.
   See [quota modes](https://developer.spotify.com/documentation/web-api/concepts/quota-modes).
2. Add **`http://127.0.0.1:8765/callback`** to the app's redirect URIs and enable
   Web API access. Use this exact value in the plugin config. Spotify disallows
   `localhost`; this plugin uses an explicit loopback IP and port.
   See [redirect URI rules](https://developer.spotify.com/documentation/web-api/concepts/redirect_uri).
3. Build the plugin and copy its example config, from the repository root:

   ```powershell
   go -C spotify_plugin build -o spotify_plugin.exe .
   Copy-Item spotify_plugin/config_example.toml spotify_plugin/config.toml
   ```

   On macOS/Linux:

   ```sh
   go -C spotify_plugin build -o spotify_plugin .
   cp spotify_plugin/config_example.toml spotify_plugin/config.toml
   ```

4. Set `client_id` in `spotify_plugin/config.toml`. No client secret is needed:
   authorization uses [PKCE](https://developer.spotify.com/documentation/web-api/tutorials/code-pkce-flow).
   You can override the client ID with `SPOTIFY_CLIENT_ID`. The config lives
   beside the executable regardless of the working directory; set
   `SPOTIFY_PLUGIN_CONFIG` to select another config file. `token_file` is resolved
   relative to that config file.
5. Run login manually before starting the assistant:

   ```powershell
   ./spotify_plugin/spotify_plugin.exe --authorize
   ```

   macOS/Linux: `./spotify_plugin/spotify_plugin --authorize`.
   Complete approval in the browser within five minutes. The listener binds
   only to the configured loopback IP. Tokens refresh automatically and refresh
   token rotations are saved. `tokens.json` is private: do not commit or share
   it. New token files use mode 0600 on Unix; Windows uses the containing
   directory's ACL. If the cache is corrupt or belongs to another client ID,
   delete it and authorize again. Login never runs during plugin registration
   or a model tool call.
6. Install Spotify, log into the same account on this computer, and open it.
   Run `./spotify_plugin/spotify_plugin.exe --list-devices` (omit `.exe` on
   macOS/Linux). This prints the detected name plus available Connect devices.
   If the detected name doesn't match, set `device_name` to this computer's
   exact Connect name. An optional `device_id` overrides name matching; Spotify
   device IDs may change. Verify that any manually configured ID belongs to
   this computer.
7. Add the executable to the host's existing plugin list in root `config.toml`:

   ```toml
   [plugins]
   paths = ["brave_search_plugin/brave_search_plugin.exe", "spotify_plugin/spotify_plugin.exe"]
   call_timeout_seconds = 60
   ```

   Use your platform's executable filenames. Keep other desired plugins in
   `paths`. The plugin's default call budget is 50 seconds, including HTTP
   requests and a 25-second local-device startup wait; the host timeout should
   be greater than `call_timeout_seconds` in the Spotify config. Host timeouts
   also include time queued behind another call to the same plugin.

## Tools on both models

| Tool | Arguments | Behavior |
| --- | --- | --- |
| `Spotify_play` | `{"song_name":"Can't Sleep Love","artist_name":"Pentatonix"}` | Search tracks using both filters and start the first playable result within its album. |
| `Spotify_play` | `{"song_name":"Hello"}` | Search by song title. |
| `Spotify_play` | `{"artist_name":"Adele"}` | Play the first playable track returned for that artist; this does not start an artist radio or discography. |
| `Spotify_play` | `{}` | Resume, with the same device rules as `resume`. |
| `Spotify_resume` | `{}` | Resume current playback context. |
| `Spotify_pause` | `{}` | Pause on the current device. |
| `Spotify_next_song` | `{}` | Skip forward on the current device. |
| `Spotify_prev_song` | `{}` | Skip back; Spotify may restart the current track first. |
| `Spotify_get_current_song` | `{}` | Return song title, artist names, and playing state, including playback on other devices. |

All six tools return only `song_name`, `artist_names`, and `is_playing`:

```json
{"song_name":"Example","artist_names":["Artist"],"is_playing":true}
```

`song_name` is null and `artist_names` is empty when no song is available.
Episodes may have no artists. Play/resume poll for up to two seconds after
Spotify accepts the command, waiting for the requested track and playing state
on the selected device. They never return the old song as confirmation of a
new song request. If confirmation is delayed or a state read fails, the command
still succeeds with the requested song details (when known) and `is_playing`
null. Pause and next/previous also wait up to two seconds: pause waits for
paused state on the same device; skips wait for a changed track or a position
rewind of more than one second (including previous restarting the same song).
When a skip/pause is not yet confirmed, both `song_name` and `is_playing` are
null and `artist_names` is empty, rather than returning the old song. A
same-song restart near the beginning can be indistinguishable from stale state
and remains pending. Null means accepted but not yet confirmed, not failure;
the assistant must not automatically retry any playback command. No control
command is repeated just to obtain confirmation. IDs, URIs, device details, progress, context, and
action restrictions stay internal. Search results and names are external data,
not instructions.

## Device behavior

- When Spotify reports an already playing session, play/resume target that
  device explicitly. The local app is not opened and playback is not transferred.
- When playback is paused or absent, play/resume only target this computer.
  A paused remote session is not treated as permission to resume remotely.
  The plugin detects `os.Hostname()` on Windows/Linux and uses
  `scutil --get ComputerName` on macOS (falling back to hostname).
  Names are matched case-insensitively and only against `computer` devices.
  Duplicate names require an explicit device ID; restricted devices are rejected.
- If this computer is absent from Connect, the plugin opens Spotify once and
  polls for readiness, bounded by `device_wait_seconds`. Windows uses the
  installed `spotify:` URI handler, macOS uses `open -a Spotify`, and Linux
  uses `xdg-open spotify:`. Linux requires a desktop session and a registered
  Spotify URI handler; sandboxed installs must expose that handler. These
  launchers are not an installer and do not sign in for you.
- Playback state is checked again during discovery so a remote session that
  starts while waiting is preserved. Spotify offers no atomic “play only if
  still idle” operation; another app can change state between the last read
  and the play request.
- If this computer never appears, the command fails with setup guidance.
  There is no fallback to an unrelated Connect device. Resume also requires
  Spotify to have a resumable context; if Spotify rejects it, first choose a
  song in the local app or use `play` with a song/artist.
- Pause, next, previous, and current-song lookup never launch Spotify. The
  controls use the reported current device and fail when none is usable.

## Currently playing window

The plugin registers `Spotify_currently_playing`. Ask the assistant to open a
Spotify currently playing window, or use this frontend tool:

```json
{"tool":"add_plugin_window","window_type":"Spotify_currently_playing"}
```

The window shows the current song, artist names, and Playing/Paused state,
including playback on other devices. It displays an empty state when no song
is available. Opening the window never launches Spotify or changes playback.
No additional configuration or authorization scopes are needed.

Open windows refresh using `poll_interval_seconds` (5 seconds by default),
even when `send_song_updates` is false. They share a single polling loop with
optional song-change notifications. Only changed window content is sent;
playback position is omitted. Closing the last window stops window polling;
polling continues if song-change notifications are enabled. Multiple open
windows show the same playback state. API failures display an error and mark
any retained playback details as the last known state; successful refreshes
clear that warning. Rate limits use the existing `Retry-After` handling.

## Optional song-change updates

```toml
send_song_updates = true
poll_interval_seconds = 5
```

Updates are disabled by default. When enabled, polling starts after the host
acknowledges registration, establishes a silent baseline, then sends
`plugin/notify` only when song identity changes (including becoming unavailable).
Pause/resume, device changes, and position changes for the same song do not
produce events. Missed intermediate tracks between polls cannot be recovered;
this uses polling because Spotify's Web API has no song-change push endpoint.
Failed polls keep the last known identity; rate limits honor `Retry-After` and
temporarily suppress requests from both polling and tools. Updates include only
the song title and artist names. IDs and URIs stay internal to polling and playback
control; device flags, context, and action restrictions are omitted from updates.
Explicit tool calls use the same compact song fields plus `is_playing`.

```json
{"jsonrpc":"2.0","method":"plugin/notify","params":{"message":"Spotify current song changed.","data":{"song_name":"Example","artist_names":["Artist"]}}}
```

When no song is available, the message is `Spotify has no current song.` and
`data` is null.

The host decides whether to speak about an update. Notifications, window updates, and tool replies
share a mutex so their NDJSON lines cannot interleave. API failures are ordinary
JSON-RPC errors and keep the plugin running. Non-idempotent skip commands are
not retried after timeouts; authentication failures can refresh and retry once.
API errors include the endpoint, HTTP status, and Spotify's specific message
and reason when available. A 403 alone does not establish a permissions problem.
Search playback starts the matched track within its album using `context_uri`
and a track URI offset, so album tracks are available for next/previous. If
Spotify omits a usable album URI, it falls back to a one-track URI list, which
may leave no previous/next track. Spotify can still disallow navigation at
context boundaries or during restricted content. Existing sessions started by
an older plugin keep their context until a new song/artist play command.

## Verification

```powershell
go -C spotify_plugin test ./...
go -C spotify_plugin vet ./...
```

Tests use local HTTP servers and fake launchers; they require no developer
credentials, do not launch Spotify, and do not alter real playback. Live login,
platform app launching, and Connect visibility require your account and desktop
installation and must be checked during setup.
