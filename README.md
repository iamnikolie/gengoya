# gengoya

Agent-facing image-, video-, music- and speech-generation CLI (OpenAI + Gemini; Veo and
Gemini Omni for video, Lyria for music, Gemini TTS for speech). Named after Francisco Goya.

Writes files to disk and prints **absolute paths** on stdout so agents can
`Read` them. Status and cost go to stderr.

Video adds one twist: an agent cannot `Read` an mp4, so every clip also gets a
PNG contact sheet next to it (`<clip>-poster.png`, needs `ffmpeg`) whose path is
printed on the next line. That is the frame an agent looks at.

## Install

**Homebrew:**

```bash
brew trust iamnikolie/tap   # Homebrew 6 refuses untrusted third-party taps
brew tap iamnikolie/tap
brew install iamnikolie/tap/gengoya
```

**Prebuilt binary** from [Releases](https://github.com/iamnikolie/gengoya/releases)
(macOS, Linux, Windows; amd64/arm64), or **from source** (Go 1.24+):

```bash
go install github.com/iamnikolie/gengoya@latest
# or, in a checkout:
make install   # builds ./gengoya and symlinks ~/.local/bin/gengoya
```

Video poster frames need `ffmpeg` on `PATH` (optional; without it clips are
still saved). You bring your own OpenAI and/or Gemini API keys, and every call
is billed to them.

**Agent skill:** `gengoya skill` prints the full agent reference — put it where
your agent loads skills or paste it into its instructions.

## Quick start

```bash
gengoya config init --config myproj
gengoya models --config myproj
gengoya generate "a red cube on white" --config myproj --out /tmp
gengoya video "a red paper boat in a rain puddle" --config myproj --out /tmp
```

`--config <name>` (or `GENGOYA_CONFIG`) is **required** on every command except
`skill` / `version` / help. Profile lives at `~/.gengoya/<name>/config.yaml`.

## Commands

| Command | Purpose |
|---|---|
| `gengoya generate "<prompt>"` | text → image(s) |
| `gengoya edit <image>... "<prompt>"` | image edit (+ optional `--mask`) |
| `gengoya video "<prompt>"` | text/image → video (Veo, Gemini Omni); `--edit` revises a clip (Omni) |
| `gengoya jobs list` / `status` / `fetch` | detached video jobs |
| `gengoya music "<prompt>"` | text/image → song or 30 s clip (Lyria, Gemini key) |
| `gengoya speak "<text>"` | text → WAV speech, 1 voice or 2-speaker dialogue (Gemini TTS) |
| `gengoya models` | resolved registry table (`--kind image\|video\|music\|speech`) |
| `gengoya config init` / `show` | write / inspect profile |
| `gengoya skill` | embedded agent reference (SSOT) |
| `gengoya version` | version string |

Common flags: `--provider`, `--model`, `--out`, `--name`, `-n`, `--size`,
`--aspect` (Gemini), `--quality`, `--format`, `--background` / `--compression` /
`--moderation` (OpenAI), `--json`, `--verbose`, `--yes`.

`--size` is OpenAI `WxH`/`auto`, or Gemini `WxH` aliases / `1K`/`2K`/`4K`.
Gemini resolution is sent as `generationConfig.imageConfig` (`aspectRatio` +
`imageSize`). File extensions follow the response mime type.

## Image models

**OpenAI:** `gpt-image-2.5-flare` (default), `gpt-image-2.5-sunburst`,
`gpt-image-2`, `gpt-image-1.5`, `gpt-image-1`, `gpt-image-1-mini`. The 2.5 pair
is the current generation: arbitrary `WxH` (multiples of 16, ≤ 3840/edge), two
extra quality steps `xhigh` and `max` above `high`, and working
`--background transparent` — which `gpt-image-2` never supported. Both 2.5
models bill at the same token rate, so quality-for-quality they cost the same;
`flare` is the fast one, `sunburst` trades latency for edit precision. At 1K,
`high` is $0.053 and `max` $0.211 — `max` is exactly what `gpt-image-2 high`
used to cost, which makes 2.5 a straight price cut at every step below it.

**Gemini:** `nano-banana-2` (default, $0.067 @1K), `nano-banana-pro` ($0.134
@1K–2K, $0.24 @4K), `nano-banana-2-lite` ($0.0336, 1K only), `nano-banana`
(legacy). `--quality` is ignored here; size comes from `imageConfig`.

`gengoya models --config <name>` prints the resolved table with api_ids and
price notes.

## Video

```bash
# text → video (waits, downloads, prints clip + poster paths)
gengoya video "waves at dusk" --config myproj --out /tmp --name waves

# image → video, first frame from a still
gengoya video "slow push in" --config myproj --image cat.png --out /tmp

# don't block (Veo only): start now, collect later
op=$(gengoya video "a storm over a lake" --config myproj --detach --out /tmp)
gengoya jobs status "$op" --config myproj
gengoya jobs fetch  "$op" --config myproj
```

Video flags: `--image`, `--last-frame`, `--ref`, `--negative` (Veo only),
`--resolution`, `--duration`, `--aspect`, `--person`
(Veo only), `--edit` and `--delivery` (Omni only), `--detach` (Veo only), `--poll-interval`,
`--timeout`, `--poster-frames`.

**Gemini Omni 1.1 Flash** (`--model omni-flash`, api id `gemini-omni-1.1-flash`)
runs on the Interactions API, not Veo's `predictLongRunning`. Same Gemini key.
`--resolution 360p|720p|1080p|4k` (default 720p), `--duration 3..10` (default 5),
`--aspect 16:9|9:16`, `--image` (+ `--last-frame` for first/last-frame
interpolation), `--ref` (up to 6, not combinable with `--image`). Billed per
token (about $0.034/s @360p, $0.101/s @720p, $0.152/s @1080p — the figure
printed is the **actual** cost from the response usage). Audio is included.
Every `video` call is stored server-side; edit a previous result by id:

```bash
gengoya video "a violin player on a stage" --model omni-flash --resolution 360p --json  # note job_id
gengoya video "make the violin invisible" --model omni-flash --resolution 360p --edit <job_id>
gengoya video "make the mirror ripple" --model omni-flash --edit clip.mp4   # local <=10s mp4 (was rejected as content_blocked in our tests)
```

Omni is one blocking request (about 15-45 s). `--detach` is refused for it:
background interactions cannot be fetched back with an API key, so the clip
would be billed and never downloadable.

**Veo (direct, billed in USD per output second):** `veo-3.1-fast` (default,
$0.10/s @720p), `veo-3.1` ($0.40/s), `veo-3.1-lite` ($0.05/s, no 4k). Audio comes
with the clip at no extra charge. `1080p` and `4k` accept only `--duration 8`.
Aspect is `16:9` or `9:16` — **no square**.

All constraint violations are rejected before the API call.

Billing is **per output second**, so the cost guard for video trips at $1.00
rather than the $0.50 image threshold (TTY only — an agent is never prompted).

OpenAI video is deliberately absent: the Sora 2 Videos API shuts down
2026-09-24 with no announced replacement.

## Music and speech

Both run on the Gemini key. Output is a file path on stdout; status and cost on stderr.

```bash
# music: song with generated lyrics (writes <name>.mp3 + <name>.txt with the lyrics)
gengoya music "upbeat indie-pop, handclaps, a summer road trip" --config myproj --out /tmp
# cheapest iteration: the fixed 30 s clip, instrumental
gengoya music "lo-fi beat, Rhodes chords, 82 BPM" --model lyria-3-clip-preview --instrumental --config myproj
# your own lyrics, a length hint, image-to-music
gengoya music "warm acoustic ballad" --lyrics-file lyrics.txt --duration 90 --config myproj
gengoya music "score for this scene" --image still.png --config myproj

# speech: single voice + delivery style; text from arg, --file, or stdin ("-")
gengoya speak "Welcome back." --voice Puck --style "warm, unhurried" --config myproj
echo "Привіт, світе" | gengoya speak - --model gemini-3.8-flash-lite-tts --config myproj
# two-speaker dialogue ("Name: text" / "Name [style]: text" lines)
gengoya speak --file script.txt --speaker Joe=Puck --speaker Jane=Kore --config myproj
```

**Music** (`lyria-3.5` default, $0.08/song; `lyria-3-clip-preview` 30 s, $0.04;
`lyria-3-pro-preview` $0.08): MP3 44.1 kHz stereo. Lyria has no duration, negative or
seed parameter, so `--instrumental`, `--lyrics` and `--duration` are folded into the
prompt (length is a loose hint). Up to 10 `--image` inputs.

**Speech** (`gemini-3.8-flash-tts` default, `gemini-3.8-flash-lite-tts` cheaper): WAV
24 kHz mono 16-bit, written with a proper header. Cost comes from the response usage
(about $0.002 per 7 s on flash). Exactly two speakers for dialogue. Inline tags like
`<laugh>`, `<sigh>`, `<short pause>` work inside the text; put sustained delivery in
`--style`. Introductory token prices run through 2026-12-31 and then double.

```bash
make build    # embeds git version via ldflags
gengoya version
make test && make vet
```

Full agent-oriented reference: `gengoya skill` (also [`cmd/skill.md`](cmd/skill.md)).

Live bake-off / smoke numbers and model notes: [`RESEARCH.md`](RESEARCH.md).

## Config

```yaml
default_provider: openai
default_model: gpt-image-2.5-flare
providers:
  openai:
    api_key: sk-...
  gemini:
    api_key: AIza...
```

Env fallbacks: `OPENAI_API_KEY`, `GEMINI_API_KEY` / `GOOGLE_API_KEY`.  
`GENGOYA_HOME` overrides `~/.gengoya` (for tests). Drop a `registry.yaml` in the
profile dir to replace the embedded model registry.

## Develop

```bash
make test
make vet
make build
```

## License

MIT. Not affiliated with OpenAI or Google; model names and APIs belong to them.
