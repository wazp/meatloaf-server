package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test helpers
func createTestServer(t *testing.T) (*server, string) {
	tempDir := t.TempDir()
	
	// Create test files and directories
	testFiles := map[string][]byte{
		"game.prg":     []byte("GAME PRG FILE"),
		"data.seq":     []byte("DATA SEQ FILE"), 
		"music.usr":    []byte("MUSIC USR FILE"),
		"readme.txt":   []byte("README TEXT FILE"),
		"test.d64":     make([]byte, 174848), // Standard D64 size
		"subdir/file1.prg": []byte("SUBDIR FILE"),
		"subdir/file2.seq": []byte("SUBDIR SEQ"),
	}
	
	for path, content := range testFiles {
		fullPath := filepath.Join(tempDir, path)
		dir := filepath.Dir(fullPath)
		
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create dir %s: %v", dir, err)
		}
		
		if err := os.WriteFile(fullPath, content, 0644); err != nil {
			t.Fatalf("Failed to create test file %s: %v", fullPath, err)
		}
	}
	
	return &server{root: tempDir}, tempDir
}

func makeTestRequest(srv *server, method, path, userAgent string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	
	w := httptest.NewRecorder()
	srv.handle(w, req)
	return w
}

// Test User-Agent detection
func TestIsMeatloafUA(t *testing.T) {
	tests := []struct {
		userAgent string
		expected  bool
	}{
		{"MEATLOAF/1.0", true},
		{"MEATLOAF CBM", true},
		{"Mozilla/5.0 MEATLOAF", true},
		{"ESP32 HTTP Client/1.0", false},
		{"Mozilla/5.0", false},
		{"", false},
	}
	
	for _, tt := range tests {
		t.Run(tt.userAgent, func(t *testing.T) {
			result := isMeatloafUA(tt.userAgent)
			if result != tt.expected {
				t.Errorf("isMeatloafUA(%q) = %v, expected %v", tt.userAgent, result, tt.expected)
			}
		})
	}
}

// Test directory listing generation
func TestDirectoryListing(t *testing.T) {
	srv, _ := createTestServer(t)
	
	tests := []struct {
		name      string
		path      string
		userAgent string
		contains  []string
		notContains []string
	}{
		{
			name:      "Basic Directory - Non-Meatloaf",
			path:      "/",
			userAgent: "Mozilla/5.0",
			contains:  []string{"<!doctype html>", "Meatloaf C64"},
		},
		{
			name:      "Basic Directory - Meatloaf",
			path:      "/",
			userAgent: "MEATLOAF/1.0",
			contains:  []string{"MEATLOAF ARCHIVE", "\"GAME.PRG\"", "\"DATA.SEQ\"", "\"SUBDIR\"", "DIR"},
		},
	{
		name:      "Subdirectory Listing",
		path:      "/subdir/",
		userAgent: "MEATLOAF/1.0",
		contains:  []string{"\"FILE1.PRG\"", "\"FILE2.SEQ\""},
	},
}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := makeTestRequest(srv, "GET", tt.path, tt.userAgent)
			
			body := w.Body.String()
			
			for _, expected := range tt.contains {
				if !strings.Contains(body, expected) {
					t.Errorf("Response should contain %q, body: %s", expected, body[:min(200, len(body))])
				}
			}
			
			for _, notExpected := range tt.notContains {
				if strings.Contains(body, notExpected) {
					t.Errorf("Response should not contain %q", notExpected)
				}
			}
		})
	}
}

// Test BASIC program structure
func TestBasicProgramStructure(t *testing.T) {
	srv, _ := createTestServer(t)
	
	w := makeTestRequest(srv, "GET", "/", "MEATLOAF/1.0")
	
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}
	
	body := w.Body.Bytes()
	
	// Check BASIC program structure
	if len(body) < 4 {
		t.Fatal("Response too short to be a valid BASIC program")
	}
	
	// Check load address (should be 0x0401)
	loadAddr := uint16(body[0]) | (uint16(body[1]) << 8)
	if loadAddr != basicStart {
		t.Errorf("Expected load address 0x%04X, got 0x%04X", basicStart, loadAddr)
	}
	
	// Check for program termination (should end with 0x00 0x00)
	if len(body) >= 2 {
		lastTwo := body[len(body)-2:]
		if lastTwo[0] != 0x00 || lastTwo[1] != 0x00 {
			t.Errorf("Program should end with 0x00 0x00, got 0x%02X 0x%02X", lastTwo[0], lastTwo[1])
		}
	}
	
	// Check for header line structure
	if len(body) >= 32 {
		// First line should have proper structure: [next][line][data...]
		if body[2] != 0x01 || body[3] != 0x01 {
			t.Error("First line should have line number structure")
		}
	}
}

// Test file serving
func TestFileServing(t *testing.T) {
	srv, _ := createTestServer(t)
	
	tests := []struct {
		path          string
		userAgent     string
		expectedCode  int
		expectedType  string
		expectedBody  string
	}{
		{
			path:         "/game.prg",
			userAgent:    "MEATLOAF/1.0", 
			expectedCode: http.StatusOK,
			expectedType: "application/octet-stream",
			expectedBody: "GAME PRG FILE",
		},
		{
			path:         "/readme.txt",
			userAgent:    "MEATLOAF/1.0",
			expectedCode: http.StatusOK,
			expectedBody: "README TEXT FILE",
		},
		{
			path:         "/nonexistent.prg",
			userAgent:    "MEATLOAF/1.0", 
			expectedCode: http.StatusNotFound,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			w := makeTestRequest(srv, "GET", tt.path, tt.userAgent)
			
			if w.Code != tt.expectedCode {
				t.Errorf("Expected status %d, got %d", tt.expectedCode, w.Code)
			}
			
			if tt.expectedType != "" {
				contentType := w.Header().Get("Content-Type")
				if !strings.Contains(contentType, tt.expectedType) {
					t.Errorf("Expected content type to contain %q, got %q", tt.expectedType, contentType)
				}
			}
			
			if tt.expectedBody != "" {
				body := w.Body.String()
				if !strings.Contains(body, tt.expectedBody) {
					t.Errorf("Expected body to contain %q, got %q", tt.expectedBody, body[:min(100, len(body))])
				}
			}
		})
	}
}

// Test logging functions
func TestVlog(t *testing.T) {
	// Test with verbose off
	verbose = false
	// We can't easily test log output, but we can test the function doesn't crash
	vlog("test message %s", "param")
	
	// Test with verbose on  
	verbose = true
	vlog("test message %s", "param")
	
	// Reset verbose for other tests
	verbose = false
}

func TestLogRequest(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?query=value", nil)
	req.Header.Set("User-Agent", "MEATLOAF/1.0")
	req.Header.Set("CF-Connecting-IP", "1.2.3.4")
	req.RemoteAddr = "192.168.1.100:54321"
	
	// Test with verbose off (should not log)
	verbose = false
	logRequest(req, "/decoded/path", "/cwd")
	
	// Test with verbose on (should log)
	verbose = true
	logRequest(req, "/decoded/path", "/cwd")
	
	// Reset verbose
	verbose = false
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name           string
		remoteAddr     string
		cfConnectingIP string
		expected       string
	}{
		{
			name:           "With CF-Connecting-IP header",
			remoteAddr:     "192.168.1.100:54321",
			cfConnectingIP: "203.0.113.1",
			expected:       "203.0.113.1",
		},
		{
			name:       "Without CF header, valid RemoteAddr",
			remoteAddr: "192.168.1.100:54321",
			expected:   "192.168.1.100",
		},
		{
			name:       "Invalid RemoteAddr (no port)",
			remoteAddr: "192.168.1.100",
			expected:   "192.168.1.100",
		},
		{
			name:       "IPv6 RemoteAddr",
			remoteAddr: "[::1]:54321",
			expected:   "::1",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			
			if tt.cfConnectingIP != "" {
				req.Header.Set("CF-Connecting-IP", tt.cfConnectingIP)
			}
			
			result := clientIP(req)
			if result != tt.expected {
				t.Errorf("clientIP() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestNormalizeURLPath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "/"},
		{"/", "/"},
		{"/path", "/path"},
		{"/path with spaces", "/path%20with%20spaces"},
		{"/already%20encoded", "/already%20encoded"},
		{"/multiple spaces here", "/multiple%20spaces%20here"},
	}
	
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeURLPath(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeURLPath(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetType(t *testing.T) {
	_, tempDir := createTestServer(t)
	
	tests := []struct {
		name     string
		filename string
		expected string
	}{
		{"Directory", "subdir", "DIR"},
		{"PRG file", "game.prg", "PRG"},
		{"SEQ file", "data.seq", "SEQ"},
		{"USR file", "music.usr", "USR"},
		{"D64 file", "test.d64", "D64"},
		{"Unknown extension", "readme.txt", "TXT"},
		{"No extension", "README", "PRG"}, // Defaults to PRG for short extensions
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getType(tempDir, "/", tt.filename)
			if result != tt.expected {
				t.Errorf("getType(%q) = %q, expected %q", tt.filename, result, tt.expected)
			}
		})
	}
}

func TestListDirFiltered(t *testing.T) {
	_, tempDir := createTestServer(t)
	
	// Test root directory
	files, err := listDirFiltered(tempDir, "/")
	if err != nil {
		t.Fatalf("listDirFiltered failed: %v", err)
	}
	
	expected := []string{"data.seq", "game.prg", "music.usr", "readme.txt", "subdir", "test.d64"}
	if len(files) != len(expected) {
		t.Errorf("Expected %d files, got %d: %v", len(expected), len(files), files)
	}
	
	// Test subdirectory
	subFiles, err := listDirFiltered(tempDir, "/subdir")
	if err != nil {
		t.Fatalf("listDirFiltered failed for subdir: %v", err)
	}
	
	expectedSub := []string{"file1.prg", "file2.seq"}
	if len(subFiles) != len(expectedSub) {
		t.Errorf("Expected %d subdir files, got %d: %v", len(expectedSub), len(subFiles), subFiles)
	}
	
	// Test non-existent directory
	_, err = listDirFiltered(tempDir, "/nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent directory")
	}
}

func TestSendLine(t *testing.T) {
	var buf strings.Builder
	
	err := sendLine(&buf, 123, "TEST LINE")
	if err != nil {
		t.Fatalf("sendLine failed: %v", err)
	}
	
	result := buf.String()
	// Should contain the binary data plus "TEST LINE\x00"
	if !strings.Contains(result, "TEST LINE") {
		t.Error("Result should contain 'TEST LINE'")
	}
	
	// Check it ends with null terminator
	if !strings.HasSuffix(result, "\x00") {
		t.Error("Result should end with null terminator")
	}
}

func TestWriteUint16LE(t *testing.T) {
	var buf strings.Builder
	
	err := writeUint16LE(&buf, 0x1234)
	if err != nil {
		t.Fatalf("writeUint16LE failed: %v", err)
	}
	
	result := buf.String()
	if len(result) != 2 {
		t.Errorf("Expected 2 bytes, got %d", len(result))
	}
	
	// Little endian: 0x1234 should be written as 0x34, 0x12
	if result[0] != 0x34 || result[1] != 0x12 {
		t.Errorf("Expected [0x34, 0x12], got [0x%02x, 0x%02x]", result[0], result[1])
	}
}

func TestToUpperASCII(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "HELLO"},
		{"HELLO", "HELLO"},
		{"Hello World", "HELLO WORLD"},
		{"test123", "TEST123"},
		{"", ""},
	}
	
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			input := []byte(tt.input)
			toUpperASCII(input)
			result := string(input)
			if result != tt.expected {
				t.Errorf("toUpperASCII(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

// Test error conditions and edge cases
func TestErrorConditions(t *testing.T) {
	srv, _ := createTestServer(t)
	
	tests := []struct {
		name     string
		path     string
		expected int
	}{
		{"Root directory", "/", http.StatusOK},
		{"File that exists", "/game.prg", http.StatusOK},
		{"Non-existent file", "/nonexistent.prg", http.StatusNotFound},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := makeTestRequest(srv, "GET", tt.path, "MEATLOAF/1.0")
			if w.Code != tt.expected {
				t.Errorf("Expected status %d, got %d", tt.expected, w.Code)
			}
		})
	}
}

// Test query parameter support (PHP compatibility)
func TestQueryParameterSupport(t *testing.T) {
	srv, _ := createTestServer(t)
	
	tests := []struct {
		name         string
		path         string
		userAgent    string
		expectedPath string
	}{
		{
			name:         "Query parameter p with subdirectory",
			path:         "/?p=/games/",
			userAgent:    "MEATLOAF/1.0", 
			expectedPath: "/games/",
		},
		{
			name:         "No query parameter uses path",
			path:         "/games/",
			userAgent:    "MEATLOAF/1.0",
			expectedPath: "/games/",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// For this test, we'll check that the server processes the path correctly
			req := httptest.NewRequest("GET", tt.path, nil)
			req.Header.Set("User-Agent", tt.userAgent)
			req.RemoteAddr = "192.168.1.100:12345"
			
			w := httptest.NewRecorder()
			srv.handle(w, req)
			
			// Should get a response (either directory listing or file)
			if w.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", w.Code)
			}
		})
	}
}

// Test path-based navigation (CD commands are handled by firmware, not server)
func TestPathBasedNavigation(t *testing.T) {
	srv, _ := createTestServer(t)
	
	tests := []struct {
		name         string
		path         string
		userAgent    string
		expectListing bool
	}{
		{
			name:         "Regular directory path",
			path:         "/subdir",
			userAgent:    "MEATLOAF/1.0",
			expectListing: true,
		},
		{
			name:         "Root directory path",
			path:         "/",
			userAgent:    "MEATLOAF/1.0", 
			expectListing: true,
		},
		{
			name:         "Non-Meatloaf gets HTML",
			path:         "/subdir",
			userAgent:    "Mozilla/5.0",
			expectListing: false, // Should get landing page
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make request
			req := httptest.NewRequest("GET", tt.path, nil)
			req.Header.Set("User-Agent", tt.userAgent)
			req.RemoteAddr = "192.168.1.100:12345"
			
			w := httptest.NewRecorder()
			srv.handle(w, req)
			
			// Check response
			if tt.expectListing {
				// Directory requests without trailing slash should redirect first
				if !strings.HasSuffix(tt.path, "/") && tt.path != "/" {
					if w.Code != http.StatusMovedPermanently {
						t.Errorf("Expected status 301 for directory redirect, got %d", w.Code)
					}
					// Follow the redirect
					location := w.Header().Get("Location")
					if location != tt.path+"/" {
						t.Errorf("Expected redirect to %s/, got %s", tt.path, location)
					}
					
					// Make the redirected request
					req2 := httptest.NewRequest("GET", location, nil)
					req2.Header.Set("User-Agent", tt.userAgent)
					req2.RemoteAddr = "192.168.1.100:12345"
					
					w = httptest.NewRecorder()
					srv.handle(w, req2)
				}
				
				if w.Code != http.StatusOK {
					t.Errorf("Expected status 200, got %d", w.Code)
				}
				
				// Check if it's a binary listing (BASIC program)
				body := w.Body.Bytes()
				if len(body) < 4 {
					t.Error("Expected binary directory listing, got too short response")
				}
			} else {
				// Should get HTML landing page for non-Meatloaf
				body := w.Body.String()
				if !strings.Contains(body, "<!doctype html>") {
					t.Error("Expected HTML landing page for non-Meatloaf request")
				}
			}
		})
	}
}

// Helper function (Go 1.21+ has this built-in)
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Benchmark tests
func BenchmarkDirectoryListing(b *testing.B) {
	srv, _ := createTestServer(&testing.T{})
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := makeTestRequest(srv, "GET", "/", "MEATLOAF/1.0")
		if w.Code != http.StatusOK {
			b.Errorf("Expected status 200, got %d", w.Code)
		}
	}
}

func TestPrintVersion(t *testing.T) {
	// Can't easily test stdout, but we can call it to ensure it doesn't panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("printVersion() panicked: %v", r)
		}
	}()
	
	printVersion()
}

// Test edge cases in listDirFiltered
func TestListDirFilteredEdgeCases(t *testing.T) {
	_, tempDir := createTestServer(t)
	
	// Create files that should be filtered out
	hiddenDir := filepath.Join(tempDir, ".hidden")
	if err := os.MkdirAll(hiddenDir, 0755); err != nil {
		t.Fatalf("Failed to create hidden dir: %v", err)
	}
	
	if err := os.WriteFile(filepath.Join(tempDir, ".hiddenfile"), []byte("hidden"), 0644); err != nil {
		t.Fatalf("Failed to create hidden file: %v", err)
	}
	
	if err := os.WriteFile(filepath.Join(tempDir, "test.html"), []byte("html"), 0644); err != nil {
		t.Fatalf("Failed to create html file: %v", err)
	}
	
	if err := os.WriteFile(filepath.Join(tempDir, "test.php"), []byte("php"), 0644); err != nil {
		t.Fatalf("Failed to create php file: %v", err)
	}
	
	if err := os.WriteFile(filepath.Join(tempDir, "web.config"), []byte("config"), 0644); err != nil {
		t.Fatalf("Failed to create web.config: %v", err)
	}
	
	if err := os.MkdirAll(filepath.Join(tempDir, "api"), 0755); err != nil {
		t.Fatalf("Failed to create api dir: %v", err)
	}
	
	files, err := listDirFiltered(tempDir, "/")
	if err != nil {
		t.Fatalf("listDirFiltered failed: %v", err)
	}
	
	// Should not contain filtered files
	for _, file := range files {
		if strings.HasPrefix(file, ".") {
			t.Errorf("Hidden file %q should be filtered out", file)
		}
		if strings.HasSuffix(strings.ToLower(file), ".html") || 
		   strings.HasSuffix(strings.ToLower(file), ".php") {
			t.Errorf("Web file %q should be filtered out", file)
		}
		if file == "web.config" || file == "api" {
			t.Errorf("System file/dir %q should be filtered out", file)
		}
	}
}

// Test more sendLine edge cases
func TestSendLineEdgeCases(t *testing.T) {
	var buf strings.Builder
	
	// Test with empty line
	err := sendLine(&buf, 0, "")
	if err != nil {
		t.Fatalf("sendLine with empty string failed: %v", err)
	}
	
	// Test with special characters
	err = sendLine(&buf, 65535, "TEST\x12\x34\x56")
	if err != nil {
		t.Fatalf("sendLine with special chars failed: %v", err)
	}
}
