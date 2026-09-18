package qbit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/parsoFish/healarr/internal/check"
)

// importedDownloadIDs returns the union of Sonarr's and Radarr's
// "downloadFolderImported" history download IDs since the given time,
// upper-cased so callers can compare them directly against qBittorrent
// torrent hashes (which are themselves upper-cased before comparison). A
// nil client is skipped rather than treated as an error: the two checks
// that call this enforce "at least one of Sonarr/Radarr configured"
// themselves before calling in, so both being nil never reaches here in
// practice, but skipping keeps this helper correct on its own terms too.
func importedDownloadIDs(ctx context.Context, d check.Deps, since time.Time) (map[string]bool, error) {
	ids := map[string]bool{}
	if d.Sonarr != nil {
		recs, err := d.Sonarr.History(ctx, since, "downloadFolderImported")
		if err != nil {
			return nil, fmt.Errorf("sonarr history: %w", err)
		}
		for _, r := range recs {
			addDownloadID(ids, r.DownloadID)
		}
	}
	if d.Radarr != nil {
		recs, err := d.Radarr.History(ctx, since, "downloadFolderImported")
		if err != nil {
			return nil, fmt.Errorf("radarr history: %w", err)
		}
		for _, r := range recs {
			addDownloadID(ids, r.DownloadID)
		}
	}
	return ids, nil
}

// addDownloadID upper-cases id and adds it to ids, skipping the empty
// string (a history record with no download ID, which a real *arr instance
// can produce for manually-imported items).
func addDownloadID(ids map[string]bool, id string) {
	if id == "" {
		return
	}
	ids[strings.ToUpper(id)] = true
}
