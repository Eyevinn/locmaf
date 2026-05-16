---
marp: true
theme: locmaf
paginate: true
footer: 'LOCMAF · Low Overhead CMAF for MOQ · locmaf.dev'
size: 16:9
---

<!-- _class: lead -->

# LOCMAF
## Low Overhead CMAF for MOQ

<br>

Hugo Björs (MSc thesis) · Torbjörn Einarsson · Eyevinn · 2026

---

# Why a new packaging format?

- CMAF chunk = one `moof` + one `mdat`
- **Single-sample CMAF chunk header is ~104 B** of metadata
- LOC (~9 B) and WebCodecs carry **codec frames only** — no DRM, no CMAF semantics
- For low latency we want sample-level objects —
  moof overhead becomes a meaningful share of the wire cost (~25 % for low-bitrate audio)

> LOCMAF closes the gap to **as little as 2 bytes per delta moof** — and the same
> approach extends to **DRM-protected content** (`cenc` / `cbcs`, per-sample IVs,
> subsample maps carried verbatim, transparent to the CDM).

---

# The shape of the compression

![w:1000](../assets/logomark-dark.svg)

- Big CMAF chunks on the sender side
- Tiny LOCMAF deltas on the wire — **as low as 2 B per moof** (~45 : 1 on clear-content moof bytes)
- **Functionally lossless** CMAF chunks reconstructed on the receiver
- **Same shape for clear and DRM-protected content** — encrypted `mdat`, IVs, subsample maps round-trip unchanged

Sample bytes are byte-identical; framing is regenerated from the LOCMAF fields and catalog init.

---

<!-- _class: section -->

# How MoQ groups map to CMAF

---

# One group per segment, one object per chunk

![w:760](../assets/diagrams/moq-cmaf-mapping.svg)

- **Group boundary = random access point** (IDR for video)
- **Object = one CMAF chunk** (one `moof` + one `mdat`)
- Audio groups are aligned to video groups so tune-in is a joint operation

---

# Inside a group: ordered objects

- Each group is independent — a new subscriber can start at any group
- Inside a group, objects are delivered in order
- **The interesting part is the series of `moof` boxes**:
  consecutive `moof` headers are almost identical

---

<!-- _class: section -->

# Where LOCMAF wins:<br>the moof delta stream

---

# A moof has very predictable structure

![w:1080](../assets/diagrams/moof-anatomy.svg)

---

# Two-stage compression

1. **Tfhd against trex defaults.**
   If `tfhd` already matches the `trex` defaults in the moov, omit it.
2. **Delta encoding within a group.**
   First `moof` per group is *full*; every subsequent `moof` is a *delta*
   carrying only what changed. BMDT is derived from the previous moof.

`mdat` size is implied by the MoQ object length — the 8-byte `size + 'mdat'`
box header never goes on the wire.

---

# On the wire — one MoQ group

![w:860](../assets/diagrams/delta-stream.svg)

- Steady-state delta moof = **2 bytes** (header_id varint + length varint = 0)
- IDR / discontinuity transitions cost a handful of extra bytes
- The rest of the group runs flat

---

# Measured compression

![w:920](../assets/diagrams/bytes-saved.svg)

---

# Per-track wire bitrate (CMSF catalog)

| track                  | sample     | CMAF       | LOCMAF     | saved      |
| ---------------------- | ---------- | ---------- | ---------- | ---------- |
| `audio_128kbps_aac`    | 128.0 kbps | 171.5 kbps | 131.9 kbps | **23.1 %** |
| `video_400kbps_avc`    | 373.2 kbps | 396.4 kbps | 376.5 kbps |    5.0 %   |

- ~100 B → 2 B per moof ≈ **99.6 B/object** saved — essentially constant
- Percentage grows as track bitrate shrinks → **audio gains the most**
- 128 kbps AAC lands within ~3 % of the raw sample bitrate

---

<!-- _class: section -->

# Wire format

---

# Object framing

![w:1080](../assets/diagrams/object-framing.svg)

Same framing for every LOCMAF object kind. Unknown `header_id` is **logged and skipped** using `properties_length` — the format extends without breaking older decoders.

---

# Top-level object IDs

| ID | Symbol            | Object kind                              |
| -- | ----------------- | ---------------------------------------- |
| 21 | `MoovHeader`      | LOCMAF moov (init data in the catalog)   |
| 23 | `MoofHeader`      | full moof + mdat                         |
| 25 | `MoofDeltaHeader` | delta moof + mdat                        |
| 27 | `MoovGzipHeader`  | gzip-wrapped CMAF moov *(optional)*      |
| 31 | `MoofRawHeader`   | raw CMAF moof + mdat *(fallback)*        |

IDs start at 21 so they don't collide with LOC's properties (1–16).

---

# Properties: parity-typed tuples

The properties block is a flat sequence of `(field_id, value)` tuples.

- **Even ID → scalar varint** (no length prefix; self-describing varint)
- **Odd ID → length-prefixed bytes**
  (`field_id | value_length | value_bytes`, all varints)

Field IDs may appear in any order. The reference encoder emits them
sorted so the wire bytes are deterministic.

---

# Full moof: what is emitted

A full moof is the first moof of each group. It carries only fields whose
values are *not* derivable from the moov's `trex` defaults:

- `moofSampleCount` (always) + `moofBaseMediaDecodeTime` (always)
- `moofFirstSampleFlags` only if `trun` carries it
- Per-sample arrays (`sizes`, `flags`, `comp_time_offsets`) only if the
  source's `tr_flags` set them
- **`sizes` is dropped when `moofSampleCount == 1`** — the lone sample's
  size equals the `mdat` length, which the MoQ object length already gives us
- Encryption fields (`iv`, `subsamples`, …) only for encrypted tracks

For sample-level objects the typical full moof is **~6–20 B**.

---

# Delta moof: incremental encoding

1. **BMDT is derived**, not emitted, unless source diverges (preroll / splice)
2. **Each value is a signed zigzag delta** of its previous representation:
   - scalar (even ID): single zigzag varint
   - varint-list (odd ID): zigzag deltas element-wise
   - raw bytes (id 9 = IV): full bytes verbatim
3. `moofDeltaDeletedLocmafIDs` (ID 17) lists fields removed since previous moof

Empty payload = "no field changed since last moof." Steady state: **2 bytes**.

---

# Scope: CMAF-shaped MP4 only

CMAF (ISO/IEC 23000-19) tightens ISOBMFF in ways LOCMAF relies on:

- **One media track** per CMAF Track (§7.3.2)
- **One `trun`** per `traf` (Table 4, Format Req. = 1)
- **One `mdat`** per CMAF Chunk (§7.3.3.2: one `moof` followed by exactly one `mdat`)

That's what makes "one MoQ object = one `moof` + one `mdat`" unambiguous on the wire.

General fragmented MP4 may carry multiple `traf` / `trun` boxes, multiple `mdat` boxes, or multiplexed tracks — **LOCMAF does not address those layouts directly**. Source must be CMAF-conformant (or repackaged) first.

---

# Prerequisite: commensurate timescales

Each frame must have an **integer duration** in the chosen media timescale.

| stream                         | timescale | ticks/frame |
| ------------------------------ | --------- | ----------- |
| 48 kHz AAC                     | 48 000    | 1 024       |
| 60000/1001 fps video (NTSC)    | 60 000    | 1 001       |

Otherwise the per-frame duration drifts ±1 tick and must be sent per fragment — the 2-byte steady state is lost.

---

# Init segments — a smaller bonus

- CMSF carries `initData` in the catalog → init is a **one-time** cost
- LOCMAF-compressed init: **8–20 % of CMAF** for typical moovs
- Codec-config-heavy moovs (HEVC `hvcC` ~2.5 KB) → LOCMAF can only reach
  ~50–76 %; **gzip wraps better** there
- Both encodings can coexist behind the same `header_id` dispatch

---

<!-- _class: section -->

# DRM with LOCMAF

---

# Designed for protected MoQ streaming

- Primary use case: **low-latency DRM-protected streaming over MoQ**
- Encrypted `mdat` bytes carried **verbatim** — no re-encryption, no metadata loss
- Per-sample IV, subsample maps, `tenc` defaults (KID, IV size, pattern) all round-trip exactly
- Standard CDM / MSE / EME path on the receiver — **LOCMAF is invisible to the player**

> "Functionally lossless" extends to the CDM: the bytes the CDM reads — ciphertext, IVs, subsample ranges, KID — are byte-identical end-to-end.

---

# Catalog DRM signalling

CMSF carries the DRM description; LOCMAF doesn't replace it.

```json
{
  "contentProtections": [{
    "refID": "widevine",
    "scheme": "cbcs",
    "defaultKID": ["abcdef…789"],
    "drmSystem": {
      "systemID": "edef8ba9-…-d51d21ed",
      "laURL": "https://lic.example.com/wv",
      "pssh": "<base64-pssh>"
    }
  }],
  "tracks": [{
    "name": "video_400kbps_avc_drm",
    "packaging": "locmaf", "locmafVersion": "0.1",
    "contentProtectionRefIDs": ["widevine", "playready"]
  }]
}
```

---

# `cenc` vs `cbcs` on the wire

| scheme | per-sample IV         | subsample map     | extra delta-moof cost  |
| ------ | --------------------- | ----------------- | ---------------------- |
| `cenc` | per-sample, 8 or 16 B | ~3 B/subsample    | ~16 B IV + subsamples  |
| `cbcs` | constant IV in `tenc` | ~3 B/subsample    | subsamples only        |

- **Audio cbcs == audio clear** on the LOCMAF wire — no subsample encryption, constant IV in the moov
- Video carries the subsample map under both schemes
- A future **CENC IV counter-prediction** would erase the cenc/cbcs gap entirely

---

# Bitrate under DRM (measured)

| track            | scheme | CMAF       | LOCMAF     | saved      |
| ---------------- | ------ | ---------- | ---------- | ---------- |
| AAC 128 kbps     | `cbcs` | 191.4 kbps | 131.9 kbps | **31.1 %** |
| AAC 128 kbps     | `cenc` | 197.4 kbps | 138.6 kbps |    29.8 %  |
| AVC 400 kbps     | `cbcs` | 408.8 kbps | 378.5 kbps |     7.4 %  |
| AVC 400 kbps     | `cenc` | 412.0 kbps | 382.1 kbps |     7.3 %  |

LOCMAF saves **more** relative to CMAF under DRM than on clear content — the encrypted CMAF moof grows (`senc` + `saio` + `saiz`), while LOCMAF only emits what it needs.

---

<!-- _class: section -->

# Forward extensibility

---

# Headroom for new object kinds

- New `header_id` → new object kind. Old decoders **skip and log**.
- Most plausible next addition: **`prft`** (Producer Reference Time) for wall-clock signalling
  - Full first, signed-delta after; steady state ~7–8 B per object
- `sidx`, `emsg`, `tkhd` extensions follow the same pattern
- A `MoofRawHeader` / `MoofGzipHeader` fallback can carry any chunk the encoder can't express in delta form

---

# Catalog-level `locmafVersion`

- CMSF catalog Track carries `"locmafVersion": "0.1"` when `packaging == "locmaf"`
- Receivers compare against their highest supported version; fall back or refuse if the encoder is ahead
- Complements the header-ID skip-unknown rule, which only covers **additive** new object kinds
- Behavioural changes inside an existing kind (e.g. BMDT override in delta moof) bump the version

---

# Possible improvements

- **Pack `sample_flags` more compactly** (1 B instead of 4 B at IDR boundaries)
- **Predict CENC per-sample IVs** from the previous IV + protected-byte counter
  → up to **~960 B/s saved** at 60 fps with 16-byte IVs
- **`prft` with delta-of-delta** or approximate-timestamp variant
- **Pre-flight source validation** — refuse to LOCMAF-encode mismatched timescales
- **Strict `cmf2` mode** — emit `tfhd` defaults so fragments are self-decodable

---

<!-- _class: section -->

# Summary

---

# Summary

- **Per-fragment moof compression** is the main contribution
  - Sample-level objects: **2 B steady-state delta moof** = **45 : 1**
- **Init compression is a bonus**, not the goal
- **Header-ID varint** is the type tag and the extension hook
- Reference implementation in [Eyevinn/moqlivemock][moqlivemock] +
  [Eyevinn/warp-player][warp-player], demo at [moqlivemock.demo.osaas.io][demo]

[moqlivemock]: https://github.com/Eyevinn/moqlivemock
[warp-player]: https://github.com/Eyevinn/warp-player
[demo]: https://moqlivemock.demo.osaas.io

---

# References

- [draft-ietf-moq-transport](https://datatracker.ietf.org/doc/draft-ietf-moq-transport/) — Media over QUIC Transport
- [draft-ietf-moq-cmsf](https://datatracker.ietf.org/doc/draft-ietf-moq-cmsf/) — CMAF MoQ Streaming Format
- [draft-ietf-moq-loc](https://datatracker.ietf.org/doc/draft-ietf-moq-loc/) — Low Overhead Container
- [draft-lcurley-compressed-mp4](https://datatracker.ietf.org/doc/draft-lcurley-compressed-mp4/) — Compressed MP4
- **ISO/IEC 14496-12** ISO BMFF · **ISO/IEC 23000-19** CMAF · **ISO/IEC 23001-7** CENC
- *Efficient DRM in MoQ using Low Overhead CMAF* — Hugo Björs, KTH MSc Thesis, 2026

---

<!-- _class: closing -->

# THANK <span class="cyan">YOU</span>!

[**locmaf.dev**](https://locmaf.dev) · [github.com/Eyevinn/moqlivemock](https://github.com/Eyevinn/moqlivemock)
