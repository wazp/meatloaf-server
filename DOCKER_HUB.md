# Meatloaf Server

A tiny, blazing-fast replacement for the original Meatloaf PHP Server Script
available at <https://gist.github.com/idolpx/ab8874f8396b6fa0d89cc9bab1e4dee2>
written in Go.

This server exposes a directory of Commodore 64 files (PRG, D64, T64, CRT,
etc.) and returns either a normal HTML landing page or a C64-compatible BASIC
directory listing (`index.prg`) for the Meatloaf device.

You can find more information about Meatloaf here:
<https://meatloaf.cc>

---

## Features

- Generates C64 BASIC directory listings (`index.prg`)
- Serves `.d64`, `.prg`, `.tap`, `.t64`, `.crt`, and many more file types
- Mimics the original PHP Meatloaf script behavior
- Ultra-small footprint (around a few MB)
- Runs on any OS (Windows / macOS / Linux) via Docker
- Built for:
  - `linux/amd64`
  - `linux/arm64`

---

## Quick Start

```bash
docker run -d -p 8080:8080 -v /path/to/c64/files:/data:ro wazpys/meatloaf-server:latest
```

Then visit:

`http://localhost:8080/`

Or point your Meatloaf hardware to:

`http://YOUR-IP:8080/`, so on the C64 you'd type:
`LOAD"HTTP://192.168.1.10:8080",8` if your IP is `192.168.1.10`. After this
you can `LIST` the directory and load any file, as well as going into sub directories.

For more information about Meatloaf and its usage, please visit <https://meatloaf.cc>
or <https://github.com/idolpx/meatloaf>

---

## Docker Compose

To use **docker compose**, create a file named `docker-compose.yml`:

```
version: "3.9"

services:
  meatloaf:
    image: wazpys/meatloaf-server:latest
    container_name: meatloaf
    ports:
      - "8080:8080"
    volumes:
      # Change this path to the folder where your C64 files are stored
      - /path/to/c64/files:/data:ro
    restart: unless-stopped
```

Start the server:

```
docker compose up -d
```

Stop it:

```
docker compose down
```

---

## Notes

- Replace `/path/to/c64/files` with the folder containing your C64 disk images
  and PRG files.
- Replace `8080:` with whatever port you want to expose. The internal port must always be 8080.
- Files are mounted read-only by default (`:ro`).
- The container does not modify your C64 files.

### Prefer a native binary?

Releases also include prebuilt archives for Linux, macOS, and Windows:
<https://github.com/wazp/meatloaf-server/releases>.
Extract the archive for your OS/CPU and run `meatloaf-server -root /path/to/files -addr :8080` (8080 avoids needing root/Administrator on most systems).

---

## Source Code

See the GitHub repository for source code, Dockerfile, and CI configuration.
Can be found at: <https://github.com/wazp/meatloaf-server/>
