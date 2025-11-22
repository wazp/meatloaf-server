# Meatloaf Server — Quick Start Guide (For Non-Technical Users)

This guide shows you how to run your own Meatloaf C64 file server using Docker.
You do NOT need Go, PHP, or any programming knowledge.

---

## 1. Install Docker

Download Docker Desktop:

- Windows: <https://www.docker.com/get-started>
- macOS: <https://www.docker.com/get-started>
- Linux: install via your package manager

---

## 2. Prepare Your Files

Create a folder on your computer with your C64 files:

- Example on Windows: `C:\meatloaf`
- Example on macOS/Linux: `/home/you/meatloaf`

Put `.d64`, `.prg`, `.tap`, `.crt`, etc. into this folder.

---

## 3. Run the Meatloaf Server

### Windows (PowerShell)

```powershell
docker run -d `
  -p 8080:80 `
  -v C:\meatloaf:/data `
  wazpys/meatloaf-server:latest
```

### macOS / Linux

```bash
docker run -d   -p 8080:80   -v /home/you/meatloaf:/data   wazpys/meatloaf-server:latest
```

---

## 4. Find Your Computer's IP Address

### Windows

Open PowerShell and type:

```powershell
ipconfig
```

Look for a line like:

`IPv4 Address . . . . . . . : 192.168.1.42`

### macOS

```bash
ifconfig | grep inet
```

### Linux

```bash
ip a
```

Your IP will look like: `192.168.x.x` or `10.x.x.x`.

---

## 5. Access Your Server

In a web browser, go to:

`http://YOUR-IP:8080/`

Example:

`http://192.168.1.42:8080/`

If you're using a Meatloaf device, point it to the same URL.

---

## 6. You're Done

Your Meatloaf device now has access to all your games and disk images stored in the
folder you mounted.
