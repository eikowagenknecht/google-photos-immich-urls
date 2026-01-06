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
| `--skip-verify-ssl` | Skip SSL verification |
| `--dry-run` | List found URLs without querying Immich |
| `--fallback-filename` | Fall back to filename+timestamp matching if hash doesn't match (may produce wrong matches) |

## Matching

By default, it matches by **SHA1 hash** only. This is reliable but may miss files where Google Photos modified the content (e.g., edited photos).

With `--fallback-filename`, it will also try to match by **filename + timestamp** (2s tolerance) if the hash doesn't match. Use with caution - this may produce wrong matches (e.g., matching an edited version instead of the original).

## Output

The mapping result will be saved as a json file so you can do with it whatever you want later in your own scripts or tools.

```json
{
  "mappings": [
    {
      "google_url": "https://photos.google.com/lr/photo/APiKkD-...",
      "immich_url": "https://immich.example.com/photos/abc123-...",
      "match_method": "hash"
    }
  ],
  "stats": {
    "total_google_urls": 100,
    "matched": 95,
    "matched_by_hash": 10,
    "matched_by_filename": 85,
    "not_found_in_immich": 5
  }
}
```

## License

AGPL-3.0 (compatible with [immich-go](https://github.com/simulot/immich-go))
