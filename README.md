# Ambient

## Conversation turns

Speech detection stays enabled during transcription and front-end inference.
When the user resumes speaking, the in-flight front-end request is cancelled
immediately at speech onset. Captured utterances are transcribed in order, and
the next request includes those earlier fragments. Cancelled and superseded
replies cannot enter history, run tools, or queue speech. Worker updates wait
during user speech, transcription, and assistant playback.

Detection is disabled after a spoken reply is accepted, through synthesis and
playback, then enabled again. Sleep also disables detection until UI wakeup.
Capture frames from an earlier detection session are discarded to prevent
playback audio being interpreted as user speech after the microphone reopens.

To check whether repeated words originate in the configured TTS provider rather
than playback, run the optional live probe. It synthesizes three short examples
and transcribes the raw PCM without playing it. This uses the TTS and
transcription credentials in `config.toml` and incurs their API charges:

```powershell
$env:AMBIENT_TEST_TTS = '1'
go test -count=1 -v . -run TestProviderSpeechProbe
Remove-Item Env:AMBIENT_TEST_TTS
```

## Building without CGO

On Windows PowerShell:

```powershell
$env:CGO_ENABLED = '0'
go build .
go test ./...
```

Audio playback uses [Ebitengine Oto](https://github.com/ebitengine/oto).
Microphone capture uses [audio-go](https://github.com/vcaesar/audio-go), a
pure-Go replacement for malgo. Capture remains mono signed 16-bit PCM at
16 kHz, assembled into 512-sample frames for Silero VAD. A fallback to the
library's silent null backend is treated as an initialization error.

Capture backends and limitations:

- Windows amd64/arm64: WASAPI shared mode handles sample rate and channel conversion.
- macOS amd64/arm64: Core Audio handles conversion; the host application needs microphone permission.
- Linux: direct ALSA hardware access requires a device supporting 16 kHz mono
  signed 16-bit PCM. Devices held by PulseAudio/PipeWire can be busy; this
  backend does not provide software format conversion.

The hardware capture test is optional and discards samples without saving or
transmitting them. Run it on a machine with a working default microphone:

```powershell
$env:CGO_ENABLED = '0'
$env:AMBIENT_TEST_MICROPHONE = '1'
go test -count=1 -v ./vad -run TestMicrophoneCaptureLifecycle
```

## Markdown window bodies

Window bodies render CommonMark with GitHub-style tables, strikethrough, task
lists, and automatic links. `ui/markdown.go` converts the parsed document into
positioned text and rectangle elements for direct rendering. It supports all six
heading levels, nested bold/italic, ordered and unordered lists, blockquotes,
horizontal rules, inline code, fenced and indented code blocks, and hard breaks.

Text wraps using the actual styled font metrics; table cells wrap independently
and respect column alignment. Code preserves spacing and blank lines with
four-column tab stops. Layout is cached until the body, window width, or font
settings change, and scrolling stops at the content's bottom edge.

Links are styled text (not clickable). Images show their alt text; remote images
are not fetched. Raw HTML is displayed literally. Code blocks use monospace text
and an optional language label, without syntax highlighting.
