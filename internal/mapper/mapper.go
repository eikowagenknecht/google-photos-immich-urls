// Package mapper provides the core logic for mapping Google Photos URLs to Immich URLs.
package mapper

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/simulot/immich-go/immich"
	"github.com/thedirtyfew/google-photos-immich-urls/internal/fshelper"
	"github.com/thedirtyfew/google-photos-immich-urls/internal/googlephotos"
)

// Mapping represents a single URL mapping from Google Photos to Immich.
type Mapping struct {
	GoogleURL   string `json:"google_url"`
	ImmichURL   string `json:"immich_url"`
	JSONFile    string `json:"json_file"`
	Path        string `json:"path"`
	Hash        string `json:"hash"`
	MatchMethod string `json:"match_method"` // "hash" or "filename+timestamp"
}

// NotFound represents a Google Photos asset that could not be matched in Immich.
type NotFound struct {
	GoogleURL string `json:"google_url"`
	JSONFile  string `json:"json_file"`
	Path      string `json:"path"`
	Hash      string `json:"hash"`
}

// OrphanMedia represents a media file without a JSON sidecar (no Google URL available).
type OrphanMedia struct {
	Path           string `json:"path"`
	Hash           string `json:"hash,omitempty"`
	ImmichURL      string `json:"immich_url,omitempty"`      // Set if found in Immich
	ImmichFilename string `json:"immich_filename,omitempty"` // Filename in Immich (to detect renames)
}

// Stats contains statistics about the mapping process.
type Stats struct {
	TotalJSONFiles    int `json:"total_json_files"`
	TotalGoogleURLs   int `json:"total_google_urls"`
	Matched           int `json:"matched"`
	MatchedByHash     int `json:"matched_by_hash"`
	MatchedByFilename int `json:"matched_by_filename"`
	NotFoundInImmich  int `json:"not_found_in_immich"`
	NoMediaFile       int `json:"no_media_file"`
	HashErrors        int `json:"hash_errors"`
	OrphanMedia       int `json:"orphan_media"`
}

// Result contains the complete mapping result.
type Result struct {
	Mappings    []Mapping     `json:"mappings"`
	NotFound    []NotFound    `json:"not_found"`
	OrphanMedia []OrphanMedia `json:"orphan_media"`
	Stats       Stats         `json:"stats"`
}

// Mapper handles the URL mapping process.
type Mapper struct {
	client           *immich.ImmichClient
	httpClient       *http.Client
	serverURL        string
	apiKey           string
	dryRun           bool
	fallbackFilename bool
	fsyss            []fs.FS
	logger           func(format string, args ...interface{})
}

// Config contains mapper configuration.
type Config struct {
	Server           string
	APIKey           string
	SkipSSL          bool
	DryRun           bool
	FallbackFilename bool
	TakeoutPaths     []string
	Logger           func(format string, args ...interface{})
}

// New creates a new Mapper instance.
func New(cfg Config) (*Mapper, error) {
	m := &Mapper{
		serverURL:        strings.TrimSuffix(cfg.Server, "/"),
		apiKey:           cfg.APIKey,
		dryRun:           cfg.DryRun,
		fallbackFilename: cfg.FallbackFilename,
		logger:           cfg.Logger,
	}

	if m.logger == nil {
		m.logger = func(format string, args ...interface{}) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}
	}

	// Parse takeout paths (handles ZIP files and wildcards)
	var err error
	m.fsyss, err = fshelper.ParsePaths(cfg.TakeoutPaths)
	if err != nil {
		return nil, fmt.Errorf("failed to parse takeout paths: %w", err)
	}

	if len(m.fsyss) == 0 {
		return nil, fmt.Errorf("no valid takeout files found")
	}

	// Create Immich client (unless dry-run)
	if !cfg.DryRun {
		m.client, err = immich.NewImmichClient(
			cfg.Server,
			cfg.APIKey,
			immich.OptionVerifySSL(cfg.SkipSSL),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create Immich client: %w", err)
		}

		// Create HTTP client for direct API calls
		m.httpClient = &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.SkipSSL},
			},
		}
	}

	return m, nil
}

// Close releases resources.
func (m *Mapper) Close() error {
	return fshelper.CloseFSs(m.fsyss)
}

// Run executes the mapping process.
func (m *Mapper) Run(ctx context.Context) (*Result, error) {
	result := &Result{
		Mappings:    make([]Mapping, 0),
		NotFound:    make([]NotFound, 0),
		OrphanMedia: make([]OrphanMedia, 0),
	}

	// Validate Immich connection (unless dry-run)
	if !m.dryRun && m.client != nil {
		if err := m.client.PingServer(ctx); err != nil {
			return nil, fmt.Errorf("failed to connect to Immich server: %w", err)
		}
		user, err := m.client.ValidateConnection(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to validate Immich connection: %w", err)
		}
		m.logger("Connected to Immich as: %s", user.Email)
	}

	// Process each filesystem (ZIP file or directory)
	for _, fsys := range m.fsyss {
		if err := m.processFS(ctx, fsys, result); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// mediaExtensions lists file extensions considered as media files.
var mediaExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".heic": true, ".heif": true, ".webp": true, ".bmp": true, ".tiff": true,
	".mp4": true, ".mov": true, ".avi": true, ".mkv": true, ".3gp": true, ".webm": true,
}

// isMediaFile checks if a filename has a media extension.
func isMediaFile(filename string) bool {
	ext := strings.ToLower(path.Ext(filename))
	return mediaExtensions[ext]
}

// processFS walks a single filesystem and processes JSON files.
func (m *Mapper) processFS(ctx context.Context, fsys fs.FS, result *Result) error {
	// Build a map of directory -> files for matching JSON to media
	dirFiles := make(map[string][]string)
	// Track all media files (full paths)
	allMediaFiles := make(map[string]bool)

	err := fs.WalkDir(fsys, ".", func(fpath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		dir := path.Dir(fpath)
		filename := path.Base(fpath)
		dirFiles[dir] = append(dirFiles[dir], filename)

		// Track media files
		if isMediaFile(filename) {
			allMediaFiles[fpath] = true
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to walk filesystem: %w", err)
	}

	// Track which media files have been claimed by a JSON sidecar
	claimedMedia := make(map[string]bool)

	// Process JSON files
	err = fs.WalkDir(fsys, ".", func(fpath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if d.IsDir() || !strings.HasSuffix(strings.ToLower(fpath), ".json") {
			return nil
		}

		result.Stats.TotalJSONFiles++

		// Read and parse JSON
		data, err := fs.ReadFile(fsys, fpath)
		if err != nil {
			m.logger("Warning: failed to read %s: %v", fpath, err)
			return nil
		}

		md, err := googlephotos.ParseMetadata(data)
		if err != nil {
			// Not all JSON files are metadata files
			return nil
		}

		// Skip if not an asset or has no URL
		if !md.IsAsset() || !md.HasURL() {
			return nil
		}

		result.Stats.TotalGoogleURLs++

		// Find the corresponding media file
		dir := path.Dir(fpath)
		jsonBase := path.Base(fpath)
		mediaFile := m.findMediaFile(jsonBase, md.Title, dirFiles[dir])

		if mediaFile == "" {
			result.Stats.NoMediaFile++
			m.logger("Warning: no media file found for %s", fpath)
			return nil
		}

		// Compute hash of media file
		mediaPath := path.Join(dir, mediaFile)
		claimedMedia[mediaPath] = true

		hash, err := m.computeHash(fsys, mediaPath)
		if err != nil {
			result.Stats.HashErrors++
			m.logger("Warning: failed to compute hash for %s: %v", mediaPath, err)
			return nil
		}

		// Query Immich for matching asset
		if m.dryRun {
			m.logger("Dry-run: would query Immich for hash %s (file: %s, URL: %s)", hash, mediaFile, md.URL)
			return nil
		}

		m.logger("Processing: %s (hash: %s)", mediaPath, hash)

		// Try hash-based matching first (searches all visibility types)
		foundAssets, err := m.searchAssetsByHash(ctx, hash)
		if err != nil {
			m.logger("Warning: failed to query Immich by hash for %s: %v", mediaPath, err)
		}

		matchedByHash := len(foundAssets) > 0

		// Fallback to filename-based matching if hash didn't work (opt-in)
		if len(foundAssets) == 0 && m.fallbackFilename {
			// Try with the original filename from metadata
			searchName := md.Title
			if searchName == "" {
				searchName = mediaFile
			}
			// Remove extension for search (Immich stores without extension sometimes)
			baseName := strings.TrimSuffix(searchName, path.Ext(searchName))

			foundAssets, err = m.searchAssetsByFilename(ctx, searchName)
			if err != nil {
				m.logger("Warning: failed to query Immich by filename for %s: %v", searchName, err)
			}

			// If still not found, try base name
			if len(foundAssets) == 0 && baseName != searchName {
				foundAssets, err = m.searchAssetsByFilename(ctx, baseName)
				if err != nil {
					m.logger("Warning: failed to query Immich by basename for %s: %v", baseName, err)
				}
			}

			// If multiple matches, filter by timestamp from Google metadata
			if len(foundAssets) > 1 && md.PhotoTakenTime != nil {
				googleTime := md.PhotoTakenTime.Time()
				if !googleTime.IsZero() {
					foundAssets = filterByTimestamp(foundAssets, googleTime)
				}
			}
		}

		if len(foundAssets) == 0 {
			result.Stats.NotFoundInImmich++
			result.NotFound = append(result.NotFound, NotFound{
				GoogleURL: md.URL,
				JSONFile:  fpath,
				Path:      mediaPath,
				Hash:      hash,
			})
			m.logger("Not found in Immich: %s (hash: %s)", mediaPath, hash)
			return nil
		}

		// Use first match
		immichURL := fmt.Sprintf("%s/photos/%s", m.serverURL, foundAssets[0].ID)
		var matchMethod string
		if matchedByHash {
			matchMethod = "hash"
			result.Stats.MatchedByHash++
		} else {
			matchMethod = "filename+timestamp"
			result.Stats.MatchedByFilename++
			m.logger("Matched by filename (hash mismatch): %s", mediaFile)
		}
		result.Mappings = append(result.Mappings, Mapping{
			GoogleURL:   md.URL,
			ImmichURL:   immichURL,
			JSONFile:    fpath,
			Path:        mediaPath,
			Hash:        hash,
			MatchMethod: matchMethod,
		})
		result.Stats.Matched++

		if len(foundAssets) > 1 {
			m.logger("Warning: multiple Immich assets found for %s, using first match", mediaFile)
		}

		return nil
	})

	if err != nil {
		return err
	}

	// Find orphan media files (media without JSON sidecar)
	for mediaPath := range allMediaFiles {
		if !claimedMedia[mediaPath] {
			result.Stats.OrphanMedia++

			orphan := OrphanMedia{Path: mediaPath}

			// Try to compute hash and check Immich (unless dry-run)
			if !m.dryRun {
				hash, err := m.computeHash(fsys, mediaPath)
				if err == nil {
					orphan.Hash = hash
					m.logger("Orphan media: %s (hash: %s)", mediaPath, hash)

					// Check if it exists in Immich
					assets, err := m.searchAssetsByHash(ctx, hash)
					if err == nil && len(assets) > 0 {
						asset := assets[0]
						orphan.ImmichURL = fmt.Sprintf("%s/photos/%s", m.serverURL, asset.ID)
						orphan.ImmichFilename = asset.OriginalFileName

						// Log if filename differs (for user awareness)
						takeoutFilename := path.Base(mediaPath)
						if asset.OriginalFileName != takeoutFilename {
							m.logger("Filename mismatch: takeout=%s, immich=%s", takeoutFilename, asset.OriginalFileName)
						}
					}
				} else {
					m.logger("Orphan media: %s (hash error: %v)", mediaPath, err)
				}
			} else {
				m.logger("Orphan media: %s", mediaPath)
			}

			result.OrphanMedia = append(result.OrphanMedia, orphan)
		}
	}

	return nil
}

// findMediaFile finds the media file corresponding to a JSON sidecar.
func (m *Mapper) findMediaFile(jsonName, title string, filesInDir []string) string {
	// Remove .json extension to get base name
	baseName := strings.TrimSuffix(jsonName, ".json")

	// Try exact match first (photo.jpg.json -> photo.jpg)
	for _, f := range filesInDir {
		if f == baseName {
			return f
		}
	}

	// Try matching by title from metadata
	if title != "" {
		for _, f := range filesInDir {
			if f == title {
				return f
			}
		}
	}

	// Handle Google's naming patterns:
	// - photo.jpg.json -> photo.jpg
	// - photo(1).jpg.json -> photo(1).jpg
	// - photo.jpg(1).json -> photo.jpg (duplicate JSON)

	// Check if baseName ends with a media extension
	mediaExts := []string{".jpg", ".jpeg", ".png", ".gif", ".heic", ".heif", ".webp", ".mp4", ".mov", ".avi", ".mkv", ".3gp"}
	for _, ext := range mediaExts {
		if strings.HasSuffix(strings.ToLower(baseName), ext) {
			for _, f := range filesInDir {
				if strings.EqualFold(f, baseName) {
					return f
				}
			}
		}
	}

	// Try removing trailing (N) from JSON name and find media file
	// e.g., "photo.jpg(1).json" -> look for "photo.jpg"
	if idx := strings.LastIndex(baseName, "("); idx > 0 {
		possibleName := baseName[:idx]
		for _, f := range filesInDir {
			if f == possibleName {
				return f
			}
		}
	}

	return ""
}

// computeHash computes the SHA1 hash of a file and returns it as base64.
func (m *Mapper) computeHash(fsys fs.FS, fpath string) (string, error) {
	f, err := fsys.Open(fpath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// simpleMapping is the non-verbose mapping output.
type simpleMapping struct {
	GoogleURL string `json:"google_url"`
	ImmichURL string `json:"immich_url"`
}

// simpleResult is the non-verbose result output.
type simpleResult struct {
	Mappings []simpleMapping `json:"mappings"`
}

// WriteJSON writes the result to a writer as JSON.
// If verbose is false, only mappings with google_url and immich_url are included.
func (r *Result) WriteJSON(w io.Writer, verbose bool) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if verbose {
		return enc.Encode(r)
	}

	// Non-verbose: only include simple mappings
	simple := simpleResult{
		Mappings: make([]simpleMapping, len(r.Mappings)),
	}
	for i, m := range r.Mappings {
		simple.Mappings[i] = simpleMapping{
			GoogleURL: m.GoogleURL,
			ImmichURL: m.ImmichURL,
		}
	}
	return enc.Encode(simple)
}

// searchMetadataResponse matches the Immich API response structure.
type searchMetadataResponse struct {
	Assets struct {
		Items []*immich.Asset `json:"items"`
	} `json:"assets"`
}

// searchWithVisibility searches for assets using the Immich API with a specific visibility.
func (m *Mapper) searchWithVisibility(ctx context.Context, query map[string]interface{}, visibility string) ([]*immich.Asset, error) {
	query["visibility"] = visibility
	query["size"] = 100

	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", m.serverURL+"/api/search/metadata", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", m.apiKey)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var result searchMetadataResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Assets.Items, nil
}

// searchAssetsByHash searches for assets by hash across timeline and archive.
func (m *Mapper) searchAssetsByHash(ctx context.Context, hash string) ([]*immich.Asset, error) {
	query := map[string]interface{}{"checksum": hash}

	// Try timeline first
	assets, err := m.searchWithVisibility(ctx, query, "timeline")
	if err != nil {
		return nil, err
	}
	if len(assets) > 0 {
		return assets, nil
	}

	// Try archive
	assets, err = m.searchWithVisibility(ctx, map[string]interface{}{"checksum": hash}, "archive")
	if err != nil {
		return nil, err
	}

	return assets, nil
}

// searchAssetsByFilename searches for assets by filename across timeline and archive.
func (m *Mapper) searchAssetsByFilename(ctx context.Context, filename string) ([]*immich.Asset, error) {
	query := map[string]interface{}{"originalFileName": filename}

	// Try timeline first
	assets, err := m.searchWithVisibility(ctx, query, "timeline")
	if err != nil {
		return nil, err
	}
	if len(assets) > 0 {
		return assets, nil
	}

	// Try archive
	assets, err = m.searchWithVisibility(ctx, map[string]interface{}{"originalFileName": filename}, "archive")
	if err != nil {
		return nil, err
	}

	return assets, nil
}

// filterByTimestamp filters assets to find matches by timestamp.
// Returns only assets that match within a 2 second tolerance.
func filterByTimestamp(assets []*immich.Asset, targetTime time.Time) []*immich.Asset {
	const tolerance = 2 * time.Second // Allow 2 second difference for timezone/rounding issues

	var matches []*immich.Asset

	for _, a := range assets {
		// Try different time fields from Immich asset
		var assetTime time.Time
		if !a.LocalDateTime.Time.IsZero() {
			assetTime = a.LocalDateTime.Time
		} else if !a.FileCreatedAt.Time.IsZero() {
			assetTime = a.FileCreatedAt.Time
		} else if !a.ExifInfo.DateTimeOriginal.Time.IsZero() {
			assetTime = a.ExifInfo.DateTimeOriginal.Time
		}

		if assetTime.IsZero() {
			continue
		}

		diff := assetTime.Sub(targetTime)
		if diff < 0 {
			diff = -diff
		}

		if diff <= tolerance {
			matches = append(matches, a)
		}
	}

	if len(matches) > 0 {
		return matches
	}

	// No match within tolerance, return empty to signal no match
	return nil
}
