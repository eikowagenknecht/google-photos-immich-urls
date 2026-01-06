# google-photos-immich-urls

Maps Google Photos URLs from Google Takeout ZIP archives to Immich asset URLs.

Useful for updating links in notes (Obsidian, etc.) after migrating from Google Photos to Immich.

This project was created in about an hour with Claude Opus 4.5 to help me migrate the links in my Obsidian Vault to Immich.

Since it's one-time-use for me, there won't be any support from my side.

## Code from immich-go

This project could not have been done (so fast) without the amazing [immich-go](https://github.com/simulot/immich-go) from which it uses quite a lot of code:

- **Immich API client** (`github.com/simulot/immich-go/immich`) - used directly as a dependency
- **Google Takeout JSON parsing** (`internal/googlephotos/json.go`) - adapted from `adapters/googlePhotos/json.go`, modified to extract the actual Google Photos URL string instead of just checking for presence
- **ZIP file handling** (`internal/fshelper/zip.go`) - simplified version inspired by `internal/fshelper/`

## Usage

You can use the same API key you generated for immich-go, the permissions should be fine.

```bash
google-photos-immich-urls \
  -s https://immich.example.com \
  -k YOUR_API_KEY \
  -o mapping.json \
  takeout-*.zip
```

## Flags

| Flag | Description |
|------|-------------|
| `-s, --server` | Immich server URL |
| `-k, --api-key` | Immich API key |
| `-o, --output` | Output file (default: stdout) |
| `-v, --verbose` | Include detailed output (not_found, orphan_media, stats, extra fields) |
| `--skip-verify-ssl` | Skip SSL verification |
| `--dry-run` | List found URLs without querying Immich |
| `--fallback-filename` | Fall back to filename+timestamp matching if hash doesn't match (may produce wrong matches) |

## Matching

By default, it matches by **SHA1 hash** only. This is reliable but may miss files where Google Photos modified the content (e.g., edited photos).

With `--fallback-filename`, it will also try to match by **filename + timestamp** (2s tolerance) if the hash doesn't match. Use with caution - this may produce wrong matches (e.g., matching an edited version instead of the original).

## Output

### Default Output

By default, the output only includes the essential URL mappings:

```json
{
  "mappings": [
    {
      "google_url": "https://photos.google.com/lr/photo/APiKkD-...",
      "immich_url": "https://immich.example.com/photos/abc123-..."
    }
  ]
}
```

### Verbose Output (`-v`)

With the `-v` flag, additional details are included:

```json
{
  "mappings": [
    {
      "google_url": "https://photos.google.com/lr/photo/APiKkD-...",
      "immich_url": "https://immich.example.com/photos/abc123-...",
      "json_file": "Google Photos/Photos from 2023/IMG_1234.jpg.json",
      "path": "Google Photos/Photos from 2023/IMG_1234.jpg",
      "hash": "base64-encoded-sha1-hash",
      "match_method": "hash"
    }
  ],
  "not_found": [
    {
      "google_url": "https://photos.google.com/lr/photo/...",
      "json_file": "Google Photos/Photos from 2023/IMG_5678.jpg.json",
      "path": "Google Photos/Photos from 2023/IMG_5678.jpg",
      "hash": "base64-encoded-sha1-hash"
    }
  ],
  "orphan_media": [
    {
      "path": "Google Photos/Photos from 2023/IMG_1234-edited.jpg",
      "hash": "base64-encoded-sha1-hash",
      "immich_url": "https://immich.example.com/photos/xyz789-...",
      "immich_filename": "IMG_1234.jpg"
    }
  ],
  "stats": {
    "total_json_files": 1000,
    "total_google_urls": 500,
    "matched": 480,
    "matched_by_hash": 475,
    "matched_by_filename": 5,
    "not_found_in_immich": 15,
    "no_media_file": 3,
    "hash_errors": 2,
    "orphan_media": 10
  }
}
```

### Output Sections

| Section | Description |
|---------|-------------|
| `mappings` | Successfully matched files with Google URL and Immich URL |
| `not_found` | Files with a Google URL that couldn't be found in Immich |
| `orphan_media` | Media files in takeout without a JSON sidecar (no Google URL available) |
| `stats` | Summary statistics |

## Orphan Media Detection

The tool detects **orphan media files** - files in the takeout that have no accompanying JSON sidecar. These files don't have a Google Photos URL, but the tool will:

1. Compute their SHA1 hash
2. Check if they exist in Immich
3. Report the Immich filename (useful for detecting renamed files)

This is useful for finding files that were uploaded to Immich but may have been renamed during the upload process (e.g., `IMG_1234-edited.jpg` uploaded as `IMG_1234.jpg`).

## License

AGPL-3.0 (compatible with [immich-go](https://github.com/simulot/immich-go))
