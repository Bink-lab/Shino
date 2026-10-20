# Shino

Steam depot downloader with a terminal UI. Point it at a SteamTools/LMS-style `.lua` config and a folder of `.manifest` files (or a single zip containing both), and it pulls the depots from Steam's CDN into an output folder.

![Shino TUI](docs/demo.gif)

Runs on Windows, macOS, and Linux. File pickers use native dialogs on Windows.

## Build

Go 1.27 or newer:

```
go build -o Shino.exe .
```

### CLI

```
./Shino.exe \
  -lua app_123456.lua \
  -manifests-dir ./manifests \
  -output ./game \
  -max-downloads 16 \
  -download
```

| Flag | Description |
| --- | --- |
| `-lua` | Game lua config. If omitted, the TUI starts. |
| `-manifests-dir` | Folder with `.manifest` files. A depot whose manifest is missing fails. |
| `-output` | Target download directory. Required with `-download`. |
| `-max-downloads` | Parallel chunk downloads per depot (default 16). |
| `-download` | Download after resolving the depot list. Without it Shino only parses and reports. |

## Lua config format

```lua
addappid(123456)
addappid(123457, 0, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
setManifestid(123457, "1234567890123456789")
pinapp(123456)
```

- `addappid(id[, flag[, key]])` declares a depot and its 64-character hex key
- `setManifestid(depotID, manifestGID)` pins the manifest to download
- `pinapp(id)` declares the app

Depots without a key or manifest fail during download.

## Contributing

Don't send AI-generated PRs. I'll close them without reading them. If you can't explain every line of your own diff, don't open it.

## License

Copyright (C) 2026 Bink-lab.

Licensed under the [GNU Affero General Public License v3.0 or later](LICENSE). The Shino name and logo are trademarks of Bink-lab and are not granted for use with modified versions.
