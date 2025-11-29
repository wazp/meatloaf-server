// Meatloaf Server
// Version: 1.3.0
//
// Changelog:
// 1.3.0 - Fixed CD command context handling via trailing-slash redirects:
//         - Added directory-to-trailing-slash redirects for proper firmware URL context
//         - CD commands now work correctly on the Meatloaf firmware purely via URL/path
//         - Cleaned up excessive debug logging while preserving functionality
//         - Removed CMD-style directory support for now (pure CBM listings only)
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

	verbose bool // verbose logging enabled

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
	// Send basic load address
	if err := writeUint16LE(w, basicStart); err != nil {
		return err
	}

	// HEADER line (truncate to 16 chars)
	hdr := headerText
	if len(hdr) > 16 {
		hdr = hdr[:16]
	}

	if err := sendLine(w, 0, "\x12\""+hdr+"\" 08 2A"); err != nil {
		return err
	}

	// NFO lines
	if err := sendLine(w, 0, fmt.Sprintf("\"%-19s\" NFO", "[URL]")); err != nil {
		return err
	}
	if err := sendLine(w, 0, fmt.Sprintf("\"%-19s\" NFO", host)); err != nil {
		return err
	}

	// ALWAYS send PATH info - this is critical for firmware CD context
	if err := sendLine(w, 0, fmt.Sprintf("\"%-19s\" NFO", "[PATH]")); err != nil {
		return err
	}

	// Ensure path has trailing slash for non-root, like the PHP server
	pathForContext := dir
	if len(dir) > 1 && !strings.HasSuffix(pathForContext, "/") {
		pathForContext += "/"
	}

	vlog("Serving directory listing - URL: %s, PATH: %s", host, pathForContext)

	if err := sendLine(w, 0, fmt.Sprintf("\"%-19s\" NFO", pathForContext)); err != nil {
		return err
	}

	if err := sendLine(w, 0, "\"-------------------\" NFO"); err != nil {
		return err
	}

	entries, err := listDirFiltered(root, dir)
	if err != nil {
		vlog("listDirFiltered error for %q: %v (empty listing)", dir, err)
		entries = []string{}
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

	// verbose logging
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
	addr string
}

//
// -------------------- HTTP handler ----------------------
//

func (s *server) handle(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	isML := isMeatloafUA(r.UserAgent())

	urlPath := normalizeURLPath(r.URL.Path)
	decoded := decodePath(urlPath)

	// Optional PHP-style query parameter (for backwards compatibility)
	if p := r.URL.Query().Get("p"); p != "" {
		vlog("Found 'p' query parameter: %q", p)
		decoded = decodePath(p)
		urlPath = p
	}

	// Firmware bug: Host header may lose port; reconstruct host:port for NFO/debug
	correctHost := r.Host
	if !strings.Contains(correctHost, ":") {
		if s.addr != "" && strings.Contains(s.addr, ":") {
			parts := strings.Split(s.addr, ":")
			if len(parts) == 2 && parts[1] != "80" && parts[1] != "443" && parts[1] != "" {
				correctHost = correctHost + ":" + parts[1]
				vlog("Host header missing port, corrected to: %q", correctHost)
			}
		}
	}

	cwd := "/"
	if v, ok := clientCWD.Load(ip); ok {
		if str, ok2 := v.(string); ok2 && str != "" {
			cwd = str
		}
	}

	logRequest(r, decoded, cwd)
	vlog("Request: %s %s from %s (Meatloaf: %v, CWD: %q)", r.Method, decoded, ip, isML, cwd)

	// Apache-style: if <path>/index.prg exists, serve it directly (before anything else)
	if !strings.HasSuffix(decoded, "/") {
		indexPrgPath := filepath.Join(s.localPath(decoded), "index.prg")
		if info, err := os.Stat(indexPrgPath); err == nil && !info.IsDir() {
			vlog("Apache rule: serving index.prg from %s", indexPrgPath)
			s.serveFileWithInfo(w, r, indexPrgPath, info)
			return
		}
	}

	// For all logic below, we use a filesystem-decoded path
	fsDecoded := decoded

	// Try to resolve exact / case-insensitive match for the decoded path
	localPath, info, ok := s.findFileIgnoreCase(fsDecoded)

	// 1) Direct path exists (file or directory)
	if ok {
		if info.IsDir() && isML {
			// For directories, redirect to trailing slash to establish firmware URL context
			if !strings.HasSuffix(urlPath, "/") {
				redirectURL := urlPath + "/"
				vlog("Redirecting directory %q -> %q (establishing firmware context)", urlPath, redirectURL)
				http.Redirect(w, r, redirectURL, http.StatusMovedPermanently)
				return
			}

			vlog("Serving directory listing: %q", fsDecoded)
			clientCWD.Store(ip, fsDecoded)
			s.serveListing(w, urlPath, fsDecoded, correctHost)
			return
		}

		if !info.IsDir() {
			vlog("Serving file: %s", localPath)
			s.serveFileWithInfo(w, r, localPath, info)
			return
		}
	}

	// 2) Fallback: CWD + basename (for Meatloaf requests)
	if isML && cwd != "" {
		base := filepath.Base(fsDecoded)
		if base != "" && base != "." && base != "/" {
			fallbackDecoded := filepath.Join(cwd, base)
			fallbackLocal, info2, ok2 := s.findFileIgnoreCase(fallbackDecoded)

			if ok2 {
				if info2.IsDir() {
					// Redirect fallback directories to trailing slash too
					if !strings.HasSuffix(urlPath, "/") {
						redirectURL := urlPath + "/"
						vlog("Fallback directory redirect %q -> %q", urlPath, redirectURL)
						http.Redirect(w, r, redirectURL, http.StatusMovedPermanently)
						return
					}

					vlog("Fallback directory listing (CWD-based): %q", fallbackDecoded)
					clientCWD.Store(ip, fallbackDecoded)
					s.serveListing(w, urlPath, fallbackDecoded, correctHost)
					return
				}

				vlog("Fallback file serve (CWD-based): %s", fallbackLocal)
				s.serveFileWithInfo(w, r, fallbackLocal, info2)
				return
			}
		}
	}

	// 3) Last resort for Meatloaf: directory-only fallback
	if isML {
		// Only treat as directory last-resort if decoded path has NO extension
		if filepath.Ext(fsDecoded) == "" {
			// Direct path is an existing directory (case-insensitive)?
			if lp3, info3, ok3 := s.findFileIgnoreCase(fsDecoded); ok3 && info3.IsDir() {
				_ = lp3 // path not needed for listing
				vlog("Last-resort directory listing: %q", fsDecoded)
				clientCWD.Store(ip, fsDecoded)
				s.serveListing(w, urlPath, fsDecoded, correctHost)
				return
			}

			// CWD directory listing fallback
			if cwd != "" {
				cwdLocal, info4, ok4 := s.findFileIgnoreCase(cwd)
				if ok4 && info4.IsDir() {
					_ = cwdLocal
					vlog("Last-resort directory listing using CWD: %q", cwd)
					s.serveListing(w, urlPath, cwd, correctHost)
					return
				}
			}
		}

		// Not a directory, not found as file → 404
		vlog("File not found: %q", decoded)
		http.NotFound(w, r)
		return
	}

	// 4) Non-meatloaf: landing
	vlog("Serving HTML landing page")
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

// findFileIgnoreCase attempts to find a file or directory with case-insensitive matching
// Returns the actual file system path, its FileInfo, and a bool indicating success.
func (s *server) findFileIgnoreCase(decoded string) (string, os.FileInfo, bool) {
	// First try exact match
	localPath := s.localPath(decoded)
	if info, err := os.Stat(localPath); err == nil {
		return localPath, info, true
	}

	// If exact match fails, try case-insensitive search within the parent directory
	dir := filepath.Dir(decoded)
	filename := filepath.Base(decoded)

	dirPath := s.localPath(dir)
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return localPath, nil, false
	}

	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), filename) {
			actualPath := filepath.Join(dirPath, entry.Name())
			if info, err := os.Stat(actualPath); err == nil {
				return actualPath, info, true
			}
		}
	}

	return localPath, nil, false
}

func (s *server) serveFileWithInfo(w http.ResponseWriter, r *http.Request, localPath string, info os.FileInfo) {
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	w.Header().Set("Accept-Ranges", "bytes")

	if isBinaryExt(localPath) {
		w.Header().Set("Content-Type", "application/octet-stream")
		// For file downloads, set Content-Disposition with actual filename
		filename := filepath.Base(localPath)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	}

	http.ServeFile(w, r, localPath)
}

func (s *server) serveListing(w http.ResponseWriter, urlPath, dirToList, host string) {
	w.Header().Set("Content-Type", "application/octet-stream")
	// Tells the firmware this response contains a directory listing PRG
	w.Header().Set("Content-Disposition", `attachment; filename="index.prg"`)

	// Meatloaf-Debug header must contain full URL context for firmware CD behavior
	// Format: http://host/path/ (with trailing slash for directories)
	debugContext := fmt.Sprintf("http://%s%s", host, urlPath)
	if !strings.HasSuffix(debugContext, "/") {
		debugContext += "/"
	}
	w.Header().Set("Meatloaf-Debug", debugContext)

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

	srv := &server{root: root, addr: cfg.addr}

	http.HandleFunc("/", srv.handle)

	log.Printf("Serving %s on %s", root, cfg.addr)
	if verbose {
		log.Printf("Verbose logging enabled")
	}

	if err := http.ListenAndServe(cfg.addr, nil); err != nil {
		log.Fatal(err)
	}
}
