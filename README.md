# Meatloaf Server (Go + Docker)

> 👉 Want the fastest overview of how to run the published image? Check
> [GETTING_STARTED.md](GETTING_STARTED.md).

A tiny, blazing-fast replacement for the original [Meatloaf PHP Server Script](https://gist.github.com/idolpx/ab8874f8396b6fa0d89cc9bab1e4dee2).
This server exposes a directory of Commodore 64 files (PRG, D64, T64, CRT,
etc.) and returns either a normal HTML landing page or a C64-compatible BASIC
directory listing (`index.prg`) for the Meatloaf device.

The final Docker image is a **single static binary** running on a `scratch`
base image — extremely small, portable, and dependency-free.

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

## 📦 Running with Docker

Mount your folder of C64 files at `/data`:

```bash
docker run -d   -p 8080:80   -v /path/to/c64/files:/data   wazpys/meatloaf-server:latest
```

Open in browser:

```
http://localhost:8080/
```

Meatloaf hardware will automatically get the PRG listing.

---

## 🛠️ Build locally (optional)

You do **not** need Go installed — everything builds through Docker.

```bash
docker build -t meatloaf-server-local .
```

Run it:

```bash
docker run -p 8080:80 -v /path/to/files:/data meatloaf-server-local
```

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
