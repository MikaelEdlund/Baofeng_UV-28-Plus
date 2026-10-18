# uv28prog — Baofeng UV-28 Plus/Pro Bluetooth programmer for Windows

A small command-line tool that programs a **Baofeng UV-28 Plus / UV-28 Pro**
(and the related **UV-21 Pro**, **UV-32 Pro**, **AR-152 Pro**) over its
built-in **Bluetooth LE**, from Windows — no Android app required.

It reverse-implements the vendor app's protocol (see `PROTOCOL.md`). It reads
the radio to a backup file, lets you edit channels as a plain CSV in any
spreadsheet, and writes them back.

> ⚠️ **This is built from reverse engineering and has so far been tested
> successfully against only one physical radio (the author's own).** Other
> radios, firmware versions and Bluetooth modules may behave differently, so
> treat your first `read` as the important test. `read` is non-destructive;
> always keep the backup it makes before you `write`. `write` also saves a
> fresh backup of the radio automatically before changing anything.
>
> Whether it works for you or not, please [share how it went](#feedback).

## Quick start

1. On the radio, turn Bluetooth on. In **Windows → Settings → Bluetooth &
   devices**, if a device called **`walkie-talkie`** appears, pair it. (If it
   doesn't need pairing, that's fine too.)
2. Open a terminal (PowerShell or cmd) in the folder with `uv28prog.exe`.

```
uv28prog.exe scan
```

You should see a line with an address and the name `walkie-talkie`. Then:

```
uv28prog.exe read -o mine.json
```

That reads the whole radio into `mine.json` (your backup). If this works,
everything else will.

```
uv28prog.exe export -i mine.json -o channels.csv
```

Edit `channels.csv` in Excel/LibreOffice/Notepad. Then merge it back and write:

```
uv28prog.exe import -i mine.json -csv channels.csv -o new.json
uv28prog.exe write  -i new.json -yes
```

`write` reads and saves a timestamped backup first, then writes `new.json`.
By default only the channel memory is written. Use `-full` to write every
region (advanced). If the image's model string does not match the radio,
the write is refused unless you add `-force-mismatch`.

## Commands

| Command | What it does |
|---------|--------------|
| `scan` | List nearby radios. |
| `read -o FILE` | Read the radio into an image file (full backup). |
| `write -i FILE -yes` | Write an image file to the radio (auto-backup first). |
| `export -i FILE -o CSV` | Export channels to CSV (`-all` to include empty slots). |
| `import -i FILE -csv CSV -o OUT` | Merge an edited CSV into an image. |
| `show -i FILE` | Print the channel list from an image. |

Useful flags: `-addr <MAC>` to target a specific radio, `-timeout 30s`,
`-v` for a full packet-level log, and `-force-mismatch` to override a model
mismatch on write.

`export`, `import` and `show` work on any OS; `scan`/`read`/`write` need the
Windows build.

## CSV columns

`Channel, Name, RXFreq, TXFreq, RXTone, TXTone, Power, Narrow, Scan,
BusyLock, FreqHop, SigCode, PTTID`

* **RXFreq / TXFreq** — MHz, e.g. `145.50000`. Leave `RXFreq` empty to clear
  the channel. Leave `TXFreq` empty for a receive-only channel.
* **Tones** — `OFF`, a CTCSS value like `88.5`, or DCS like `D023N` / `D023I`.
* **Power** — `High`, `Mid`, or `Low`.
* **Narrow/Scan/BusyLock/FreqHop** — `1` or `0`.
* **PTTID** — `Off`, `BOT`, `EOT`, `Both`.

Only channels listed in the CSV are changed; anything you delete from the CSV
is simply left as-is on the radio (it is not erased). To erase a channel, keep
its row but clear `RXFreq`.

## If it doesn't work

Run with `-v` and look at the log:

* **"no radio found"** — the radio isn't advertising. Turn Bluetooth on at the
  radio, keep it close, and try `scan` again. Pair it in Windows first if
  needed.
* **Connects, then "no ack to program magic"** — the BLE link is up but the
  radio isn't answering the clone handshake. The verbose log shows exactly how
  far it got; that output is what's needed to extend the handshake (this is the
  one spot where some modules need an extra baud-set step — see the end of
  `PROTOCOL.md`).
* Anything else — the `-v` log from the point of failure is the thing to
  capture, and to include when you report it (see [Feedback](#feedback)).

## Feedback

So far this tool has been confirmed on a single radio, so every report helps,
including "it just worked". Please
[open an issue](https://github.com/MikaelEdlund/Baofeng_UV-28-Plus/issues) and
say:

* which radio you have, and the model string that `read` prints
  (e.g. `Model "UV28PRO"`)
* your Windows version
* which commands you ran (`scan`, `read`, `write`, …) and whether they worked
* if something failed, the `-v` log from the point of failure

## Building from source

Needs Go 1.22+. Dependencies are vendored, so it builds offline:

```
# Windows binary (from any OS)
GOOS=windows GOARCH=amd64 go build -mod=vendor -o uv28prog.exe ./cmd/uv28prog

# native binary (offline commands only off Windows)
go build -mod=vendor -o uv28prog ./cmd/uv28prog

go test -mod=vendor ./...
```

Layout:

```
cmd/uv28prog        CLI and the Windows BLE transport
internal/radio      protocol, crypto, memory map, channel codec, CSV, tests
PROTOCOL.md         the reverse-engineered protocol
```

## Safety / scope

This talks to **your own** radio over its own Bluetooth module to do what the
vendor app does. It edits channel memory and round-trips all other settings
byte-for-byte. Keep the backups it produces; if a write ever leaves the radio
unhappy, `write` the backup file to restore it. You can also use the built in
menu option `Radio Setting -> Reset` to restore to factory defaults.
