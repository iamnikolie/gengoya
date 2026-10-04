# gengoya — agent skill reference

Agent-facing image-, video-, music- and speech-generation CLI (named after Francisco Goya). Same
doctrine as `fibery` and `gl`: the agent runs `gengoya <cmd>` from Bash and reads
**token-lean stdout**.

## Output contract (critical)

The CLI writes files to disk and prints their **absolute paths** to stdout
(one per line). The agent then `Read`s each path — Claude Code / Cursor render
images natively. **Bytes never go through stdout or the model context.**

- **stdout** — absolute path(s), one per line (or one JSON object with `--json`)
- **stderr** — status, one line per artifact:
  - image: `model=… id=… size=… quality=… cost≈$… ms=…` + optional `revised_prompt="…"`
  - video: `model=… id=… res=… dur=…s aspect=… cost≈$… ms=…`
  - video polling: `job=<id> running|done t=<seconds>s` while waiting
  - music/speech: `model=… id=… fmt=… bytes=… [dur=…s] [voice=…] cost≈$… (usage|registry) ms=…`
- Exit non-zero on provider errors (error body on stderr)

**Video is the exception an agent must know about:** an `.mp4` cannot be `Read`.
So `gengoya video` also writes a **PNG contact sheet** (`<clip>-poster.png`) next
to the clip and prints its path on the following line. Read the poster, not the
mp4. No ffmpeg on PATH → no poster (stderr note), clip still written.

**Music and speech** write audio files; an agent cannot listen, so verify with
`ffprobe -v error -show_entries stream=codec_name,sample_rate,channels,duration -of default=nw=1 <path>`.
`music` prints the audio path, then (when the model returned lyrics / timed lyrics /
song structure) a `<name>.txt` path — **Read the .txt** to see what was sung.

## Config (mandatory)

Every command except `skill` / `--help` / `version` requires `--config <name>`
(or `GENGOYA_CONFIG`). There is **no default profile**.

```bash
gengoya config init --config myproj
gengoya config show --config myproj
```

Profile path: `~/.gengoya/<name>/config.yaml` (mode `0600`). Override base with
`GENGOYA_HOME` (tests).

```yaml
default_provider: openai
default_model: gpt-image-2.5-flare  # optional
providers:
  openai:
    api_key: sk-...
  gemini:
    api_key: AIza...
```

Key resolution: config file → `OPENAI_API_KEY` / (`GEMINI_API_KEY` then
`GOOGLE_API_KEY`).

Optional whole-file registry override: `~/.gengoya/<name>/registry.yaml`.

## Commands

### generate

```bash
gengoya generate "a red cube on white" --config myproj
gengoya generate "logo mark" --config myproj --model nano-banana-2 --out /tmp --name logo
gengoya generate "hero" --config myproj -n 2 --quality high --json
```

### edit

Leading args are input images; **last** arg is the prompt (≥1 image + prompt).

```bash
gengoya edit ./photo.png "remove the background" --config myproj
gengoya edit a.png b.png "merge into one scene" --config myproj --mask mask.png
```

`--mask` is OpenAI's dedicated mask field (PNG + alpha). Gemini receives the mask
as an extra image part (no dedicated mask API).

### video

Text-to-video (and image-to-video) via Veo or Gemini Omni (both Gemini, same key). Veo is
long-running (start, poll, download); Omni is one blocking request (~15-45 s; `--detach`
is refused for it). Either way gengoya downloads the clip and prints its path.

```bash
gengoya video "a red paper boat in a rain puddle" --config myproj --out /tmp
gengoya video "pan across the scene" --config myproj --image first.png --name pan
gengoya video "close on the dress" --config myproj --ref dress.png --ref model.png
gengoya video "waves at dusk" --config myproj --model veo-3.1 --resolution 1080p --json
gengoya video "marble on a track" --config myproj --model omni-flash --resolution 360p --duration 3
gengoya video "make the marble blue" --config myproj --model omni-flash --edit <job_id>
```

Video-only flags:

| Flag | Default | Meaning |
|---|---|---|
| `--image <path>` | (none) | First frame → image-to-video |
| `--last-frame <path>` | (none) | Final frame → interpolation (**requires `--image`**) |
| `--ref <path>` | (none) | Reference image for subject/style; repeatable, per-model cap |
| `--negative "<text>"` | (none) | Negative prompt (**Veo only**) |
| `--resolution 360p\|720p\|1080p\|4k` | model default | Per model; see the table below |
| `--duration <n>` | model default | Veo takes `4\|6\|8`; Omni `3..10` (default 5) |
| `--aspect <ratio>` | first declared (`16:9`) | Veo: `16:9\|9:16` (no square) |
| `--person allow_all\|allow_adult` | (omit) | `personGeneration` (**Veo only**) |
| `--edit <mp4\|id>` | (none) | **Omni only.** Edit/extend: a prior interaction id / `job_id` (stateful, verified) or a local mp4 ≤10 s (inline upload; **was blocked in our tests**, likely regional) |
| `--delivery inline\|uri` | auto | **Omni only.** inline ≤720p, `files/` uri for 1080p/4k |
| `--detach` | false | **Veo only.** Start the job, print the operation name, exit |
| `--poll-interval <dur>` | `10s` | Operation poll cadence |
| `--timeout <dur>` | `15m` | Give up waiting (`0` = no limit) |
| `--poster-frames <n>` | `4` | PNG contact sheet next to the clip (`0` = off; `1` = single frame) |

stdout is the clip path then the poster path; `-n` must be 1 (one clip per
request).

Constraint errors are raised **before** the API call, e.g.
`resolution 1080p requires --duration 8 (got 4)` or
`duration "5" not allowed for this model; allowed: 8, 6, 4`.

#### Picking a video model

| Alias | Provider | Resolutions | Duration | Audio | Billing |
|---|---|---|---|---|---|
| `veo-3.1-fast` (default) | gemini | 720p/1080p/4k | 4/6/8 | always | $0.10–0.30/s |
| `veo-3.1` | gemini | 720p/1080p/4k | 4/6/8 | always | $0.40–0.60/s |
| `veo-3.1-lite` | gemini | 720p/1080p | 4/6/8 | always | $0.05–0.08/s |
| `omni-flash` | gemini | 360p/720p/1080p/4k | 3–10 (any int) | always | tokens: ~$0.034/s 360p, $0.101/s 720p, $0.152/s 1080p (4k unverified) |

Veo and Omni have no square format (`16:9` / `9:16` only). Audio is always generated.

Omni notes: 360p is the cheap draft tier (3 s ≈ $0.11). `--negative` and `--person`
error (put negations in the prompt). `--image` alone is the first frame; add
`--last-frame` to interpolate; `--ref` images and `--image` cannot be mixed. Edits
chain: each result is a new interaction, so `--edit <job_id>` of an edit works too.
To *extend*, prompt it ("Extend this video, the camera keeps panning") together
with `--edit` (not live-tested). `--detach` is refused for Omni (a background
interaction cannot be fetched with an API key); just wait the ~15-45 s. Omni job ids are hashed to
≤64 chars; `jobs status/fetch` accept them like any other.

`--json` payload:

```json
{"videos": [{"path": "/tmp/waves.mp4", "poster": "/tmp/waves-poster.png",
             "bytes": 2064105, "mime": "video/mp4"}],
 "model": "veo-3.1-fast", "api_id": "veo-3.1-fast-generate-preview",
 "provider": "gemini", "resolution": "720p", "duration_seconds": "8",
 "aspect": "16:9", "operation": "models/…/operations/<id>", "job_id": "<id>",
 "state": "fetched", "cost_estimate_usd": 0.8, "cost_source": "registry",
 "elapsed_ms": 34696}
```

With `--detach` the same object comes back with `"videos": []` and
`"state": "pending"` — read `operation` / `job_id` from it. Plain (non-`--json`)
stdout prints just the operation name.

### music

Lyria via the Gemini key. One track per call (`-n` must be 1).

```bash
gengoya music "upbeat indie-pop, handclaps, summer road trip" --config myproj --out /tmp
gengoya music "lo-fi beat, Rhodes, 82 BPM" --model lyria-3-clip-preview --instrumental --config myproj
gengoya music "warm acoustic ballad" --lyrics-file lyrics.txt --duration 90 --config myproj
gengoya music "score for this scene" --image still.png --config myproj --json
```

| Flag | Default | Meaning |
|---|---|---|
| `--model` | `lyria-3.5` | `lyria-3.5` ($0.08, full song ~1-2 min), `lyria-3-clip-preview` ($0.04, always 30 s — iterate here first), `lyria-3-pro-preview` ($0.08, legacy) |
| `--instrumental` | false | Appends "Instrumental only, no vocals." (exclusive with lyrics) |
| `--lyrics "<text>"` / `--lyrics-file <path>` | (none) | Own lyrics under a `Lyrics:` header; use `[Verse]`/`[Chorus]` tags. Write them in the language you want sung |
| `--duration <sec>` | (model decides) | **Prompt hint only** (60 asked, 70 got); refused for the 30 s clip model |
| `--image <path>` | (none) | Image-to-music; repeatable, max 10 |

No `--negative`, `--seed`, `--format wav` (not honoured by the API) or tempo flags:
put BPM, key, instruments and mood in the prompt. Output is `.mp3`, 44.1 kHz stereo.
`--json`: `{"audio":[{"path","lyrics","bytes","mime"}],"model","api_id","cost_estimate_usd","cost_source":"registry","elapsed_ms"}`.
Latency: clip ~9-15 s, song ~25-30 s. Prompts naming real artists or copyrighted
lyrics get blocked (error carries `blockReason`).

### speak

Gemini 3.8 TTS via the Gemini key. Writes a 24 kHz mono 16-bit **WAV**.

```bash
gengoya speak "Welcome back." --voice Puck --style "warm, unhurried" --config myproj
gengoya speak --file article.txt --model gemini-3.8-flash-lite-tts --config myproj
echo "Привіт, світе" | gengoya speak - --config myproj
gengoya speak --file script.txt --speaker Joe=Puck --speaker Jane=Kore --config myproj
```

| Flag | Default | Meaning |
|---|---|---|
| text arg / `--file <path>` / stdin (`-`, or piped with no arg) | required | The verbatim transcript (cap ~8k tokens, no chunking) |
| `--model` | `gemini-3.8-flash-tts` | or `gemini-3.8-flash-lite-tts` (cheaper, fewer languages) |
| `--voice <v>` | `Kore` | Prebuilt name (Puck, Zephyr, Charon, Fenrir, Leda, Aoede, Sulafat, …; case-insensitive), an Extended Voice Library id, or a `voice_…` id. Unknown name = bare HTTP 400 |
| `--style "<direction>"` | (none) | Sustained delivery ("whispered urgently", "speaking slowly"); sent as `speech_metadata.style`, not read aloud |
| `--speaker Name=Voice` | (none) | Dialogue; give **exactly two**. Script lines `Name: text` or `Name [style]: text`; unmarked lines continue the previous turn |

Inline tags inside the text: `<laugh> <sigh> <cough> <breath> <short pause> <long pause>`
(English tags even in non-English text). Language is auto-detected (Russian/Ukrainian
verified). `--sample-rate` does not exist (API ignores it). Cost is real usage
(`cost_source: "usage"`): ~$0.002 per 7 s on flash, ~$0.0008 per 4 s on lite.
`--json` adds `voice`, `speakers`, `usage`, and `duration_seconds` per file.

### jobs (detached video)

`--detach` records the job under `~/.gengoya/<profile>/jobs/<id>.json` (prompt,
model, resolution, output dir, cost estimate) and prints the operation name.

```bash
op=$(gengoya video "long shot of a storm" --config myproj --detach --out /tmp)
gengoya jobs list --config myproj
gengoya jobs status "$op" --config myproj      # single poll: pending|done|failed
gengoya jobs fetch "$op" --config myproj       # wait, download, write, print paths
```

`jobs status`/`jobs fetch` take a job id **or** a full operation name. `fetch`
reuses the recorded settings unless `--out` / `--name` / `--poster-frames`
override them, and an already-fetched job re-prints its recorded paths without
re-downloading. `fetch` also honours `--poll-interval` / `--timeout`.

`jobs status` prints the state on stdout and **exits non-zero when the job
failed** (the provider message goes to stderr) — poll it in a loop and check the
exit code, don't grep the text. `jobs list` reads local records only: it works
without an API key.

### models

```bash
gengoya models --config myproj
gengoya models --config myproj --kind video     # image|video|music|speech
gengoya models --config myproj --json
```

### skill / version

```bash
gengoya skill
gengoya version
```

## Persistent flags

| Flag | Default | Meaning |
|---|---|---|
| `--config <name>` | `$GENGOYA_CONFIG` | **Required.** Profile under `~/.gengoya/<name>/` |
| `--provider openai\|gemini` | profile default | Override provider |
| `--model <alias>` | provider default | Registry alias; infers provider |
| `--out <dir>` | `.` | Output directory (created if missing) |
| `--name <base>` | slug from prompt | Basename (no extension) |
| `-n <count>` | `1` | Number of images |
| `--size <WxH\|auto\|1K\|2K\|4K\|512>` | model default | OpenAI: WxH/`auto`. Gemini: WxH aliases or `1K`/`2K`/`4K`/`512` → `imageConfig` |
| `--aspect <ratio>` | (none) | Gemini only (`1:1`, `16:9`, `9:16`, …). With `--size auto` → aspect + `1K` |
| `--quality low\|medium\|high\|xhigh\|max\|auto` | model default | OpenAI only; ignored for Gemini (stderr note). `xhigh`/`max` are **gpt-image-2.5 only** |
| `--format png\|jpeg\|webp` | `png` | OpenAI `output_format`. Gemini: not sent (API has no request mime); file ext follows response mime |
| `--background auto\|transparent\|opaque` | (omit) | OpenAI only. `transparent` works on the gpt-image-2.5 pair (png/webp); **unsupported on gpt-image-2**, which is why 2.5 is the default |
| `--compression 0..100` | (omit) | OpenAI jpeg/webp only |
| `--moderation auto\|low` | (omit) | OpenAI only |
| `--json` | false | JSON object on stdout instead of path lines |
| `--verbose` | false | Dump request/response (keys redacted) to stderr |
| `--yes` | false | Skip TTY cost confirmation (> $0.50 images/music/speech, > $1.00 video) |

Image-only flags (`--size`, `--quality`, `--format`, `--background`,
`--compression`, `--moderation`) are ignored by `video`; `-n` must be 1 there, and for
`music` / `speak` as well (`--aspect`/`--size`/`--quality` ignored; `--format` only
validated for `music`, whose only format is mp3).

`--model` vs `--provider`: if both set and disagree → exit 1. If only `--provider`,
use that provider's registry default model. If neither, use profile defaults.

### Size mapping (Gemini)

| `--size` | `imageConfig` |
|---|---|
| `auto` (no `--aspect`) | omitted (API default ~1K) |
| `1024x1024` | `1:1` + `1K` |
| `2048x2048` | `1:1` + `2K` |
| `3840x2160` | `16:9` + `4K` |
| `1536x1024` / `1024x1536` | `3:2` / `2:3` + `1K` |
| `2K` + optional `--aspect` | that token + aspect (default `1:1`) |

`nano-banana-2-lite` / legacy `nano-banana`: **1K only**. stderr `size=` is measured WxH when decodable.

## Filenames

- `--name foo` → `foo.<ext>`; with `-n 2` → `foo-1.<ext>`, `foo-2.<ext>`
- Without `--name` → `<slug>-<8hex>.<ext>` (slug ≈ first 6 prompt words)
- Extension follows actual mime (`image/jpeg` → `.jpeg`), not blindly `--format`
- Video: `<base>.mp4` plus `<base>-poster.png` beside it
- Collisions append ` (n)` before the extension

## Cost

Images: pre-call guard uses `n * price_per_img`. After the call, OpenAI cost
prefers `usage` token rates from the registry (`cost_source: "usage"` in
`--json`); otherwise falls back to `price_per_img`. Gemini always uses registry
per-image. Guard threshold **$0.50**.

Video: Veo is billed per **output second**, `cost_source: "registry"`. Omni is billed per
token; the pre-call estimate is `duration × price_per_sec` and the reported cost
(stderr line, `--json` `cost_estimate_usd`) is recomputed from the response usage with
`cost_source: "usage"`.

- Veo (USD) — `duration × price_per_sec[resolution]`, guard threshold **$1.00**
  (one default clip costs more than a whole image batch). A `veo-3.1` 8s/1080p
  clip is $3.20.

Music: flat per song/clip ($0.08 / $0.04 / $0.08), `cost_source: "registry"`.
Speech: real token usage (text in $0.50/1M; audio out $9.00/1M flash, $6.00/1M lite,
~32 audio tokens per second) with `cost_source: "usage"`. Those token rates are
introductory **through 2026-12-31**, then double. Both are far below the $0.50 guard.

The guards only prompt when stdout is a **TTY**; non-TTY (the agent case) never
prompts — the cost is reported on stderr instead. `--yes` skips the prompt.

## Deferred

- Request-side output mime (`outputMimeType`) —
  consumer generateContent does not support request mime today. (The Interactions
  API is used for Omni video only; Gemini images stay on generateContent.)
- **Voice design / replication** (`POST /v1beta/voices`), streaming TTS, long-text
  chunking, Lyria RealTime — not implemented. A designed `voice_…` id works in `--voice`.
- **OpenAI video (Sora 2)** — deliberately not implemented: the Videos API and
  all `sora-2*` models shut down **2026-09-24** with no announced replacement.

## Models (embedded registry)

Image — OpenAI: `gpt-image-2.5-flare` (default), `gpt-image-2.5-sunburst`,
`gpt-image-2`, `gpt-image-1.5`, `gpt-image-1`, `gpt-image-1-mini`  
Image — Gemini: `nano-banana-2` (default), `nano-banana-pro`, `nano-banana-2-lite`, `nano-banana`  
Video — Gemini (Veo): `veo-3.1-fast` (default), `veo-3.1`, `veo-3.1-lite`  
Video — Gemini (Omni, Interactions API): `omni-flash` (`gemini-omni-1.1-flash`)  
Music — Gemini (Lyria): `lyria-3.5` (default), `lyria-3-clip-preview`, `lyria-3-pro-preview`  
Speech — Gemini (TTS): `gemini-3.8-flash-tts` (default), `gemini-3.8-flash-lite-tts`  

Picking an OpenAI model: `gpt-image-2.5-flare` for everything by default — it is
both faster and cheaper than `gpt-image-2` at equal quality (1K high: $0.053 vs
$0.211). Reach for `gpt-image-2.5-sunburst` when an **edit** has to land exactly;
it costs the same per token and only trades latency. `xhigh` and `max` exist on
the 2.5 pair only: `max` matches `gpt-image-2 high` in tokens and price.

Run `gengoya models --config <name> [--kind video|music|speech]` for api_id, ops, and price
notes. Image prices are rough; Veo per-second rates are
exact per Google's published price list.

## Agent workflow

```bash
# image: 1) generate  2) Read the path  3) optionally edit
path=$(gengoya generate "flat icon of a fox" --config myproj --out /tmp --name fox)
gengoya edit "$path" "add a soft shadow" --config myproj --out /tmp --name fox-shadow

# video: the poster is what you Read; the mp4 is the deliverable
gengoya video "the fox icon comes alive and blinks" --config myproj \
  --image "$path" --out /tmp --name fox-clip
# → /tmp/fox-clip.mp4
#   /tmp/fox-clip-poster.png   ← Read this one

# music + speech: Read the lyrics .txt; ffprobe the audio
gengoya music "folk song about cats avoiding puddles" --config myproj --out /tmp --name cats
# → /tmp/cats.mp3
#   /tmp/cats.txt   ← lyrics / structure
gengoya speak "Hello there" --config myproj --out /tmp --name hello   # → /tmp/hello.wav

# long jobs without blocking the agent
op=$(gengoya video "a storm rolling over a lake" --config myproj --detach --out /tmp)
# …do other work…
gengoya jobs fetch "$op" --config myproj
```
