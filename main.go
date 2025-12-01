// Meatloaf Server
// Version: 1.4.0
//
// Changelog:
// 1.4.0 - Added CMD-style directory filtering support:
//         - LOAD"$=P",8 filters by Program files only
//         - LOAD"$=S",8 filters by Sequential files only
//         - LOAD"$=R",8 filters by Relative files only
//         - LOAD"$=U",8 filters by User files only
//         - LOAD"$=D",8 filters by Directory entries only
//         - LOAD"$PATTERN*",8 supports wildcard filtering
//         - LOAD"$PATTERN*=P",8 supports combined pattern + type filtering
//         - Modular design with separate CBM and CMD listing functions
// 1.3.0 - Fixed CD command context handling via trailing-slash redirects:
//         - Added directory-to-trailing-slash redirects for proper firmware URL context
//         - CD commands now work correctly on the Meatloaf firmware purely via URL/path
//         - Cleaned up excessive debug logging while preserving functionality
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
	"path"
	"path/filepath"
	"sort"
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
// -------------------- CMD Directory Support -----------------------
//

// CmdFilter represents a CMD-style directory filter
type CmdFilter struct {
	Pattern  string // wildcard pattern (e.g., "GAME*")
	FileType string // file type filter ("P", "S", "R", "U", "D")
}

// DirEntry represents a directory entry for listing
type DirEntry struct {
	Name    string
	Type    string // "DIR", "PRG", "SEQ", etc.
	Blocks  uint16
	IsDir   bool
	CbmType rune // 'D', 'P', 'S', etc.
}

// parseCmdFilter parses CMD-style directory filter syntax from path
// Examples: "$", "$=P", "$GAME*", "$GAME*=P", "$=D", "/games/$=P"
// Returns filter and the directory path to list
func parseCmdFilter(path string) (*CmdFilter, string) {
	// Check if the last segment starts with $
	base := filepath.Base(path)

	if !strings.HasPrefix(base, "$") {
		return nil, path // Not a CMD filter request
	}

	// Get the directory part (everything before the final $ segment)
	dir := filepath.Dir(path)
	if dir == "." {
		dir = "/"
	}

	// Parse the filter from the $ segment
	filterStr := base[1:] // Remove the $ prefix

	if filterStr == "" {
		// Simple "$" directory listing (no filter)
		return &CmdFilter{}, dir
	}

	filter := &CmdFilter{}

	// Parse CMD filter syntax: supports both $=TYPE:PATTERN and $PATTERN*=TYPE
	if strings.Contains(filterStr, "=") {
		parts := strings.Split(filterStr, "=")
		if len(parts) == 2 {
			// Check for colon syntax: $=TYPE:PATTERN (e.g., $=P:GAME*)
			if strings.Contains(parts[1], ":") {
				typeParts := strings.Split(parts[1], ":")
				if len(typeParts) == 2 {
					filter.FileType = strings.ToUpper(strings.TrimSpace(typeParts[0]))
					filter.Pattern = strings.TrimSpace(typeParts[1])
				}
			} else {
				// Original syntax: $PATTERN*=TYPE
				filter.Pattern = strings.TrimSpace(parts[0])
				filter.FileType = strings.ToUpper(strings.TrimSpace(parts[1]))
			}
		}
	} else {
		// No type filter, just pattern
		filter.Pattern = filterStr
	}

	vlog("Parsed CMD filter - pattern: %q, type: %q, dir: %q", filter.Pattern, filter.FileType, dir)
	return filter, dir
}

// cbmMatch performs CBM-style wildcard matching (*, ?) case-insensitive
func cbmMatch(pattern, name string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}

	p := strings.ToUpper(pattern)
	n := strings.ToUpper(name)

	matched, err := path.Match(p, n)
	if err != nil {
		// On invalid pattern, be conservative and match nothing
		return false
	}
	return matched
}

// matchesCmdFilter checks if a directory entry matches the CMD filter
func (f *CmdFilter) matchesCmdFilter(entry DirEntry) bool {
	// Check file type filter
	if f.FileType != "" {
		expectedCbmType := rune(f.FileType[0])
		if entry.CbmType != expectedCbmType {
			return false
		}
	}

	// Check pattern filter (wildcard matching)
	if f.Pattern != "" {
		return cbmMatch(f.Pattern, entry.Name)
	}

	return true
}

// buildDirEntries scans directory and builds DirEntry slice
func buildDirEntries(root, dir string) ([]DirEntry, error) {
	names, err := listDirFiltered(root, dir)
	if err != nil {
		return nil, err
	}

	entries := make([]DirEntry, 0, len(names))
	for _, name := range names {
		relPath := filepath.Join(strings.TrimPrefix(dir, "/"), name)
		full := filepath.Join(root, relPath)

		info, err := os.Stat(full)
		if err != nil {
			continue
		}

		typ := getType(root, dir, name)
		isDir := info.IsDir()

		var blocks uint16
		if !isDir {
			size := info.Size()
			blocks = uint16((size + 255) / 256)
		}

		cbmType := getCbmType(root, dir, name)

		entries = append(entries, DirEntry{
			Name:    name,
			Type:    typ,
			Blocks:  blocks,
			IsDir:   isDir,
			CbmType: cbmType,
		})
	}

	return entries, nil
}

// applyCmdFilter filters and optionally sorts directory entries according to CMD filter
func applyCmdFilter(entries []DirEntry, filter *CmdFilter) []DirEntry {
	if filter == nil {
		return entries
	}

	// Filter entries
	filtered := make([]DirEntry, 0, len(entries))
	for _, entry := range entries {
		if filter.matchesCmdFilter(entry) {
			filtered = append(filtered, entry)
		}
	}

	// Basic alphabetical sort for filtered results
	sort.Slice(filtered, func(i, j int) bool {
		// Directories first, then files
		if filtered[i].IsDir != filtered[j].IsDir {
			return filtered[i].IsDir && !filtered[j].IsDir
		}
		return strings.ToLower(filtered[i].Name) < strings.ToLower(filtered[j].Name)
	})

	return filtered
}

// convertToPETSCII converts problematic characters in filenames to PETSCII-friendly equivalents
// Based on SD2IEC's ASCII->PETSCII conversion approach
func convertToPETSCII(name string) string {
	result := make([]byte, len(name))

	for i, ch := range []byte(name) {
		switch ch {
		case '_':
			// Underscore becomes left arrow (← ) in PETSCII, replace with dash
			result[i] = '-'
		case '~':
			// Tilde becomes Pi (π) symbol in PETSCII, use converted form
			result[i] = 255
		case '\\':
			// Backslash problematic, use slash
			result[i] = '/'
		case '|':
			// Pipe character problematic, use dash
			result[i] = '-'
		case '^':
			// Caret problematic in PETSCII, use dash
			result[i] = '-'
		case '`':
			// Backtick problematic, use apostrophe
			result[i] = '\''
		case '{', '}', '[', ']':
			// Braces and brackets problematic, use parentheses
			result[i] = '('
			if ch == '}' || ch == ']' {
				result[i] = ')'
			}
		default:
			// Most ASCII characters convert fine to PETSCII
			if ch >= 32 && ch <= 126 {
				result[i] = ch
			} else {
				// Non-printable or high-bit characters, use safe replacement
				result[i] = '?'
			}
		}
	}

	return string(result)
}

//
// -------------------- CBM Directory Listing (Original) -----------------------
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

// getCbmType returns the proper CBM file type character for CMD filtering
func getCbmType(root, dir, name string) rune {
	relPath := filepath.Join(strings.TrimPrefix(dir, "/"), name)
	full := filepath.Join(root, relPath)
	info, err := os.Stat(full)
	if err == nil && info.IsDir() {
		return 'D' // Directory
	}

	// Map file extensions to CBM file types
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "prg", "p00":
		return 'P' // Program
	case "seq", "s00":
		return 'S' // Sequential
	case "usr", "u00":
		return 'U' // User
	case "rel", "r00":
		return 'R' // Relative
	case "del":
		return 'L' // Deleted (rare, but valid CBM type)
	default:
		// For disk images and other binary formats, treat as Program files
		// This matches how real CMD devices handle non-standard extensions
		return 'P'
	}
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

// sendCbmListing sends a traditional CBM-style directory listing
func sendCbmListing(w io.Writer, root, dir, host string) error {
	return sendListing(w, root, dir, host, nil)
}

// sendCmdListing sends a CMD-filtered directory listing
func sendCmdListing(w io.Writer, root, dir, host string, filter *CmdFilter) error {
	return sendListing(w, root, dir, host, filter)
}

// sendListing sends a directory listing (CBM or CMD-filtered)
func sendListing(w io.Writer, root, dir, host string, filter *CmdFilter) error {
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

	// Build directory entries
	entries, err := buildDirEntries(root, dir)
	if err != nil {
		vlog("buildDirEntries error for %q: %v (empty listing)", dir, err)
		entries = []DirEntry{}
	}

	// Apply CMD filter if present
	if filter != nil {
		entries = applyCmdFilter(entries, filter)
		vlog("CMD filter applied, %d entries after filtering", len(entries))
	}

	// Send directory entries
	for _, entry := range entries {
		var blocks uint16 = entry.Blocks
		blockSpc := 3

		if !entry.IsDir {
			if blocks > 9 {
				blockSpc--
			}
			if blocks > 99 {
				blockSpc--
			}
		}

		// Truncate filename to 16 characters to match CBM/CMD compatibility
		displayName := entry.Name
		if len(displayName) > 16 {
			displayName = displayName[:16]
		}

		// Convert problematic characters to PETSCII-friendly alternatives
		displayName = convertToPETSCII(displayName)

		line := fmt.Sprintf("%s%-18s %s",
			strings.Repeat(" ", blockSpc),
			"\""+displayName+"\"",
			entry.Type,
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

			<title>Meatloaf C64</title>

			<!-- Bootstrap CSS -->
			<link rel="stylesheet" href="https://stackpath.bootstrapcdn.com/bootstrap/4.3.1/css/bootstrap.min.css" integrity="sha384-ggOyR0iXCbMQv3Xipma34MD+dH/1fQ784/j6cY/iJTQUOhcWr7x9JvoRxT2MZw1T" crossorigin="anonymous">
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
	</html>`

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

	// Check for CMD-style directory filter
	var cmdFilter *CmdFilter
	var actualDir string
	if isML {
		cmdFilter, actualDir = parseCmdFilter(decoded)
		if cmdFilter != nil {
			// This is a CMD filter request, use the directory part for filesystem operations
			decoded = actualDir
			vlog("CMD filter detected, processing directory: %q", decoded)
		}
	}

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
			// BUT skip redirect for CMD filter requests (they can't have trailing slashes)
			if !strings.HasSuffix(urlPath, "/") && cmdFilter == nil {
				redirectURL := urlPath + "/"
				vlog("Redirecting directory %q -> %q (establishing firmware context)", urlPath, redirectURL)
				http.Redirect(w, r, redirectURL, http.StatusMovedPermanently)
				return
			}

			// Convert the resolved local path back to relative path for listing
			resolvedRelPath := s.localPathToRelative(localPath)
			vlog("Serving directory listing: %q (resolved to %q)", fsDecoded, resolvedRelPath)
			clientCWD.Store(ip, resolvedRelPath)
			s.serveListing(w, urlPath, resolvedRelPath, correctHost, cmdFilter)
			return
		}

		if !info.IsDir() {
			vlog("Serving file: %s", localPath)
			s.serveFileWithInfo(w, r, localPath, info)
			return
		}
	}

	// 2) CMD filter fallback: try filter in CWD
	if isML && cmdFilter != nil && cwd != "" && cwd != decoded {
		vlog("CMD filter request fallback to CWD: %q", cwd)
		clientCWD.Store(ip, cwd)
		s.serveListing(w, urlPath, cwd, correctHost, cmdFilter)
		return
	}

	// 3) Fallback: CWD + basename (for Meatloaf requests)
	if isML && cwd != "" && cmdFilter == nil {
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
					s.serveListing(w, urlPath, fallbackDecoded, correctHost, nil)
					return
				}

				vlog("Fallback file serve (CWD-based): %s", fallbackLocal)
				s.serveFileWithInfo(w, r, fallbackLocal, info2)
				return
			}
		}
	}

	// 4) Last resort for Meatloaf: directory-only fallback
	if isML {
		// Only treat as directory last-resort if decoded path has NO extension or is CMD filter
		if filepath.Ext(fsDecoded) == "" || cmdFilter != nil {
			// Direct path is an existing directory (case-insensitive)?
			if lp3, info3, ok3 := s.findFileIgnoreCase(fsDecoded); ok3 && info3.IsDir() {
				_ = lp3 // path not needed for listing
				vlog("Last-resort directory listing: %q", fsDecoded)
				clientCWD.Store(ip, fsDecoded)
				s.serveListing(w, urlPath, fsDecoded, correctHost, cmdFilter)
				return
			}

			// CWD directory listing fallback
			if cwd != "" {
				cwdLocal, info4, ok4 := s.findFileIgnoreCase(cwd)
				if ok4 && info4.IsDir() {
					_ = cwdLocal
					vlog("Last-resort directory listing using CWD: %q", cwd)
					s.serveListing(w, urlPath, cwd, correctHost, cmdFilter)
					return
				}
			}
		}

		// Not a directory, not found as file → 404
		vlog("File not found: %q", decoded)
		http.NotFound(w, r)
		return
	}

	// 5) Non-meatloaf: landing
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

// localPathToRelative converts an absolute local path back to a relative path
func (s *server) localPathToRelative(localPath string) string {
	if !strings.HasPrefix(localPath, s.root) {
		return "/"
	}
	rel := strings.TrimPrefix(localPath, s.root)
	if rel == "" {
		return "/"
	}
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	return rel
}

// findFileIgnoreCase attempts to find a file or directory with case-insensitive matching
// Also handles truncated/PETSCII-converted filenames from directory listings
// Returns the actual file system path, its FileInfo, and a bool indicating success.
func (s *server) findFileIgnoreCase(decoded string) (string, os.FileInfo, bool) {
	// First try exact match
	localPath := s.localPath(decoded)
	if info, err := os.Stat(localPath); err == nil {
		return localPath, info, true
	}

	// If exact match fails, try case-insensitive path resolution
	resolvedPath, ok := s.resolveCaseInsensitivePath(decoded)
	if ok {
		if info, err := os.Stat(resolvedPath); err == nil {
			return resolvedPath, info, true
		}
	}

	// Fallback to the original simple logic for any single-level path
	// This is needed for truncated/PETSCII filename matching
	dir := filepath.Dir(decoded)
	filename := filepath.Base(decoded)

	// For any directory that we can access, try filename matching
	dirPath := s.localPath(dir)
	entries, err := os.ReadDir(dirPath)
	if err == nil {
		for _, entry := range entries {
			if s.matchesFinalComponent(entry.Name(), filename) {
				actualPath := filepath.Join(dirPath, entry.Name())
				if info, err := os.Stat(actualPath); err == nil {
					return actualPath, info, true
				}
			}
		}
	}

	return localPath, nil, false
}

// resolveCaseInsensitivePath resolves a path with case-insensitive matching for each component
// Also tries truncated/PETSCII-converted filename matching for the final component
func (s *server) resolveCaseInsensitivePath(decoded string) (string, bool) {
	// Start from root
	currentPath := s.root
	
	// Split path into components, skipping empty ones
	parts := strings.Split(strings.TrimPrefix(decoded, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		// Root directory case
		return currentPath, true
	}

	// Resolve each component
	for i, part := range parts {
		if part == "" {
			continue
		}

		// Try to find this component in current directory (case-insensitive)
		entries, err := os.ReadDir(currentPath)
		if err != nil {
			return "", false
		}

		found := false
		for _, entry := range entries {
			// For non-final components, only do exact case-insensitive match
			if i < len(parts)-1 {
				if strings.EqualFold(entry.Name(), part) {
					currentPath = filepath.Join(currentPath, entry.Name())
					found = true
					break
				}
			} else {
				// For final component, try all matching strategies
				if s.matchesFinalComponent(entry.Name(), part) {
					currentPath = filepath.Join(currentPath, entry.Name())
					found = true
					break
				}
			}
		}

		if !found {
			return "", false
		}
	}

	return currentPath, true
}

// matchesFinalComponent checks if an entry matches the requested final path component
// using various strategies (exact, case-insensitive, truncated, PETSCII-converted)
func (s *server) matchesFinalComponent(entryName, requestedName string) bool {
	// Try exact case-insensitive match first
	if strings.EqualFold(entryName, requestedName) {
		return true
	}

	// Try matching against truncated+converted filename
	// This handles when user tries to LOAD a filename they saw in directory listing
	displayName := entryName
	if len(displayName) > 16 {
		displayName = displayName[:16]
	}
	displayName = convertToPETSCII(displayName)

	if strings.EqualFold(displayName, requestedName) {
		vlog("Matched truncated/converted filename: %q -> %q", requestedName, entryName)
		return true
	}

	// Also try matching if the requested name starts with the converted/truncated display name
	// This handles cases where the user might type a longer version of what they saw
	if strings.HasPrefix(strings.ToLower(requestedName), strings.ToLower(displayName)) {
		vlog("Matched as prefix of truncated/converted filename: %q -> %q (via %q)", requestedName, entryName, displayName)
		return true
	}

	// Try the reverse: see if the display name starts with the requested name
	// This handles partial matching from the other direction  
	if strings.HasPrefix(strings.ToLower(displayName), strings.ToLower(requestedName)) {
		vlog("Matched as requested prefix of display name: %q -> %q (via %q)", requestedName, entryName, displayName)
		return true
	}

	// Also try matching if the requested name is a prefix of the actual name
	// This handles truncated filenames where user might not include the full converted name
	if len(entryName) > 16 && len(requestedName) <= 16 {
		truncatedActual := entryName[:16]
		convertedTruncated := convertToPETSCII(truncatedActual)
		if strings.EqualFold(convertedTruncated, requestedName) {
			vlog("Matched prefix of long filename: %q -> %q", requestedName, entryName)
			return true
		}
	}

	return false
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

func (s *server) serveListing(w http.ResponseWriter, urlPath, dirToList, host string, cmdFilter *CmdFilter) {
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

	if err := sendListing(w, s.root, dirToList, host, cmdFilter); err != nil {
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
