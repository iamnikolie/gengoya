# gengoya — implementation spec

> **Status:** §1–§11 are the original image-only build contract and are shipped.
> **[§12](#12--video-phase-1--veo)** adds video generation (Veo) and is also
> shipped; read it alongside §2/§3/§5/§6, which it extends. **§13** adds Gemini
> Omni video; **[§14](#14--music-lyria-and-speech-gemini-tts)** adds music (Lyria)
> and speech (Gemini TTS), both on the existing Gemini key.

Agent-facing image-generation CLI. Named after Francisco Goya. Same doctrine as the
sibling tools `fibery` (`github.com/iamnikolie/fibery-cli`) and `gl`
(`github.com/iamnikolie/gitlab-cli`): the agent runs `gengoya <cmd>` from Bash and reads
**token-lean stdout**. Because the payload is an image (binary), the inversion that makes
this work is:

> **The CLI writes image files to disk and prints their paths to stdout. The agent then
> `Read`s the path — Claude Code / Cursor render images natively.** Bytes never go through
> stdout or the model context.

This document is the complete build contract. Implement exactly what is here. When a detail
is unspecified, copy the pattern from `fibery`/`gl` (cobra + `cmd/` one-file-per-command +
`internal/{config,client,...}` + embedded `skill.md` + Makefile symlink).

Module path: `github.com/iamnikolie/gengoya`. Binary: `gengoya`. Go: match sibling (`go 1.26`).

---

## 1 · Decisions already locked (do not relitigate)

1. **Binary name** `gengoya`.
2. **Providers**: OpenAI + Gemini, both behind a `Provider` interface; pluggable for more later.
3. **Operations**: `generate` **and** `edit`, both providers, from day one.
4. **Config is mandatory** — every command except `skill`/`--help`/`version` requires
   `--config <name>` (or `GENGOYA_CONFIG` env). No default profile. Missing → exit 1 with a hint.
   (This mirrors `gl` exactly.)
5. **Model registry, not hardcoded IDs** — model aliases → real API ids/prices/caps live in an
   embedded `registry.yaml` (via `//go:embed`), overridable per-profile. Model IDs rot fast;
   adding a new model must be a data edit, never a code change.

---

## 2 · Output contract (the core of "agent-friendly")

Every `generate`/`edit` run:

- **stdout** — the absolute path of each written image, **one per line**, nothing else.
  Pipeable; each line is directly `Read`-able by the agent.
- **stderr** — one human/agent status line per image:
  `model=<alias> id=<api_id> size=<WxH> quality=<q> cost≈$<n> ms=<n>` and, when the provider
  returns a rewritten prompt, `revised_prompt="<...>"` on its own line. (OpenAI gpt-image
  rewrites prompts; surfacing it tells the agent what was actually drawn.)
- **`--json`** — instead of the plain path list, write one JSON object to stdout:
  ```json
  {
    "images": [
      {"path": "/abs/out/cat-a1b2c3d4.png", "size": "1024x1024", "bytes": 1234567}
    ],
    "model": "gpt-image-2",
    "api_id": "gpt-image-2",
    "provider": "openai",
    "quality": "high",
    "revised_prompt": "…or empty…",
    "cost_estimate_usd": 0.20,
    "cost_source": "usage",
    "usage": {"input_tokens": 17, "output_tokens": 1756, "total_tokens": 1773},
    "elapsed_ms": 8123
  }
  ```

Exit non-zero on any provider error, writing the provider's error body to stderr (never a
silent success). Retry on HTTP 429 and 5xx with exponential backoff (3 tries), same as
`gl`/`fibery`.

---

## 3 · Command surface

```
gengoya generate "<prompt>"          --config <name>   # text → image(s)
gengoya edit <image>... "<prompt>"   --config <name>   # image(s) [+ --mask] → image(s)
gengoya video "<prompt>"             --config <name>   # §12/§13
gengoya music "<prompt>"             --config <name>   # §14: Lyria song / 30 s clip
gengoya speak "<text>"               --config <name>   # §14: Gemini TTS, 1 or 2 speakers
gengoya models [--kind K]            --config <name>   # print the resolved registry as a table
gengoya config init                  --config <name>   # write provider keys + defaults
gengoya config show                  --config <name>   # show profile: default provider/model, key state (masked)
gengoya skill                                          # print embedded skill.md
gengoya version                                        # also gengoya --version
```

`edit` args: all leading args are input image paths, the **last** arg is the prompt. Require
≥1 image + a prompt (`cobra.MinimumNArgs(2)` then split `args[:len-1]`, `args[len-1]`).

### Persistent flags (root)

| Flag | Default | Meaning |
|---|---|---|
| `--config <name>` | `$GENGOYA_CONFIG` | **Required.** Profile dir `~/.gengoya/<name>/`. No default. |
| `--provider openai\|gemini` | profile default | Override provider for this call. |
| `--model <alias>` | provider's registry default | Registry alias (see `gengoya models`). Setting `--model` infers its provider. |
| `--out <dir>` | `.` | Output directory (created if missing, `0755`). |
| `--name <base>` | derived from prompt | Output basename (no extension). |
| `-n <count>` | `1` | Number of images. |
| `--size <WxH\|auto\|1K\|…>` | model default | Validated per model; Gemini maps to `imageConfig`. |
| `--aspect <ratio>` | (none) | Gemini aspect ratio (`1:1`, `16:9`, …). |
| `--quality low\|medium\|high\|auto` | model default | OpenAI only; ignored (with a stderr note) for providers that don't support it. |
| `--format png\|jpeg\|webp` | `png` | OpenAI encoding; Gemini uses response mime for the file ext. |
| `--background auto\|transparent\|opaque` | (omit) | OpenAI only; transparent unsupported on gpt-image-2. |
| `--compression 0..100` | (omit) | OpenAI jpeg/webp `output_compression`. |
| `--moderation auto\|low` | (omit) | OpenAI only. |
| `--json` | false | JSON output (see §2). |
| `--verbose` | false | Dump request/response (key redacted) to stderr. |
| `--yes` | false | Reserved; confirm potentially costly batch (see §8). |

`--model` and `--provider` interaction: if `--model` given, resolve its provider from the
registry; if `--provider` also given and disagrees, exit 1. If only `--provider`, use that
provider's default model. If neither, use profile `default_provider` + `default_model`.

### Filename rules

- With `--name foo`: `foo.<ext>` for n=1; `foo-1.<ext>`, `foo-2.<ext>`, … for n>1.
- Without `--name`: `<slug>-<8hex>.<ext>` where `slug` = first ~6 words of the prompt,
  lowercased, non-alphanumerics → `-`, collapsed, trimmed, capped ~40 chars (fallback
  `image` if empty); `8hex` = first 8 hex chars of `sha256(prompt + "\x00" + index + "\x00" +
  unixnano)`. Extension from `--format`.
- Always de-dupe against existing files in `--out` by appending ` (n)` before the ext
  (reuse the `dedupeName`/`sanitizeFilename` approach from `fibery/cmd/files.go`).

---

## 4 · Config

Path: `~/.gengoya/<name>/config.yaml`, mode `0600`. `GENGOYA_HOME` env overrides the base dir
(test escape hatch — mirror `FIBERY_HOME`). One `internal/config` package owns load/save and
`gengoyaHome(profile)`.

```yaml
default_provider: openai         # required
default_model: gpt-image-2       # optional; falls back to the provider's registry default
providers:
  openai:
    api_key: sk-...
  gemini:
    api_key: AIza...
```

Key resolution order per provider: `providers.<p>.api_key` in config → env
(`OPENAI_API_KEY`; `GEMINI_API_KEY` then `GOOGLE_API_KEY`). If the selected provider has no key
from either source → exit 1 with a hint to run `gengoya config init --config <name>`.

`config init` prompts (stdin) for: default provider, OpenAI key, Gemini key (blank = skip),
optional default model; writes the file `0600`. `config show` prints default provider/model and,
per provider, whether a key is present (masked as `sk-…last4`) — never the raw key.

---

## 5 · Model registry

Embedded default at `internal/registry/registry.yaml` (`//go:embed`). If
`~/.gengoya/<name>/registry.yaml` exists, it **replaces** the embedded one (whole-file
override, simplest; document this). `internal/registry` loads it into:

```go
type Model struct {
    Provider    string   // "openai" | "gemini"
    APIID       string   // exact string sent to the provider
    Ops         []string // "generate", "edit"
    Sizes       []string // allowed --size values; first is the default when --size omitted
    Qualities   []string // allowed --quality; empty = provider ignores quality
    PriceNote   string   // human hint shown by `gengoya models`
    PricePerImg float64  // rough default-quality per-image USD for cost_estimate
}
```

Helpers: `Lookup(alias) (Model, ok)`, `DefaultFor(provider) alias`, `All() map[string]Model`,
`ValidateSize(m, size)`, `ValidateQuality(m, q)`. `gengoya models` renders alias · provider ·
api_id · ops · price_note as a table (`--json` dumps the map).

### `registry.yaml` — the seed content

Researched 2026-07-21; treat prices as rough. `default:` marks each provider's default alias.

> **Stale on purpose.** This block is the original seed, kept for the shape it
> demonstrates. `internal/registry/registry.yaml` is the live catalogue and has
> moved on from it — new models (the `gpt-image-2.5` pair), GA
> api_ids replacing `-preview` ones, corrected prices, and the video-only fields
> added in §12.4. Read the file, not this listing, for what ships today.

```yaml
# Model aliases → provider API ids. Edit to add/replace models; no rebuild needed
# if you drop a registry.yaml into ~/.gengoya/<profile>/.
defaults:
  openai: gpt-image-2
  gemini: nano-banana-2

models:
  # ---------- OpenAI (Image API: /v1/images/generations, /v1/images/edits) ----------
  gpt-image-2:
    provider: openai
    api_id: gpt-image-2
    ops: [generate, edit]
    sizes: [auto, 1024x1024, 1536x1024, 1024x1536, 2048x2048, 2048x1152, 3840x2160]
    qualities: [auto, low, medium, high]
    price_note: "low~$0.006 / med~$0.05 / high~$0.20 per img"
    price_per_img: 0.05
  gpt-image-1.5:
    provider: openai
    api_id: gpt-image-1.5
    ops: [generate, edit]
    sizes: [auto, 1024x1024, 1536x1024, 1024x1536]
    qualities: [auto, low, medium, high]
    price_note: "prev flagship"
    price_per_img: 0.04
  gpt-image-1:
    provider: openai
    api_id: gpt-image-1
    ops: [generate, edit]
    sizes: [auto, 1024x1024, 1536x1024, 1024x1536]
    qualities: [auto, low, medium, high]
    price_note: "older"
    price_per_img: 0.04
  gpt-image-1-mini:
    provider: openai
    api_id: gpt-image-1-mini
    ops: [generate, edit]
    sizes: [auto, 1024x1024, 1536x1024, 1024x1536]
    qualities: [auto, low, medium, high]
    price_note: "cheap/fast"
    price_per_img: 0.01

  # ---------- Gemini (Nano Banana; generateContent, responseModalities IMAGE) ----------
  nano-banana-2:
    provider: gemini
    api_id: gemini-3.1-flash-image-preview
    ops: [generate, edit]
    sizes: [auto, 1024x1024, 2048x2048, 3840x2160]   # 1K default, 4K supported
    qualities: []                                     # provider ignores --quality
    price_note: "1K $0.045 / 4K $0.151 per img"
    price_per_img: 0.045
  nano-banana-pro:
    provider: gemini
    api_id: gemini-3-pro-image-preview
    ops: [generate, edit]
    sizes: [auto, 1024x1024, 2048x2048]
    qualities: []
    price_note: "premium; complex scenes/text/brand; 1K-2K $0.134"
    price_per_img: 0.134
  nano-banana-2-lite:
    provider: gemini
    api_id: gemini-3.1-flash-lite-image
    ops: [generate, edit]
    sizes: [auto, 1024x1024]
    qualities: []
    price_note: "cheapest/fastest"
    price_per_img: 0.02
  nano-banana:
    provider: gemini
    api_id: gemini-2.5-flash-image
    ops: [generate, edit]
    sizes: [auto, 1024x1024]
    qualities: []
    price_note: "legacy"
    price_per_img: 0.039
```

> Note for implementer: the `-preview` suffixes and 4K-resolution knobs on the Gemini side are
> version-dependent and will change. That is exactly why they live in this data file. Do not
> encode any model id as a Go constant.
>
> That prediction landed: on 2026-09-16 Nano Banana 2 and Nano Banana Pro went GA
> and the registry dropped both `-preview` suffixes with no code change. Re-check
> with `GET /v1beta/models` before assuming any Gemini id is current — a model id
> that 404s on `generateContent` is dead, while a live one answers 400 on a
> deliberately bad `imageConfig.imageSize`, which is the free way to probe it.

---

## 6 · Provider interface & API details

```go
// internal/provider
type GenRequest struct {
    Model   registry.Model
    Prompt  string
    N       int
    Size    string   // "" or "auto" → omit / let provider default
    Quality string   // "" → omit
    Format  string   // png|jpeg|webp
    Images  [][]byte // edit only: input image bytes (0 for generate)
    Mask    []byte   // edit only, optional
}
type Image struct {
    Data          []byte
    Size          string // best-known "WxH" (from request or provider echo)
}
type GenResult struct {
    Images        []Image
    RevisedPrompt string
    CostUSD       float64 // usage-based when available; else n * PricePerImg
    CostSource    string  // "usage" | "registry"
}
type Provider interface {
    Generate(ctx context.Context, r GenRequest) (GenResult, error)
    Edit(ctx context.Context, r GenRequest) (GenResult, error) // r.Images non-empty
}
```

`cmd/` builds the `GenRequest`, calls the provider selected via the registry, then writes each
`Image.Data` to disk and prints paths. Shared HTTP client in `internal/client` (timeout ~300s —
image gen is slow; `--verbose`; 429/5xx backoff). Auth headers are set per provider.

### 6.1 OpenAI

- **Generate** — `POST https://api.openai.com/v1/images/generations`, header
  `Authorization: Bearer <key>`, JSON body:
  ```json
  {"model":"gpt-image-2","prompt":"<p>","n":1,"size":"1024x1024","quality":"high","output_format":"png"}
  ```
  Omit `size` when `auto`/empty; omit `quality` when empty.

  **Quality ladder (2026-09-16).** `gpt-image-2.5-flare` / `-sunburst` accept
  `low|medium|high|xhigh|max|auto`; every earlier GPT Image model stops at
  `high`. The API's own enum error still reads *"Supported values are: 'low',
  'medium', 'high', and 'auto'"* on **all** models — it is stale, and `xhigh`
  passes validation where `zzz` does not. Registry data is the contract; the
  error string is not evidence. Both 2.5 models bill identically ($5/$8/$30 per
  1M text-in/image-in/image-out) and share one output-token table:
  `tokens = ceil(u*h*(2e6 + w*h)/4e6)`, `u`/`h` derived from a per-quality base
  of 16/24/48/64/96 for low…max — 196 tokens at low 1024×1024, confirmed live
  against a real `usage` payload.

  `background: "transparent"` (with `output_format` png/webp) works on the 2.5
  pair — verified live, RGBA out with a fully transparent corner. It never
  worked on `gpt-image-2`, and that alone is a reason to default to 2.5.

  Response:
  ```json
  {"created":123,"data":[{"b64_json":"<base64>"}],"usage":{...}}
  ```
  Decode each `data[i].b64_json`. gpt-image models return b64 (no `url`). There is no
  `revised_prompt` field for gpt-image → leave empty.
- **Edit** — `POST https://api.openai.com/v1/images/edits`, `multipart/form-data`:
  fields `model`, `prompt`, `n`, `size`, `quality`, `output_format`; each input image as a
  repeated `image[]` file part (filename + `Content-Type` by extension — reuse a
  `detectContentType` map like `fibery/cmd/files.go`); optional `mask` file part (PNG w/ alpha).
  Response shape identical to generate.

### 6.2 Gemini (Nano Banana)

- Endpoint (both generate and edit):
  `POST https://generativelanguage.googleapis.com/v1beta/models/<api_id>:generateContent`,
  header `x-goog-api-key: <key>`, `Content-Type: application/json`.
- **Generate** body:
  ```json
  {"contents":[{"parts":[{"text":"<prompt>"}]}],
   "generationConfig":{
     "responseModalities":["IMAGE","TEXT"],
     "imageConfig":{"aspectRatio":"1:1","imageSize":"1K"}
   }}
  ```
  (`imageConfig` omitted when `--size` is `auto` and `--aspect` unset.)
- **Edit** body — prepend the input image parts before the text part:
  ```json
  {"contents":[{"parts":[
     {"inline_data":{"mime_type":"image/png","data":"<b64 of input>"}},
     {"text":"<prompt>"}]}],
   "generationConfig":{"responseModalities":["IMAGE","TEXT"],"imageConfig":{…}}}
  ```
  (Multiple input images = multiple `inline_data` parts. A mask, if provided, is passed as an
  additional image part; document that Gemini has no dedicated mask field.)
- **Response** — walk `candidates[0].content.parts[]`; each part with `inlineData`
  (`{"mimeType":"image/png","data":"<b64>"}`) is an output image (base64-decode). Text parts
  are ignored for output but the first text part may be surfaced as `revised_prompt` on stderr.
  Read `usageMetadata` if present (informational only). File extension follows response `mimeType`.
- **Size/quality/format**: Gemini ignores `--quality` (stderr note). `--format` is not sent
  (stderr note when not png); extension follows response mime. `--size` / `--aspect` map to
  `imageConfig.imageSize` (`1K`/`2K`/`4K`/`512`) + `aspectRatio` via `MapGeminiParams`
  (WxH aliases → native tokens; validated against per-model `image_sizes` / `aspect_ratios`
  in the registry). Lite = 1K only.

---

## 7 · Layout

```
main.go                      thin → cmd.Execute()
Makefile                     build/install (symlink ~/.local/bin/gengoya), test, vet
README.md                    user-facing
CLAUDE.md                    repo instructions (short; point at skill.md as SSOT)
.gitignore                   /gengoya binary, etc.
cmd/
  root.go                    cobra root, persistent flags, PersistentPreRunE (load config+registry+provider)
  generate.go                generate command
  edit.go                    edit command
  models.go                  models command
  config.go                  config init / config show
  skill.go + skill.md        embedded skill reference (//go:embed)
  version.go                 version (ldflags -X cmd.version=)
  output.go                  writeImages(), stdout/stderr/json emitters, filename/slug/dedupe helpers
  *_test.go                  pure-logic tests
internal/
  config/                    ~/.gengoya/<name>/config.yaml load/save, GENGOYA_HOME
  registry/                  registry.yaml (embedded) + loader + Lookup/Default/Validate
  client/                    HTTP client (timeout, 429/5xx backoff, --verbose, redaction)
  provider/                  Provider interface + openai.go + gemini.go
```

Build artifact `./gengoya` (gitignored). `make install` symlinks
`~/.local/bin/gengoya → $(CURDIR)/gengoya` (copy `gl`/`fibery` Makefile verbatim, swap `BIN`).

---

## 8 · Cost guard (billing-conscious, keep minimal)

- Always print `cost≈$<n>` on stderr and `cost_estimate_usd` in `--json`
  (`n * model.price_per_img`).
- If estimated batch cost for the call `> $0.50` **and** stdout is a TTY, require `--yes` or
  prompt `proceed? [y/N]`. When stdout is **not** a TTY (the agent case), never prompt — just
  proceed and report the cost on stderr. (Do not block the agent; just make spend visible.)

---

## 9 · Testing

- `*_test.go` next to code, `testify/assert`, **no network**. Cover: slugify, filename +
  dedupe, `--model`/`--provider` resolution, registry load + `ValidateSize`/`ValidateQuality`,
  config load/save round-trip via `GENGOYA_HOME` tmpdir, OpenAI request body builder, Gemini
  request body builder, and response parsers (feed canned JSON with a tiny b64 payload; assert
  decoded bytes). Factor each provider's request-build and response-parse into pure functions so
  they test without a live client.
- `make test` = `go test ./...`; `make vet` = `go vet ./...`. Both green before done.

## 10 · skill.md (embedded)

Write `cmd/skill.md` as the agent-facing SSOT (same role as `gl skill` / `fibery skill`): what
the tool is, the stdout=paths / `Read`-the-path contract, every command + flag with examples,
the model table (from the registry), the mandatory `--config` rule, and the cost note. Any CLI
surface change must update `skill.md` and `README.md` — state this in `CLAUDE.md`.

---

## 11 · Build sequence (suggested order for the implementer)

1. `go mod init github.com/iamnikolie/gengoya`; copy `Makefile`/`.gitignore` from `gl`, swap names.
2. `internal/config` + `GENGOYA_HOME`, with round-trip test.
3. `internal/registry` + embedded `registry.yaml` (§5 content) + loader/validators + tests.
4. `cmd/root.go`: persistent flags, `PersistentPreRunE` that enforces `--config`, loads config +
   registry, resolves provider/model/key, constructs the provider. Mirror `gl/cmd/root.go`.
5. `internal/client` HTTP (timeout, backoff, verbose+redaction).
6. `internal/provider` interface + `openai.go` (generate+edit) + `gemini.go` (generate+edit),
   with pure request-build / response-parse funcs + tests.
7. `cmd/output.go`: filename/slug/dedupe, `writeImages`, stdout/stderr/json emitters.
8. `cmd/generate.go`, `cmd/edit.go`, `cmd/models.go`, `cmd/config.go`, `cmd/skill.go`+`skill.md`,
   `cmd/version.go`.
9. `README.md`, `CLAUDE.md`. `go test ./...` + `go vet ./...` green. `make install`, then smoke:
   `gengoya models --config test`, `gengoya generate "a red cube on white" --config test --out /tmp`.

Done = both providers generate and edit end-to-end, paths on stdout, cost+revised_prompt on
stderr, `--json` valid, tests+vet green, `gengoya skill` prints the reference.

---

## 12 · Video (phase 1 — Veo)

Shipped 2026-08-02. Extends §2/§3/§5/§6; everything unstated here follows them.

### 12.1 Decisions locked

1. **Gemini/Veo only.** Same host, same `x-goog-api-key`, same profile key as the
   Gemini image path — **zero new auth, zero new accounts**.
2. **No OpenAI video.** The Videos API and every `sora-2*` model shut down
   **2026-09-24** with no announced replacement (Altman's "Spud" has no product,
   date, or price). Writing that provider would buy ~7 weeks of life.
3. **Sync by default, `--detach` optional.** A clip takes ~30 s (lite/4 s) to a
   few minutes; the default blocks and returns paths. `--detach` prints the
   operation name and records the job for later collection.
4. **Aggregators are out of scope.** A *many models, one key* provider (fal.ai
   and the like) would share Veo's start → poll → download shape, so
   `VideoProvider` is the seam; nothing else should need to change.
5. **Models are data, as in §5.** `kind: video` models live in the same
   `registry.yaml`; adding one is a data edit.

### 12.2 Output contract (extends §2)

An agent **cannot `Read` an mp4**. So `writeVideos` also emits a PNG contact
sheet of `--poster-frames` frames (default 4) next to the clip via `ffmpeg`:

- **stdout** — the clip path, then the poster path, one per line
- **stderr** — `model=… id=… res=… dur=…s aspect=… cost≈$… ms=…`
- no `ffmpeg` on PATH → stderr note, clip still written, no poster (never fatal)
- poster extraction failure is likewise reported and non-fatal — the clip is the
  deliverable

`--json` payload: `{"videos":[{"path","poster","bytes","mime"}], "model",
"api_id", "provider", "resolution", "duration_seconds", "aspect", "operation",
"job_id", "state", "cost_estimate_usd", "cost_source", "elapsed_ms"}`.

### 12.3 Command surface

```
gengoya video "<prompt>"        --config <name>   # text/image → video
gengoya jobs list                --config <name>   # recorded jobs, newest first
gengoya jobs status <id|op>      --config <name>   # single poll
gengoya jobs fetch  <id|op>      --config <name>   # wait → download → write
```

Video-only flags: `--image` (first frame), `--last-frame` (requires `--image`),
`--ref` (repeatable, ≤3), `--negative`, `--resolution`, `--duration`, `--person`,
`--detach`, `--poll-interval` (10s), `--timeout` (15m, 0 = none),
`--poster-frames` (4, 0 = off). `--aspect` is shared with images. `-n` must be 1
— Veo returns one clip per request.

Model resolution uses `video_defaults` (`resolveVideoModelProvider`), not the
image defaults: `--model` must name a `kind: video` alias; otherwise the profile
provider's video default is used, falling back to the only provider that has one.
So a profile with `default_provider: openai` still runs `gengoya video`.

### 12.4 Registry fields (extends §5)

`kind: video` models add: `resolutions`, `durations` (first entry = default for
both), `duration_by_resolution` (a resolution that accepts exactly one
duration), `ref_requires_duration`, `max_ref_images` and
`price_per_sec` (resolution → USD/s).
Constraints are enforced **client-side before the API call**; `ResolveVideoDuration`
fills an omitted `--duration` from those same rules.

Shipped aliases (verified live 2026-08-02 — only the 3.1 family is served;
veo-2/veo-3 were shut down 2026-06-30):

| Alias | api_id | $/s |
|---|---|---|
| `veo-3.1-fast` *(default)* | `veo-3.1-fast-generate-preview` | 0.10 / 0.12 / 0.30 |
| `veo-3.1` | `veo-3.1-generate-preview` | 0.40 / 0.40 / 0.60 |
| `veo-3.1-lite` | `veo-3.1-lite-generate-preview` | 0.05 / 0.08 / — |

### 12.5 Veo API — corrections to the published docs

Both of these were found by live 400s; the REST samples on
`ai.google.dev/gemini-api/docs/veo` are wrong. Encoded in `BuildVeoRequest` and
pinned by unit tests so a future refactor cannot silently regress:

1. `parameters.durationSeconds` must be a **number**. The docs show `"8"`; the
   API answers *"The value type for `durationSeconds` needs to be a number."*
2. Image inputs use the flat predict encoding
   `{"bytesBase64Encoded": "…", "mimeType": "…"}` — **not** `{"inlineData":{…}}`,
   which the API rejects with *"`inlineData` isn't supported by this model."*

Flow: `POST /v1beta/models/<api_id>:predictLongRunning` → `{"name": "models/…/operations/…"}`
→ poll `GET /v1beta/<operation>` until `done` → download
`response.generateVideoResponse.generatedSamples[].video.uri` with the API key
header (`DownloadURL` appends `alt=media` to `:download` URIs). `ParseVeoOperation`
also accepts the `generatedVideos[]` / `videos[]` shapes, surfaces `error.message`,
and turns an empty `raiMediaFiltered*` result into a real error instead of a
silent success.

### 12.6 Job records

`--detach` writes `~/.gengoya/<profile>/jobs/<id>.json` (mode 0600 — it holds the
prompt), where `id` is the operation's last path segment, sanitized. It stores
everything `jobs fetch` needs to finish without re-passing flags: operation,
model/api_id, prompt, resolution/duration/aspect, out dir, name, poster frames,
cost estimate, state (`pending|done|fetched|failed`) and, once written, paths.
`jobs fetch` on an already-fetched job re-prints recorded paths without
re-downloading; `--out`/`--name`/`--poster-frames` override the stored values.

### 12.7 Cost guard (extends §8)

Video is billed per **output second**: `duration × price_per_sec[resolution]`,
`cost_source: "registry"` (no usage-based source exists). The §8 $0.50 threshold
would fire on every call, so video uses **$1.00** (`videoCostThreshold`), which a
default `veo-3.1-fast` 8 s/720p clip ($0.80) stays under and a `veo-3.1` 8 s/1080p
clip ($3.20) trips. TTY-only, exactly as §8 — an agent is never prompted.

### 12.8 Layout additions

```
cmd/video.go       video command, param resolution, wait→fetch→write
cmd/jobs.go        jobs list/status/fetch
cmd/poster.go      ffmpeg poster/contact-sheet extraction
cmd/videoout.go    video filenames, writeVideos, emitVideoResult
internal/jobs/     job record persistence
internal/provider/veo.go   VideoProvider + Veo (start/poll/wait/download)
internal/client    DoBinary (binary-safe, no verbose body dump)
```

### 12.9 Provider resolution for jobs

`jobs status` / `jobs fetch` must use the provider **recorded on the job**, not
the one the current flags resolve to — a job collected from a profile whose
default is another provider would otherwise poll the wrong API. Both commands are
therefore marked `local: true` (no pre-run provider setup) and build their
provider via `videoProviderFor(job.Provider)`. `waitFetchWrite` takes the
provider as an argument and returns the updated job record.

## 13 · Video (phase 2 — Gemini Omni, Interactions API)

Shipped 2026-10-04. Model alias `omni-flash` → `gemini-omni-1.1-flash` (GA
2026-08-27; the deprecated `gemini-omni-flash-preview` is deliberately not
registered). Same `gemini` provider key, same `GEMINI_API_KEY`. Facts below are
live-verified unless marked otherwise (RESEARCH §12).

### 13.1 Plumbing

- `internal/provider/omni.go`: `Omni` implements `VideoProvider`; `GeminiVideo`
  (the only thing `videoProviderFor("gemini")` returns) routes `Start` by
  `IsOmniModel(api_id)` and `Poll/Wait` by the operation prefix `interactions/`.
  Veo records written before this change keep working (no prefix → Veo).
- An Omni "operation" is `interactions/<interaction id>`. Job id = the tail,
  hashed down to ≤64 chars when longer (`jobs.IDForOperation`).
- `VideoOperation` gained `Videos` (inline payloads) and `CostUSD`/`CostSource`;
  `waitFetchWrite` writes `op.Videos` plus anything downloaded from `op.URIs`.
  `jobs.Job` gained `cost_usd` / `cost_source`; `--json` reports usage cost when set.
- `BuildOmniRequest` / `ParseOmniInteraction` / `OmniCostFromUsage` are pure.

### 13.2 Request mapping

| Flag | Wire |
|---|---|
| prompt | `input` string, or a part list `[media…, {type:text}]` |
| `--aspect` | `response_format.aspect_ratio` (`16:9`, `9:16`) |
| `--resolution` | `response_format.resolution` (`360p|720p|1080p|4k`, default 720p) |
| `--duration N` | `response_format.duration: "Ns"` (3–10; **string with `s`**) |
| `--image` / `--last-frame` | image parts in order: first, last |
| `--ref` | image parts (no tags); not combinable with `--image` |
| `--edit <id>` | `previous_interaction_id` (id, job id or `interactions/<id>`) |
| `--edit <file>` | `{type:video,data,mime_type}` part, ≤15 MB inline |
| `--delivery` | `response_format.delivery` (auto: `uri` for 1080p/4k) |
| `--detach` | refused (see 13.3) |

`--negative` and `--person` are errors for Omni. No `task` parameter is sent
(docs: prompt-only inference preferred).

### 13.3 Execution modes

- **Only mode**: one blocking POST, `background=false`. The response already
  carries the clip; `Omni.Start` parks it so `Wait` returns without any GET.
- **`--detach` is refused** before any request. A `background: true`
  interaction cannot be read back with an API key: `GET /interactions/{id}`
  answers 400 "Multiple authentication credentials received" whether the key
  goes in the header or the query (verified 2026-10-04), while the same GET on
  a non-background interaction works. The live test billed ~$0.11 for a clip
  that was never retrievable.

- **No 5xx retry on the create POST.** The POST *is* the generation; a 5xx
  (e.g. a gateway 504) may come after the clip was generated and billed, so
  `client.DoOnce` retries 429 only and the error tells the user to check usage
  before re-running. A failed or video-less interaction reports its usage cost
  in the error. The same rule covers every billed POST in gengoya (OpenAI and
  Gemini image generate/edit, the Veo start); `client.Do` with 5xx retries is
  left for reads.
- **uri delivery** (default at 1080p/4k): `GeminiVideo.Download` waits for the
  `files/` handle to turn `ACTIVE` before fetching (one metadata GET for Veo).
- Flag combinations are validated by building the request body before the cost
  prompt, so a refused flag never follows a confirmed spend.

### 13.4 Cost

Pre-call estimate = `duration × price_per_sec[resolution]` (registry). After the
call, cost is recomputed from `usage`: input tokens × $1.50/1M + video output
tokens × $17.50/1M + other output + thought tokens × $9.00/1M, reported with
`cost_source: "usage"`. Measured video tokens/s: 360p 1,931 · 720p 5,792 · 1080p
8,688; 4k is an unverified guess ($0.40/s). Cost guard threshold stays $1.00.

### 13.5 Not done

Extend as a first-class flag (works by prompting through `--edit`, untested),
`task` parameter, `<FIRST_FRAME>`/`<IMAGE_REF_N>` prompt tags, video references,
Files API upload for >15 MB sources, `GET`-less `--detach`, 4k live run.

## 14 · Music (Lyria) and speech (Gemini TTS)

Shipped 2026-10-04. Two new registry kinds, `music` and `speech`, on the existing
`gemini` provider/key. Facts are live-verified (RESEARCH §13) unless marked.

### 14.1 Decisions locked

- **API style: `generateContent`**, not the Interactions API. Both work live with
  an API key for Lyria (identical audio; Interactions wraps it in `steps[]`), and
  `generateContent` is what images already use and what the TTS docs show. The
  Interactions API stays Omni-video-only (§13). A WAV request is ignored by both.
- Command names: `music` and `speak` (`say`/`tts` read worse; `speak` takes text
  where `music` takes a prompt). One command each; no `-n` (must be 1).
- Both kinds are **not** image models: `generate`/`edit`/`video` reject them with
  a pointer to the right command; `models --kind music|speech` filters them.
- Defaults are registry data: `music_defaults.gemini: lyria-3.5`,
  `speech_defaults.gemini: gemini-3.8-flash-tts`. `resolveKindModelProvider`
  mirrors the video resolver (profile default provider, then providers that
  declare a default for the kind), so an `openai` profile default still works.
- Billed POSTs use `client.DoOnce` (no 5xx re-POST, §13.3). The "may already be
  billed" note is appended for 5xx/transport errors only; a 4xx is a pre-work reject.
- Unit tests only; no network (`httptest` for the no-retry and header checks).

### 14.2 Music (`gengoya music`)

Models: `lyria-3.5` (default, GA, full song), `lyria-3-clip-preview` (always 30 s),
`lyria-3-pro-preview` (legacy full song). Flat per-song price in the registry
(`price_per_song`: $0.08 / $0.04 / $0.08), `cost_source: "registry"`.

Request: `POST models/<id>:generateContent`, `contents[0].parts` = `[{text}, {inline_data}…]`
(text first, then up to 10 images, the documented order). **No** duration,
negative-prompt, seed or structure parameter exists; `MusicPrompt` folds flags
into the prompt string:

| Flag | Effect |
|---|---|
| `<prompt>` | prompt text |
| `--instrumental` | appends `Instrumental only, no vocals.` |
| `--duration N` | appends `The song should be about N seconds long.`; refused for `fixed_seconds` models (clip); stderr says it is a hint |
| `--lyrics` / `--lyrics-file` | appended under a `Lyrics:` header (mutually exclusive with `--instrumental`) |
| `--image` (repeatable) | image parts; capped by registry `max_ref_images` (10) |
| `--format` | validated against registry `formats` (see 14.4) |

Response: every `inlineData` audio part (mime `audio/mpeg`) plus all text parts,
order-independent. Text is the lyrics / timed lyrics / structure markers
(`[[A0]]`, `[0.0:5.3] …`); `<instrumental>` alone counts as no text. Audio is
written as `<base>.mp3`; non-empty text goes to `<base>.txt`. stdout: audio path,
then the text path; `--json`: `audio[{path,lyrics,bytes,mime,duration_seconds}]`.
A response with no audio (safety block) errors with `blockReason`/`finishReason`.

### 14.3 Speech (`gengoya speak`)

Models: `gemini-3.8-flash-tts` (default), `gemini-3.8-flash-lite-tts`.

Request: `contents[0] = {role: user, parts: [{text, speech_metadata:{speaker?,style?}}]}`,
`generationConfig = {responseModalities:["AUDIO"], speechConfig}`. Text is a
verbatim transcript; style goes in `speech_metadata.style`, never into the text.

| Flag | Wire |
|---|---|
| `<text>` / `--file` / stdin (`-` or piped) | transcript |
| `--voice V` (default Kore) | `speechConfig.voiceConfig.voice` — prebuilt name (case-insensitive), Extended Voice Library id, or `voice_…` id; **not validated client-side** (the API answers a bare 400, which gets a hint) |
| `--style S` | `speech_metadata.style` (default style for every turn in dialogue mode) |
| `--speaker Name=Voice` ×2 | `multiSpeakerVoiceConfig.speakerVoiceConfigs[]` (`prebuiltVoiceConfig.voiceName`); one part per turn with `speech_metadata.speaker` |

Dialogue script: a line opens a turn only when its `Name:` / `Name [style]:` prefix
is a declared speaker; other lines continue the previous turn. **Exactly two
speakers** (API: "number of speaker_voice_configs must equal 2"; registry
`max_speakers: 2`).

Output: the unary API already returns a complete RIFF WAV (24 kHz mono s16le), which
is written untouched. `ParseSpeechResponse` still handles headerless PCM
(`audio/L16;rate=…`, the 3.1-preview default and the streaming default): it is wrapped
by `WrapPCMAsWAV` (pure Go, 44-byte header, no ffmpeg). `--format pcm` requests
`AUDIO_L16` to exercise that path. `sampleRate` is never sent (ignored live).
Voice design (`POST /v1beta/voices`, persistent `voice_…` ids, 200/project) is not
implemented; a designed id already works in `--voice`.

### 14.4 Cost

- Music: flat `price_per_song`; guard compares it to `audioCostThreshold` ($0.50, TTY only).
- Speech: from `usageMetadata` — TEXT prompt tokens × `price_text_in` + AUDIO
  candidate tokens × `price_audio_out` (per 1M), `cost_source: "usage"`; without
  usage, measured WAV seconds × `audio_tokens_per_sec` (**32**, measured; docs say
  25). Pre-call estimate: ~4 chars/token in, ~15 chars/s out. Rates are the
  introductory ones through **2026-12-31** (flash $9.00 / lite $6.00 audio-out,
  $0.50 text-in); from 2027-01-01 they double. Update the registry then.
- stderr line: `model=… id=… fmt=… bytes=… [dur=…s] [voice=…] cost≈$0.0000 (source) ms=…`.

### 14.5 Registry fields (extends §5, §12.4)

`kind: music` — `ops: [music]`, `formats` (first = default), `fixed_seconds`,
`max_ref_images`, `price_per_song`. `kind: speech` — `ops: [speak]`, `voices`
(informational list), `default_voice`, `max_speakers`, `price_text_in`,
`price_audio_out`, `audio_tokens_per_sec`. `music_defaults` / `speech_defaults`
top-level maps. `formats` for `lyria-3.5` is `[mp3]` only — WAV is documented but
not honoured live (RESEARCH §13.1); add `wav` there if that changes.

### 14.6 Layout additions

```
cmd/music.go cmd/speak.go    commands (script parsing, text input, flag folding)
cmd/audio.go                 kind resolver, writeAudio (+ .txt sidecar), emitAudioResult
internal/provider/audio.go   MusicRequest/SpeechRequest, Build*/Parse*, Gemini.GenerateMusic/Speak
internal/provider/wav.go     WrapPCMAsWAV, WAVSeconds, PCM mime parsing
internal/registry            KindMusic/KindSpeech, DefaultKindFor, EstimateSpeechCost
```

### 14.7 Not done

Voice design / replication / `GET /voices` listing command, streaming TTS, long text
chunking (input cap 8192 tokens), `mulaw`/`alaw` formats, Lyria WAV/48 kHz,
`--seed`/`--negative` (no API), Lyria RealTime (WebSocket; out of scope).
