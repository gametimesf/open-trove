package storage

import "time"

// RecordView keeps one entry per artifact without allowing a delayed writer to
// move its last-viewed time backwards. Timestamps are supplied by the caller.
func (m *UserManifest) RecordView(record ActivityRecord) {
	// Older artifacts lack object-level attribution, but this browser may have
	// the original upload record. Never use the viewer's email as the owner.
	if record.OwnerEmail == "" {
		var newest time.Time
		for _, upload := range m.Uploads {
			at, _ := time.Parse(time.RFC3339Nano, upload.At)
			if upload.Slug == record.Slug && !at.Before(newest) {
				record.OwnerEmail = upload.OwnerEmail
				if record.OwnerEmail == "" {
					record.OwnerEmail = upload.UserEmail
				}
				newest = at
			}
		}
	}
	m.Views = mergeActivity(m.Views, record)
}

func (m *UserManifest) RecordUpload(record ActivityRecord) {
	m.Uploads = mergeActivity(m.Uploads, record)
}

func mergeActivity(records []ActivityRecord, record ActivityRecord) []ActivityRecord {
	newTime, _ := time.Parse(time.RFC3339Nano, record.At)
	out := make([]ActivityRecord, 0, len(records)+1)
	for _, previous := range records {
		if previous.Slug != record.Slug {
			out = append(out, previous)
			continue
		}
		previousTime, _ := time.Parse(time.RFC3339Nano, previous.At)
		if previousTime.After(newTime) {
			record, newTime = previous, previousTime
		}
	}
	return append(out, record)
}
