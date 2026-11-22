package main

import (
	"os"
	"path/filepath"
	"testing"
)

// minimalConfig is a complete config that satisfies the validation rules.
const minimalValidConfig = `
[transcription]
api_key = "sk-or-test"
model = "openai/whisper-large-v3-turbo"
url = "https://openrouter.ai/api/v1/audio/transcriptions"

[chat]
api_key = "sk-or-test"
url = "https://openrouter.ai/api/v1/chat/completions"
temperature = 0.7
max_tokens = 16384

[tts]
api_key = "sk-or-test"
url = "https://openrouter.ai/api/v1/audio/speech"
model = "speechify/simba-3.5"
voice = "beth"
mode = "auto"

[vad]
path = "vad/silero_vad.bin"

[ai]
ai_name = "Zoe"
user_name = "boss"

[database]
path = "custom.db"
compact_after_hours = 5
max_compaction_tokens = 16384
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestReadConfigDatabaseSection(t *testing.T) {
	path := writeConfig(t, minimalValidConfig)

	_, _, _, _, _, _, _, databaseConfig, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if databaseConfig.Path != "custom.db" {
		t.Errorf("Path = %q, want %q", databaseConfig.Path, "custom.db")
	}
	if databaseConfig.CompactAfterHours != 5 {
		t.Errorf("CompactAfterHours = %d, want 5", databaseConfig.CompactAfterHours)
	}
}

func TestReadConfigDatabaseDefaults(t *testing.T) {
	body := minimalValidConfig
	body = body[:len(body)-len("\n[database]\npath = \"custom.db\"\ncompact_after_hours = 5\n")]
	path := writeConfig(t, body)

	_, _, _, _, _, _, _, databaseConfig, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if databaseConfig.Path != "conversation.db" {
		t.Errorf("Path = %q, want %q", databaseConfig.Path, "conversation.db")
	}
	if databaseConfig.CompactAfterHours != 3 {
		t.Errorf("CompactAfterHours = %d, want 3", databaseConfig.CompactAfterHours)
	}
}

func TestReadConfigDatabaseSectionInvalid(t *testing.T) {
	// Zero or negative compact_after_hours falls back to the default.
	body := minimalValidConfig
	body = body[:len(body)-len("compact_after_hours = 5\n")] + "compact_after_hours = 0\n"
	path := writeConfig(t, body)

	_, _, _, _, _, _, _, databaseConfig, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if databaseConfig.CompactAfterHours != 3 {
		t.Errorf("CompactAfterHours = %d, want 3", databaseConfig.CompactAfterHours)
	}
}
