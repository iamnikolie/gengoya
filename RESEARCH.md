# gengoya research notes

Сводка live-прогонов (2026-07-21), API-наблюдений и рекомендаций по моделям.
Артефакты картинок лежат в `output/` (gitignored). Детальные черновики:
[`output/SUMMARY.md`](output/SUMMARY.md), [`output/SMOKE.md`](output/SMOKE.md).

Профиль: `--config personal`. Клиент: HTTP timeout 300s, retry на 429/5xx.

---

## 1 · Model matrix (registry)

| Alias | Provider | API id | Size mode | Caps | Fallback $/img | Token rates (OpenAI, $/1M) |
|-------|----------|--------|-----------|------|----------------|----------------------------|
| `gpt-image-2` | openai | `gpt-image-2` | flexible | up to 3840×2160 | 0.05 | text_in 5 / image_in 10 / image_out 40 |
| `gpt-image-1.5` | openai | `gpt-image-1.5` | enum | 1024² / 1536×1024 / 1024×1536 | 0.04 | 5 / 8 / 32 |
| `gpt-image-1` | openai | `gpt-image-1` | enum | same | 0.04 | 5 / 10 / 40 |
| `gpt-image-1-mini` | openai | `gpt-image-1-mini` | enum | same | 0.01 | 2 / 2.5 / 8 |
| `nano-banana-2` | gemini | `gemini-3.1-flash-image-preview` | gemini | 512–4K | 0.045 | — |
| `nano-banana-pro` | gemini | `gemini-3-pro-image-preview` | gemini | 1K–2K | 0.134 | — |
| `nano-banana-2-lite` | gemini | `gemini-3.1-flash-lite-image` | gemini | **1K only** | 0.02 | — |
| `nano-banana` | gemini | `gemini-2.5-flash-image` | gemini | 1K | 0.039 | — |

Defaults: OpenAI → `gpt-image-2`, Gemini → `nano-banana-2`.

---

## 2 · Smoke — fixed 1024 / 1K (generate)

**Date:** 2026-07-21  
**Prompt:** `flat red cube on white background, simple studio product photo`  
**Size:** OpenAI `--size 1024x1024`; Gemini `--size 1K --aspect 1:1`  
**Artifacts:** `output/smoke-*`

### Core matrix

| File | Model | Quality | Bytes | Cost USD | Source | elapsed_ms | Notes |
|------|-------|---------|-------|----------|--------|------------|-------|
| `smoke-gpt-image-2.png` | gpt-image-2 | medium | 885055 | 0.070325 | usage | 43362 | out_tok 1756 |
| `smoke-gpt-image-1.5.png` | gpt-image-1.5 | medium | 463612 | 0.033877 | usage | 95266 | image_out 1056 (+ text_out 265) |
| `smoke-gpt-image-1.png` | gpt-image-1 | medium | 1265334 | 0.042325 | usage | 15041 | 2× fail before ok (timeout / empty JSON) |
| `smoke-gpt-image-1-mini.png` | gpt-image-1-mini | low | 1157262 | 0.00221 | usage | 89732 | out_tok 272 |
| `smoke-nano-banana-2.jpeg` | nano-banana-2 | — | 371667 | 0.045 | registry | 9119 | response mime jpeg |
| `smoke-nano-banana-pro.jpeg` | nano-banana-pro | — | 416655 | 0.134 | registry | 15769 | |
| `smoke-nano-banana-2-lite.jpeg` | nano-banana-2-lite | — | 412139 | 0.020 | registry | 3444 | fastest |
| `smoke-nano-banana.png` | nano-banana | — | 1065092 | 0.039 | registry | 5803 | mime png; revised_prompt set |

### Extras / format probes

| File | Flags | Model | Cost | elapsed_ms | Result |
|------|-------|-------|------|------------|--------|
| `smoke-openai-extras.png` | `--background opaque --moderation low` | gpt-image-1.5 | 0.033877 usage | 15154 | ok |
| `smoke-openai-jpeg.jpeg` | `--format jpeg --compression 70` | gpt-image-1-mini low | 0.00221 usage | 144024 | ok; 47 KB vs ~1.1 MB png |

### Usage cost formula (OpenAI)

```
usd = (text_in * price_text_in + image_in * price_image_in + image_out * price_image_out) / 1e6
```

When `output_tokens_details.image_tokens > 0`, bill image_out from that field (not raw `output_tokens`).  
Gemini always uses `n * price_per_img` (`cost_source: registry`).

### Smoke failures (transient)

| Attempt | Model | Error |
|---------|-------|-------|
| 1 | gpt-image-1 | `ParseOpenAIResponse: unexpected end of JSON input` |
| 1–2 | gpt-image-1 / mini | `context deadline exceeded` (Client.Timeout awaiting headers) @ 180s |
| Fix | — | HTTP timeout raised **180s → 300s** |

---

## 3 · Logo bake-off (edit)

**Pipeline:** `goya-cat.png` → `gengoya edit`  
**Prompt idea:** flat circular logo, cat painting a cat, black / ochre / cream  
**Artifacts:** `output/goya-logo-*`

| File | Model | Quality | Cost≈ | Latency | Role |
|------|-------|---------|-------|---------|------|
| `goya-logo-gpt-image-2.png` | gpt-image-2 | high | $0.05 | ~169s | best brand badge + wordmark |
| `goya-logo-gpt-image-1.5.png` | gpt-image-1.5 | medium | $0.04 | ~63s | best app-icon / flat mark |
| `goya-logo-gpt-image-1.png` | gpt-image-1 | medium | $0.04 | ~21s | solid silhouette draft |
| `goya-logo-gpt-image-1-mini.png` | gpt-image-1-mini | low | $0.01 | ~30s | cheap probe |
| `goya-logo-nano-banana-2-lite.png` | nano-banana-2-lite | — | $0.02 | ~7s | best Gemini $/quality badge |
| `goya-logo-nano-banana.png` | nano-banana | — | $0.04 | ~12s | legacy, readable |
| `goya-logo-nano-banana-2.png` | nano-banana-2 | — | $0.04 | ~10s | hero badge, busy text |
| `goya-logo-nano-banana-pro.png` | nano-banana-pro | — | $0.13 | ~20s | clean mark + wordmark |

Aliases: `goya-logo-v1.png` = gpt-image-2; `goya-logo-mini.png` = gpt-image-1-mini.

Gemini first pass: free-tier `429 limit: 0`; retry succeeded.

### Logo ranking

1. gpt-image-2 high — ship candidate (badge + wordmark)  
2. gpt-image-1.5 medium — ship candidate (icon)  
3. nano-banana-2-lite — best Gemini price/quality  
4. nano-banana-pro — cleanest Gemini mark  
5. nano-banana-2 — strong hero, noisy at small sizes  
6. gpt-image-1 medium — draft  
7. nano-banana — legacy ok  
8. gpt-image-1-mini low — probe only  

---

## 4 · Source scene bake-off (`goya-cat` generate)

Same generate prompt as the reference scene. `goya-cat.png` = gpt-image-2 high.

| File | Model | Quality | Cost≈ | Latency |
|------|-------|---------|-------|---------|
| `goya-cat.png` | gpt-image-2 | high | $0.05 | ~133s |
| `goya-cat-gpt-image-1.5.png` | gpt-image-1.5 | high | $0.04 | ~49s |
| `goya-cat-gpt-image-1.png` | gpt-image-1 | high | $0.04 | ~50s |
| `goya-cat-gpt-image-1-mini.png` | gpt-image-1-mini | high | $0.01 | ~49s |
| `goya-cat-nano-banana-2-lite.png` | nano-banana-2-lite | — | $0.02 | ~3s |
| `goya-cat-nano-banana.png` | nano-banana | — | $0.04 | ~7s |
| `goya-cat-nano-banana-2.png` | nano-banana-2 | — | $0.04 | ~10s |
| `goya-cat-nano-banana-pro.png` | nano-banana-pro | — | $0.13 | ~21s |

### Scene ranking (Goya vibe)

1. gpt-image-2  
2. gpt-image-1 / gpt-image-1.5 (mood vs detail)  
3. nano-banana-2 / nano-banana-pro  
4. gpt-image-1-mini  
5. nano-banana / nano-banana-2-lite (cute, not Goya)

OpenAI wins atmosphere on full scenes; Gemini often drifts to “cute studio”.

---

## 5 · Latency

`elapsed_ms` ≈ wall-clock of one sync provider HTTP call (local write is negligible).

| Provider | Observed range | Notes |
|----------|----------------|-------|
| Gemini | ~3–21s | Stable; lite often &lt;5s |
| OpenAI | ~15–170s | High variance; same model can differ 2–10× |

Evidence of queue/capacity (not CLI):

- gpt-image-1-mini smoke: **90s** (png) vs **144s** (jpeg), same quality/size/cost tokens  
- gpt-image-1: timeouts awaiting headers, then success in **15s**  
- Logo gpt-image-2 high: **~169s**

Quality/size also matter (`high` ≫ `low`), but OpenAI sync Images API dominates.

---

## 6 · Cost observations

| Finding | Detail |
|---------|--------|
| Usage vs registry | OpenAI smoke billed **below** `price_per_img` for mini ($0.002 vs $0.01); gpt-image-2 medium **above** ($0.070 vs $0.05) |
| Token driver | Image output tokens dominate (272 mini → 1756 gpt-image-2) |
| Gemini | No usage object → registry flat rate |
| Pre-call guard | Still uses `n * price_per_img` (conservative for mini, optimistic for heavy medium/high) |
| Logo budget | Full OpenAI logo set ≈ $0.14; full Gemini logo set ≈ $0.23; scenes similar order |

Rough total smoke spend (core 8 + 2 extras): **~$0.42** (usage + registry).

---

## 7 · API / product quirks

| Topic | Finding |
|-------|---------|
| Gemini request mime | `imageConfig.outputMimeType` **unsupported** on consumer generateContent; ext follows response `mimeType` (often jpeg) |
| Gemini Interactions API | Deferred — stay on generateContent |
| Gemini `-n` | No native `n`; CLI loops calls |
| Gemini quality | Ignored (stderr note) |
| gpt-image-2 background | `--background transparent` **unsupported** |
| `--compression` | OpenAI jpeg/webp only |
| Size mapping Gemini | WxH aliases / `1K`/`2K`/`4K` → `imageConfig.aspectRatio` + `imageSize` |
| gpt-image-1* sizes | Strict enum; gpt-image-2 flexible WxH |
| Free-tier Gemini | Can return `429` with `limit: 0` |
| Empty/truncated OpenAI body | Rare; parse fails with `unexpected end of JSON input` |

---

## 8 · Recommendations

| Use case | Pick |
|----------|------|
| Final brand badge | gpt-image-2 high (`goya-logo-gpt-image-2.png`) |
| Favicon / app icon | gpt-image-1.5 or crop of nano-banana-pro |
| Cheap iteration | gpt-image-1-mini low or nano-banana-2-lite |
| Fast draft badge | nano-banana-2-lite |
| Dark/painterly scene | gpt-image-2 (then 1 / 1.5) |
| Speed-sensitive agent loops | Gemini (lite/2); expect OpenAI 30–120s+ |
| Cost-aware OpenAI | Prefer `low` + trust `usage` cost over registry |

**Workflow:** iterate on mini/lite → finalize on gpt-image-2 or 1.5.

---

## 9 · Artifact index (`output/`)

```
goya-cat.png / goya-cat-*.png          # scene generate bake-off
goya-logo-*.png                        # logo edit bake-off
smoke-*.{png,jpeg,json,err}            # fixed-size smoke + extras
smoke-veo-*.{mp4,png}                  # video smoke: clip + poster contact sheet
SUMMARY.md / SMOKE.md                  # narrative drafts (superseded by this file)
```

---

## 10 · How to reproduce

```bash
# smoke (all models)
PROMPT='flat red cube on white background, simple studio product photo'
gengoya generate "$PROMPT" --config personal --model gpt-image-2 \
  --size 1024x1024 --quality medium --out output --name smoke-gpt-image-2 --json --yes
gengoya generate "$PROMPT" --config personal --model nano-banana-2 \
  --size 1K --aspect 1:1 --out output --name smoke-nano-banana-2 --json --yes
# …repeat per model

# logo edit
gengoya edit output/goya-cat.png "flat circular logo …" --config personal \
  --model gpt-image-2 --quality high --out output --name goya-logo-gpt-image-2 --yes
```

---

## 11 · Video (Veo) — 2026-08-02

### 11.1 Provider landscape (why Veo only)

| Option | Status on 2026-08-02 | Verdict |
|--------|----------------------|---------|
| **Gemini / Veo 3.1** | live; same host + key gengoya already uses | **shipped (phase 1)** |
| OpenAI / Sora 2 | Videos API + all `sora-2*` **shut down 2026-09-24**; deprecation announced 2026-03-24; no named replacement | skipped — ~7 weeks of life |
| Aggregators (fal.ai, Replicate) | one key → many third-party video models; ~30–50% under official list | not pursued |
| Direct third-party vendors | separate portals, CN onboarding | not worth it |

### 11.2 Live model list

`GET /v1beta/models` with the personal key returns exactly three video models —
`veo-2`/`veo-3` were shut down 2026-06-30, so the registry ships only these:

```
models/veo-3.1-generate-preview       [predictLongRunning]
models/veo-3.1-fast-generate-preview  [predictLongRunning]
models/veo-3.1-lite-generate-preview  [predictLongRunning]
```

| Alias | api_id | $/s 720p | $/s 1080p | $/s 4k | Durations |
|-------|--------|----------|-----------|--------|-----------|
| `veo-3.1-fast` *(default)* | `veo-3.1-fast-generate-preview` | 0.10 | 0.12 | 0.30 | 4/6/8 |
| `veo-3.1` | `veo-3.1-generate-preview` | 0.40 | 0.40 | 0.60 | 4/6/8 |
| `veo-3.1-lite` | `veo-3.1-lite-generate-preview` | 0.05 | 0.08 | — | 4/6/8 |

Audio is generated with the clip at no extra charge. `1080p`/`4k` and reference
images accept only `--duration 8`. Aspect is `16:9` or `9:16` — no square.

### 11.3 Doc bugs found by live 400s

The REST samples on `ai.google.dev/gemini-api/docs/veo` are wrong twice. Both
are now pinned by unit tests:

| Docs say | API actually wants | Error if you follow the docs |
|---|---|---|
| `"durationSeconds": "8"` (string) | number `8` | `The value type for `durationSeconds` needs to be a number.` |
| `"image": {"inlineData": {"mimeType","data"}}` | `"image": {"bytesBase64Encoded","mimeType"}` | ``\`inlineData\` isn't supported by this model.`` |

Failed starts are **not** billed, so probing cost nothing; only a successful
start bills.

### 11.4 Smoke (generate + i2v)

| File | Model | Mode | Res/Dur | Cost | Wall | Notes |
|------|-------|------|---------|------|------|-------|
| `smoke-veo-lite.mp4` | veo-3.1-lite | t2v | 720p / 4s | $0.20 | 34.7 s | 2.06 MB; 4-frame poster shows real motion |
| `smoke-veo-i2v.mp4` | veo-3.1-lite | i2v (`--image goya-cat-nano-banana-2.png`) | 720p / 4s | $0.20 | 33.1 s | 1.28 MB; started with `--detach`, collected via `jobs fetch` |

Both operations completed on the 4th poll (~30 s at a 10 s interval), so the
default 15 m timeout is generous for short lite clips. Cost is always
`cost_source: "registry"` — no usage-based figure exists for video.

### 11.5 Poster frames

`--poster-frames N` (default 4) samples N evenly spaced timestamps with ffmpeg
(`-ss t -frames:v 1` per frame, then `scale=480:-2,tile=CxR`) and writes
`<clip>-poster.png`. This is what makes video usable by an agent at all: the mp4
path is printed for the human/pipeline, the poster path for `Read`. Missing
ffmpeg or a failed extraction degrades to a stderr note, never a failed run.

### 11.6 Reproduce

```bash
# text → video (cheapest tier)
gengoya video "a red paper boat floating down a rain puddle on asphalt" \
  --config personal --model veo-3.1-lite --resolution 720p --duration 4 \
  --out output --name smoke-veo-lite --json

# image → video, detached, collected later
op=$(gengoya video "slow push in, the cat blinks and turns its head" \
  --config personal --model veo-3.1-lite --resolution 720p --duration 4 \
  --image output/goya-cat-nano-banana-2.png --out output --name smoke-veo-i2v --detach)
gengoya jobs status "$op" --config personal
gengoya jobs fetch  "$op" --config personal --json

# what the API actually serves
curl -s -H "x-goog-api-key: $KEY" \
  "https://generativelanguage.googleapis.com/v1beta/models?pageSize=200" \
  | grep -o 'models/veo[^"]*'
```

## 12 · Gemini Omni 1.1 Flash (Interactions API) — 2026-10-04

Source docs (ai.google.dev omni guide, interactions reference, model page, pricing)
read first, then every claim probed against the live API with the `personal` key.
Failed/invalid requests are free; only completed generations bill.

### 12.1 Docs vs. live

| Docs say | Live |
|---|---|
| `response_format.duration` is a `string` | `"3"` and `3` → `400 Invalid input at 'response_format'`; **`"3s"` works**. Range enforced: `1s` → "less than the minimum allowed 3s", `11s` → "exceeds the maximum allowed 10s". The omni guide never mentions duration at all |
| background interactions poll via `GET /interactions/{id}` | `background:true` returns `status: in_progress` fine, but **GET returns `API_KEY_SERVICE_BLOCKED` / "Multiple authentication credentials received"** for this key (POST is allowed). Not retried further; assume key method restrictions. So the default path is the synchronous POST |
| `store=false` for speed | `background:true` + `store:false` → `store=true is required for background interactions`. Default store=true is kept so `previous_interaction_id` edits work |
| 1080p/4k "upscaled", ">4MB use delivery=uri" | 1080p 3s inline = 2.4 MB worked; `delivery:"uri"` verified at 360p (returns `files/<id>:download?alt=media`, downloads with the same key). 4k not run |
| uploaded-video edit (`type: video`, inline base64) | **`400 content_blocked` ("prompt contains sensitive words")** for both the flat and the `user_input`-wrapped shape, with an innocuous prompt. Matches the docs' regional caveat (uploads not available everywhere); unverified which. `previous_interaction_id` editing works |
| 5,792 tokens per second of 720p, ~$0.10/s | 720p exactly 17,376 video tokens / 3 s. **360p 5,793 tokens / 3 s (1,931/s = 1/3)**, **1080p 26,064 / 3 s (8,688/s = 1.5x)**. Thought tokens are reported separately from `total_output_tokens` and bill at the text rate (~$0.004-0.01/call) |

Also: output matches request exactly (360p → 640x360, 720p → 1280x720, 1080p →
1920x1080, 3.00 s video + audio stream, 24 fps). Wall time for 3 s: 360p 16-23 s,
720p 25 s, 1080p 45 s, edit 35 s.

### 12.2 Live calls (personal key, budget cap $3)

| # | What | Settings | Wall | Usage / cost |
|---|---|---|---|---|
| 0 | probe, prompt "x", background (cannot be fetched back) | 360p 16:9, model-chosen length | n/a | not retrievable, est ~$0.1-0.2 |
| 1 | t2v marble | 360p 3s | 22.9 s | 5,793 vid tok, $0.1096 |
| 2 | i2v (poster of #1) | 360p 3s | 19.5 s | 1,079 in (1,068 image) / 5,793 vid, ~$0.11 |
| 3 | t2v paper boat | 720p 3s | 25.0 s | 17,376 vid tok, ~$0.31 |
| 4 | t2v hummingbird | 1080p 3s | 44.9 s | 26,064 vid tok, $0.47 |
| 5 | t2v candle, `--delivery uri` | 360p 3s | 15.8 s | ~$0.11 |
| 6 | edit via job id ("Make the marble blue") | 360p 3s | 35.1 s | 5,319 in (5,314 video) / 5,793 vid, $0.12; marble turned blue, scene kept |
| 7 | `--detach` t2v, then `jobs status` | 360p 3s | start fast; GET blocked | ~$0.11 wasted, unfetchable |
| 8 | first+last frame (#1 poster to #5 poster) | 360p 3s | 16.0 s | $0.114; poster shows marble track morphing to candle |

Total about $1.7 (below the $3 cap). Outputs: `output/omni/` (gitignored).
Not run: 4k (price in registry is a guess), extend, file-upload edit (blocked, #6b: free 400s).

### 12.3 Side finding

`gengoya --verbose` printed `x-goog-api-key` in clear for new-style `AQ.` keys:
`Redact` was applied to header *values* only while the header regex needs the name.
Fixed (redact `name: value`), regression test added. The key appeared once in an
agent transcript before the fix; rotate if that transcript is retained.

### 12.4 Reproduce

```bash
gengoya video "A red marble rolling down a wooden track" --config personal \
  --model omni-flash --resolution 360p --duration 3 --out output/omni --json
gengoya video "Make the marble blue" --config personal --model omni-flash \
  --resolution 360p --duration 3 --edit <job_id> --out output/omni
```

## 13 · Music (Lyria) + speech (Gemini TTS) — 2026-10-04

Docs read: music-generation / generate-content music-generation, lyria-prompt-guide,
speech-generation, voice-design, model pages, pricing. Verified live on the personal
Gemini key; budget cap $1.50, spent ~$0.50.

### 13.1 Docs vs. live

| Docs say | Live |
|---|---|
| Two API styles (Interactions, generateContent) | **Both work with an API key** for Lyria; same MP3, Interactions wraps it in `steps[].content[]` (`{type:audio,data,mime_type}`). Chose `generateContent`. |
| Lyria 3.5 `responseFormat.audio.mimeType: "audio/wav"` gives WAV | **`"audio/wav"` is a 400** (`Invalid value at ...AudioResponseFormat.MimeType`). `"AUDIO_WAV"` (the TTS enum) is accepted but the song **still comes back MP3** (`ID3`, `audio/mpeg`) and is billed. Same through Interactions `response_format {type:audio, mime_type:audio/wav}`. WAV is not offered. |
| Clip / Pro are 48 kHz stereo | 44.1 kHz stereo MP3 (clip, 3.5, pro all) |
| Input token limit 131,072 (model pages) | ListModels reports 1,048,576 (irrelevant in practice) |
| `lyria-3-pro-preview` page lists model code `lyria-3-clip-preview` | copy-paste typo; both ids are served by ListModels |
| Docs imply a prompt-only control surface | confirmed: no duration / negative / seed field. `--duration 60` was a hint (got 70.1 s); 45 gave 63.8 s; clip is a fixed 30.6-30.8 s |
| Response: lyrics "or a JSON description of structure" | text is lyrics, timed lyrics for user lyrics (`[0.0:5.3] …`), generated lyrics with `[[A0]]`/`[:]` markers, bare `[[A0]]\n[[B1]]` for instrumentals, or just `<instrumental>` on the clip. Audio part came second. |
| 3.8 TTS unary returns WAV with a RIFF header; remove your PCM wrapper | **True** (24 kHz mono s16le). `responseFormat.audio.mimeType:"AUDIO_L16"` gives `audio/l16; rate=24000; channels=1` raw PCM, which gengoya wraps. |
| `responseFormat.audio.sampleRate` selects the rate | **Ignored**: 16000 requested with `AUDIO_WAV` and `AUDIO_L16`, both came back 24 kHz. |
| Audio tokens = 25 per second | measured ~32/s (236 tok / 7.36 s; 66 / 2.04 s; 171 / 5.32 s) |
| Multi-speaker: "configure two speakers" | exactly 2 enforced: 3 gives `number of speaker_voice_configs must equal 2` |
| Voice by name (`voice: "Kore"`) | works, case-insensitive (`kore`); a bad name is a bare `400 Request contains an invalid argument` with no field detail. `GET /v1beta/voices` lists 1000+ ids (e.g. `ar-eg-advisor-11`, lowercase prebuilt ids); `language_code` filtering returned nothing for ru/uk (no ru/uk voices listed), but Russian and Ukrainian text synthesise fine with prebuilt voices. |

### 13.2 Live calls (personal key)

| # | What | Settings | Wall | Cost | Output / ffprobe |
|---|---|---|---|---|---|
| 1 | clip, raw curl probe | instrumental chiptune | 7.1 s | $0.04 | mp3 44.1 kHz stereo 30.72 s |
| 2 | clip via CLI | `--instrumental`, lo-fi | 15.1 s | $0.04 | `music-clip.mp3` mp3 44.1 kHz 2ch 30.56 s |
| 3 | 3.5 song | `--duration 60`, generated lyrics | 29.9 s | $0.08 | `music-35-song.mp3` 44.1 kHz 2ch 70.09 s; `.txt` lyrics with `[[A0]]` markers |
| 4 | 3.5 `--format wav` (pre-fix, `audio/wav`) | | 0 s | $0 | HTTP 400 |
| 5 | 3.5 `AUDIO_WAV` instrumental | `--duration 45` | 24.6 s | $0.08 | `music-35-wav.mp3` (MP3!) 63.76 s |
| 6 | 3.5 via Interactions + wav | 20 s ambient | 24 s | $0.08 | mp3 (ID3) |
| 7 | clip + `--lyrics-file` (Russian) | | 8.9 s | $0.04 | 30.72 s; timed lyrics `[0.0:5.3] Утро выходит на крыши,` |
| 8 | clip image-to-music | `--image goya-cat.png` | 11.1 s | $0.04 | 30.77 s |
| 9 | pro | 30 s piano hint | 16.3 s | $0.08 | `music-pro.mp3` 44.1 kHz 2ch 60.84 s (hint ignored) |
| 10 | TTS flash | Puck, style "calm, unhurried" | 3.8 s | $0.0021 (19 in / 236 out) | `tts-flash.wav` pcm_s16le 24 kHz mono 7.36 s |
| 11 | TTS lite 2-speaker Ukrainian | Joe/Puck + Jane/Kore, per-turn style, `<laugh>` | 4.5 s | $0.0017 | `tts-lite-multi-uk.wav` 24 kHz mono 8.88 s |
| 12 | TTS lite Russian | Zephyr | 1.9 s | $0.0008 | `tts-lite-ru.wav` 4.36 s |
| 13 | TTS lite stdin + `--format pcm` | AUDIO_L16 wrapped | 2.5 s | $0.0006 | `tts-lite-l16wrap.wav` 24 kHz mono 3.24 s, header verified |
| 14 | TTS lite `--sample-rate 16000` (flag since removed), twice | WAV and L16 | 1.8-2.6 s | $0.0010 | both 24 kHz |
| p | raw-curl probes: lite short, L16, bad voice (400), lowercase voice, 2-speaker RU, 3-speaker (400) | | 1-3 s | ~$0.005 | |

Total about $0.50. Outputs under the session scratchpad `media-out/`, not in the repo.

### 13.3 Findings worth keeping

- A failed WAV request costs a full song: probe format enums with a free 400 first
  where possible (the `"audio/wav"` string was such a free 400; `AUDIO_WAV` was not).
- `--verbose` redaction holds for TTS/Lyria (`x-goog-api-key: ***`); the verbose dump
  does include the base64 audio body, so do not paste it.
- Re-check on 2027-01-01: TTS prices double; and whether `AUDIO_WAV` starts working for
  Lyria 3.5.

### 13.4 Reproduce

```bash
gengoya music "lo-fi beat, Rhodes chords, 82 BPM" --config personal --model lyria-3-clip-preview --instrumental
gengoya speak --file script.txt --speaker Joe=Puck --speaker Jane=Kore --config personal --model gemini-3.8-flash-lite-tts
ffprobe -v error -show_entries stream=codec_name,sample_rate,channels,duration -of default=nw=1 <path>
```
