package api

import "time"

// UploadResponse — JSON для POST /api/v1/avatars.
type UploadResponse struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	URL       string    `json:"url"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// ThumbnailDTO — одна миниатюра.
type ThumbnailDTO struct {
	Size string `json:"size"`
	URL  string `json:"url"`
}

// MetadataResponse — JSON для GET /metadata.
type MetadataResponse struct {
	ID               string         `json:"id"`
	UserID           string         `json:"user_id"`
	FileName         string         `json:"file_name"`
	MimeType         string         `json:"mime_type"`
	SizeBytes        int64          `json:"size_bytes"`
	UploadStatus     string         `json:"upload_status"`
	ProcessingStatus string         `json:"processing_status"`
	Thumbnails       []ThumbnailDTO `json:"thumbnails"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// AvatarListItem — элемент списка аватарок.
type AvatarListItem struct {
	ID         string         `json:"id"`
	UserID     string         `json:"user_id"`
	FileName   string         `json:"file_name"`
	MimeType   string         `json:"mime_type"`
	SizeBytes  int64          `json:"size_bytes"`
	URL        string         `json:"url"`
	CreatedAt  time.Time      `json:"created_at"`
	Thumbnails []ThumbnailDTO `json:"thumbnails"`
}

// UserAvatarsResponse — JSON для GET /users/{user_id}/avatars.
type UserAvatarsResponse struct {
	UserID string           `json:"user_id"`
	Items  []AvatarListItem `json:"items"`
}

// ComponentHealth — одна запись о компоненте в /health.
type ComponentHealth struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// HealthResponse — JSON для GET /health.
type HealthResponse struct {
	Status     string                     `json:"status"`
	Components map[string]ComponentHealth `json:"components"`
}
