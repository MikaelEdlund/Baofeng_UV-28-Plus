# UV-28 Plus / Pro Bluetooth programming protocol

Reverse-engineered from the vendor Android app `com.silictec.kdh.radio`
(package `UV-28 Plus Programming app`, versionName 1.4.7). This documents the
"device family 16/19" path, which covers **BF-UV28PRO / UV-28 Plus,
BF-UV21PRO, BF-UV32PRO** and the closely related **BF-AR152PRO**. All four use
an identical frame format and memory map; only the advertised band limits and
the model string differ.

Everything here was recovered by decompiling the app (jadx + apktool/smali)
and cross-checked against CHIRP's `baofeng_uv17Pro.py`, which speaks the same
radio-side protocol over a serial cable. The `internal/radio` package in this
repo is a direct implementation of what follows, and its unit tests verify the
crypto and framing against an independent reimplementation of the app's Java.

## 1. Transport: the Bluetooth LE bridge

The radio contains a BLE↔UART bridge module that advertises with the device
name **`walkie-talkie`**. It exposes (UUIDs matched by 16-bit substring):

| Characteristic | Role                                             |
|----------------|--------------------------------------------------|
| `…ff31`        | **control** — module auth challenge is written here |
| `…ff32`        | control-notify (present on some modules)         |
| `…ffe1`        | **data** — a transparent pipe to the radio UART  |

The app enables notifications on `ff32` if present, otherwise on `ffe1`, and
writes data in **≤20-byte chunks** with a few ms between them. Responses from
the radio come back as a stream of notifications that must be reassembled by
length. (This implementation subscribes to both `ff32` and `ffe1` and merges
them, which is robust across module variants.)

## 2. Module authentication (BLE only)

Before the data pipe is usable, the app writes a 20-byte **challenge** to
`ff31`:

```
bytes 0..4 : 3F 3F 3F 3F 02        (0x02 selects the UV-28 Pro family)
bytes 5..19: random 0..98 each
```

The module replies (on the notify characteristic) with a 20-byte **key**. The
app accepts the session if the reply equals either of two derivations of the
challenge. With `c` the signed challenge bytes and `%` the Java (truncated)
modulo:

```
key[i]      = 0x21                                for i < 5
keyA[i]     = (sum(c[0..i]))            % (c[19-(i-5)] + (i-4))      (i>=5)
keyB[i]     = (sum(c[24-i-5 .. 24-i]))  % (c[24-i]     + (25-i))     (i>=5)
```

(`keyA` = `generateSecretKey`, `keyB` = `generateSecretKey_new` in the app.)
This is only a weak gate; its purpose is to confirm a genuine module. A cable
connection skips this step entirely.

## 3. Clone handshake (over the `ffe1` data pipe)

Identical to the serial clone protocol (and to CHIRP's `_do_ident` + magics):

| Step | Send                                   | Expect            |
|------|----------------------------------------|-------------------|
| 1    | `PROGRAMCOLORPROU` (16 ASCII bytes)    | `0x06` (ACK)      |
| 2    | `F` (0x46)                             | 16 bytes (info)   |
| 3    | `M` (0x4D)                             | 7 bytes = model, e.g. `UV28PRO` |
| 4    | key-select packet (25 bytes, below)    | `0x06` (ACK)      |

The **key-select packet** chooses the block cipher key. CHIRP uses a fixed
value; the app randomises it but derives the same index from it. The fixed
form (selecting key row 1) is:

```
53 45 4E 44 21 05 0D 01 01 01 04 11 08 05 0D 0D 01 11 0F 09 12 09 10 04 00
"S  E  N  D  !" ...
```

Index derivation from the packet `p`: `n = p[4]`; if `n & 0x20` then
`i = (n-0x20)*2+1` else `i = (n-0x10)*2`; key index = `p[i+5]`. For the packet
above this yields **1**.

## 4. Block read / write

Both use a 4-byte header and 128-byte (`0x80`) payloads.

```
READ   ->  52 AH AL 80                         ('R', addr hi, addr lo, len)
READ  <-   52 AH AL 80 <128 encrypted bytes>   (strip 4-byte header, decrypt)

WRITE  ->  57 AH AL 80 <128 encrypted bytes>   ('W', ...)
WRITE <-   06                                   (ACK)
```

Addresses are big-endian. There is **no CRC** for this radio family (the GT-12
/ UV-26 shell families do add a CRC-CCITT; not used here).

### Block cipher

A reversible XOR over each 128-byte payload. Key rows are 4 bytes; row 1 is
`"CO 7"` = `43 4F 20 37`. For payload byte `b` at position `j` with key byte
`k = row[j % 4]`:

```
out = b              if k == ' '(0x20) or b == 0x00 or b == 0xFF
                        or b == k or b == (k ^ 0xFF)
out = b ^ k          otherwise
```

Encryption and decryption are the same operation.

## 5. Memory map

Read/written as four regions (the app's `getValidAddr` for family 16/19):

| Region        | Contents                                   |
|---------------|--------------------------------------------|
| `0x0000`–`0x7CFF` | 1000 channels × 32 bytes               |
| `0x8000`–`0x807F` | VFO A/B and band settings              |
| `0x9000`–`0x907F` | general settings                       |
| `0xA000`–`0xA37F` | radio-ID DTMF, DTMF code list + names, zone names |

259 blocks of 128 bytes in total.

### Channel record (32 bytes, channel *n* at `n*32`)

First 16 bytes — frequency/tone/flags:

| Offset | Meaning |
|--------|---------|
| 0–3  | RX frequency, little-endian BCD pairs, MHz = `BBBBBBBB / 100000`. `0xFF` at offset 0 ⇒ channel empty. |
| 4–7  | TX frequency, same encoding. `0xFF` at offset 4 ⇒ TX disabled. |
| 8–9  | RX tone (lo, hi); see tone encoding. |
| 10–11| TX tone (lo, hi). |
| 12   | signal code (0–19 ⇒ shown as 1–20). |
| 13   | PTT-ID: 1=BOT, 2=EOT, 3=Both, else Off. |
| 14   | power: 0=High, 2=Mid, else Low. |
| 15   | flags: bit0 freq-hopping, bit2 scan-add, bit3 busy-lockout, bit4 mute-way, bit6 W/N. |

Second 16 bytes (`n*32 + 16`): the **channel name** is 12 bytes at offset 4–15,
GBK-encoded, padded with `0xFF`. (Offsets 0–3 are unrelated and preserved.)

### Tone encoding (`v = hi<<8 | lo`)

```
1   .. 105  -> DCS  "D"+code+"N"     (code = dcs_table[v-1])
106 .. 210  -> DCS  "D"+code+"I"     (code = dcs_table[v-106])
600 .. 2599 -> CTCSS v/10 Hz         (e.g. 885 -> "88.5")
otherwise   -> OFF
```

**"No tone" on a programmed channel must be stored as `0x0000`, not
`0xFFFF`.** On read the radio accepts either (both decode to OFF), but writing
`0xFFFF` to an active channel makes the radio transmit a continuous audible
tone. `0xFFFF` means "empty channel" only. The vendor app's channel editor
writes `00 00` when you type OFF (its tone-list dialog's use of `FFFF` is an
app-internal inconsistency that does not matter on read).

The 105-entry DCS table is the standard Baofeng order (see `tone.go`).

## 6. Notes / unknowns

* The app also has an `AT+BAUD?/AT+BAUD=/AT+RST` dance on `ff31` to set the
  module's UART speed (target 115200) and reboot it. Modules that already ship
  at the radio's rate skip it; this implementation attempts the clone
  handshake directly and reports clearly if the radio does not answer, which is
  where that step would need to be added.
* Settings-region byte meanings (regions `0x8000`/`0x9000`/`0xA000`) are partly
  mapped in the app's `OptionalFeaturesFrag`/`FreqModelFrag`; this tool
  round-trips them byte-for-byte and only edits channel records, so programming
  channels never disturbs other settings.
