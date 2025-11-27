// Meatloaf Server
// Version: 1.2.0
//
// Changelog:
// 1.2.0 - Tracks per-client CWD (like Apache+PHP effectively do for Meatloaf).
//         CWD-aware fallback:
//         Direct path first (decoded URL).
//         If that doesn't exist, try CWD + "/" + basename(path).
// 1.1.1 - Fixed unescaped-space handling for Meatloaf firmware.
//         Now normalizes paths like "Some File.d64" → "Some%20File.d64"
//         Restores Apache/mod_rewrite behavior.
// 1.1.0 - Added Content-Length, Last-Modified, and Accept-Ranges headers to file responses for improved performance.
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
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	basicStart = 0x0401
	headerText = "MEATLOAF ARCHIVE"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"

	verbose bool // <-- NEW: verbose logging enabled

	// Per-client current working directory (CWD)
	clientCWD sync.Map
)

//
// -------------------- LOGGING -----------------------
//

// helper for verbose logs
func vlog(format string, a ...interface{}) {
	if verbose {
		log.Printf(format, a...)
	}
}

func logRequest(r *http.Request, decoded string, cwd string) {
	if !verbose {
		return
	}

	log.Printf("---- REQUEST ----")
	log.Printf("RemoteAddr:     %s", r.RemoteAddr)

	if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
		log.Printf("CF-Connecting-IP: %s", cf)
	}

	log.Printf("Method:         %s", r.Method)
	log.Printf("Raw URL.Path:   %q", r.URL.Path)
	log.Printf("Decoded Path:   %q", decoded)
	log.Printf("User-Agent:     %q", r.UserAgent())
	log.Printf("Query:          %v", r.URL.RawQuery)
	log.Printf("Is Meatloaf UA: %v", isMeatloafUA(r.UserAgent()))
	log.Printf("Client CWD:     %q", cwd)
	log.Printf("-----------------")
}

//
// -------------------- C64 Helpers -----------------------
//

func clientIP(r *http.Request) string {
	if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
		return cf
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

//
// -------------------- Directory listing -----------------------
//

func listDirFiltered(root, dir string) ([]string, error) {
	absDir := filepath.Join(root, strings.TrimPrefix(dir, "/"))
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, e := range entries {
		name := e.Name()

		if strings.HasPrefix(name, ".") {
			continue
		}

		lower := strings.ToLower(name)

		if strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".php") {
			continue
		}
		if lower == "api" {
			continue
		}
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

func sendLine(w io.Writer, blocks uint16, line string) error {
	if err := writeUint16LE(w, 0x0101); err != nil {
		return err
	}
	if err := writeUint16LE(w, blocks); err != nil {
		return err
	}

	lineBytes := []byte(line + "\x00")
	toUpperASCII(lineBytes)

	_, err := w.Write(lineBytes)
	return err
}

func sendListing(w io.Writer, root, dir, host string) error {
	if err := writeUint16LE(w, basicStart); err != nil {
		return err
	}

	hdr := headerText
	if len(hdr) > 16 {
		hdr = hdr[:16]
	}

	if err := sendLine(w, 0, "\x12\""+hdr+"\" 08 2A"); err != nil {
		return err
	}

	entries, err := listDirFiltered(root, dir)
	if err != nil {
		vlog("listDirFiltered error for %q: %v (empty listing)", dir, err)
		entries = []string{}
	}

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
			blocks = uint16((size + 255) / 256)

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

	if err := sendLine(w, 65535, "BLOCKS FREE"); err != nil {
		return err
	}

	_, err = w.Write([]byte{0x00, 0x00})
	return err
}

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

//
// -------------------- HTML landing page -----------------------
//

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

func printVersion() {
	fmt.Printf("Meatloaf Server %s (%s, %s)\n", version, commit, date)
}

//
// -------------------- Config -----------------------
//

type config struct {
	version bool
	root    string
	addr    string
}

func parseConfig() config {
	versionFlag := flag.Bool("version", false, "Print version information and exit")
	flag.BoolVar(versionFlag, "v", false, "Print version information and exit")

	rootFlag := flag.String("root", ".", "Root directory to serve")
	addrFlag := flag.String("addr", ":8080", "Address to listen on (e.g. :80, 0.0.0.0:8080)")

	// NEW: verbose logging
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&verbose, "V", false, "Enable verbose logging")

	flag.Parse()

	return config{
		version: *versionFlag,
		root:    *rootFlag,
		addr:    *addrFlag,
	}
}

//
// -------------------- Server -----------------------
//

type server struct {
	root string
}

//
// -------------------- HTTP handler ----------------------
//

func (s *server) handle(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	isML := isMeatloafUA(r.UserAgent())

	urlPath := normalizeURLPath(r.URL.Path)
	decoded := decodePath(urlPath)

	cwd := "/"
	if v, ok := clientCWD.Load(ip); ok {
		if str, ok2 := v.(string); ok2 && str != "" {
			cwd = str
		}
	}

	logRequest(r, decoded, cwd)

	localPath := s.localPath(decoded)

	// 1) Direct path exists
	if info, err := os.Stat(localPath); err == nil {
		if info.IsDir() && isML {
			vlog("ACTION: direct directory listing for %q", decoded)
			clientCWD.Store(ip, decoded)
			s.serveListing(w, urlPath, decoded, r.Host)
			return
		}

		if !info.IsDir() {
			vlog("ACTION: direct file serve: %s", localPath)
			s.serveFileWithInfo(w, r, localPath, info)
			return
		}
	}

	// 2) Fallback: CWD + basename
	if isML && cwd != "" {
		base := filepath.Base(decoded)
		if base != "" && base != "." && base != "/" {
			fallbackDecoded := filepath.Join(cwd, base)
			fallbackLocal := s.localPath(fallbackDecoded)

			if info2, err2 := os.Stat(fallbackLocal); err2 == nil {
				if info2.IsDir() {
					vlog("ACTION: fallback directory listing, CWD-based: %q", fallbackDecoded)
					clientCWD.Store(ip, fallbackDecoded)
					s.serveListing(w, urlPath, fallbackDecoded, r.Host)
					return
				}

				vlog("ACTION: fallback file serve, CWD-based: %s", fallbackLocal)
				s.serveFileWithInfo(w, r, fallbackLocal, info2)
				return
			}
		}
	}

	// 3) Last resort listing
	if isML {
		if cwd != "" {
			cwdLocal := s.localPath(cwd)
			if info, err := os.Stat(cwdLocal); err == nil && info.IsDir() {
				vlog("ACTION: last-resort directory listing using CWD %q", cwd)
				s.serveListing(w, urlPath, cwd, r.Host)
				return
			}
		}

		vlog("ACTION: last-resort directory listing for %q", decoded)
		if info, err := os.Stat(localPath); err == nil && info.IsDir() {
			clientCWD.Store(ip, decoded)
		}
		s.serveListing(w, urlPath, decoded, r.Host)
		return
	}

	// 4) Non-meatloaf: landing
	vlog("ACTION: landing page")
	s.serveLanding(w)
}

func normalizeURLPath(urlPath string) string {
	if urlPath == "" {
		return "/"
	}
	if strings.Contains(urlPath, " ") {
		return strings.ReplaceAll(urlPath, " ", "%20")
	}
	return urlPath
}

func decodePath(urlPath string) string {
	decoded, _ := url.PathUnescape(urlPath)
	return decoded
}

func (s *server) localPath(decoded string) string {
	clean := filepath.Clean(strings.TrimPrefix(decoded, "/"))
	return filepath.Join(s.root, clean)
}

func (s *server) serveFileWithInfo(w http.ResponseWriter, r *http.Request, localPath string, info os.FileInfo) {
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	w.Header().Set("Accept-Ranges", "bytes")

	if isBinaryExt(localPath) {
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	http.ServeFile(w, r, localPath)
}

func (s *server) serveListing(w http.ResponseWriter, urlPath, dirToList, host string) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="index.prg"`)
	w.Header().Set("Meatloaf-Debug", urlPath)

	if err := sendListing(w, s.root, dirToList, host); err != nil {
		log.Printf("sendListing error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (s *server) serveLanding(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, landingHTML)
}

func isMeatloafUA(userAgent string) bool {
	return strings.Contains(userAgent, "MEATLOAF")
}

//
// -------------------- Main -----------------------
//

func main() {
	cfg := parseConfig()

	if cfg.version {
		printVersion()
		return
	}

	root, err := filepath.Abs(cfg.root)
	if err != nil {
		log.Fatalf("failed to resolve root: %v", err)
	}

	srv := &server{root: root}

	http.HandleFunc("/", srv.handle)

	log.Printf("Serving %s on %s", root, cfg.addr)
	if verbose {
		log.Printf("Verbose logging enabled")
	}

	if err := http.ListenAndServe(cfg.addr, nil); err != nil {
		log.Fatal(err)
	}
}
