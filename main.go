// Meatloaf Server
// Version: 1.0.0
// 
// Changelog:
// 1.0.0 - Initial public release.
//         Rewrites the original PHP Meatloaf server as a Go static binary.
//         Supports directory listings, PRG generation, file serving, and HTML fallback.
// 0.9.0 - Internal prototypes; not publicly released.

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	basicStart = 0x0401
	headerText = "MEATLOAF ARCHIVE"
)

// same exclusion semantics as the PHP preg_filter
func listDirFiltered(root, dir string) ([]string, error) {
	absDir := filepath.Join(root, strings.TrimPrefix(dir, "/"))
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, e := range entries {
		name := e.Name()

		// skip dotfiles and . / ..
		if strings.HasPrefix(name, ".") {
			continue
		}

		lower := strings.ToLower(name)

		// skip .html, .php
		if strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".php") {
			continue
		}

		// skip api
		if lower == "api" {
			continue
		}

		// skip web.config
		if lower == "web.config" {
			continue
		}

		out = append(out, name)
	}

	return out, nil
}

func getType(root, dir, name string) string {
	relPath := filepath.Join(strings.TrimPrefix(dir, "/"), name)
	full := filepath.Join(root, relPath)
	info, err := os.Stat(full)
	if err == nil && info.IsDir() {
		return "DIR"
	}

	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	if len(ext) < 3 {
		ext = "PRG"
	}
	return strings.ToUpper(ext)
}

// uppercase ASCII like PHP's strtoupper (good enough for this use)
func toUpperASCII(b []byte) {
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
}

func writeUint16LE(w io.Writer, v uint16) error {
	buf := []byte{byte(v & 0xff), byte(v >> 8)}
	_, err := w.Write(buf)
	return err
}

// sendLine mimics PHP sendLine(): next pointer always 0x0101, line number = blocks
func sendLine(w io.Writer, blocks uint16, line string) error {
	// next line pointer (hard-coded in PHP)
	if err := writeUint16LE(w, 0x0101); err != nil {
		return err
	}
	// "line number" is actually blocks
	if err := writeUint16LE(w, blocks); err != nil {
		return err
	}

	lineBytes := []byte(line + "\x00")
	toUpperASCII(lineBytes)

	_, err := w.Write(lineBytes)
	return err
}

func sendListing(w io.Writer, root, dir, host string) error {
	// load address
	if err := writeUint16LE(w, basicStart); err != nil {
		return err
	}

	// HEADER line (truncate to 16 chars)
	hdr := headerText
	if len(hdr) > 16 {
		hdr = hdr[:16]
	}
	// 0x12 is reverse-video control char
	if err := sendLine(w, 0, "\x12\""+hdr+"\" 08 2A"); err != nil {
		return err
	}

	// directory listing
	entries, err := listDirFiltered(root, dir)
	if err != nil {
		// silently fall back to empty dir listing
		entries = []string{}
	}

	// extra info
	if err := sendLine(w, 0, fmt.Sprintf("\"%-19s\" NFO", "[URL]")); err != nil {
		return err
	}
	if err := sendLine(w, 0, fmt.Sprintf("\"%-19s\" NFO", host)); err != nil {
		return err
	}

	if len(dir) > 1 {
		if err := sendLine(w, 0, fmt.Sprintf("\"%-19s\" NFO", "[PATH]")); err != nil {
			return err
		}
		if err := sendLine(w, 0, fmt.Sprintf("\"%-19s\" NFO", dir)); err != nil {
			return err
		}
	}

	if err := sendLine(w, 0, "\"-------------------\" NFO"); err != nil {
		return err
	}

	// file entries
	for _, name := range entries {
		relPath := filepath.Join(strings.TrimPrefix(dir, "/"), name)
		full := filepath.Join(root, relPath)
		info, err := os.Stat(full)
		if err != nil {
			continue
		}

		typ := getType(root, dir, name)
		var blocks uint16
		blockSpc := 3

		if typ != "DIR" {
			size := info.Size()
			blocks = uint16((size + 255) / 256) // ceil(size/256)

			if blocks > 9 {
				blockSpc--
			}
			if blocks > 99 {
				blockSpc--
			}
		}

		line := fmt.Sprintf("%s%-18s %s",
			strings.Repeat(" ", blockSpc),
			"\""+name+"\"",
			typ,
		)

		if err := sendLine(w, blocks, line); err != nil {
			return err
		}
	}

	// final line
	if err := sendLine(w, 65535, "BLOCKS FREE"); err != nil {
		return err
	}

	// end-of-program marker
	_, err = w.Write([]byte{0x00, 0x00})
	return err
}

// C64-ish extensions that should be sent as application/octet-stream
var binaryExts = map[string]struct{}{
	".bas": {}, ".prg": {}, ".p00": {},
	".bin": {}, ".rom": {}, ".crt": {},
	".bbt": {}, ".d8b": {}, ".dfi": {}, ".rp9": {},
	".d64": {}, ".d71": {}, ".d80": {}, ".d81": {}, ".d82": {}, ".d90": {}, ".dnp": {},
	".g41": {}, ".g64": {}, ".g71": {}, ".nib": {}, ".nbz": {},
	".t64": {}, ".tcrt": {}, ".tap": {}, ".htap": {},
}

func isBinaryExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := binaryExts[ext]
	return ok
}

// HTML landing page – copied from your PHP script
const landingHTML = `<!doctype html>
<html lang="en">
    <head>
        <!-- Required meta tags -->
        <meta charset="utf-8">
        <meta name="viewport" content="width=device-width, initial-scale=1, shrink-to-fit=no">

        <!-- Bootstrap CSS -->
        <link rel="stylesheet" href="https://stackpath.bootstrapcdn.com/bootstrap/4.3.1/css/bootstrap.min.css" integrity="sha384-ggOyR0iXCbMQv3Xipma34
MD+dH/1fQ784/j6cY/iJTQUOhcWr7x9JvoRxT2MZw1T" crossorigin="anonymous">

        <link rel="apple-touch-icon" sizes="180x180" href="/apple-touch-icon.png">
        <link rel="icon" type="image/png" sizes="32x32" href="/favicon-32x32.png">
        <link rel="icon" type="image/png" sizes="16x16" href="/favicon-16x16.png">
        <link rel="manifest" href="/site.webmanifest">
        <style>
            #text {
                border-radius: 20px;
                padding: 20px;
                background-color: white;
                width: 75%;
                margin: 0 auto;
            }
            div.social {
                background-color: white;
                margin: auto;
                padding: 8px;
				border-radius: 8px 0 0 0;
				text-align: center;
				position: fixed;
				bottom: 0;
				right: 0;
				opacity: .8;
		    }
			div.social img {
				width: 125px !important;
				margin: 0 5px;
			}
			html, body {
				height: 100%;
				background-color: #4D4D4D;
				background-position: cover;
			}
			.link {
			   position: absolute;
			   width: 100%;
			   height: 100%;
			}
			.fullscreen-container {
				position: absolute;
				top: 20%;
				left: 10%;
				background-repeat: no-repeat;
				background-size: contain;
				background-image: url(https://meatloaf.cc/media/meatloaf.logo.svg);
				width: 80%;
				height: 80%;
			}
		</style>
	
        <title>Meatloaf C64</title>
    </head>
    <body>
        <a href="https://meatloaf.cc">
            <div class="link"></div>
            <div class="fullscreen-container">
            </div>
            <div class="social">
                <a href="https://discord.gg/FwJUe8kQpS" target="_blank"><img src="https://meatloaf.cc/media/discord.sm.png" class="img-fluid" /></a>
            </div>
        </a>
    </body>
</html>
`

func main() {
	rootFlag := flag.String("root", ".", "Root directory to serve (equivalent to DOCUMENT_ROOT)")
	addrFlag := flag.String("addr", ":80", "Address to listen on (e.g. :80, 0.0.0.0:8080)")
	flag.Parse()

	root, err := filepath.Abs(*rootFlag)
	if err != nil {
		log.Fatalf("failed to resolve root: %v", err)
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		// Normalized path with leading slash
		urlPath := r.URL.Path
		if urlPath == "" {
			urlPath = "/"
		}

		localPath := filepath.Join(root, filepath.Clean(strings.TrimPrefix(urlPath, "/")))

		// If the path refers to an existing regular file, serve it directly
		if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
			if isBinaryExt(localPath) {
				w.Header().Set("Content-Type", "application/octet-stream")
			}
			http.ServeFile(w, r, localPath)
			return
		}

		// Otherwise emulate index.php behavior
		if strings.Contains(r.UserAgent(), "MEATLOAF") {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="index.prg"`)
			w.Header().Set("Meatloaf-Debug", urlPath)

			if err := sendListing(w, root, urlPath, r.Host); err != nil {
				log.Printf("sendListing error: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
			return
		}

		// Non-Meatloaf: show landing HTML
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, landingHTML)
	}

	http.HandleFunc("/", handler)

	log.Printf("Serving %s on %s", root, *addrFlag)
	if err := http.ListenAndServe(*addrFlag, nil); err != nil {
		log.Fatal(err)
	}
}

