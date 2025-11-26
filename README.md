# Meatloaf Server (Go + Docker)

> 👉 Want the fastest overview of how to run the Docker image? Check
> [GETTING_STARTED.md](GETTING_STARTED.md).

A tiny, blazing-fast replacement for the original [Meatloaf PHP Server Script](https://gist.github.com/idolpx/ab8874f8396b6fa0d89cc9bab1e4dee2).
This server exposes a directory of Commodore 64 files (PRG, D64, T64, CRT,
etc.) and returns either a normal HTML landing page or a C64-compatible BASIC
directory listing (`index.prg`) for the Meatloaf device.

Read more about Meatloaf:

- [meatloaf.cc](https://meatloaf.cc)
- [github repo](https://github.com/idolpx/meatloaf)

---

## ✨ Features

- 🟦 **C64 directory listing generator**
  Responds with a PRG-formatted listing when accessed with `User-Agent: MEATLOAF`.

- 📁 **File server for all major C64 formats**
  `.d64`, `.prg`, `.t64`, `.tap`, `.crt`, `.rom`, etc.

- 🌐 **HTML landing page**
  When accessed from a normal web browser.

- 🧩 **Drop-in replacement for the original PHP script**

- 🐳 **Ultra-small Docker image (~6–8MB)**
  Single static Go binary, no extra layers.

- 🔁 **Multi-arch support**
  GitHub Actions builds both `amd64` and `arm64`.

---
## 📥 Download prebuilt binaries

Every release publishes ready-to-run archives for Linux, macOS, and Windows:

- Go to the **Releases** page: <https://github.com/wazp/meatloaf-server/releases>
- Download the archive that matches your OS/CPU (e.g. `meatloaf-server_linux_amd64.tar.gz`, `meatloaf-server_linux_arm64.tar.gz`, `meatloaf-server_darwin_arm64.tar.gz`, `meatloaf-server_windows_amd64.zip`).
- Extract it and run the binary:

```bash
tar -xzf meatloaf-server_linux_amd64.tar.gz
./meatloaf-server -root /path/to/c64/files -addr :8080
```

On macOS (Apple Silicon example), download the `darwin_arm64` archive and run:

```bash
tar -xzf meatloaf-server_darwin_arm64.tar.gz
./meatloaf-server -root /path/to/c64/files -addr :8080
```

On Windows, unzip and run:

```powershell
meatloaf-server.exe -root C:\path\to\c64\files -addr :8080
```

Port 8080 is the default so it runs without elevated privileges on most systems. Adjust `-addr` if you need a different port.

## 🕹️ How to use

1) Start the server from the folder that holds your C64 files (or point `-root` elsewhere):

```bash
./meatloaf-server -root /path/to/c64/files -addr :8080
```

Flags:
- `-root` — directory to serve (default `.`)
- `-addr` — listen address/port (default `:8080`)
- `-v`, `-version` — print version and exit

2) Verify in a browser (HTML landing page): `http://localhost:8080/`

3) Point your Meatloaf hardware at the same host. It automatically receives the PRG listing and can load files. Example for host `192.168.1.42`:

`LOAD"HTTP//192.168.1.42:8080",8`

You should see a BASIC-style listing of your served files, ready to `LOAD`.

---

## 📦 Running with Docker

The Docker image is a **single static binary** running on a `scratch`
base image — extremely small, portable, and dependency-free.

Mount your folder of C64 files at `/data`:

```bash
docker run -d   -p 8080:8080   -v /path/to/c64/files:/data   wazpys/meatloaf-server:latest
```

---

## 🛠️ Build locally (optional)

You do **not** need Go installed — everything builds through Docker.

```bash
docker build -t meatloaf-server-local .
```

Run it:

```bash
docker run -p 8080:8080 -v /path/to/files:/data meatloaf-server-local
```

To build the binaries locally, simply use:

```bash
docker run --rm -v "$PWD":/workspace -w /workspace goreleaser/goreleaser:latest release --snapshot --clean
```

This will build all the binaries and place them under `/dist`.

---

## 📂 Project Structure

```
meatloaf-server/
├── src/
│   └── main.go
├── go.mod
├── go.sum
├── Dockerfile
└── .github/workflows/docker.yml
```

---

## 📄 License

GPLv3 (matching original Meatloaf licensing)
