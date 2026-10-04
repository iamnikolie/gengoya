# gengoya — repo notes

Agent-facing image + video + music + speech CLI. Doctrine matches sibling tools `fibery` and `gl`:
token-lean stdout (**paths** only); human/agent status on stderr.

Video keeps that doctrine honest with a poster frame: an agent cannot `Read` an
mp4, so every clip ships a `<clip>-poster.png` contact sheet whose path is
printed after the clip's. Never "fix" video output by dropping the poster.

## Sources of truth

- **[`SPEC.md`](SPEC.md)** — build contract / product decisions
- **[`cmd/skill.md`](cmd/skill.md)** — agent-facing CLI reference (embedded; `gengoya skill`)
- **[`README.md`](README.md)** — user-facing install/usage

Any CLI surface change (commands, flags, output contract, models) **must** update
`cmd/skill.md` and `README.md`.

## Layout

```
main.go
cmd/           cobra commands (one file per command) + skill.md
               video.go / jobs.go / poster.go / videoout.go = the video surface
internal/
  config/      ~/.gengoya/<profile>/config.yaml, GENGOYA_HOME
  registry/    embedded registry.yaml + Lookup/Validate (image + kind: video|music|speech)
  client/      HTTP (300s, 429/5xx backoff, verbose redaction); DoBinary for video bytes
  provider/    OpenAI + Gemini behind Provider; Veo + Omni behind VideoProvider
               (omni.go; GeminiVideo routes by model / "interactions/" operation prefix)
               audio.go (Lyria music + Gemini TTS on the Gemini struct) + wav.go
               cmd/music.go / speak.go / audio.go = the music + speech surface
  jobs/        detached video job records (~/.gengoya/<profile>/jobs/*.json)
```

## Conventions

- Module: `github.com/iamnikolie/gengoya`
- Mandatory `--config` / `GENGOYA_CONFIG` (no default profile)
- Provider request-build / response-parse are pure functions (unit-tested, no network)
- Errors wrap as `pkg.Method: %w`
- `make test` / `make vet` must stay green (CI: `.github/workflows/ci.yml`, gofmt + vet + test -race on Linux and macOS)
- **Model ids and their caps/prices are data** (`registry.yaml`), never Go constants —
  this now covers video `resolutions` / `durations` / `price_per_sec` too

## Model catalogue re-verified 2026-09-16

- OpenAI shipped `gpt-image-2.5-flare` / `-sunburst`: cheaper than `gpt-image-2`
  at equal quality, two extra steps (`xhigh`, `max`) above `high`, and working
  `--background transparent`. `flare` is now the OpenAI default.
- **The OpenAI quality-enum error string is stale** on every model — it names
  only `low/medium/high/auto` while `xhigh`/`max` validate fine. Check quality
  support against the docs table, never against that message.
- Nano Banana 2 and Nano Banana Pro went GA; registry api_ids dropped `-preview`.
  A dead Gemini id 404s on `generateContent`; a live one answers 400 to a bad
  `imageConfig.imageSize`, which probes it for free.
- Veo is unchanged: still the 3.1 family, still preview-only ids.

## Video gotchas (live-verified 2026-08-02)

- The published Veo REST samples are **wrong** in two places; unit tests pin the
  working shapes. `durationSeconds` must be a **number** (docs show a string), and
  image inputs use flat `{"bytesBase64Encoded","mimeType"}` — `inlineData` is
  rejected outright. See SPEC §12.5 before touching `BuildVeoRequest`.
- Only the `veo-3.1` family is served; `veo-2`/`veo-3` were shut down 2026-06-30.
  Verify with `curl -H "x-goog-api-key: $KEY" .../v1beta/models` before adding one.
- Video billing is per output second → its own cost guard ($1.00, not $0.50).

## Omni video gotchas (live-verified 2026-10-04)

- `gemini-omni-1.1-flash` uses `POST /v1beta/interactions`, not predictLongRunning.
  `response_format.duration` must be a Duration **string** (`"3s"`); `"3"`/`3` →
  `Invalid input at 'response_format'`. Range 3–10s enforced server side.
- Default path is ONE blocking POST (clip inline in `steps[].content[]`); `Omni.Start`
  parks the result so Poll needs no GET. `--detach` is refused for Omni: a
  background interaction can't be read back with an API key (`GET /interactions/{id}`
  → "Multiple authentication credentials received", header or query alike;
  non-background GETs work) — detaching billed a clip that could never be fetched.
- Cost = actual usage (`cost_source: usage`); tokens/s measured: 360p 1,931,
  720p 5,792, 1080p 8,688. 4k never run live. See SPEC §13, RESEARCH §12.
- `Redact` once logged `x-goog-api-key` unredacted under `--verbose` (value-only
  match, new-style `AQ.` keys); fixed — redact the whole `name: value` line.

## Music + speech gotchas (live-verified 2026-10-04)

- Both ride `generateContent` on the Gemini key; the Interactions API also serves
  Lyria but adds nothing. SPEC §14, RESEARCH §13.
- **Lyria WAV is a docs lie**: `responseFormat.audio.mimeType:"audio/wav"` is a 400,
  `"AUDIO_WAV"` is accepted and *still returns MP3* (also via Interactions) — and
  bills. `lyria-3.5` `formats` is `[mp3]`; never re-add `wav` without a live check.
  Clip/Pro are 44.1 kHz stereo, not the documented 48 kHz.
- Lyria has no duration/negative/seed field; `--duration` is prompt text only (asked
  60 s, got 70 s). Response text is lyrics / timed lyrics / `[[A0]]` structure
  markers, not always first; `<instrumental>` alone = no lyrics.
- Gemini 3.8 TTS unary returns a **complete WAV** (24 kHz mono s16le); the docs'
  "wrap raw PCM yourself" is the 3.1-preview/streaming behaviour. gengoya passes
  RIFF through and wraps `audio/L16` only if it ever shows up. `sampleRate` in
  `responseFormat` is ignored (16 kHz request → 24 kHz).
- Multi-speaker needs **exactly 2** speakers (API 400 otherwise). A bad voice name is
  a bare `400 Request contains an invalid argument` — voices are free-form (prebuilt,
  Extended Library ids like `ar-eg-advisor-11`, `voice_…`), so they are not validated
  client-side. Names are case-insensitive.
- TTS audio tokens measured **~32/s**, docs say 25. Cost is read from `usageMetadata`;
  introductory rates end 2026-12-31 (they double) — bump the registry on 2027-01-01.

## Deferred

- **Voice design / replication / `GET /v1beta/voices`** — a designed `voice_…` id
  already works in `--voice`; no create/list command yet.
- **Gemini Interactions API for images** — stay on `generateContent` (Omni video only).
- **Gemini request output mime** (`imageConfig.outputMimeType`) — not supported on
  consumer Gemini API; filenames still follow response `mimeType`.
- **OpenAI video (Sora 2)** — API shuts down 2026-09-24, no replacement. Do not
  implement; revisit only if OpenAI ships a successor with a public API.
- **`nano-banana-pro` at 4K** — added from the published price card ($0.24/img),
  not from a live 4K render.
