# Ambient

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
