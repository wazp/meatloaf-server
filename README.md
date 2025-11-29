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

- 🧭 **Directory navigation**
  Full support for directory navigation. The firmware handles CD commands locally and constructs appropriate HTTP requests.

- 🌐 **HTML landing page**
  When accessed from a normal web browser.

- 🧩 **Drop-in replacement for the original PHP script**

- 🐳 **Ultra-small Docker image (~6–8MB)**
  Single static Go binary, no extra layers.

- 🔁 **Multi-arch support**
  GitHub Actions builds both `amd64` and `arm64`.

- 📂 **Per-client CWD tracking**
  Maintains current working directory state for each Meatloaf client.

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

**Basic directory listing:**
```basic
LOAD"HTTP://192.168.1.42:8080",8
LIST
```
`LOAD"HTTP//192.168.1.42:8080",8`

**Directory navigation:**
```basic
LOAD"HTTP://192.168.1.42:8080/GAMES",8   : REM Navigate to GAMES directory
LOAD"SUBDIR",8                           : REM Navigate to subdirectory (relative)
LOAD"HTTP://192.168.1.42:8080/",8        : REM Go to server root
LOAD"HTTP://192.168.1.42:8080/PARENT",8  : REM Navigate to parent directory 
LOAD"$",8                                : REM List current directory
```

**⚠️ Known Issue:** CD up commands (`LOAD"CD_",8`) may not work consistently with all servers. Your server now includes Apache-compatible CD command handling that processes CD requests server-side. If CD commands don't work, use full HTTP URLs for navigation instead.

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

## 🧪 Testing & Development

This project includes comprehensive unit tests for all functionality, including CMD-style directory operations, BASIC program generation, and file serving.

### Running Tests with Docker (Recommended)

Since Go is not installed locally, use Docker for testing:

```bash
# Run all tests (basic test runner)
docker compose -f docker-compose.test.yml run --rm test

# Run tests with coverage report (generates coverage.html)
docker compose -f docker-compose.test.yml run --rm test-coverage

# Run performance benchmarks  
docker compose -f docker-compose.test.yml run --rm benchmark

# Start a debug server with test data for manual testing
docker compose -f docker-compose.test.yml up debug-server
# Then visit http://localhost:8080 or test with Meatloaf hardware

# Clean up test containers when done
docker compose -f docker-compose.test.yml down
```

**Test Results:** The test suite achieves **83.5% code coverage** and includes comprehensive tests for all CMD functionality, CD navigation, BASIC program generation, and file serving.

### Test Coverage

The tests cover:
- ✅ User-Agent detection (Meatloaf vs regular browsers)
- ✅ CMD command parsing (`$=T`, `$=P`, `$=U` with patterns and filters)
- ✅ Wildcard pattern matching (CBM-style `*` and `?`)
- ✅ Directory listing generation (CBM and CMD formats)
- ✅ BASIC program structure validation
- ✅ File serving for different formats
- ✅ CWD tracking and fallback logic
- ✅ Binary file detection and content-type headers

### Adding New Tests

Tests are in `main_test.go`. To add new functionality tests:

1. Add test cases to the relevant `Test*` function
2. For CMD features, add cases to `TestCmdDirectoryListings`  
3. Run tests: `docker compose -f docker-compose.test.yml run --rm test`

**Benchmark Results:**
- Directory listing generation: ~51μs per operation
- CMD command parsing: ~20ns per operation

### Manual Testing & Debugging

For manual testing against real Meatloaf hardware:

```bash
# Option 1: Start server with verbose logging using your own files
docker run -p 8080:8080 -v /your/files:/data wazpys/meatloaf-server -verbose

# Option 2: Use the debug server with pre-created test data
docker compose -f docker-compose.test.yml up debug-server
```

Then test CMD commands on your C64:
```basic
REM Basic directory
LOAD"HTTP://YOUR.SERVER.IP:8080",8
LIST
```


---

## 📄 License

GPLv3 (matching original Meatloaf licensing)
