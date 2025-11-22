# Meatloaf Server

A tiny Dockerized version of the original Meatloaf C64/128 directory listing server.
Written in Go as a single static binary and packaged in a minimal `scratch` image.

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
docker run -d   -p 8080:80   -v /path/to/c64/files:/data   yourdocker/meatloaf-server:latest
```

Then visit:

`http://localhost:8080/`

Or point your Meatloaf hardware to:

`http://YOUR-IP:8080/`

---

## Source Code

See the GitHub repository for source code, Dockerfile, and CI configuration.

---

## License

GPLv3 (matches original Meatloaf license)
